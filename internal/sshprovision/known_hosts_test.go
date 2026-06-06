package sshprovision

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testEd25519Key      = "AAAAC3NzaC1lZDI1NTE5AAAAIC6JxQKUfHw2JMc2+5ZUTc5xI8QX1sGm8c5C7h4eY7p1"
	testOtherEd25519Key = "AAAAC3NzaC1lZDI1NTE5AAAAIHP7O1LPaDr6RfFdqHtKc9m8gw98RK54GpcfwoAK2JhH"
	testRSAKey          = "AAAAB3NzaC1yc2EAAAADAQABAAABAQC7kMUR5W3sljGXhgmwsMOFGv17tZuxKQnF4k8sJgMhaY20"
)

func TestKnownHostsPattern(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		host string
		port int
		want string
	}{
		{name: "dns default port", host: "Example.COM.", port: 22, want: "example.com"},
		{name: "dns non default port", host: "Example.COM", port: 2200, want: "[example.com]:2200"},
		{name: "ipv4 default port", host: "192.0.2.10", port: 22, want: "192.0.2.10"},
		{name: "ipv6 default port", host: "2001:DB8::1", port: 22, want: "2001:db8::1"},
		{name: "ipv6 non default port", host: "2001:DB8::1", port: 2200, want: "[2001:db8::1]:2200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := KnownHostsPattern(tc.host, tc.port)
			if err != nil {
				t.Fatalf("KnownHostsPattern() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("KnownHostsPattern() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestKnownHostsPatternRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		host string
		port int
	}{
		{name: "empty host", host: "", port: 22},
		{name: "host with user", host: "me@example.com", port: 22},
		{name: "host with port suffix", host: "example.com:22", port: 22},
		{name: "zero port", host: "example.com", port: 0},
		{name: "too large port", host: "example.com", port: 65536},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := KnownHostsPattern(tc.host, tc.port); err == nil {
				t.Fatal("KnownHostsPattern() error = nil, want error")
			}
		})
	}
}

func TestParseKnownHostScanLine(t *testing.T) {
	t.Parallel()

	pin, err := ParseKnownHostScanLine("Example.COM", 2200, "[example.com]:2200 ssh-ed25519 "+testEd25519Key+" comment")
	if err != nil {
		t.Fatalf("ParseKnownHostScanLine() error = %v", err)
	}
	if pin.Pattern != "[example.com]:2200" {
		t.Fatalf("Pattern = %q", pin.Pattern)
	}
	if pin.KeyType != "ssh-ed25519" {
		t.Fatalf("KeyType = %q", pin.KeyType)
	}
	if pin.PublicKey != testEd25519Key {
		t.Fatalf("PublicKey = %q", pin.PublicKey)
	}
	if got := pin.Line(); got != "[example.com]:2200 ssh-ed25519 "+testEd25519Key {
		t.Fatalf("Line() = %q", got)
	}
}

func TestParseKnownHostScanLineRejectsWrongHost(t *testing.T) {
	t.Parallel()

	_, err := ParseKnownHostScanLine("example.com", 22, "other.example ssh-ed25519 "+testEd25519Key)
	if !errors.Is(err, ErrKnownHostMismatch) {
		t.Fatalf("ParseKnownHostScanLine() error = %v, want ErrKnownHostMismatch", err)
	}
}

func TestUpsertKnownHostPinCreatesFileAtomically(t *testing.T) {
	t.Parallel()

	path := filepath.Join(knownHostsTempDir(t), "clipfan", "ssh", "known_hosts")
	pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)

	if err := UpsertKnownHostPin(path, pin); err != nil {
		t.Fatalf("UpsertKnownHostPin() error = %v", err)
	}

	assertFileBody(t, path, "example.com ssh-ed25519 "+testEd25519Key+"\n")
	assertMode(t, path, 0o600)
	assertMode(t, filepath.Dir(path), 0o700)
	assertMode(t, path+".lock", 0o600)
	assertNoKnownHostsTemps(t, filepath.Dir(path))
}

func TestUpsertKnownHostPinIsIdempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(knownHostsTempDir(t), "known_hosts")
	pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)
	if err := os.WriteFile(path, []byte("example.com ssh-ed25519 "+testEd25519Key+"\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	before := mustReadFile(t, path)

	if err := UpsertKnownHostPin(path, pin); err != nil {
		t.Fatalf("UpsertKnownHostPin() error = %v", err)
	}

	after := mustReadFile(t, path)
	if after != before {
		t.Fatalf("known_hosts changed on idempotent upsert:\n got %q\nwant %q", after, before)
	}
}

func TestUpsertKnownHostPinRoundTripsNonDefaultPort(t *testing.T) {
	t.Parallel()

	path := filepath.Join(knownHostsTempDir(t), "known_hosts")
	pin := mustKnownHostPin(t, "example.com", 2200, "ssh-ed25519", testEd25519Key)

	if err := UpsertKnownHostPin(path, pin); err != nil {
		t.Fatalf("UpsertKnownHostPin() error = %v", err)
	}
	if err := VerifyKnownHostPin(path, pin); err != nil {
		t.Fatalf("VerifyKnownHostPin() error = %v", err)
	}
	assertFileBody(t, path, "[example.com]:2200 ssh-ed25519 "+testEd25519Key+"\n")
}

func TestUpsertKnownHostPinPreservesUnmanagedLinesAndAppends(t *testing.T) {
	t.Parallel()

	path := filepath.Join(knownHostsTempDir(t), "known_hosts")
	before := strings.Join([]string{
		"# user managed",
		"github.com ssh-ed25519 " + testEd25519Key,
		"other.example ssh-rsa " + testRSAKey,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)

	if err := UpsertKnownHostPin(path, pin); err != nil {
		t.Fatalf("UpsertKnownHostPin() error = %v", err)
	}

	want := before + "example.com ssh-ed25519 " + testEd25519Key + "\n"
	assertFileBody(t, path, want)
}

func TestUpsertKnownHostPinAllowsSameTargetDifferentKeyType(t *testing.T) {
	t.Parallel()

	path := filepath.Join(knownHostsTempDir(t), "known_hosts")
	before := "example.com ssh-rsa " + testRSAKey + "\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)

	if err := UpsertKnownHostPin(path, pin); err != nil {
		t.Fatalf("UpsertKnownHostPin() error = %v", err)
	}

	assertFileBody(t, path, before+"example.com ssh-ed25519 "+testEd25519Key+"\n")
}

func TestUpsertKnownHostPinRejectsMismatchesAndLeavesFileUnchanged(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		before string
	}{
		{
			name:   "same key type different key",
			before: "example.com ssh-ed25519 " + testOtherEd25519Key + "\n",
		},
		{
			name:   "same target marker line",
			before: "@revoked example.com ssh-ed25519 " + testEd25519Key + "\n",
		},
		{
			name:   "same target wildcard line",
			before: "*.com ssh-ed25519 " + testEd25519Key + "\n",
		},
		{
			name:   "hashed line cannot be proven unrelated",
			before: "|1|saltsaltsalt=|hashhashhash= ssh-ed25519 " + testEd25519Key + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(knownHostsTempDir(t), "known_hosts")
			if err := os.WriteFile(path, []byte(tc.before), 0o600); err != nil {
				t.Fatalf("write known_hosts: %v", err)
			}
			pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)

			err := UpsertKnownHostPin(path, pin)
			if !errors.Is(err, ErrKnownHostMismatch) {
				t.Fatalf("UpsertKnownHostPin() error = %v, want ErrKnownHostMismatch", err)
			}
			assertFileBody(t, path, tc.before)
			assertNoKnownHostsTemps(t, filepath.Dir(path))
		})
	}
}

func TestUpsertKnownHostPinRejectsBracketedWildcardMismatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		before string
	}{
		{
			name:   "any host on port",
			before: "[*]:2200 ssh-ed25519 " + testEd25519Key + "\n",
		},
		{
			name:   "dns wildcard on port",
			before: "[*.com]:2200 ssh-ed25519 " + testEd25519Key + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(knownHostsTempDir(t), "known_hosts")
			if err := os.WriteFile(path, []byte(tc.before), 0o600); err != nil {
				t.Fatalf("write known_hosts: %v", err)
			}
			pin := mustKnownHostPin(t, "example.com", 2200, "ssh-ed25519", testEd25519Key)

			err := UpsertKnownHostPin(path, pin)
			if !errors.Is(err, ErrKnownHostMismatch) {
				t.Fatalf("UpsertKnownHostPin() error = %v, want ErrKnownHostMismatch", err)
			}
			err = VerifyKnownHostPin(path, pin)
			if !errors.Is(err, ErrKnownHostMismatch) {
				t.Fatalf("VerifyKnownHostPin() error = %v, want ErrKnownHostMismatch", err)
			}
			assertFileBody(t, path, tc.before)
		})
	}
}

func TestNewKnownHostPinRejectsKeyTypeMismatch(t *testing.T) {
	t.Parallel()

	if _, err := NewKnownHostPin("example.com", 22, "ssh-rsa", testEd25519Key); !errors.Is(err, ErrInvalidKnownHostPin) {
		t.Fatalf("NewKnownHostPin() error = %v, want ErrInvalidKnownHostPin", err)
	}
}

func TestVerifyKnownHostPin(t *testing.T) {
	t.Parallel()

	path := filepath.Join(knownHostsTempDir(t), "known_hosts")
	pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)
	if err := os.WriteFile(path, []byte("alias.example,example.com ssh-ed25519 "+testEd25519Key+" comment\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	if err := VerifyKnownHostPin(path, pin); err != nil {
		t.Fatalf("VerifyKnownHostPin() error = %v", err)
	}
}

func TestVerifyKnownHostPinReportsMissingAndMismatch(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(knownHostsTempDir(t), "known_hosts")
		pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)
		if err := os.WriteFile(path, []byte("other.example ssh-ed25519 "+testEd25519Key+"\n"), 0o600); err != nil {
			t.Fatalf("write known_hosts: %v", err)
		}

		err := VerifyKnownHostPin(path, pin)
		if !errors.Is(err, ErrKnownHostNotFound) {
			t.Fatalf("VerifyKnownHostPin() error = %v, want ErrKnownHostNotFound", err)
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(knownHostsTempDir(t), "known_hosts")
		pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)
		if err := os.WriteFile(path, []byte("example.com ssh-ed25519 "+testOtherEd25519Key+"\n"), 0o600); err != nil {
			t.Fatalf("write known_hosts: %v", err)
		}

		err := VerifyKnownHostPin(path, pin)
		if !errors.Is(err, ErrKnownHostMismatch) {
			t.Fatalf("VerifyKnownHostPin() error = %v, want ErrKnownHostMismatch", err)
		}
	})
}

func TestKnownHostPinRejectsUnsafeFile(t *testing.T) {
	t.Parallel()

	t.Run("symlink", func(t *testing.T) {
		t.Parallel()

		dir := knownHostsTempDir(t)
		target := filepath.Join(dir, "target")
		path := filepath.Join(dir, "known_hosts")
		if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)
		err := UpsertKnownHostPin(path, pin)
		if !errors.Is(err, ErrKnownHostsUnsafe) {
			t.Fatalf("UpsertKnownHostPin() error = %v, want ErrKnownHostsUnsafe", err)
		}
		assertFileBody(t, target, "target")
	})

	t.Run("symlink ancestor", func(t *testing.T) {
		t.Parallel()

		targetRoot := knownHostsTempDir(t)
		linkRoot := filepath.Join(knownHostsTempDir(t), "config-link")
		if err := os.Symlink(targetRoot, linkRoot); err != nil {
			t.Fatalf("symlink ancestor: %v", err)
		}

		path := filepath.Join(linkRoot, "ssh", "known_hosts")
		pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)
		err := UpsertKnownHostPin(path, pin)
		if !errors.Is(err, ErrKnownHostsUnsafe) {
			t.Fatalf("UpsertKnownHostPin() error = %v, want ErrKnownHostsUnsafe", err)
		}
		if _, statErr := os.Lstat(filepath.Join(targetRoot, "ssh", "known_hosts")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("redirected known_hosts exists after symlink ancestor rejection: %v", statErr)
		}
	})

	t.Run("verify symlink", func(t *testing.T) {
		t.Parallel()

		dir := knownHostsTempDir(t)
		target := filepath.Join(dir, "target")
		path := filepath.Join(dir, "known_hosts")
		if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)
		err := VerifyKnownHostPin(path, pin)
		if !errors.Is(err, ErrKnownHostsUnsafe) {
			t.Fatalf("VerifyKnownHostPin() error = %v, want ErrKnownHostsUnsafe", err)
		}
	})

	t.Run("hardlink", func(t *testing.T) {
		t.Parallel()

		dir := knownHostsTempDir(t)
		target := filepath.Join(dir, "target")
		path := filepath.Join(dir, "known_hosts")
		if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Link(target, path); err != nil {
			t.Fatalf("link: %v", err)
		}

		pin := mustKnownHostPin(t, "example.com", 22, "ssh-ed25519", testEd25519Key)
		err := UpsertKnownHostPin(path, pin)
		if !errors.Is(err, ErrKnownHostsUnsafe) {
			t.Fatalf("UpsertKnownHostPin() error = %v, want ErrKnownHostsUnsafe", err)
		}
		assertFileBody(t, target, "target")
	})
}

func mustKnownHostPin(t *testing.T, host string, port int, keyType, publicKey string) KnownHostPin {
	t.Helper()
	pin, err := NewKnownHostPin(host, port, keyType, publicKey)
	if err != nil {
		t.Fatalf("NewKnownHostPin() error = %v", err)
	}
	return pin
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertFileBody(t *testing.T, path, want string) {
	t.Helper()
	if got := mustReadFile(t, path); got != want {
		t.Fatalf("%s body:\n got %q\nwant %q", path, got, want)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func assertNoKnownHostsTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".known-hosts-") {
			t.Fatalf("left temporary known_hosts file %s", entry.Name())
		}
	}
}

func knownHostsTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
