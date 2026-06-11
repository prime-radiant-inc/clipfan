package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
	"github.com/prime-radiant-inc/clipfan/internal/version"
)

func TestRunSSHGatewayAllowsProbeCommand(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	oldVersion := version.Version
	version.Version = "test-version"
	t.Cleanup(func() { version.Version = oldVersion })

	var stdout, stderr bytes.Buffer
	err := runSSHGateway(
		[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewayProbeCommand
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("runSSHGateway() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Status  string `json:"status"`
		PeerID  string `json:"peer_id"`
		KeyID   string `json:"key_id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Status != "ok" || payload.PeerID != "linux-a" || payload.KeyID != "key-123456" || payload.Version != "test-version" {
		t.Fatalf("payload = %#v", payload)
	}
	assertPathMissing(t, filepath.Join(configRoot, "clipfan"))
	assertPathMissing(t, filepath.Join(stateRoot, "clipfan"))
}

func TestRunSSHGatewayAllowsDirectProbeCommandWithoutForcedOriginalCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := runSSHGateway(
		[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456", "--direct-command", sshprovision.SSHGatewayProbeCommand},
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("runSSHGateway() error = %v", err)
	}
	var payload struct {
		Status string `json:"status"`
		PeerID string `json:"peer_id"`
		KeyID  string `json:"key_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Status != "ok" || payload.PeerID != "linux-a" || payload.KeyID != "key-123456" {
		t.Fatalf("payload = %#v", payload)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunSSHGatewayForcedOriginalCommandTakesPrecedenceOverDirectCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := runSSHGateway(
		[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456", "--direct-command", sshprovision.SSHGatewayProbeCommand},
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return "sh"
			}
			return ""
		},
	)
	if !errors.Is(err, ErrSSHGatewayCommandRejected) {
		t.Fatalf("runSSHGateway() error = %v, want ErrSSHGatewayCommandRejected", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunSSHGatewayAllowsInjectedPrivateSyncStreamCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	var got SSHGatewayIdentity
	err := runSSHGatewayWithHandlers(
		[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"},
		strings.NewReader("stream input\n"),
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewaySyncStreamCommand
			}
			return ""
		},
		SSHGatewayHandlers{
			SyncStream: func(identity SSHGatewayIdentity, stdin io.Reader, stdout io.Writer) error {
				got = identity
				data, err := io.ReadAll(stdin)
				if err != nil {
					return err
				}
				if string(data) != "stream input\n" {
					return fmt.Errorf("stdin = %q", data)
				}
				_, err = stdout.Write([]byte("stream-owned\n"))
				return err
			},
		},
	)
	if err != nil {
		t.Fatalf("runSSHGatewayWithHandlers() error = %v", err)
	}
	if got.PeerID != "linux-a" || got.KeyID != "key-123456" {
		t.Fatalf("identity = %#v", got)
	}
	if stdout.String() != "stream-owned\n" {
		t.Fatalf("stdout = %q, want handler-owned stream output", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunSSHGatewayDefaultSyncStreamBridgesStateToLocalDaemon(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	sharedKey := config.NewSharedKey()
	auth, err := transport.NewAuth(sharedKey)
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan struct {
		content clipboard.Content
		origin  string
	}, 1)
	srv := transport.NewServer("127.0.0.1:0", auth, nil)
	srv.SetCurrentApply(func(c clipboard.Content, origin string) error {
		received <- struct {
			content clipboard.Content
			origin  string
		}{content: c, origin: origin}
		return nil
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ServeListener(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		<-serveErr
	})
	listenPort := ln.Addr().(*net.TCPAddr).Port
	writeGatewayConfig(t, sharedKey, listenPort)

	var stdin, stdout, stderr bytes.Buffer
	initiator := transport.NewSSHSyncStream(auth, "m4", "linux-b", bytes.NewReader(nil), &stdin)
	hello, err := transport.NewSSHStreamHello(auth, transport.SSHStreamPurposeSyncStream, "m4", "linux-b", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := initiator.WriteHello(context.Background(), hello); err != nil {
		t.Fatalf("initiator WriteHello error = %v", err)
	}
	content := clipboard.New(clipboard.KindText, []byte("through gateway"), time.Now().UTC())
	content.ID = "clip-gateway"
	if err := initiator.WriteState(context.Background(), 1, content, "m4"); err != nil {
		t.Fatalf("initiator WriteState error = %v", err)
	}

	err = runSSHGateway(
		[]string{"--authorized-peer", "m4", "--authorized-key-id", "key-123456"},
		&stdin,
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewaySyncStreamCommand
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("runSSHGateway() error = %v; stderr=%q", err, stderr.String())
	}
	reader := transport.NewSSHSyncStream(auth, "m4", "linux-b", &stdout, io.Discard)
	reader.SetHelloNonceCache(transport.NewSSHStreamHelloNonceCache())
	if _, err := reader.ReadHello(context.Background(), time.Now()); err != nil {
		t.Fatalf("initiator ReadHello error = %v", err)
	}
	event, err := reader.ReadNext(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("initiator ReadNext error = %v", err)
	}
	if event.Type != transport.SSHStreamFrameAck || event.Ack.Seq != 1 || event.Ack.ID != "clip-gateway" || event.Ack.Status != "applied" {
		t.Fatalf("ack event = %#v", event)
	}
	select {
	case got := <-received:
		if got.origin != "m4" || got.content.ID != "clip-gateway" || string(got.content.Bytes) != "through gateway" {
			t.Fatalf("received = %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for local daemon receive")
	}
}

func TestRunSSHGatewayDefaultSyncStreamPublishesLocalCurrentToInitiator(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	sharedKey := config.NewSharedKey()
	auth, err := transport.NewAuth(sharedKey)
	if err != nil {
		t.Fatal(err)
	}
	content := clipboard.New(clipboard.KindText, []byte("from accepted side"), time.Now().UTC())
	content.ID = "clip-accepted-side"
	srv := transport.NewServer("127.0.0.1:0", auth, nil)
	srv.SetCurrentFunc(func() transport.CurrentPayload {
		return transport.CurrentPayloadFromContent(content, "linux-b")
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ServeListener(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		<-serveErr
	})
	listenPort := ln.Addr().(*net.TCPAddr).Port
	writeGatewayConfig(t, sharedKey, listenPort)
	oldPoll := sshGatewayCurrentPollInterval
	sshGatewayCurrentPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { sshGatewayCurrentPollInterval = oldPoll })

	gatewayInR, gatewayInW := io.Pipe()
	gatewayOutR, gatewayOutW := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := runSSHGateway(
			[]string{"--authorized-peer", "m4", "--authorized-key-id", "key-123456"},
			gatewayInR,
			gatewayOutW,
			io.Discard,
			func(key string) string {
				if key == "SSH_ORIGINAL_COMMAND" {
					return sshprovision.SSHGatewaySyncStreamCommand
				}
				return ""
			},
		)
		_ = gatewayOutW.Close()
		errCh <- err
	}()
	t.Cleanup(func() {
		_ = gatewayInW.Close()
		_ = gatewayOutR.Close()
	})

	initiator := transport.NewSSHSyncStream(auth, "m4", "linux-b", gatewayOutR, gatewayInW)
	hello, err := transport.NewSSHStreamHello(auth, transport.SSHStreamPurposeSyncStream, "m4", "linux-b", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := initiator.WriteHello(context.Background(), hello); err != nil {
		t.Fatalf("initiator WriteHello error = %v", err)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRead()
	if _, err := initiator.ReadHello(readCtx, time.Now()); err != nil {
		t.Fatalf("initiator ReadHello error = %v", err)
	}
	event, err := initiator.ReadNext(readCtx, time.Now())
	if err != nil {
		t.Fatalf("initiator ReadNext error = %v", err)
	}
	if event.Type != transport.SSHStreamFrameState || event.State.Content.ID != "clip-accepted-side" || event.State.Origin != "linux-b" || string(event.State.Content.Bytes) != "from accepted side" {
		t.Fatalf("event = %#v", event)
	}
	if err := initiator.WriteAck(context.Background(), event.State.Seq, event.State.Content.ID, "applied", ""); err != nil {
		t.Fatalf("initiator WriteAck error = %v", err)
	}
	_ = gatewayInW.Close()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runSSHGateway() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for gateway exit")
	}
}

func TestSSHGatewayHostMatchUsesTransportRules(t *testing.T) {
	t.Parallel()

	if sshGatewayHostsMatch("linux-b", "b") {
		t.Fatal("linux-b matched short peer id b")
	}
	if !sshGatewayHostsMatch("jesse-paradise-park", "paradise-park") {
		t.Fatal("tailnet-prefixed host did not match short peer id")
	}
}

func TestRunSSHGatewayDefaultSyncStreamRejectsAcceptOnlyPeer(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	writeAcceptOnlyGatewayConfig(t, config.NewSharedKey(), 7853)

	var stdout, stderr bytes.Buffer
	err := runSSHGateway(
		[]string{"--authorized-peer", "m4", "--authorized-key-id", "key-123456"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewaySyncStreamCommand
			}
			return ""
		},
	)
	if !errors.Is(err, ErrSSHGatewayCommandRejected) {
		t.Fatalf("runSSHGateway() error = %v, want ErrSSHGatewayCommandRejected", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunSSHGatewayRejectsShellAndUnknownCommands(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		command string
	}{
		{name: "empty command", command: ""},
		{name: "leading whitespace", command: " " + sshprovision.SSHGatewayProbeCommand},
		{name: "trailing whitespace", command: sshprovision.SSHGatewayProbeCommand + " "},
		{name: "newline", command: sshprovision.SSHGatewayProbeCommand + "\n"},
		{name: "shell command", command: "sh"},
		{name: "probe plus extra token", command: sshprovision.SSHGatewayProbeCommand + " --shell"},
		{name: "probe shell separator", command: sshprovision.SSHGatewayProbeCommand + ";sh"},
		{name: "scp", command: "scp -t file"},
		{name: "sftp", command: "sftp"},
		{name: "version", command: "version"},
		{name: "receive", command: "receive"},
		{name: "peer metadata in command", command: sshprovision.SSHGatewayProbeCommand + " --authorized-peer linux-a"},
		{name: "key metadata in command", command: sshprovision.SSHGatewayProbeCommand + " --authorized-key-id key-123456"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			err := runSSHGateway(
				[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"},
				strings.NewReader(""),
				&stdout,
				&stderr,
				func(key string) string {
					if key == "SSH_ORIGINAL_COMMAND" {
						return tc.command
					}
					return ""
				},
			)
			if !errors.Is(err, ErrSSHGatewayCommandRejected) {
				t.Fatalf("runSSHGateway() error = %v, want ErrSSHGatewayCommandRejected", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunSSHGatewayRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing peer", args: []string{"--authorized-key-id", "key-123456"}},
		{name: "invalid peer", args: []string{"--authorized-peer", "bad peer", "--authorized-key-id", "key-123456"}},
		{name: "missing key id", args: []string{"--authorized-peer", "linux-a"}},
		{name: "invalid key id", args: []string{"--authorized-peer", "linux-a", "--authorized-key-id", "short"}},
		{name: "extra arg", args: []string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456", "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			err := runSSHGateway(tc.args, strings.NewReader(""), &stdout, &stderr, func(string) string { return sshprovision.SSHGatewayProbeCommand })
			if err == nil {
				t.Fatal("runSSHGateway() error = nil, want error")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestSSHGatewayForcedCommandMatchesManagedAuthorizedKey(t *testing.T) {
	t.Parallel()

	entry := sshprovision.ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   "AAAAC3NzaC1lZDI1NTE5AAAAIC6JxQKUfHw2JMc2+5ZUTc5xI8QX1sGm8c5C7h4eY7p1",
	}
	managed, err := sshprovision.NewManagedAuthorizedKey(entry)
	if err != nil {
		t.Fatalf("NewManagedAuthorizedKey() error = %v", err)
	}

	want := "/home/jesse/.local/bin/clipfan ssh-gateway --authorized-peer linux-a --authorized-key-id key-123456"
	if got := managed.ForcedCommand(); got != want {
		t.Fatalf("ForcedCommand() = %q, want %q", got, want)
	}
	if sshprovision.SSHGatewayProbeCommand != "probe-authorized-key" {
		t.Fatalf("SSHGatewayProbeCommand = %q, want probe-authorized-key", sshprovision.SSHGatewayProbeCommand)
	}
	if sshprovision.SSHGatewaySyncStreamCommand != "sync-stream" {
		t.Fatalf("SSHGatewaySyncStreamCommand = %q, want sync-stream", sshprovision.SSHGatewaySyncStreamCommand)
	}
}

func TestRunSSHGatewayDoesNotEchoRejectedOriginalCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := runSSHGateway(
		[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return "secret-command with tokens"
			}
			return ""
		},
	)
	if !errors.Is(err, ErrSSHGatewayCommandRejected) {
		t.Fatalf("runSSHGateway() error = %v, want ErrSSHGatewayCommandRejected", err)
	}
	if strings.Contains(stderr.String(), "secret-command") || strings.Contains(err.Error(), "secret-command") {
		t.Fatalf("rejected command leaked: stderr=%q err=%v", stderr.String(), err)
	}
}

func writeGatewayConfig(t *testing.T, sharedKey string, listenPort int) {
	t.Helper()
	path := config.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{
  "config_version": 2,
  "config_revision": 1,
  "shared_key": %q,
  "hostname": "linux-b",
  "transport": "ssh",
  "listen": "127.0.0.1:%d",
  "port": 1,
  "ssh": {
    "sync_key": "/tmp/clipfan-sync",
    "known_hosts": "/tmp/clipfan-known-hosts",
    "peers": [{
      "id": "m4",
      "ssh_host": "m4.example.com",
      "ssh_user": "jesse",
      "ssh_port": 22,
      "enabled": true,
      "accept": true,
      "connect": true,
      "persistent": true,
      "migration_state": "ssh_keys_ready",
      "proof": {
        "accept_key_id": "key-123456",
        "accept_gateway_path": "/tmp/clipfan",
        "accept_verified_at": "2026-06-03T12:00:00Z",
        "accept_verified_by": "regular_ssh",
        "connect_key_id": "key-654321",
        "connect_gateway_path": "/tmp/clipfan",
        "connect_verified_at": "2026-06-03T12:00:00Z",
        "connect_verified_by": "regular_ssh"
      }
    }]
  }
}`, sharedKey, listenPort)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeAcceptOnlyGatewayConfig(t *testing.T, sharedKey string, listenPort int) {
	t.Helper()
	path := config.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{
  "config_version": 2,
  "config_revision": 1,
  "shared_key": %q,
  "hostname": "linux-b",
  "transport": "ssh",
  "listen": "127.0.0.1:%d",
  "ssh": {
    "peers": [{
      "id": "m4",
      "enabled": true,
      "accept": true,
      "migration_state": "ssh_keys_ready",
      "proof": {
        "accept_key_id": "key-123456",
        "accept_gateway_path": "/tmp/clipfan",
        "accept_verified_at": "2026-06-03T12:00:00Z",
        "accept_verified_by": "regular_ssh"
      }
    }]
  }
}`, sharedKey, listenPort)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists or stat failed unexpectedly: %v", path, err)
	}
}

func TestRunSSHGatewayDefaultSyncStreamSurvivesRejectedApply(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	sharedKey := config.NewSharedKey()
	auth, err := transport.NewAuth(sharedKey)
	if err != nil {
		t.Fatal(err)
	}
	applied := make(chan string, 2)
	srv := transport.NewServer("127.0.0.1:0", auth, nil)
	srv.SetCurrentApply(func(c clipboard.Content, origin string) error {
		applied <- c.ID
		if c.ID == "clip-poison" {
			return fmt.Errorf("apply refused")
		}
		return nil
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ServeListener(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		<-serveErr
	})
	writeGatewayConfig(t, sharedKey, ln.Addr().(*net.TCPAddr).Port)

	var stdin, stdout, stderr bytes.Buffer
	initiator := transport.NewSSHSyncStream(auth, "m4", "linux-b", bytes.NewReader(nil), &stdin)
	hello, err := transport.NewSSHStreamHello(auth, transport.SSHStreamPurposeSyncStream, "m4", "linux-b", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := initiator.WriteHello(context.Background(), hello); err != nil {
		t.Fatal(err)
	}
	poison := clipboard.New(clipboard.KindText, []byte("poison"), time.Now().UTC())
	poison.ID = "clip-poison"
	if err := initiator.WriteState(context.Background(), 1, poison, "m4"); err != nil {
		t.Fatal(err)
	}
	good := clipboard.New(clipboard.KindText, []byte("good"), time.Now().UTC())
	good.ID = "clip-good"
	if err := initiator.WriteState(context.Background(), 2, good, "m4"); err != nil {
		t.Fatal(err)
	}

	err = runSSHGateway(
		[]string{"--authorized-peer", "m4", "--authorized-key-id", "key-123456"},
		&stdin,
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewaySyncStreamCommand
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("runSSHGateway() error = %v; stderr=%q — rejected apply must not be fatal", err, stderr.String())
	}

	reader := transport.NewSSHSyncStream(auth, "m4", "linux-b", &stdout, io.Discard)
	reader.SetHelloNonceCache(transport.NewSSHStreamHelloNonceCache())
	if _, err := reader.ReadHello(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	first, err := reader.ReadNext(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.Type != transport.SSHStreamFrameAck || first.Ack.Seq != 1 || first.Ack.Status != "rejected" || first.Ack.Reason != "local_apply_failed" {
		t.Fatalf("first ack = %#v, want seq 1 rejected/local_apply_failed", first)
	}
	second, err := reader.ReadNext(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("stream did not continue past rejected apply: %v", err)
	}
	if second.Type != transport.SSHStreamFrameAck || second.Ack.Seq != 2 || second.Ack.ID != "clip-good" || second.Ack.Status != "applied" {
		t.Fatalf("second ack = %#v, want seq 2 clip-good applied", second)
	}
}

func TestRunSSHGatewayDefaultSyncStreamToleratesCurrentPollFailures(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	sharedKey := config.NewSharedKey()
	auth, err := transport.NewAuth(sharedKey)
	if err != nil {
		t.Fatal(err)
	}
	// No SetCurrentFunc: every GET /v1/current returns 503, so every poll tick fails.
	srv := transport.NewServer("127.0.0.1:0", auth, nil)
	srv.SetCurrentApply(func(c clipboard.Content, origin string) error { return nil })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ServeListener(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		<-serveErr
	})
	writeGatewayConfig(t, sharedKey, ln.Addr().(*net.TCPAddr).Port)

	oldInterval := sshGatewayCurrentPollInterval
	sshGatewayCurrentPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { sshGatewayCurrentPollInterval = oldInterval })

	// stdin carries a hello, then a pause long enough for several failing poll
	// ticks, then EOF. An io.Pipe gives us the pause.
	pr, pw := io.Pipe()
	initiator := transport.NewSSHSyncStream(auth, "m4", "linux-b", bytes.NewReader(nil), pw)
	hello, err := transport.NewSSHStreamHello(auth, transport.SSHStreamPurposeSyncStream, "m4", "linux-b", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = initiator.WriteHello(context.Background(), hello)
		time.Sleep(100 * time.Millisecond) // ~20 failing poll ticks
		_ = pw.Close()
	}()

	var stdout, stderr bytes.Buffer
	err = runSSHGateway(
		[]string{"--authorized-peer", "m4", "--authorized-key-id", "key-123456"},
		pr,
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewaySyncStreamCommand
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("runSSHGateway() error = %v — transient poll failures must not be fatal", err)
	}
}
