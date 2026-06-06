package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
	"github.com/prime-radiant-inc/clipfan/internal/version"
)

var ErrSSHGatewayCommandRejected = errors.New("ssh_gateway_command_rejected")

var sshGatewayCurrentPollInterval = 250 * time.Millisecond

type SSHGatewayIdentity struct {
	PeerID string
	KeyID  string
}

type SSHGatewayHandlers struct {
	Probe      func(SSHGatewayIdentity, io.Writer) error
	SyncStream func(SSHGatewayIdentity, io.Reader, io.Writer) error
}

func RunSSHGateway(args []string, stdout io.Writer, stderr io.Writer) error {
	return runSSHGateway(args, os.Stdin, stdout, stderr, os.Getenv)
}

func runSSHGateway(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, getenv func(string) string) error {
	return runSSHGatewayWithHandlers(args, stdin, stdout, stderr, getenv, defaultSSHGatewayHandlers())
}

func runSSHGatewayWithHandlers(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, getenv func(string) string, handlers SSHGatewayHandlers) error {
	fs := flag.NewFlagSet("ssh-gateway", flag.ContinueOnError)
	fs.SetOutput(stderr)
	peerID := fs.String("authorized-peer", "", "authorized peer id")
	keyID := fs.String("authorized-key-id", "", "authorized key id")
	directCommand := fs.String("direct-command", "", "direct gateway command")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected ssh-gateway argument")
	}
	if err := config.ValidateHostID(*peerID); err != nil {
		return fmt.Errorf("invalid authorized peer: %w", err)
	}
	if err := sshprovision.ValidateManagedAuthorizedKeyID(*keyID); err != nil {
		return fmt.Errorf("invalid authorized key id: %w", err)
	}
	identity := SSHGatewayIdentity{PeerID: *peerID, KeyID: *keyID}

	command := getenv("SSH_ORIGINAL_COMMAND")
	if command == "" {
		command = *directCommand
	}
	switch command {
	case sshprovision.SSHGatewayProbeCommand:
		if handlers.Probe == nil {
			return ErrSSHGatewayCommandRejected
		}
		return handlers.Probe(identity, stdout)
	case sshprovision.SSHGatewaySyncStreamCommand:
		if handlers.SyncStream == nil {
			return ErrSSHGatewayCommandRejected
		}
		return handlers.SyncStream(identity, stdin, stdout)
	default:
		return ErrSSHGatewayCommandRejected
	}
}

func defaultSSHGatewayHandlers() SSHGatewayHandlers {
	handlers := SSHGatewayHandlers{
		Probe: func(identity SSHGatewayIdentity, stdout io.Writer) error {
			return json.NewEncoder(stdout).Encode(map[string]string{
				"status":  "ok",
				"peer_id": identity.PeerID,
				"key_id":  identity.KeyID,
				"version": version.Version,
			})
		},
	}
	if releaseflags.SSHSyncStreamEnabled {
		handlers.SyncStream = runDefaultSSHGatewaySyncStream
	}
	return handlers
}

func runDefaultSSHGatewaySyncStream(identity SSHGatewayIdentity, stdin io.Reader, stdout io.Writer) error {
	if _, err := os.Stat(config.Path()); err != nil {
		return fmt.Errorf("ssh gateway config unavailable: %w", err)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	localID := cfg.Hostname
	if localID == "" {
		if host, err := os.Hostname(); err == nil {
			localID = strings.TrimSuffix(strings.SplitN(host, ".", 2)[0], ".local")
		}
	}
	if err := config.ValidateHostID(localID); err != nil {
		return fmt.Errorf("invalid local host id: %w", err)
	}
	if err := validateSSHGatewaySyncPeer(cfg, identity); err != nil {
		return err
	}
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		return err
	}
	stream := transport.NewSSHSyncStream(auth, localID, identity.PeerID, stdin, stdout)
	ctx := context.Background()
	if _, err := stream.ReadHello(ctx, time.Now()); err != nil {
		return err
	}
	hello, err := transport.NewSSHStreamHello(auth, transport.SSHStreamPurposeSyncStream, localID, identity.PeerID, time.Now(), "")
	if err != nil {
		return err
	}
	if err := writeSSHGatewayFrame(ctx, func(ctx context.Context) error { return stream.WriteHello(ctx, hello) }); err != nil {
		return err
	}
	localHost, localPort, err := sshGatewayLocalDaemonTarget(cfg)
	if err != nil {
		return err
	}
	client := transport.NewClientWithPeerHTTPRuntimeDisabled(auth, localID, true)
	var writeMu sync.Mutex
	writeFrame := func(fn func(context.Context) error) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeSSHGatewayFrame(ctx, fn)
	}
	events := make(chan sshGatewayStreamReadEvent, 8)
	go readSSHGatewayStreamEvents(ctx, stream, events)
	ticker := time.NewTicker(sshGatewayCurrentPollInterval)
	defer ticker.Stop()
	var seq uint64
	sentCurrent := map[string]struct{}{}
	for {
		select {
		case event := <-events:
			if event.err != nil {
				return event.err
			}
			if event.done {
				return nil
			}
			switch event.frame.Type {
			case transport.SSHStreamFrameState:
				if err := handleSSHGatewayState(ctx, writeFrame, stream, client, localHost, localPort, localID, event.frame.State); err != nil {
					return err
				}
			case transport.SSHStreamFrameAck:
			case transport.SSHStreamFrameError:
				return fmt.Errorf("ssh stream error frame: %s", event.frame.ErrorCode)
			default:
				return fmt.Errorf("%w: %s", transport.ErrSSHStreamUnexpectedFrame, event.frame.Type)
			}
		case <-ticker.C:
			if err := publishSSHGatewayCurrent(ctx, writeFrame, stream, client, localHost, localPort, identity.PeerID, &seq, sentCurrent); err != nil {
				return err
			}
		}
	}
}

type sshGatewayStreamReadEvent struct {
	frame transport.SSHStreamEvent
	err   error
	done  bool
}

func readSSHGatewayStreamEvents(ctx context.Context, stream *transport.SSHSyncStream, events chan<- sshGatewayStreamReadEvent) {
	for {
		event, err := stream.ReadNextNow(ctx)
		if errors.Is(err, transport.ErrSSHStreamUnexpectedEOF) {
			events <- sshGatewayStreamReadEvent{done: true}
			return
		}
		if err != nil {
			events <- sshGatewayStreamReadEvent{err: err}
			return
		}
		select {
		case events <- sshGatewayStreamReadEvent{frame: event}:
		case <-ctx.Done():
			events <- sshGatewayStreamReadEvent{err: ctx.Err()}
			return
		}
	}
}

func handleSSHGatewayState(ctx context.Context, writeFrame func(func(context.Context) error) error, stream *transport.SSHSyncStream, client *transport.Client, localHost string, localPort int, localID string, state transport.SSHStreamStateResult) error {
	if state.NullReason != "" {
		return writeFrame(func(ctx context.Context) error {
			return stream.WriteAck(ctx, state.Seq, "", "no_state", "")
		})
	}
	status := "applied"
	reason := ""
	if err := pushSSHGatewayStateToLocalDaemon(ctx, client, localHost, localPort, localID, state.Content, state.Origin); err != nil {
		status = "rejected"
		reason = "local_apply_failed"
		if writeErr := writeFrame(func(ctx context.Context) error {
			return stream.WriteAck(ctx, state.Seq, state.Content.ID, status, reason)
		}); writeErr != nil {
			return writeErr
		}
		return err
	}
	return writeFrame(func(ctx context.Context) error {
		return stream.WriteAck(ctx, state.Seq, state.Content.ID, status, reason)
	})
}

func publishSSHGatewayCurrent(ctx context.Context, writeFrame func(func(context.Context) error) error, stream *transport.SSHSyncStream, client *transport.Client, localHost string, localPort int, peerID string, seq *uint64, sent map[string]struct{}) error {
	currentCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	payload, err := client.Current(currentCtx, localHost, localPort)
	if err != nil {
		if errors.Is(err, transport.ErrSSHStreamFrameTooLarge) {
			return nil
		}
		return err
	}
	content, ok, err := payload.Content()
	if err != nil {
		return err
	}
	if !ok || content.ID == "" || sshGatewayHostsMatch(payload.Origin, peerID) {
		return nil
	}
	if len(content.Bytes) > transport.MaxSSHStreamPayloadBytes {
		return nil
	}
	if _, ok := sent[content.ID]; ok {
		return nil
	}
	(*seq)++
	currentSeq := *seq
	if err := writeFrame(func(ctx context.Context) error {
		return stream.WriteState(ctx, currentSeq, content, payload.Origin)
	}); err != nil {
		return err
	}
	sent[content.ID] = struct{}{}
	return nil
}

func sshGatewayHostsMatch(a string, b string) bool {
	return transport.HostsMatch(a, b)
}

func pushSSHGatewayStateToLocalDaemon(ctx context.Context, client *transport.Client, localHost string, localPort int, localID string, content clipboard.Content, origin string) error {
	pushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return client.PushAsToRecipient(pushCtx, localHost, localPort, localID, content, origin)
}

func sshGatewayLocalDaemonTarget(cfg *config.Config) (string, int, error) {
	if cfg == nil {
		return "", 0, ErrSSHGatewayCommandRejected
	}
	plan := config.PlanListener(*cfg, config.GeneratedLoopbackDefaultsEnabled())
	if plan.SafeMode {
		return "", 0, fmt.Errorf("ssh gateway listener safe mode: %s", plan.ParseError)
	}
	host, portText, err := net.SplitHostPort(plan.BindListen)
	if err != nil {
		return "", 0, fmt.Errorf("invalid planned listen: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid planned listen port")
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	if !isSSHGatewayLoopbackHost(host) {
		return "", 0, fmt.Errorf("planned listen is not loopback: %s", host)
	}
	return host, port, nil
}

func isSSHGatewayLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeSSHGatewayFrame(ctx context.Context, fn func(context.Context) error) error {
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- fn(writeCtx)
	}()
	select {
	case err := <-done:
		return err
	case <-writeCtx.Done():
		return writeCtx.Err()
	}
}

func validateSSHGatewaySyncPeer(cfg *config.Config, identity SSHGatewayIdentity) error {
	if cfg == nil || cfg.Transport != config.TransportSSH || cfg.SSH == nil {
		return ErrSSHGatewayCommandRejected
	}
	for _, peer := range cfg.SSH.Peers {
		if peer.ID != identity.PeerID {
			continue
		}
		if !peer.Enabled || !peer.Accept || !peer.Connect || !peer.Persistent || peer.MigrationState != config.MigrationStateSSHKeysReady {
			return ErrSSHGatewayCommandRejected
		}
		if cfg.SSH.SyncKey == "" || cfg.SSH.KnownHosts == "" {
			return ErrSSHGatewayCommandRejected
		}
		if peer.Proof.AcceptKeyID != identity.KeyID {
			return ErrSSHGatewayCommandRejected
		}
		return nil
	}
	return ErrSSHGatewayCommandRejected
}
