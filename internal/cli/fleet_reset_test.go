package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/daemon"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

func TestRunLocalFleetResetRequiresTypedConfirmationBeforeStatusRead(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	resetCalled := false

	err := runLocalFleetReset(nil, &stdout, &stderr, func(context.Context, daemon.OfflineLocalFleetResetOptions) (config.LocalFleetResetResult, string, error) {
		resetCalled = true
		return config.LocalFleetResetResult{}, "", nil
	})
	if !errors.Is(err, config.ErrFleetResetConfirmationRequired) {
		t.Fatalf("error = %v, want ErrFleetResetConfirmationRequired", err)
	}
	if resetCalled {
		t.Fatal("reset ran without typed confirmation")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunLocalFleetResetReadsRevisionAndRunsOfflineReset(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	configPath := filepath.Join(configRoot, "clipfan", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853","hostname":"m4"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	var got daemon.OfflineLocalFleetResetOptions
	err := runLocalFleetResetWithGate([]string{"--confirm", config.LocalFleetResetConfirmation, "--wait-daemon-lock", "25ms"}, &stdout, &stderr, true, func(_ context.Context, opts daemon.OfflineLocalFleetResetOptions) (config.LocalFleetResetResult, string, error) {
		got = opts
		revision := uint64(8)
		return config.LocalFleetResetResult{
			HostID:         "m4",
			ConfigRevision: &revision,
			RevisionState:  config.RevisionStateVersioned,
			BackupPath:     configPath + ".fleet-reset.20260602T203000Z.bak",
		}, configPath + ".fleet-reset.20260602T203000Z.bak", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigPath != configPath || got.StateDir != filepath.Join(stateRoot, "clipfan") {
		t.Fatalf("reset roots = %q/%q, want %q/%q", got.ConfigPath, got.StateDir, configPath, filepath.Join(stateRoot, "clipfan"))
	}
	if got.WaitTimeout != 25*time.Millisecond {
		t.Fatalf("WaitTimeout = %s, want 25ms", got.WaitTimeout)
	}
	if got.Request.Confirmation != config.LocalFleetResetConfirmation ||
		got.Request.ExpectedRevisionState != config.RevisionStateVersioned ||
		got.Request.ExpectedConfigRevision == nil ||
		*got.Request.ExpectedConfigRevision != 7 {
		t.Fatalf("request = %#v, want current revision 7", got.Request)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{"local_fleet_reset_complete", "hostname=m4", "config_revision=8", "backup="} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunLocalFleetResetPublicProfileFailsClosedBeforeConfigV2Cutover(t *testing.T) {
	if releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires public generated ConfigV2WriteEnabled=false profile")
	}
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	configPath := filepath.Join(configRoot, "clipfan", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853","hostname":"m4"}`)
	if err := os.WriteFile(configPath, before, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := RunLocalFleetReset([]string{"--confirm", config.LocalFleetResetConfirmation, "--wait-daemon-lock", "25ms"}, &stdout, &stderr)
	if !errors.Is(err, config.ErrConfigV2WritesDisabled) {
		t.Fatalf("error = %v, want ErrConfigV2WritesDisabled", err)
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("public false-gate reset changed config\nbefore: %s\nafter: %s", before, after)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, statErr := os.Lstat(filepath.Join(stateRoot, "clipfan", "daemon.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("public false-gate path created daemon lock: %v", statErr)
	}
}

func TestGeneratedLocalFleetResetSucceedsWithInternalConfigV2Gate(t *testing.T) {
	if !releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires internal/test generated ConfigV2WriteEnabled=true profile")
	}
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	configPath := filepath.Join(configRoot, "clipfan", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"config_version":2,"config_revision":7,"shared_key":"not-base64","listen":"127.0.0.1:7853","discovery":"tailscale","static_peers":["old"],"hostname":"m4"}`)
	if err := os.WriteFile(configPath, before, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := RunLocalFleetReset([]string{"--confirm", config.LocalFleetResetConfirmation, "--wait-daemon-lock", "25ms"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{"local_fleet_reset_complete", "hostname=m4", "config_revision=8", ".fleet-reset."} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	after := readCLIJSONMap(t, configPath)
	if after["config_version"] != float64(2) || after["config_revision"] != float64(8) {
		t.Fatalf("version/revision = %#v/%#v, want 2/8", after["config_version"], after["config_revision"])
	}
	if after["listen"] != "127.0.0.1:7853" || after["discovery"] != "static" || after["hostname"] != "m4" {
		t.Fatalf("reset config = %#v", after)
	}
	if _, ok := after["static_peers"]; ok {
		t.Fatalf("reset kept static_peers: %#v", after["static_peers"])
	}
	key, ok := after["shared_key"].(string)
	if !ok || key == "" || key == "not-base64" {
		t.Fatalf("shared_key = %#v, want fresh key", after["shared_key"])
	}
	if _, statErr := os.Lstat(filepath.Join(stateRoot, "clipfan", "daemon.lock")); statErr != nil {
		t.Fatalf("daemon lock stat = %v, want lock file from offline reset orchestration", statErr)
	}
}

func readCLIJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
