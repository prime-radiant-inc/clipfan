package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/localdaemon"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

type removeHostDeps struct {
	loadConfig func() (*config.Config, error)
	discover   func(*config.Config) (localdaemon.Endpoint, error)
	do         func(*http.Request) (*http.Response, error)
	now        func() time.Time
}

func RunRemoveHost(args []string, stdout io.Writer, stderr io.Writer) error {
	return runRemoveHostWithDeps(args, stdout, stderr, removeHostDeps{})
}

func runRemoveHostWithDeps(args []string, stdout io.Writer, stderr io.Writer, deps removeHostDeps) error {
	fs := flag.NewFlagSet("remove-host", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reason := fs.String("reason", "user_deleted", "stable removal reason")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: clipfan remove-host <host>")
	}
	hostID := fs.Arg(0)
	if err := config.ValidateHostRemoveTarget(hostID); err != nil {
		return fmt.Errorf("invalid host: %w", err)
	}

	loadConfig := deps.loadConfig
	if loadConfig == nil {
		loadConfig = config.Load
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	discover := deps.discover
	if discover == nil {
		discover = func(cfg *config.Config) (localdaemon.Endpoint, error) {
			return localdaemon.Discover(cfg, localdaemon.PurposeSigned, localdaemon.Options{})
		}
	}
	endpoint, err := discover(cfg)
	if err != nil {
		return err
	}
	do := deps.do
	if do == nil {
		client := &http.Client{Timeout: 3 * time.Second}
		do = client.Do
	}
	now := deps.now
	if now == nil {
		now = time.Now
	}

	status, err := removeHostReadRevision(context.Background(), endpoint, auth, do)
	if err != nil {
		return err
	}
	if status.RevisionState == "" {
		return fmt.Errorf("missing_revision_state")
	}
	if status.RevisionState == config.RevisionStateVersioned && (status.ConfigRevision == nil || *status.ConfigRevision == 0) {
		return config.ErrConfigRevisionConflict
	}

	req := config.HostRemoveRequest{
		ExpectedRevisionState:  status.RevisionState,
		ExpectedConfigRevision: status.ConfigRevision,
		Reason:                 *reason,
		LogID:                  fmt.Sprintf("host-remove-%d", now().UTC().Unix()),
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	var result config.HostRemoveResult
	if err := removeHostSignedJSON(context.Background(), endpoint, auth, do, http.MethodDelete, "/v1/config/peers/"+url.PathEscape(hostID), body, &result); err != nil {
		return err
	}
	revision := "none"
	if result.ConfigRevision != nil {
		revision = fmt.Sprintf("%d", *result.ConfigRevision)
	}
	_, _ = fmt.Fprintf(stdout, "host_remove_complete host=%s removed_static_peer=%t removed_ssh_peer=%t config_revision=%s restart_required=true\n",
		result.HostID, result.RemovedStaticPeer, result.RemovedSSHPeer, revision)
	return nil
}

func removeHostReadRevision(ctx context.Context, endpoint localdaemon.Endpoint, auth *transport.Auth, do func(*http.Request) (*http.Response, error)) (config.RevisionStatus, error) {
	var status config.RevisionStatus
	err := removeHostSignedJSON(ctx, endpoint, auth, do, http.MethodGet, "/v1/peers", nil, &status)
	return status, err
}

func removeHostSignedJSON(ctx context.Context, endpoint localdaemon.Endpoint, auth *transport.Auth, do func(*http.Request) (*http.Response, error), method string, requestURI string, body []byte, out any) error {
	signed, err := localdaemon.NewSignedRequest(ctx, endpoint, auth, method, requestURI, body, localdaemon.SignedRequestOptions{})
	if err != nil {
		return err
	}
	resp, err := do(signed.Request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	authVersion := resp.Header.Get(transport.HeaderAuthVersion)
	if authVersion == "" {
		authVersion = transport.AuthVersionRequestHMAC
	}
	if err := auth.VerifyResponseWithAuthVersion(signed.Nonce, data, resp.Header.Get("X-Clipfan-Response-Sig"), authVersion); err != nil {
		return fmt.Errorf("verify signed response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := fmt.Sprintf("http_%d", resp.StatusCode)
		var apiErr struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(data, &apiErr); err == nil && apiErr.Code != "" {
			code = apiErr.Code
		}
		return fmt.Errorf("%s", code)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}
