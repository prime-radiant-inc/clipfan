package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

func TestParseKnownHostLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		ok      bool
		marker  string
		keyType string
		key     string
	}{
		{"blank", "   ", false, "", "", ""},
		{"comment", "# Host host.example found: line 1", false, "", "", ""},
		{"plain", "host.example ssh-ed25519 AAAAKEY", true, "", "ssh-ed25519", "AAAAKEY"},
		{"hashed", "|1|abc=|def= ssh-ed25519 AAAAKEY", true, "", "ssh-ed25519", "AAAAKEY"},
		{"marker", "@cert-authority host.example ssh-rsa AAAACA", true, "@cert-authority", "ssh-rsa", "AAAACA"},
		{"too short", "host.example ssh-ed25519", false, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseKnownHostLine(tt.line)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.Marker != tt.marker || got.KeyType != tt.keyType || got.Key != tt.key {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestReconcileTrustedHostKeys(t *testing.T) {
	scan := func(keyType, key, line string) scannedHostKey {
		return scannedHostKey{KeyType: keyType, Key: key, Line: line}
	}
	existing := func(marker, keyType, key string) knownHostLine {
		return knownHostLine{Marker: marker, KeyType: keyType, Key: key}
	}

	t.Run("empty existing appends all", func(t *testing.T) {
		out, err := reconcileTrustedHostKeys(nil, []scannedHostKey{scan("ssh-ed25519", "K1", "L1")})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0] != "L1" {
			t.Fatalf("out = %v", out)
		}
	})

	t.Run("same key skips", func(t *testing.T) {
		out, err := reconcileTrustedHostKeys(
			[]knownHostLine{existing("", "ssh-ed25519", "K1")},
			[]scannedHostKey{scan("ssh-ed25519", "K1", "L1")},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 0 {
			t.Fatalf("expected no append, got %v", out)
		}
	})

	t.Run("different key conflicts", func(t *testing.T) {
		if _, err := reconcileTrustedHostKeys(
			[]knownHostLine{existing("", "ssh-ed25519", "K1")},
			[]scannedHostKey{scan("ssh-ed25519", "K2", "L2")},
		); err == nil {
			t.Fatal("expected conflict")
		}
	})

	t.Run("marker conflicts", func(t *testing.T) {
		if _, err := reconcileTrustedHostKeys(
			[]knownHostLine{existing("@cert-authority", "ssh-ed25519", "K1")},
			[]scannedHostKey{scan("ssh-ed25519", "K1", "L1")},
		); err == nil {
			t.Fatal("expected conflict on marker")
		}
	})

	t.Run("new key type appends alongside existing different type", func(t *testing.T) {
		out, err := reconcileTrustedHostKeys(
			[]knownHostLine{existing("", "ssh-rsa", "R1")},
			[]scannedHostKey{scan("ssh-ed25519", "K1", "L1")},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0] != "L1" {
			t.Fatalf("out = %v", out)
		}
	})

	t.Run("divergent same-type scan conflicts", func(t *testing.T) {
		if _, err := reconcileTrustedHostKeys(nil, []scannedHostKey{
			scan("ssh-ed25519", "K1", "L1"),
			scan("ssh-ed25519", "K2", "L2"),
		}); err == nil {
			t.Fatal("expected conflict on divergent scan keys")
		}
	})
}

type fakeExitError struct{ code int }

func (e fakeExitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e fakeExitError) ExitCode() int { return e.code }

type fakeTrustRunner struct {
	keyscanLine  string
	keygenExit   int    // 1 means ssh-keygen -F found no match
	keygenLookup string // ssh-keygen -F stdout when found
	keyscanCalls int
	keygenCalls  int
}

func (r *fakeTrustRunner) Run(_ context.Context, command sshprovision.SSHCommand) (sshprovision.CommandOutput, error) {
	switch {
	case command.Args[0] == "ssh" && len(command.Args) > 1 && command.Args[1] == "-G":
		host := command.Args[len(command.Args)-1]
		return sshprovision.CommandOutput{Stdout: []byte("hostname " + host + "\nport 22\n")}, nil
	case command.Args[0] == "ssh-keyscan":
		r.keyscanCalls++
		return sshprovision.CommandOutput{Stdout: []byte(r.keyscanLine)}, nil
	case command.Args[0] == "ssh-keygen" && len(command.Args) > 1 && command.Args[1] == "-F":
		r.keygenCalls++
		if r.keygenExit == 1 {
			return sshprovision.CommandOutput{}, fakeExitError{code: 1}
		}
		return sshprovision.CommandOutput{Stdout: []byte(r.keygenLookup)}, nil
	}
	return sshprovision.CommandOutput{}, errors.New("unexpected: " + strings.Join(command.Args, " "))
}

// These exercise the REAL ExecCommandRunner against the system ssh-keygen, so
// the exit-1 detection is verified through the genuine SSHCommandError ->
// errors.Join -> *exec.ExitError chain (not just the fake), and the parser is
// proven against real `ssh-keygen -F` output (which prepends a "# Host ... found"
// comment line).

func TestLookupExistingRegularHostKeysRealRunnerNoMatch(t *testing.T) {
	entries, err := lookupExistingRegularHostKeys(context.Background(), sshprovision.ExecCommandRunner{}, "no-such-host.example", "/dev/null")
	if err != nil {
		t.Fatalf("expected nil error when ssh-keygen -F exits 1 (no match), got %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %+v", entries)
	}
}

func TestLookupExistingRegularHostKeysRealRunnerFound(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "known_hosts")
	line := "host.example ssh-ed25519 " + testDirectProvisionEd25519Key
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := lookupExistingRegularHostKeys(context.Background(), sshprovision.ExecCommandRunner{}, "host.example", path)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(entries) != 1 || entries[0].KeyType != "ssh-ed25519" || entries[0].Key != testDirectProvisionEd25519Key {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestTrustEndpointAppendsToFreshFile(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "regular_known_hosts")
	host := "host.example"
	runner := &fakeTrustRunner{keyscanLine: host + " ssh-ed25519 " + testDirectProvisionEd25519Key}

	if err := trustEndpoint(context.Background(), runner, rosterEndpoint{
		ID: "h", SSHUser: "jesse", SSHHost: host, SSHPort: 22,
	}, path); err != nil {
		t.Fatalf("trustEndpoint: %v", err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), testDirectProvisionEd25519Key) {
		t.Fatalf("file missing trusted key: %q", data)
	}
	if runner.keygenCalls != 0 {
		t.Fatalf("ssh-keygen -F should be skipped when the file is absent, calls=%d", runner.keygenCalls)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestTrustScannedHostKeysSkipsAlreadyTrusted(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "regular_known_hosts")
	host := "host.example"
	keyLine := host + " ssh-ed25519 " + testDirectProvisionEd25519Key
	if err := os.WriteFile(path, []byte(keyLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &fakeTrustRunner{keygenExit: 0, keygenLookup: keyLine}
	if err := trustScannedHostKeys(context.Background(), runner, host, 22, keyLine+"\n", path); err != nil {
		t.Fatalf("trustScannedHostKeys: %v", err)
	}

	data, _ := os.ReadFile(path)
	if strings.Count(string(data), testDirectProvisionEd25519Key) != 1 {
		t.Fatalf("key was duplicated: %q", data)
	}
}

func TestTrustScannedHostKeysConflictRefusesWrite(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "regular_known_hosts")
	host := "host.example"
	existingLine := host + " ssh-ed25519 " + testDirectProvisionEd25519Key
	scannedLine := host + " ssh-ed25519 " + testDirectProvisionOtherEd25519Key
	if err := os.WriteFile(path, []byte(existingLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &fakeTrustRunner{keygenExit: 0, keygenLookup: existingLine}
	if err := trustScannedHostKeys(context.Background(), runner, host, 22, scannedLine+"\n", path); err == nil {
		t.Fatalf("expected conflict error")
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), testDirectProvisionOtherEd25519Key) {
		t.Fatalf("conflicting key was written: %q", data)
	}
}

func TestTrustScannedHostKeysAppendsWhenAbsentFromExistingFile(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "regular_known_hosts")
	host := "host.example"
	if err := os.WriteFile(path, []byte("other.example ssh-rsa AAAAOTHER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scannedLine := host + " ssh-ed25519 " + testDirectProvisionEd25519Key

	runner := &fakeTrustRunner{keygenExit: 1}
	if err := trustScannedHostKeys(context.Background(), runner, host, 22, scannedLine+"\n", path); err != nil {
		t.Fatalf("trustScannedHostKeys: %v", err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "other.example") || !strings.Contains(string(data), testDirectProvisionEd25519Key) {
		t.Fatalf("file = %q", data)
	}
	if runner.keygenCalls != 1 {
		t.Fatalf("expected one ssh-keygen -F call, got %d", runner.keygenCalls)
	}
}

// The two below drive the WHOLE refuse path through the real ssh-keygen -F (not
// the fake), proving a genuinely different existing key, and a @cert-authority
// marker surfaced by real ssh-keygen -F, both make trust refuse without writing.

func TestTrustScannedHostKeysRealRunnerConflictRefusesWrite(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "known_hosts")
	host := "host.example"
	existingLine := host + " ssh-ed25519 " + testDirectProvisionEd25519Key
	scannedLine := host + " ssh-ed25519 " + testDirectProvisionOtherEd25519Key
	if err := os.WriteFile(path, []byte(existingLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := trustScannedHostKeys(context.Background(), sshprovision.ExecCommandRunner{}, host, 22, scannedLine+"\n", path); err == nil {
		t.Fatalf("expected conflict via real ssh-keygen -F")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), testDirectProvisionOtherEd25519Key) {
		t.Fatalf("conflicting key was written: %q", data)
	}
}

func TestTrustScannedHostKeysRealRunnerMarkerRefusesWrite(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "known_hosts")
	host := "host.example"
	caLine := "@cert-authority " + host + " ssh-ed25519 " + testDirectProvisionEd25519Key
	scannedLine := host + " ssh-ed25519 " + testDirectProvisionOtherEd25519Key
	if err := os.WriteFile(path, []byte(caLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := trustScannedHostKeys(context.Background(), sshprovision.ExecCommandRunner{}, host, 22, scannedLine+"\n", path); err == nil {
		t.Fatalf("expected marker conflict via real ssh-keygen -F")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), testDirectProvisionOtherEd25519Key) {
		t.Fatalf("key was written despite CA marker: %q", data)
	}
}
