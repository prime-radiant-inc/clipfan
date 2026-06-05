package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/localdaemon"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func TestRunRemoveHostReadsRevisionAndDeletesViaSignedDaemon(t *testing.T) {
	key := config.NewSharedKey()
	auth, err := transport.NewAuth(key)
	if err != nil {
		t.Fatal(err)
	}
	s := transport.NewServer(":0", auth, func(clipboard.Content, string) {}, func() any {
		revision := uint64(7)
		version := 2
		return map[string]any{
			"origin":          "m4",
			"peers":           []any{},
			"config_version":  &version,
			"config_revision": &revision,
			"revision_state":  config.RevisionStateVersioned,
		}
	})
	s.SetRequiredLocalAuthVersion(transport.AuthVersionRequestHMAC)
	var gotHost string
	var gotRequest config.HostRemoveRequest
	s.SetHostRemove(func(hostID string, body []byte) (any, *transport.HandlerError) {
		gotHost = hostID
		if err := json.Unmarshal(body, &gotRequest); err != nil {
			t.Fatalf("delete body: %v", err)
		}
		revision := uint64(8)
		version := 2
		return config.HostRemoveResult{
			HostID:            hostID,
			RemovedStaticPeer: true,
			RemovedSSHPeer:    true,
			ConfigRevision:    &revision,
			ConfigVersion:     &version,
			RevisionState:     config.RevisionStateVersioned,
		}, nil
	})
	server := httptest.NewServer(s.Handler())
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err = runRemoveHostWithDeps(
		[]string{"magic-kingdom"},
		&stdout,
		&stderr,
		removeHostDeps{
			loadConfig: func() (*config.Config, error) {
				return &config.Config{SharedKey: key, Listen: "127.0.0.1:7853"}, nil
			},
			discover: func(*config.Config) (localdaemon.Endpoint, error) {
				return localdaemon.Endpoint{BaseURL: server.URL, Purpose: localdaemon.PurposeSigned}, nil
			},
			do:  server.Client().Do,
			now: func() time.Time { return time.Unix(1780257600, 0) },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if gotHost != "magic-kingdom" {
		t.Fatalf("host = %q, want magic-kingdom", gotHost)
	}
	if gotRequest.ExpectedRevisionState != config.RevisionStateVersioned ||
		gotRequest.ExpectedConfigRevision == nil ||
		*gotRequest.ExpectedConfigRevision != 7 ||
		gotRequest.Reason != "user_deleted" ||
		gotRequest.LogID != "host-remove-1780257600" {
		t.Fatalf("delete request = %#v, want current revision and stable log id", gotRequest)
	}
	for _, want := range []string{
		"host_remove_complete",
		"host=magic-kingdom",
		"removed_static_peer=true",
		"removed_ssh_peer=true",
		"config_revision=8",
		"restart_required=true",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunRemoveHostReturnsSignedDaemonErrorCode(t *testing.T) {
	key := config.NewSharedKey()
	auth, err := transport.NewAuth(key)
	if err != nil {
		t.Fatal(err)
	}
	s := transport.NewServer(":0", auth, func(clipboard.Content, string) {}, func() any {
		revision := uint64(7)
		return map[string]any{
			"origin":          "m4",
			"peers":           []any{},
			"config_revision": &revision,
			"revision_state":  config.RevisionStateVersioned,
		}
	})
	s.SetRequiredLocalAuthVersion(transport.AuthVersionRequestHMAC)
	s.SetHostRemove(func(hostID string, body []byte) (any, *transport.HandlerError) {
		return nil, &transport.HandlerError{Status: http.StatusNotFound, Code: "host_not_found"}
	})
	server := httptest.NewServer(s.Handler())
	defer server.Close()

	err = runRemoveHostWithDeps(
		[]string{"missing"},
		ioDiscard{},
		ioDiscard{},
		removeHostDeps{
			loadConfig: func() (*config.Config, error) {
				return &config.Config{SharedKey: key, Listen: "127.0.0.1:7853"}, nil
			},
			discover: func(*config.Config) (localdaemon.Endpoint, error) {
				return localdaemon.Endpoint{BaseURL: server.URL, Purpose: localdaemon.PurposeSigned}, nil
			},
			do: server.Client().Do,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "host_not_found") {
		t.Fatalf("error = %v, want host_not_found", err)
	}
}

func TestRunRemoveHostRequiresUsableRevisionBeforeDelete(t *testing.T) {
	key := config.NewSharedKey()
	auth, err := transport.NewAuth(key)
	if err != nil {
		t.Fatal(err)
	}
	s := transport.NewServer(":0", auth, func(clipboard.Content, string) {}, func() any {
		return map[string]any{
			"origin":         "m4",
			"peers":          []any{},
			"revision_state": config.RevisionStateVersioned,
		}
	})
	s.SetRequiredLocalAuthVersion(transport.AuthVersionRequestHMAC)
	var removeCalled bool
	s.SetHostRemove(func(hostID string, body []byte) (any, *transport.HandlerError) {
		removeCalled = true
		return nil, nil
	})
	server := httptest.NewServer(s.Handler())
	defer server.Close()

	err = runRemoveHostWithDeps(
		[]string{"magic-kingdom"},
		ioDiscard{},
		ioDiscard{},
		removeHostDeps{
			loadConfig: func() (*config.Config, error) {
				return &config.Config{SharedKey: key, Listen: "127.0.0.1:7853"}, nil
			},
			discover: func(*config.Config) (localdaemon.Endpoint, error) {
				return localdaemon.Endpoint{BaseURL: server.URL, Purpose: localdaemon.PurposeSigned}, nil
			},
			do: server.Client().Do,
		},
	)
	if !errors.Is(err, config.ErrConfigRevisionConflict) {
		t.Fatalf("error = %v, want ErrConfigRevisionConflict", err)
	}
	if removeCalled {
		t.Fatal("delete called despite missing revision")
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
