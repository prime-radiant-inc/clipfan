package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/storagecheck"
)

func TestRunStoragePreflightPrintsOfflineRepairPromptWithoutDaemonEndpoint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	var gotConfigRoot, gotStateRoot string
	err := runStoragePreflight(nil, &stdout, &stderr, func(configRoot, stateRoot string) ([]storagecheck.Result, error) {
		gotConfigRoot = configRoot
		gotStateRoot = stateRoot
		return []storagecheck.Result{{
			Role:           storagecheck.RootState,
			NormalizedPath: stateRoot,
			Code:           storagecheck.CodeUnsupportedRuntimeStorage,
			StorageClass:   storagecheck.ClassNetwork,
			Reason:         "unsupported_filesystem",
		}}, storagecheck.ErrUnsupportedRuntimeStorage
	})

	if !errors.Is(err, storagecheck.ErrUnsupportedRuntimeStorage) {
		t.Fatalf("error = %v, want ErrUnsupportedRuntimeStorage", err)
	}
	if gotConfigRoot == "" || gotStateRoot == "" {
		t.Fatalf("checker roots = %q, %q; want local config/state roots", gotConfigRoot, gotStateRoot)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	text := stderr.String()
	for _, want := range []string{"unsupported_runtime_storage", "daemon_endpoint_required: false", "Move clipfan config and daemon state"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stderr missing %q:\n%s", want, text)
		}
	}
}

func TestRunStoragePreflightReportsSuccess(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	err := runStoragePreflight(nil, &stdout, &stderr, func(string, string) ([]storagecheck.Result, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "clipfan runtime storage is local and supported\n" {
		t.Fatalf("stdout = %q, want success message", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
