package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/version"
)

func TestRunVersionCommandPrintsBuildVersion(t *testing.T) {
	oldVersion := version.Version
	version.Version = "test-version"
	defer func() { version.Version = oldVersion }()

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"version"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if got := stdout.String(); got != "test-version\n" {
		t.Fatalf("stdout = %q, want test-version newline", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunVersionJSONCommandPrintsConfigV2CapabilityWithoutConfigLoad(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	oldVersion := version.Version
	version.Version = "test-version"
	defer func() { version.Version = oldVersion }()

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"version", "--json"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Version      string `json:"version"`
		Capabilities struct {
			ConfigV2            bool   `json:"config_v2"`
			ConfigV2LocalAuth   string `json:"config_v2_local_auth"`
			PeerHTTPRuntimeGate bool   `json:"peer_http_runtime_gate"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("version --json payload is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Version != "test-version" {
		t.Fatalf("version = %q, want test-version", payload.Version)
	}
	if !payload.Capabilities.ConfigV2 {
		t.Fatal("version --json did not advertise config_v2 capability")
	}
	if payload.Capabilities.ConfigV2LocalAuth != "clipfan-v1/request-hmac" {
		t.Fatalf("config_v2_local_auth = %q, want clipfan-v1/request-hmac", payload.Capabilities.ConfigV2LocalAuth)
	}
	if !payload.Capabilities.PeerHTTPRuntimeGate {
		t.Fatal("version --json did not advertise peer HTTP runtime gate capability")
	}
	_, err := os.Stat(filepath.Join(configRoot, "clipfan", "config.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("version --json touched config file: stat err = %v", err)
	}
}
