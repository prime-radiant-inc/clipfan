package sshprovision

import (
	"errors"
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

func TestUpsertManagedAuthorizedKeyLineAppendsAndPreservesUnmanagedLines(t *testing.T) {
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

	want := before + entry.Line() + "\n"
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

func mustManagedAuthorizedKey(t *testing.T, entry ManagedAuthorizedKey) ManagedAuthorizedKey {
	t.Helper()
	managed, err := NewManagedAuthorizedKey(entry)
	if err != nil {
		t.Fatalf("NewManagedAuthorizedKey() error = %v", err)
	}
	return managed
}
