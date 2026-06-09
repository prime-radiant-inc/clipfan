//go:build darwin

package clipboard

import (
	"testing"
	"time"
)

func TestPasteboardCommandForcesUTF8Locale(t *testing.T) {
	env := utf8PasteboardEnv([]string{
		"PATH=/usr/bin:/bin",
		"LANG=C",
		"LC_ALL=C",
		"LC_CTYPE=C",
		"CLIPFAN_TEST=preserved",
	})
	got := envValues(env)

	if got["LANG"] != "en_US.UTF-8" {
		t.Fatalf("LANG = %q, want en_US.UTF-8", got["LANG"])
	}
	if got["LC_ALL"] != "en_US.UTF-8" {
		t.Fatalf("LC_ALL = %q, want en_US.UTF-8", got["LC_ALL"])
	}
	if got["LC_CTYPE"] != "UTF-8" {
		t.Fatalf("LC_CTYPE = %q, want UTF-8", got["LC_CTYPE"])
	}
	if got["CLIPFAN_TEST"] != "preserved" {
		t.Fatalf("CLIPFAN_TEST = %q, want preserved", got["CLIPFAN_TEST"])
	}
}

// TestApplyConcealmentCachesByHash proves the concealment probe (which forks
// the Swift helper) only runs when the clipboard text actually changes, not on
// every poll. It drives the real cache decision used by Read via a counting
// stub substituted for concealedFn.
func TestApplyConcealmentCachesByHash(t *testing.T) {
	calls := 0
	result := true
	orig := concealedFn
	concealedFn = func() bool {
		calls++
		return result
	}
	t.Cleanup(func() { concealedFn = orig })

	b := &macBackend{}
	now := time.Now().UTC()

	// First observation of some text: probe runs, result cached.
	first := New(KindText, []byte("hunter2"), now)
	b.applyConcealment(&first)
	if calls != 1 {
		t.Fatalf("first read: expected 1 probe call, got %d", calls)
	}
	if !first.Concealed {
		t.Fatalf("first read: expected Concealed=true from probe")
	}

	// Same text again: hash unchanged, probe must NOT run, cached value reused.
	second := New(KindText, []byte("hunter2"), now)
	b.applyConcealment(&second)
	if calls != 1 {
		t.Fatalf("repeat read of unchanged text: expected probe NOT re-invoked, still 1 call, got %d", calls)
	}
	if !second.Concealed {
		t.Fatalf("repeat read: expected cached Concealed=true")
	}

	// Different text: hash changes, probe runs again with the new result.
	result = false
	third := New(KindText, []byte("not-a-secret"), now)
	b.applyConcealment(&third)
	if calls != 2 {
		t.Fatalf("read of changed text: expected probe re-invoked (2 total), got %d", calls)
	}
	if third.Concealed {
		t.Fatalf("changed read: expected Concealed=false from probe")
	}
}

func envValues(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		for i, ch := range entry {
			if ch == '=' {
				out[entry[:i]] = entry[i+1:]
				break
			}
		}
	}
	return out
}
