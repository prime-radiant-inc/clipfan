package sshprovision

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedAuthorizedKeyLine(t *testing.T) {
	t.Parallel()

	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})

	want := `no-agent-forwarding,no-X11-forwarding,no-port-forwarding,no-pty,no-user-rc,command="/home/jesse/.local/bin/clipfan ssh-gateway --authorized-peer linux-a --authorized-key-id key-123456" ssh-ed25519 ` + testEd25519Key + ` clipfan-sync:linux-a:key-123456`
	if got := entry.Line(); got != want {
		t.Fatalf("Line() = %q, want %q", got, want)
	}
	if got := entry.ForcedCommand(); got != "/home/jesse/.local/bin/clipfan ssh-gateway --authorized-peer linux-a --authorized-key-id key-123456" {
		t.Fatalf("ForcedCommand() = %q", got)
	}
}

func TestNewManagedAuthorizedKeyRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		entry ManagedAuthorizedKey
	}{
		{
			name: "invalid peer id",
			entry: ManagedAuthorizedKey{
				PeerID:      "bad peer",
				KeyID:       "key-123456",
				GatewayPath: "/home/jesse/.local/bin/clipfan",
				PublicKey:   testEd25519Key,
			},
		},
		{
			name: "short key id",
			entry: ManagedAuthorizedKey{
				PeerID:      "linux-a",
				KeyID:       "short",
				GatewayPath: "/home/jesse/.local/bin/clipfan",
				PublicKey:   testEd25519Key,
			},
		},
		{
			name: "unsafe gateway path",
			entry: ManagedAuthorizedKey{
				PeerID:      "linux-a",
				KeyID:       "key-123456",
				GatewayPath: "/home/jesse/.local/bin/clip fan",
				PublicKey:   testEd25519Key,
			},
		},
		{
			name: "non ed25519 key",
			entry: ManagedAuthorizedKey{
				PeerID:      "linux-a",
				KeyID:       "key-123456",
				GatewayPath: "/home/jesse/.local/bin/clipfan",
				PublicKey:   testRSAKey,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewManagedAuthorizedKey(tc.entry)
			if !errors.Is(err, ErrInvalidAuthorizedKey) {
				t.Fatalf("NewManagedAuthorizedKey() error = %v, want ErrInvalidAuthorizedKey", err)
			}
		})
	}
}

func TestUpsertManagedAuthorizedKeyLineInsertsBeforeUnmanagedKeys(t *testing.T) {
	t.Parallel()

	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	before := strings.Join([]string{
		"# user key",
		"ssh-ed25519 " + testOtherEd25519Key + " user@example",
		"",
	}, "\n")

	after, err := UpsertManagedAuthorizedKeyLine([]byte(before), entry)
	if err != nil {
		t.Fatalf("UpsertManagedAuthorizedKeyLine() error = %v", err)
	}

	want := strings.Join([]string{
		"# user key",
		entry.Line(),
		"ssh-ed25519 " + testOtherEd25519Key + " user@example",
		"",
	}, "\n")
	if string(after) != want {
		t.Fatalf("updated authorized_keys:\n got %q\nwant %q", string(after), want)
	}
}

func TestUpsertManagedAuthorizedKeyLineMovesSamePeerBeforeUnmanagedKeys(t *testing.T) {
	t.Parallel()

	oldEntry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-old123",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	newEntry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-new123",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	before := strings.Join([]string{
		"# user key",
		"ssh-ed25519 " + testOtherEd25519Key + " user@example",
		oldEntry.Line(),
		"",
	}, "\n")

	after, err := UpsertManagedAuthorizedKeyLine([]byte(before), newEntry)
	if err != nil {
		t.Fatalf("UpsertManagedAuthorizedKeyLine() error = %v", err)
	}

	want := strings.Join([]string{
		"# user key",
		newEntry.Line(),
		"ssh-ed25519 " + testOtherEd25519Key + " user@example",
		"",
	}, "\n")
	if string(after) != want {
		t.Fatalf("updated authorized_keys:\n got %q\nwant %q", string(after), want)
	}
}

func TestUpsertManagedAuthorizedKeyLineRemovesUnmanagedSamePublicKey(t *testing.T) {
	t.Parallel()

	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	before := strings.Join([]string{
		"# user key",
		"ssh-ed25519 " + testEd25519Key + " old-clipfan-sync-key",
		"ssh-ed25519 " + testOtherEd25519Key + " other-user-key mentions ssh-ed25519 " + testEd25519Key,
		"",
	}, "\n")

	after, err := UpsertManagedAuthorizedKeyLine([]byte(before), entry)
	if err != nil {
		t.Fatalf("UpsertManagedAuthorizedKeyLine() error = %v", err)
	}

	want := strings.Join([]string{
		"# user key",
		entry.Line(),
		"ssh-ed25519 " + testOtherEd25519Key + " other-user-key mentions ssh-ed25519 " + testEd25519Key,
		"",
	}, "\n")
	if string(after) != want {
		t.Fatalf("updated authorized_keys:\n got %q\nwant %q", string(after), want)
	}
}

func TestUpsertManagedAuthorizedKeyLineReplacesSamePeerOnly(t *testing.T) {
	t.Parallel()

	oldEntry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-old123",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testOtherEd25519Key,
	})
	otherPeer := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "mac-b",
		KeyID:       "key-999999",
		GatewayPath: "/Users/jesse/.local/bin/clipfan",
		PublicKey:   testOtherEd25519Key,
	})
	newEntry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-new123",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	before := "# keep\n" + oldEntry.Line() + "\n" + otherPeer.Line() + "\n"

	after, err := UpsertManagedAuthorizedKeyLine([]byte(before), newEntry)
	if err != nil {
		t.Fatalf("UpsertManagedAuthorizedKeyLine() error = %v", err)
	}

	want := "# keep\n" + newEntry.Line() + "\n" + otherPeer.Line() + "\n"
	if string(after) != want {
		t.Fatalf("updated authorized_keys:\n got %q\nwant %q", string(after), want)
	}
}

func TestUpsertManagedAuthorizedKeyLineRejectsManagedSamePublicKeyForOtherPeer(t *testing.T) {
	t.Parallel()

	existing := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "mac-b",
		KeyID:       "key-999999",
		GatewayPath: "/Users/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})

	after, err := UpsertManagedAuthorizedKeyLine([]byte(existing.Line()+"\n"), entry)
	if !errors.Is(err, ErrAuthorizedKeyConflict) {
		t.Fatalf("UpsertManagedAuthorizedKeyLine() error = %v, want ErrAuthorizedKeyConflict", err)
	}
	if after != nil {
		t.Fatalf("updated authorized_keys = %q, want nil on conflict", string(after))
	}
}

func TestUpsertManagedAuthorizedKeyLineCollapsesDuplicateSamePeerLines(t *testing.T) {
	t.Parallel()

	oldEntryA := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-old123",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testOtherEd25519Key,
	})
	oldEntryB := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-old456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testOtherEd25519Key,
	})
	newEntry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-new123",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	before := oldEntryA.Line() + "\n# middle\n" + oldEntryB.Line() + "\n"

	after, err := UpsertManagedAuthorizedKeyLine([]byte(before), newEntry)
	if err != nil {
		t.Fatalf("UpsertManagedAuthorizedKeyLine() error = %v", err)
	}

	want := newEntry.Line() + "\n# middle\n"
	if string(after) != want {
		t.Fatalf("updated authorized_keys:\n got %q\nwant %q", string(after), want)
	}
}

func TestUpsertManagedAuthorizedKeyLineIsIdempotent(t *testing.T) {
	t.Parallel()

	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	before := []byte(entry.Line() + "\n")

	after, err := UpsertManagedAuthorizedKeyLine(before, entry)
	if err != nil {
		t.Fatalf("UpsertManagedAuthorizedKeyLine() error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("idempotent update changed body: got %q want %q", string(after), string(before))
	}
}

func TestUpsertManagedAuthorizedKeyLineIsIdempotentWithoutTrailingNewline(t *testing.T) {
	t.Parallel()

	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	before := []byte(entry.Line())

	after, err := UpsertManagedAuthorizedKeyLine(before, entry)
	if err != nil {
		t.Fatalf("UpsertManagedAuthorizedKeyLine() error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("idempotent update changed body: got %q want %q", string(after), string(before))
	}
}

func TestUpsertManagedAuthorizedKeyLineRejectsMalformedManagedMarker(t *testing.T) {
	t.Parallel()

	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	before := []byte("ssh-ed25519 " + testOtherEd25519Key + " clipfan-sync:linux-a:key-old123\n")

	after, err := UpsertManagedAuthorizedKeyLine(before, entry)
	if !errors.Is(err, ErrAuthorizedKeyConflict) {
		t.Fatalf("UpsertManagedAuthorizedKeyLine() error = %v, want ErrAuthorizedKeyConflict", err)
	}
	if string(after) != "" {
		t.Fatalf("after = %q, want empty result on error", string(after))
	}
}

func TestParseManagedAuthorizedKeyMetadata(t *testing.T) {
	t.Parallel()

	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})

	metadata, ok, err := ParseManagedAuthorizedKeyMetadata(entry.Line())
	if err != nil {
		t.Fatalf("ParseManagedAuthorizedKeyMetadata() error = %v", err)
	}
	if !ok {
		t.Fatal("ParseManagedAuthorizedKeyMetadata() ok = false")
	}
	if metadata.PeerID != "linux-a" || metadata.KeyID != "key-123456" {
		t.Fatalf("metadata = %#v", metadata)
	}

	_, ok, err = ParseManagedAuthorizedKeyMetadata("ssh-ed25519 " + testEd25519Key + " unmanaged")
	if err != nil {
		t.Fatalf("unmanaged ParseManagedAuthorizedKeyMetadata() error = %v", err)
	}
	if ok {
		t.Fatal("unmanaged ParseManagedAuthorizedKeyMetadata() ok = true")
	}
}

func TestUpsertManagedAuthorizedKeyFileCreatesSSHDirectoryAndFile(t *testing.T) {
	t.Parallel()

	home := knownHostsTempDir(t)
	path := filepath.Join(home, ".ssh", "authorized_keys")
	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})

	changed, err := UpsertManagedAuthorizedKeyFile(home, entry)
	if err != nil {
		t.Fatalf("UpsertManagedAuthorizedKeyFile() error = %v", err)
	}
	if !changed {
		t.Fatal("UpsertManagedAuthorizedKeyFile() changed = false, want true")
	}
	assertFileBody(t, path, entry.Line()+"\n")
	assertMode(t, filepath.Dir(path), 0o700)
	assertMode(t, path, 0o600)
	assertMode(t, path+".lock", 0o600)

	if err := VerifyManagedAuthorizedKeyFile(home, entry); err != nil {
		t.Fatalf("VerifyManagedAuthorizedKeyFile() error = %v", err)
	}
}

func TestUpsertManagedAuthorizedKeyFileIsIdempotent(t *testing.T) {
	t.Parallel()

	home := knownHostsTempDir(t)
	path := filepath.Join(home, ".ssh", "authorized_keys")
	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})

	changed, err := UpsertManagedAuthorizedKeyFile(home, entry)
	if err != nil {
		t.Fatalf("initial UpsertManagedAuthorizedKeyFile() error = %v", err)
	}
	if !changed {
		t.Fatal("initial UpsertManagedAuthorizedKeyFile() changed = false")
	}
	before := mustReadFile(t, path)

	changed, err = UpsertManagedAuthorizedKeyFile(home, entry)
	if err != nil {
		t.Fatalf("second UpsertManagedAuthorizedKeyFile() error = %v", err)
	}
	if changed {
		t.Fatal("second UpsertManagedAuthorizedKeyFile() changed = true, want false")
	}
	assertFileBody(t, path, before)
}

func TestVerifyManagedAuthorizedKeyFileReportsMissing(t *testing.T) {
	t.Parallel()

	home := knownHostsTempDir(t)
	path := filepath.Join(home, ".ssh", "authorized_keys")
	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if err := os.WriteFile(path, []byte("ssh-ed25519 "+testOtherEd25519Key+" user@example\n"), 0o600); err != nil {
		t.Fatalf("write authorized_keys: %v", err)
	}

	err := VerifyManagedAuthorizedKeyFile(home, entry)
	if !errors.Is(err, ErrAuthorizedKeyNotFound) {
		t.Fatalf("VerifyManagedAuthorizedKeyFile() error = %v, want ErrAuthorizedKeyNotFound", err)
	}
}

func TestUpsertManagedAuthorizedKeyFileRejectsCrossPeerKeyIDConflictWithoutWriting(t *testing.T) {
	t.Parallel()

	home := knownHostsTempDir(t)
	path := filepath.Join(home, ".ssh", "authorized_keys")
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	existing := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-b",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testOtherEd25519Key,
	})
	before := existing.Line() + "\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatalf("write authorized_keys: %v", err)
	}
	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})

	changed, err := UpsertManagedAuthorizedKeyFile(home, entry)
	if !errors.Is(err, ErrAuthorizedKeyConflict) {
		t.Fatalf("UpsertManagedAuthorizedKeyFile() error = %v, want ErrAuthorizedKeyConflict", err)
	}
	if changed {
		t.Fatal("UpsertManagedAuthorizedKeyFile() changed = true on conflict")
	}
	assertFileBody(t, path, before)
	assertNoAuthorizedKeysTemps(t, filepath.Dir(path))
}

func TestManagedAuthorizedKeyFileRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	entry := mustManagedAuthorizedKey(t, ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testEd25519Key,
	})

	t.Run("invalid home path", func(t *testing.T) {
		t.Parallel()

		if _, err := ManagedAuthorizedKeysPath("relative/home"); !errors.Is(err, ErrAuthorizedKeysUnsafe) {
			t.Fatalf("ManagedAuthorizedKeysPath() error = %v, want ErrAuthorizedKeysUnsafe", err)
		}
		if _, err := UpsertManagedAuthorizedKeyFile(knownHostsTempDir(t)+"/home/../other", entry); !errors.Is(err, ErrAuthorizedKeysUnsafe) {
			t.Fatalf("UpsertManagedAuthorizedKeyFile() error = %v, want ErrAuthorizedKeysUnsafe", err)
		}
	})

	t.Run("symlink file", func(t *testing.T) {
		t.Parallel()

		home := knownHostsTempDir(t)
		dir := filepath.Join(home, ".ssh")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir .ssh: %v", err)
		}
		target := filepath.Join(dir, "target")
		path := filepath.Join(dir, "authorized_keys")
		if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		_, err := UpsertManagedAuthorizedKeyFile(home, entry)
		if !errors.Is(err, ErrAuthorizedKeysUnsafe) {
			t.Fatalf("UpsertManagedAuthorizedKeyFile() error = %v, want ErrAuthorizedKeysUnsafe", err)
		}
		assertFileBody(t, target, "target")
	})

	t.Run("symlink ancestor", func(t *testing.T) {
		t.Parallel()

		targetRoot := knownHostsTempDir(t)
		linkRoot := filepath.Join(knownHostsTempDir(t), "home-link")
		if err := os.Symlink(targetRoot, linkRoot); err != nil {
			t.Fatalf("symlink ancestor: %v", err)
		}
		_, err := UpsertManagedAuthorizedKeyFile(linkRoot, entry)
		if !errors.Is(err, ErrAuthorizedKeysUnsafe) {
			t.Fatalf("UpsertManagedAuthorizedKeyFile() error = %v, want ErrAuthorizedKeysUnsafe", err)
		}
		if _, statErr := os.Lstat(filepath.Join(targetRoot, ".ssh")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("redirected .ssh exists after symlink ancestor rejection: %v", statErr)
		}
	})

	t.Run("symlink ssh directory", func(t *testing.T) {
		t.Parallel()

		home := knownHostsTempDir(t)
		targetDir := filepath.Join(knownHostsTempDir(t), "target-ssh")
		if err := os.Mkdir(targetDir, 0o700); err != nil {
			t.Fatalf("mkdir target .ssh: %v", err)
		}
		if err := os.Symlink(targetDir, filepath.Join(home, ".ssh")); err != nil {
			t.Fatalf("symlink .ssh: %v", err)
		}

		_, err := UpsertManagedAuthorizedKeyFile(home, entry)
		if !errors.Is(err, ErrAuthorizedKeysUnsafe) {
			t.Fatalf("UpsertManagedAuthorizedKeyFile() error = %v, want ErrAuthorizedKeysUnsafe", err)
		}
		if _, statErr := os.Lstat(filepath.Join(targetDir, "authorized_keys")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("redirected authorized_keys exists after .ssh symlink rejection: %v", statErr)
		}
	})

	t.Run("hardlink file", func(t *testing.T) {
		t.Parallel()

		home := knownHostsTempDir(t)
		dir := filepath.Join(home, ".ssh")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir .ssh: %v", err)
		}
		target := filepath.Join(dir, "target")
		path := filepath.Join(dir, "authorized_keys")
		if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Link(target, path); err != nil {
			t.Fatalf("link: %v", err)
		}

		_, err := UpsertManagedAuthorizedKeyFile(home, entry)
		if !errors.Is(err, ErrAuthorizedKeysUnsafe) {
			t.Fatalf("UpsertManagedAuthorizedKeyFile() error = %v, want ErrAuthorizedKeysUnsafe", err)
		}
		assertFileBody(t, target, "target")
	})

	t.Run("symlink lock", func(t *testing.T) {
		t.Parallel()

		home := knownHostsTempDir(t)
		dir := filepath.Join(home, ".ssh")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir .ssh: %v", err)
		}
		target := filepath.Join(dir, "lock-target")
		if err := os.WriteFile(target, []byte("lock"), 0o600); err != nil {
			t.Fatalf("write lock target: %v", err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "authorized_keys.lock")); err != nil {
			t.Fatalf("symlink lock: %v", err)
		}

		_, err := UpsertManagedAuthorizedKeyFile(home, entry)
		if !errors.Is(err, ErrAuthorizedKeysUnsafe) {
			t.Fatalf("UpsertManagedAuthorizedKeyFile() error = %v, want ErrAuthorizedKeysUnsafe", err)
		}
		assertFileBody(t, target, "lock")
	})

	t.Run("too open file", func(t *testing.T) {
		t.Parallel()

		home := knownHostsTempDir(t)
		dir := filepath.Join(home, ".ssh")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir .ssh: %v", err)
		}
		path := filepath.Join(dir, "authorized_keys")
		before := "ssh-ed25519 " + testOtherEd25519Key + " user@example\n"
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatalf("write authorized_keys: %v", err)
		}

		_, err := UpsertManagedAuthorizedKeyFile(home, entry)
		if !errors.Is(err, ErrAuthorizedKeysUnsafe) {
			t.Fatalf("UpsertManagedAuthorizedKeyFile() error = %v, want ErrAuthorizedKeysUnsafe", err)
		}
		assertFileBody(t, path, before)
	})
}

func mustManagedAuthorizedKey(t *testing.T, entry ManagedAuthorizedKey) ManagedAuthorizedKey {
	t.Helper()
	managed, err := NewManagedAuthorizedKey(entry)
	if err != nil {
		t.Fatalf("NewManagedAuthorizedKey() error = %v", err)
	}
	return managed
}

func assertNoAuthorizedKeysTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".authorized-keys-") {
			t.Fatalf("left temporary authorized_keys file %s", entry.Name())
		}
	}
}
