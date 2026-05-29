# Clipboard History Browser Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a clipboard history browser to the Clipfan macOS menubar app — a two-pane window listing recent text/link/image clips with previews, search, keyboard navigation, pinning, and an origin-host badge.

**Architecture:** The daemon records every clip it sees (local copies in `pollOnce`, peer pushes in `onReceive`) into a capped, newest-first `history.json`, reusing the existing on-disk `images/<sha>.png` files as thumbnails. A new HTTP API (`GET /v1/history`, `POST /v1/restore`, `POST /v1/history/pin`, `DELETE /v1/history`) lets the SwiftUI app list, restore, pin, and delete. Restore re-copies the item and fanouts to the fleet. History is local per-host; no new sync protocol.

**Tech Stack:** Go 1.26 (daemon, `net/http`, HMAC), SwiftUI (`MenuBarExtra(.window)`, macOS 13+).

**Spec:** `docs/superpowers/specs/2026-05-28-clipboard-history-design.md`

**Build/test gotcha (MANDATORY for every go/swift command):** a broken homebrew ccache shim breaks builds. Prefix Go commands with `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc`. Swift builds: `cd apps/mac/Clipfan && swift build`.

---

## File Structure

**New files:**
- `internal/store/history.go` — `HistoryEntry`, link detection, `AppendHistory`, `LoadHistory`, `SetPinned`, `DeleteEntry`, `ClearUnpinned`, history GC, referenced-image set.
- `internal/store/history_test.go` — store-layer tests.
- `apps/mac/Clipfan/Sources/Clipfan/HistoryEntry.swift` — Codable model.
- `apps/mac/Clipfan/Sources/Clipfan/HistoryViewModel.swift` — search/filter/sort logic (testable).
- `apps/mac/Clipfan/Sources/Clipfan/HistoryRow.swift` — one list row.
- `apps/mac/Clipfan/Sources/Clipfan/HistoryWindow.swift` — two-pane window.
- `apps/mac/Clipfan/Sources/Clipfan/GlobalHotkey.swift` — Carbon hotkey registration.
- `apps/mac/Clipfan/Tests/ClipfanTests/HistoryViewModelTests.swift` — view-model tests.

**Modified files:**
- `internal/config/config.go` — add `MaxHistory` field (default 200).
- `internal/store/store.go` — make image GC history-aware.
- `internal/daemon/daemon.go` — call `AppendHistory` in `pollOnce`/`onReceive`; add `Restore(id)`; expose history accessors.
- `internal/clipboard/clipboard.go` — add `Concealed bool` to `Content`.
- `internal/clipboard/clipboard_darwin.go` — detect `org.nspasteboard.ConcealedType`.
- `internal/clipboard/clipboard_linux.go` — `Concealed` always false (no-op).
- `internal/transport/server.go` — register history endpoints + handlers.
- `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift` — history fetch/restore/pin/delete.
- `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift` — wire history window + hotkey + menu item.
- `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift` — "Clipboard History…" menu item.
- `apps/mac/Clipfan/Package.swift` — add test target if not present.

---

## Task 1: Config — MaxHistory field

**Files:**
- Modify: `internal/config/config.go` (the `Config` struct + the defaults applied in its loader)
- Test: `internal/config/config_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Create/append `internal/config/config_test.go`:

```go
package config

import "testing"

func TestMaxHistoryDefault(t *testing.T) {
	c := withDefaults(Config{})
	if c.MaxHistory != 200 {
		t.Fatalf("MaxHistory default = %d, want 200", c.MaxHistory)
	}
}

func TestMaxHistoryRespectsExplicit(t *testing.T) {
	c := withDefaults(Config{MaxHistory: 50})
	if c.MaxHistory != 50 {
		t.Fatalf("MaxHistory = %d, want 50 (explicit kept)", c.MaxHistory)
	}
}
```

> NOTE: `Load()` currently returns `*Config` and applies defaults inline (Port, Listen, Discovery). This task introduces a `withDefaults(Config) Config` helper that owns all defaulting (including the new MaxHistory), and changes `Load` to apply it: after unmarshal, do `*c = withDefaults(*c)` before returning, and in the "file missing" branch build the initial config through `withDefaults` too. Move the existing inline Port/Listen/Discovery defaults into `withDefaults` so there is one defaulting path. The test calls `withDefaults(Config{})` (value in, value out).

- [ ] **Step 2: Run test to verify it fails**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/config/ -run TestMaxHistory -v`
Expected: FAIL — `MaxHistory` field undefined / `withDefaults` undefined.

- [ ] **Step 3: Add the field and defaulting**

In `internal/config/config.go`, add to the `Config` struct:

```go
	MaxHistory  int      `json:"max_history,omitempty"`
```

Add (or extend) the defaulting helper:

```go
// withDefaults fills zero-valued fields with their defaults.
func withDefaults(c Config) Config {
	if c.MaxHistory == 0 {
		c.MaxHistory = 200
	}
	return c
}
```

Ensure `Load()` returns `withDefaults(c)` before handing the config back. (If `Load` already defaults other fields inline, move them into `withDefaults` too so there is one defaulting path.)

- [ ] **Step 4: Run test to verify it passes**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/config/ -run TestMaxHistory -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add MaxHistory setting (default 200)"
```

---

## Task 2: Store — HistoryEntry, link detection, append/load

**Files:**
- Create: `internal/store/history.go`
- Create: `internal/store/history_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/history_test.go`:

```go
package store

import (
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func ts(s int) time.Time { return time.Unix(int64(s), 0).UTC() }

func TestAppendAndLoadNewestFirst(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("first"), ts(1)), "m4", "")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("second"), ts(2)), "flower-garden", "")
	got, err := LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Preview != "second" {
		t.Fatalf("newest-first broken: got[0]=%q", got[0].Preview)
	}
	if got[0].Origin != "flower-garden" {
		t.Fatalf("origin = %q, want flower-garden", got[0].Origin)
	}
}

func TestAppendDedupMovesToTop(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("dup"), ts(1)), "m4", "")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("other"), ts(2)), "m4", "")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("dup"), ts(3)), "m4", "")
	got, _ := LoadHistory(10)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (dup collapsed)", len(got))
	}
	if got[0].Preview != "dup" {
		t.Fatalf("re-copied item not floated to top: got[0]=%q", got[0].Preview)
	}
}

func TestLinkDetection(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("https://example.com/x"), ts(1)), "m4", "")
	got, _ := LoadHistory(10)
	if got[0].Kind != "link" {
		t.Fatalf("kind = %q, want link", got[0].Kind)
	}
}

func TestImageEntryUsesPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	png := []byte("\x89PNG\r\n\x1a\nfake")
	AppendHistory(clipboard.New(clipboard.KindImage, png, ts(1)), "m4", "/abs/path.png")
	got, _ := LoadHistory(10)
	if got[0].Kind != "image" || got[0].ImagePath != "/abs/path.png" {
		t.Fatalf("image entry wrong: %+v", got[0])
	}
	if got[0].Text != "" {
		t.Fatalf("image entry should not inline bytes as text")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/store/ -run 'TestAppend|TestLink|TestImageEntry' -v`
Expected: FAIL — `AppendHistory`/`LoadHistory`/`HistoryEntry` undefined.

- [ ] **Step 3: Implement history.go**

Create `internal/store/history.go`:

```go
package store

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
)

// DefaultMaxHistory bounds history when no explicit cap is given.
const DefaultMaxHistory = 200

const previewLen = 140

var urlRe = regexp.MustCompile(`^\s*https?://\S+\s*$`)

// historyMu serializes read-modify-write of history.json across goroutines.
var historyMu sync.Mutex

// HistoryEntry is one item in the clipboard history.
type HistoryEntry struct {
	ID        string    `json:"id"`         // sha256 hex of content (identity + dedup key)
	Kind      string    `json:"kind"`       // "text" | "image" | "link"
	Preview   string    `json:"preview"`    // short display string
	Text      string    `json:"text,omitempty"`       // full text, for text/link only
	ImagePath string    `json:"image_path,omitempty"` // path to images/<sha>.png, image only
	SizeBytes int       `json:"size_bytes"`
	Origin    string    `json:"origin"`
	TS        time.Time `json:"ts"`
	Pinned    bool      `json:"pinned"`
}

func historyPath() string { return filepath.Join(config.StateDir(), "history.json") }

func classify(kind clipboard.Kind, body []byte) string {
	if kind == clipboard.KindImage {
		return "image"
	}
	if urlRe.Match(body) {
		return "link"
	}
	return "text"
}

func preview(body []byte, imagePath string) string {
	if imagePath != "" {
		return filepath.Base(imagePath)
	}
	s := strings.TrimSpace(string(body))
	if len(s) > previewLen {
		return s[:previewLen]
	}
	return s
}

// readHistory loads the raw list (newest-first as stored). Missing file = empty.
func readHistory() ([]HistoryEntry, error) {
	data, err := os.ReadFile(historyPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []HistoryEntry
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func writeHistory(list []HistoryEntry) error {
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(list, "", "  ")
	return writeAtomic(historyPath(), data, 0o644)
}

// AppendHistory records a clip. imagePath is the on-disk PNG path for images
// (empty for text). Re-copying identical content floats the existing entry to
// the top instead of duplicating. The list is trimmed to cap (pinned exempt).
func AppendHistory(c clipboard.Content, origin, imagePath string) error {
	historyMu.Lock()
	defer historyMu.Unlock()

	list, err := readHistory()
	if err != nil {
		return err
	}
	id := hex.EncodeToString(c.Hash[:])
	entry := HistoryEntry{
		ID:        id,
		Kind:      classify(c.Kind, c.Bytes),
		Preview:   preview(c.Bytes, imagePath),
		SizeBytes: len(c.Bytes),
		Origin:    origin,
		TS:        c.TS,
	}
	if c.Kind == clipboard.KindImage {
		entry.ImagePath = imagePath
	} else {
		entry.Text = string(c.Bytes)
	}

	// Dedup: drop any existing entry with the same id, preserving its pinned flag.
	out := make([]HistoryEntry, 0, len(list)+1)
	for _, e := range list {
		if e.ID == id {
			entry.Pinned = e.Pinned
			continue
		}
		out = append(out, e)
	}
	out = append([]HistoryEntry{entry}, out...) // newest first
	return writeHistory(trim(out, cap()))
}

// LoadHistory returns up to limit entries, pinned floated to the top, then
// newest-first within each group.
func LoadHistory(limit int) ([]HistoryEntry, error) {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Pinned != list[j].Pinned {
			return list[i].Pinned // pinned first
		}
		return list[i].TS.After(list[j].TS)
	})
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func cap() int {
	c, err := config.Load()
	if err == nil && c.MaxHistory > 0 {
		return c.MaxHistory
	}
	return DefaultMaxHistory
}

// trim keeps all pinned entries plus the newest unpinned up to max total.
func trim(list []HistoryEntry, max int) []HistoryEntry {
	if len(list) <= max {
		return list
	}
	kept := make([]HistoryEntry, 0, max)
	pinnedCount := 0
	for _, e := range list {
		if e.Pinned {
			pinnedCount++
		}
	}
	unpinnedBudget := max - pinnedCount
	if unpinnedBudget < 0 {
		unpinnedBudget = 0
	}
	usedUnpinned := 0
	for _, e := range list { // list is newest-first
		if e.Pinned {
			kept = append(kept, e)
			continue
		}
		if usedUnpinned < unpinnedBudget {
			kept = append(kept, e)
			usedUnpinned++
		}
	}
	return kept
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/store/ -run 'TestAppend|TestLink|TestImageEntry' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/history.go internal/store/history_test.go
git commit -m "feat(store): clipboard history append/load with dedup + link detection"
```

---

## Task 3: Store — pin, delete, clear, retention exemption

**Files:**
- Modify: `internal/store/history.go`
- Modify: `internal/store/history_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/history_test.go`:

```go
func TestPinExemptFromTrim(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // cap() reads config; isolate it
	// Pin the first item, then push more than the test cap of unpinned items.
	AppendHistory(clipboard.New(clipboard.KindText, []byte("keepme"), ts(1)), "m4", "")
	if err := SetPinned(idOf("keepme"), true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		AppendHistory(clipboard.New(clipboard.KindText, []byte(string(rune('a'+i%26))+itoa(i)), ts(100+i)), "m4", "")
	}
	got, _ := LoadHistory(1000)
	found := false
	for _, e := range got {
		if e.Preview == "keepme" {
			found = true
		}
	}
	if !found {
		t.Fatal("pinned entry was trimmed away")
	}
}

func TestDeleteEntry(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("a"), ts(1)), "m4", "")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("b"), ts(2)), "m4", "")
	if err := DeleteEntry(idOf("a")); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadHistory(10)
	if len(got) != 1 || got[0].Preview != "b" {
		t.Fatalf("delete failed: %+v", got)
	}
}

func TestClearUnpinnedKeepsPinned(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("pin"), ts(1)), "m4", "")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("drop"), ts(2)), "m4", "")
	_ = SetPinned(idOf("pin"), true)
	if err := ClearUnpinned(); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadHistory(10)
	if len(got) != 1 || got[0].Preview != "pin" {
		t.Fatalf("clear-unpinned failed: %+v", got)
	}
}
```

Add these helpers at the top of `history_test.go` (below the `ts` helper):

```go
import "crypto/sha256"
import "encoding/hex"
import "strconv"

func idOf(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func itoa(i int) string    { return strconv.Itoa(i) }
```

> If `history_test.go` already imports `testing`/`time`/`clipboard`, merge these imports into the existing import block rather than adding duplicate blocks.

- [ ] **Step 2: Run tests to verify they fail**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/store/ -run 'TestPin|TestDelete|TestClear' -v`
Expected: FAIL — `SetPinned`/`DeleteEntry`/`ClearUnpinned` undefined.

- [ ] **Step 3: Implement the mutators**

Append to `internal/store/history.go`:

```go
// SetPinned sets the pinned flag on the entry with the given id.
func SetPinned(id string, pinned bool) error {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].ID == id {
			list[i].Pinned = pinned
		}
	}
	return writeHistory(list)
}

// DeleteEntry removes the entry with the given id.
func DeleteEntry(id string) error {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return err
	}
	out := list[:0]
	for _, e := range list {
		if e.ID != id {
			out = append(out, e)
		}
	}
	return writeHistory(out)
}

// ClearUnpinned removes every entry that is not pinned.
func ClearUnpinned() error {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return err
	}
	out := list[:0]
	for _, e := range list {
		if e.Pinned {
			out = append(out, e)
		}
	}
	return writeHistory(out)
}

// EntryByID returns the entry with the given id, or false if not found.
func EntryByID(id string) (HistoryEntry, bool, error) {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return HistoryEntry{}, false, err
	}
	for _, e := range list {
		if e.ID == id {
			return e, true, nil
		}
	}
	return HistoryEntry{}, false, nil
}

// ReferencedImages returns the set of image filenames (<sha>.png) still
// referenced by any history entry, so image GC can avoid deleting them.
func ReferencedImages() (map[string]struct{}, error) {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(list))
	for _, e := range list {
		if e.ImagePath != "" {
			set[filepath.Base(e.ImagePath)] = struct{}{}
		}
	}
	return set, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/store/ -v`
Expected: PASS (all store tests)

- [ ] **Step 5: Commit**

```bash
git add internal/store/history.go internal/store/history_test.go
git commit -m "feat(store): pin/delete/clear history + image reference set"
```

---

## Task 4: Store — history-aware image GC

**Files:**
- Modify: `internal/store/store.go` (the `gc` function and `SaveImage`)
- Test: `internal/store/history_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/history_test.go`:

```go
func TestImageGCSpareReferenced(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// Save an image and record it in history.
	png := []byte("\x89PNG\r\n\x1a\n-referenced-")
	p, err := SaveImage(png)
	if err != nil {
		t.Fatal(err)
	}
	c := clipboard.New(clipboard.KindImage, png, ts(1))
	if err := AppendHistory(c, "m4", p); err != nil {
		t.Fatal(err)
	}
	// Flood with more images than maxImages to force GC.
	for i := 0; i < maxImages+10; i++ {
		_, _ = SaveImage([]byte("filler-" + itoa(i)))
	}
	// gc runs in a goroutine in SaveImage; call it synchronously to assert.
	gc(imagesDir())
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("referenced image was GC'd: %v", err)
	}
}
```

Add `import "os"` to the test file imports if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/store/ -run TestImageGCSpareReferenced -v`
Expected: FAIL — the referenced image gets deleted because GC ignores history.

- [ ] **Step 3: Make gc history-aware**

In `internal/store/store.go`, replace the body of `gc` so it never deletes a referenced image and counts the cap against the larger of `maxImages` and the history cap:

```go
// gc trims the oldest images beyond the retention bound, never deleting an
// image still referenced by a history entry.
func gc(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	referenced, _ := ReferencedImages() // best-effort; nil set = none spared

	type stamped struct {
		path string
		name string
		mod  int64
	}
	all := make([]stamped, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		all = append(all, stamped{filepath.Join(dir, e.Name()), e.Name(), fi.ModTime().UnixNano()})
	}

	bound := maxImages
	if c := cap(); c > bound {
		bound = c
	}
	if len(all) <= bound {
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod < all[j].mod })
	toRemove := len(all) - bound
	for _, s := range all {
		if toRemove == 0 {
			break
		}
		if _, ok := referenced[s.name]; ok {
			continue // keep referenced images
		}
		if err := os.Remove(s.path); err == nil {
			toRemove--
		}
	}
}
```

> `cap()` is defined in history.go (Task 2). `ReferencedImages()` is defined in history.go (Task 3). Both are in package `store`, so no import changes are needed.

- [ ] **Step 4: Run tests to verify they pass**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/history_test.go
git commit -m "feat(store): make image GC history-aware (spare referenced images)"
```

---

## Task 5: Clipboard — concealed-clip detection

**Files:**
- Modify: `internal/clipboard/clipboard.go` (the `Content` struct)
- Modify: `internal/clipboard/clipboard_darwin.go` (`Read`)
- Modify: `internal/clipboard/clipboard_linux.go` (set `Concealed=false`)
- Test: `internal/clipboard/clipboard_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Create/append `internal/clipboard/clipboard_test.go`:

```go
package clipboard

import (
	"testing"
	"time"
)

func TestNewDefaultsNotConcealed(t *testing.T) {
	c := New(KindText, []byte("x"), time.Unix(1, 0))
	if c.Concealed {
		t.Fatal("New content should default to not concealed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/clipboard/ -run TestNewDefaults -v`
Expected: FAIL — `Concealed` field undefined.

- [ ] **Step 3: Add the field + darwin detection**

In `internal/clipboard/clipboard.go`, add to `Content`:

```go
	Concealed bool // true if the source marked the clip as transient/secret
```

(`New` leaves it false by default — no change to `New` needed.)

In `internal/clipboard/clipboard_darwin.go`, in `Read()`, before returning, detect the concealed type. macOS exposes pasteboard types via `osascript`-free means is awkward; the robust path is to check the pasteboard's types with the bundled helper or `pbpaste`. Use this minimal approach: query the general pasteboard types using a tiny `osascript` call is unreliable — instead shell to the existing helper is overkill. The pragmatic, dependency-free check uses the pasteboard type list via the `pbv` approach below.

Concretely, add a helper that reads the pasteboard's declared UTI types and sets `Concealed` when `org.nspasteboard.ConcealedType` (or `org.nspasteboard.TransientType`) is present:

```go
// concealed reports whether the general pasteboard declares a concealed or
// transient type (used by password managers to opt out of clipboard managers).
func concealed() bool {
	out, err := exec.Command("osascript", "-e",
		`tell application "System Events" to return (clipboard info)`).Output()
	if err != nil {
		return false
	}
	s := string(out)
	return strings.Contains(s, "org.nspasteboard.ConcealedType") ||
		strings.Contains(s, "org.nspasteboard.TransientType")
}
```

> VERIFY DURING IMPLEMENTATION: `clipboard info` returns class names, not UTIs, so the substring check above may not fire. The reliable source of truth is `NSPasteboard.general.types`. If the `osascript` approach does not detect the type in a manual test (copy from a password manager, or simulate by declaring the type), implement detection in the existing Swift `clipfan-pasteboard-helper` instead: add a `--check-concealed` mode that prints `concealed` when `NSPasteboard.general.types` contains `org.nspasteboard.ConcealedType`, and shell to it here. Do not ship a detection that you have not observed returning true for a real concealed clip — if neither path is verified, leave `Concealed=false` and note it, rather than claiming a privacy guarantee that does not hold.

Then in `Read()` set `c.Concealed = concealed()` on the returned `Content` (both the image and text return paths). Ensure `clipboard_darwin.go` imports `os/exec` and `strings`.

In `internal/clipboard/clipboard_linux.go`, no change is required — `Concealed` stays false (Linux backends do not source concealed clips in this design).

- [ ] **Step 4: Run test to verify it passes**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/clipboard/ -v`
Expected: PASS
Also: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc GOOS=linux CGO_ENABLED=0 go build ./...` (confirms linux still builds).

- [ ] **Step 5: Manually verify detection (no automated test possible)**

Copy a password from a password manager (or any app that marks ConcealedType). Run a one-off: add a temporary `slog.Info("concealed", "v", concealed())` in `pollOnce`, observe it logs true, then remove it. Document the result in the commit message.

- [ ] **Step 6: Commit**

```bash
git add internal/clipboard/clipboard.go internal/clipboard/clipboard_darwin.go
git commit -m "feat(clipboard): detect concealed pasteboard clips on macOS"
```

---

## Task 6: Daemon — record history + skip concealed

**Files:**
- Modify: `internal/daemon/daemon.go` (`pollOnce` and `onReceive`)
- Test: `internal/daemon/history_record_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/daemon/history_record_test.go`. Reuse the fakes from the existing `echo_test.go` (same package). This test drives `onReceive` and asserts a history entry lands.

```go
package daemon

import (
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/store"
)

func TestOnReceiveRecordsHistory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := newTestDaemon(t) // helper from echo_test.go; if named differently, match it
	c := clipboard.New(clipboard.KindText, []byte("hello-history"), time.Unix(10, 0))
	d.onReceive(c, "flower-garden")
	got, err := store.LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Preview != "hello-history" || got[0].Origin != "flower-garden" {
		t.Fatalf("history not recorded correctly: %+v", got)
	}
}

func TestConcealedNotRecorded(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := newTestDaemon(t)
	c := clipboard.New(clipboard.KindText, []byte("secret"), time.Unix(11, 0))
	c.Concealed = true
	d.onReceive(c, "m4")
	got, _ := store.LoadHistory(10)
	if len(got) != 0 {
		t.Fatalf("concealed clip should not be recorded, got %+v", got)
	}
}
```

> `newTestDaemon(t)` may not exist yet. Check `echo_test.go` for how it constructs a `Daemon` with fakes (it builds a struct literal with a fakeBackend + fakePusher + static discovery). If there is no shared constructor, extract the construction from `echo_test.go` into a `newTestDaemon(t *testing.T) *Daemon` helper in `echo_test.go` and call it from both files. The fakeBackend's `Read()` must return non-empty content for the readback registration to behave; reuse the existing fake.

- [ ] **Step 2: Run test to verify it fails**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/daemon/ -run 'TestOnReceiveRecordsHistory|TestConcealedNotRecorded' -v`
Expected: FAIL — history is empty (no AppendHistory call yet) / concealed test may pass vacuously until step 3 adds recording.

- [ ] **Step 3: Wire AppendHistory into onReceive and pollOnce**

In `internal/daemon/daemon.go`, inside `onReceive`, after the `store.SaveState(state, c.Bytes)` block and before the readback registration, add:

```go
	if !c.Concealed {
		imgPath := ""
		if c.Kind == clipboard.KindImage {
			imgPath = state.ImagePath
		}
		if err := store.AppendHistory(c, origin, imgPath); err != nil {
			slog.Debug("append history", "err", err)
		}
	}
```

In `pollOnce`, after `d.seen.add(c.Hash)` / `d.lastTS = c.TS` and before/around the existing `fanout`, record the local copy. A locally-copied image must be saved to disk first so the entry has a thumbnail path:

```go
	if !c.Concealed {
		imgPath := ""
		if c.Kind == clipboard.KindImage {
			if p, err := store.SaveImage(c.Bytes); err == nil {
				imgPath = p
			}
		}
		if err := store.AppendHistory(c, d.origin, imgPath); err != nil {
			slog.Debug("append history", "err", err)
		}
	}
```

> Ensure `pollOnce` already imports nothing new — `store` and `clipboard` are already imported in daemon.go. Place the block so it runs for every new local clip (after dedup, so re-polls of unchanged content don't re-append; AppendHistory also dedups by hash as a backstop).

- [ ] **Step 4: Run tests to verify they pass**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/daemon/ -v`
Expected: PASS (including existing echo/seen/match tests)

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/history_record_test.go internal/daemon/echo_test.go
git commit -m "feat(daemon): record clipboard history, skip concealed clips"
```

---

## Task 7: Daemon — Restore path

**Files:**
- Modify: `internal/daemon/daemon.go` (add `Restore`)
- Test: `internal/daemon/history_record_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/daemon/history_record_test.go`:

```go
func TestRestoreWritesClipboardAndFanouts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := newTestDaemon(t)
	c := clipboard.New(clipboard.KindText, []byte("restore-me"), time.Unix(20, 0))
	d.onReceive(c, "m4")
	got, _ := store.LoadHistory(10)
	id := got[0].ID

	fp := d.cl.(*fakePusher) // fakePusher from echo_test.go
	fp.reset()               // if no reset(), read len before/after instead

	if err := d.Restore(id); err != nil {
		t.Fatal(err)
	}
	if fp.count() == 0 { // count()/calls from echo_test.go's fakePusher
		t.Fatal("restore did not fanout to peers")
	}
}
```

> Match the fakePusher API actually present in `echo_test.go` (it records PushAs calls — use whatever accessor exists; if it exposes a slice `calls`, assert `len(fp.calls) > 0` and skip `reset()`).

- [ ] **Step 2: Run test to verify it fails**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/daemon/ -run TestRestore -v`
Expected: FAIL — `Restore` undefined.

- [ ] **Step 3: Implement Restore**

Add to `internal/daemon/daemon.go`:

```go
// Restore makes the history entry with the given id the current clipboard:
// it writes the local OS clipboard, records it fresh in history (floating it to
// the top), and fanouts to peers so the fleet converges.
func (d *Daemon) Restore(id string) error {
	e, ok, err := store.EntryByID(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("history entry %s not found", id)
	}

	var c clipboard.Content
	if e.Kind == "image" {
		body, err := os.ReadFile(e.ImagePath)
		if err != nil {
			return fmt.Errorf("read image %s: %w", e.ImagePath, err)
		}
		c = clipboard.New(clipboard.KindImage, body, time.Now().UTC())
		if err := d.cb.WriteImage(body, e.ImagePath); err != nil {
			slog.Error("restore write image", "err", err)
		}
	} else {
		c = clipboard.New(clipboard.KindText, []byte(e.Text), time.Now().UTC())
		if err := d.cb.WriteText([]byte(e.Text)); err != nil {
			slog.Error("restore write text", "err", err)
		}
	}

	d.mu.Lock()
	d.seen.add(c.Hash)
	d.lastTS = c.TS
	d.mu.Unlock()

	imgPath := ""
	if e.Kind == "image" {
		imgPath = e.ImagePath
	}
	if err := store.AppendHistory(c, d.origin, imgPath); err != nil {
		slog.Debug("restore append history", "err", err)
	}

	d.fanout(context.Background(), c, "")
	return nil
}
```

> daemon.go already imports `context`, `os`, `time`, `log/slog`, `store`, `clipboard`. Add `"fmt"` to the import block if not present.

- [ ] **Step 4: Run tests to verify they pass**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/daemon/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/history_record_test.go
git commit -m "feat(daemon): restore history entry to clipboard + fleet"
```

---

## Task 8: Transport — history HTTP endpoints

**Files:**
- Modify: `internal/transport/server.go`
- Test: `internal/transport/history_endpoint_test.go` (create)

**Codebase facts (verified against `server.go`/`auth.go`/`client.go` — match these exactly):**
- `NewServer(listen string, auth *Auth, onRecv ReceiveFunc, peersFn PeersFunc) *Server`.
- The mux is built **inside `Serve(ctx)`** with Go 1.22 method-prefixed patterns, e.g. `mux.HandleFunc("POST /v1/clip", s.postClip)`. There is **no** `Handler()` method yet — this task adds one by extracting the mux from `Serve`.
- `s.auth.Verify(body, sig)` returns an **`error`** (nil = valid), not a bool.
- `s.auth.Sign(body)` returns a `string`. The header is the literal `"X-Clipfan-Sig"` (no `sigHeader` constant).
- Construct `*Auth` the way the existing tests/daemon do. Check `auth.go` for the real constructor (likely `NewAuth(key []byte)` or it is built from the config's base64 `shared_key`); use whatever the existing code uses and match it in the test.

- [ ] **Step 1: Add a `Handler()` method (refactor mux out of Serve)**

In `internal/transport/server.go`, extract the mux construction into a `Handler()` method and have `Serve` use it. Replace the mux-building lines at the top of `Serve` with a call to `s.Handler()`:

```go
// Handler builds the HTTP routes. Exposed so it can be exercised in tests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/clip", s.postClip)
	mux.HandleFunc("GET /v1/peers", s.getPeers)
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /v1/history", s.getHistory)
	mux.HandleFunc("DELETE /v1/history", s.deleteHistory)
	mux.HandleFunc("POST /v1/restore", s.postRestore)
	mux.HandleFunc("POST /v1/history/pin", s.postPin)
	return mux
}
```

And in `Serve`, replace the local `mux := http.NewServeMux()` + the three `mux.HandleFunc(...)` lines with:

```go
	srv := &http.Server{Addr: s.listen, Handler: s.Handler()}
```

(Delete the now-duplicated mux lines; everything else in `Serve` stays.)

- [ ] **Step 2: Write the failing test**

Create `internal/transport/history_endpoint_test.go`:

```go
package transport

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testAuth builds an *Auth the same way the rest of the package does. Adjust
// this one line to match auth.go's real constructor if it differs.
func testAuth() *Auth { return NewAuth([]byte("test-key-0123456789012345678901")) }

func TestHistoryGETNoSignature(t *testing.T) {
	called := false
	srv := NewServer(":0", testAuth(), nil, func() any { return nil })
	srv.SetHistory(
		func(limit int) (any, error) { called = true; return []string{"x"}, nil },
		nil, nil, nil,
	)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/v1/history?limit=10", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !called {
		t.Fatal("history func not invoked")
	}
}

func TestRestoreRequiresSignature(t *testing.T) {
	auth := testAuth()
	srv := NewServer(":0", auth, nil, func() any { return nil })
	restored := ""
	srv.SetHistory(
		func(limit int) (any, error) { return nil, nil },
		func(id string) error { restored = id; return nil },
		func(id string, pinned bool) error { return nil },
		func(id string, allUnpinned bool) error { return nil },
	)
	h := srv.Handler()

	body, _ := json.Marshal(map[string]string{"id": "abc"})
	req := httptest.NewRequest(http.MethodPost, "/v1/restore", bytes.NewReader(body))
	req.Header.Set("X-Clipfan-Sig", auth.Sign(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if restored != "abc" {
		t.Fatalf("restore id = %q, want abc", restored)
	}

	// Unsigned must be rejected.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/restore", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d, want 401", rec2.Code)
	}
}
```

> Confirm `NewAuth`'s real signature in `auth.go` and fix `testAuth` to match (it may take a base64 string or the `*config.Config`). The rest of the test is signature-agnostic.

- [ ] **Step 3: Run tests to verify they fail**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/transport/ -run 'TestHistory|TestRestore' -v`
Expected: FAIL — `SetHistory` / the handlers undefined.

- [ ] **Step 4: Add fields, setter, and handlers**

In `internal/transport/server.go`, add the callback types (top level):

```go
// History callbacks, wired by the daemon. Nil until SetHistory is called.
type HistoryFunc func(limit int) (any, error)
type RestoreFunc func(id string) error
type PinFunc func(id string, pinned bool) error
type DeleteHistoryFunc func(id string, allUnpinned bool) error
```

Add fields to the `Server` struct:

```go
	historyFn HistoryFunc
	restoreFn RestoreFunc
	pinFn     PinFunc
	deleteFn  DeleteHistoryFunc
```

Add the setter:

```go
// SetHistory wires the history endpoints. Called by the daemon after construction.
func (s *Server) SetHistory(h HistoryFunc, r RestoreFunc, p PinFunc, d DeleteHistoryFunc) {
	s.historyFn, s.restoreFn, s.pinFn, s.deleteFn = h, r, p, d
}
```

Add the handlers. GET is loopback-only (no signature, like `getPeers`); the mutating routes verify the HMAC exactly like `postClip` does:

```go
func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	if s.historyFn == nil {
		http.Error(w, "history disabled", http.StatusServiceUnavailable)
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	out, err := s.historyFn(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"entries": out})
}

func (s *Server) deleteHistory(w http.ResponseWriter, r *http.Request) {
	body := s.readSigned(w, r)
	if body == nil {
		return
	}
	var req struct {
		ID          string `json:"id"`
		AllUnpinned bool   `json:"all_unpinned"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.deleteFn(req.ID, req.AllUnpinned); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) postRestore(w http.ResponseWriter, r *http.Request) {
	body := s.readSigned(w, r)
	if body == nil {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.restoreFn(req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) postPin(w http.ResponseWriter, r *http.Request) {
	body := s.readSigned(w, r)
	if body == nil {
		return
	}
	var req struct {
		ID     string `json:"id"`
		Pinned bool   `json:"pinned"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.pinFn(req.ID, req.Pinned); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// readSigned reads the body and verifies the HMAC signature, mirroring postClip.
// It writes an error response and returns nil on failure. Note auth.Verify
// returns an error (nil == valid), not a bool.
func (s *Server) readSigned(w http.ResponseWriter, r *http.Request) []byte {
	sig := r.Header.Get("X-Clipfan-Sig")
	if sig == "" {
		http.Error(w, "missing X-Clipfan-Sig", http.StatusUnauthorized)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil
	}
	if err := s.auth.Verify(body, sig); err != nil {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return nil
	}
	return body
}
```

Add `"strconv"` to the import block (the file already imports `encoding/json`, `io`, `net/http`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./internal/transport/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/transport/server.go internal/transport/history_endpoint_test.go
git commit -m "feat(transport): history/restore/pin/delete HTTP endpoints"
```

---

## Task 9: Daemon — wire endpoints to store/Restore

**Files:**
- Modify: `internal/daemon/daemon.go` (in `New`, after `d.sv = transport.NewServer(...)`)
- Test: covered by an integration check (build + existing tests)

- [ ] **Step 1: Wire SetHistory in New**

In `internal/daemon/daemon.go`, in `New`, immediately after `d.sv = transport.NewServer(...)`, add:

```go
	d.sv.SetHistory(
		func(limit int) (any, error) { return store.LoadHistory(limit) },
		d.Restore,
		store.SetPinned,
		func(id string, allUnpinned bool) error {
			if allUnpinned {
				return store.ClearUnpinned()
			}
			return store.DeleteEntry(id)
		},
	)
```

> `SetHistory`'s fourth parameter type is `transport.DeleteHistoryFunc` (defined in Task 8); the closure literal above satisfies it. The other three are `transport.HistoryFunc`, `transport.RestoreFunc`, `transport.PinFunc`. `store.SetPinned` matches `PinFunc` directly.

- [ ] **Step 2: Build + full test run**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go build ./... && PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./...`
Expected: build OK, all tests PASS.

- [ ] **Step 3: Linux cross-build**

Run: `PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc GOOS=linux CGO_ENABLED=0 go build ./...`
Expected: exit 0.

- [ ] **Step 4: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat(daemon): wire history endpoints to store + Restore"
```

---

## Task 10: Swift — HistoryEntry model + decoding

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/HistoryEntry.swift`
- Create: `apps/mac/Clipfan/Tests/ClipfanTests/HistoryViewModelTests.swift` (decoding test here for now)
- Modify: `apps/mac/Clipfan/Package.swift` (add a test target if none exists)

- [ ] **Step 1: Confirm/!add the test target**

Read `apps/mac/Clipfan/Package.swift`. If there is no `.testTarget`, add one:

```swift
        .testTarget(
            name: "ClipfanTests",
            dependencies: ["Clipfan"]
        ),
```

For the executable's symbols to be visible to tests, the app code must be in a library target or `@testable import` the executable target. If the app is a single executable target named `Clipfan`, add `@testable import Clipfan` in tests (works for executable targets in SwiftPM 5.5+). If that proves unavailable, move the testable logic (HistoryEntry, HistoryViewModel) into a small library target `ClipfanCore` that both the app and tests depend on, and import that. Choose the smallest change that compiles.

- [ ] **Step 2: Write the failing decoding test**

Create `apps/mac/Clipfan/Tests/ClipfanTests/HistoryViewModelTests.swift`:

```swift
import XCTest
@testable import Clipfan

final class HistoryDecodingTests: XCTestCase {
    func testDecodeEntries() throws {
        let json = """
        {"entries":[
          {"id":"a1","kind":"image","preview":"shot.png","image_path":"/s/a1.png","size_bytes":1234,"origin":"magic-kingdom","ts":"2026-05-28T10:00:00Z","pinned":false},
          {"id":"b2","kind":"link","preview":"https://x.com","text":"https://x.com","size_bytes":13,"origin":"m4","ts":"2026-05-28T10:01:00Z","pinned":true}
        ]}
        """.data(using: .utf8)!
        let resp = try JSONDecoder.clipfan.decode(HistoryResponse.self, from: json)
        XCTAssertEqual(resp.entries.count, 2)
        XCTAssertEqual(resp.entries[0].kind, .image)
        XCTAssertEqual(resp.entries[0].origin, "magic-kingdom")
        XCTAssertEqual(resp.entries[1].kind, .link)
        XCTAssertTrue(resp.entries[1].pinned)
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd apps/mac/Clipfan && swift test --filter HistoryDecodingTests`
Expected: FAIL — `HistoryEntry`/`HistoryResponse` undefined.

- [ ] **Step 4: Implement the model**

Create `apps/mac/Clipfan/Sources/Clipfan/HistoryEntry.swift`:

```swift
import Foundation

enum ClipKind: String, Codable {
    case text, image, link
}

struct HistoryEntry: Codable, Identifiable, Equatable {
    let id: String
    let kind: ClipKind
    let preview: String
    let text: String?
    let imagePath: String?
    let sizeBytes: Int
    let origin: String
    let ts: Date
    let pinned: Bool

    enum CodingKeys: String, CodingKey {
        case id, kind, preview, text
        case imagePath = "image_path"
        case sizeBytes = "size_bytes"
        case origin, ts, pinned
    }
}

struct HistoryResponse: Codable {
    let entries: [HistoryEntry]
}
```

> `JSONDecoder.clipfan` already exists in `Models.swift` (handles ISO8601 + Go zero-time). Reuse it; do not add a second decoder.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd apps/mac/Clipfan && swift test --filter HistoryDecodingTests`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/HistoryEntry.swift apps/mac/Clipfan/Tests/ClipfanTests/HistoryViewModelTests.swift apps/mac/Clipfan/Package.swift
git commit -m "feat(mac): HistoryEntry model + decoding test"
```

---

## Task 11: Swift — HistoryViewModel (search/filter/sort)

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/HistoryViewModel.swift`
- Modify: `apps/mac/Clipfan/Tests/ClipfanTests/HistoryViewModelTests.swift`

- [ ] **Step 1: Write the failing tests**

Append to `HistoryViewModelTests.swift`:

```swift
final class HistoryFilterTests: XCTestCase {
    func entry(_ id: String, _ kind: ClipKind, _ preview: String, pinned: Bool = false) -> HistoryEntry {
        HistoryEntry(id: id, kind: kind, preview: preview, text: preview,
                     imagePath: nil, sizeBytes: 1, origin: "m4",
                     ts: Date(timeIntervalSince1970: 1), pinned: pinned)
    }

    func testSearchFiltersBySubstringCaseInsensitive() {
        let all = [entry("1", .text, "Hello World"), entry("2", .text, "goodbye")]
        let out = filteredHistory(all, search: "hello", typeFilter: .all)
        XCTAssertEqual(out.map(\.id), ["1"])
    }

    func testTypeFilter() {
        let all = [entry("1", .text, "t"), entry("2", .image, "i"), entry("3", .link, "https://x")]
        XCTAssertEqual(filteredHistory(all, search: "", typeFilter: .image).map(\.id), ["2"])
        XCTAssertEqual(filteredHistory(all, search: "", typeFilter: .link).map(\.id), ["3"])
    }

    func testPinnedFloatToTop() {
        let all = [entry("1", .text, "a"), entry("2", .text, "b", pinned: true)]
        XCTAssertEqual(filteredHistory(all, search: "", typeFilter: .all).first?.id, "2")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd apps/mac/Clipfan && swift test --filter HistoryFilterTests`
Expected: FAIL — `filteredHistory`/`TypeFilter` undefined.

- [ ] **Step 3: Implement the view-model logic**

Create `apps/mac/Clipfan/Sources/Clipfan/HistoryViewModel.swift`:

```swift
import Foundation

enum TypeFilter: String, CaseIterable, Identifiable {
    case all, text, image, link
    var id: String { rawValue }
    var label: String {
        switch self {
        case .all: return "All"
        case .text: return "Text"
        case .image: return "Image"
        case .link: return "Link"
        }
    }
}

/// filteredHistory applies the search string and type filter, then floats
/// pinned items to the top (preserving the server's newest-first order within
/// each group). Pure function — unit-tested.
func filteredHistory(_ entries: [HistoryEntry], search: String, typeFilter: TypeFilter) -> [HistoryEntry] {
    let q = search.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    let matched = entries.filter { e in
        let typeOK = typeFilter == .all || e.kind.rawValue == typeFilter.rawValue
        guard typeOK else { return false }
        guard !q.isEmpty else { return true }
        let hay = (e.preview + " " + (e.text ?? "")).lowercased()
        return hay.contains(q)
    }
    return matched.sorted { a, b in
        if a.pinned != b.pinned { return a.pinned }
        return a.ts > b.ts
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd apps/mac/Clipfan && swift test`
Expected: PASS (all Swift tests)

- [ ] **Step 5: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/HistoryViewModel.swift apps/mac/Clipfan/Tests/ClipfanTests/HistoryViewModelTests.swift
git commit -m "feat(mac): history search/filter/sort view-model"
```

---

## Task 12: Swift — DaemonClient history API

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift`

- [ ] **Step 1: Add history methods**

In `DaemonClient`, add a published property and methods. The daemon's restore/pin/delete are HMAC-signed; the app must read the shared key from the local config and sign requests exactly as the Go client does (HMAC-SHA256 over the raw body, hex-encoded, in the `X-Clipfan-Sig` header).

Add:

```swift
    @Published var history: [HistoryEntry] = []

    func refreshHistory() async {
        guard let url = URL(string: "\(base)/v1/history?limit=200") else { return }
        do {
            let (data, _) = try await URLSession.shared.data(from: url)
            let resp = try JSONDecoder.clipfan.decode(HistoryResponse.self, from: data)
            await MainActor.run { self.history = resp.entries }
        } catch {
            // leave history as-is on transient failure
        }
    }

    func restore(_ id: String) async {
        await signedPost(path: "/v1/restore", body: ["id": id])
        await refreshHistory()
    }

    func setPinned(_ id: String, _ pinned: Bool) async {
        await signedPost(path: "/v1/history/pin", body: ["id": id, "pinned": pinned])
        await refreshHistory()
    }

    func deleteEntry(_ id: String) async {
        await signedRequest(method: "DELETE", path: "/v1/history", body: ["id": id])
        await refreshHistory()
    }

    func clearUnpinned() async {
        await signedRequest(method: "DELETE", path: "/v1/history", body: ["all_unpinned": true])
        await refreshHistory()
    }

    private func signedPost(path: String, body: [String: Any]) async {
        await signedRequest(method: "POST", path: path, body: body)
    }

    private func signedRequest(method: String, path: String, body: [String: Any]) async {
        guard let url = URL(string: "\(base)\(path)"),
              let payload = try? JSONSerialization.data(withJSONObject: body),
              let key = Self.sharedKey() else { return }
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.httpBody = payload
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue(Self.sign(payload, key: key), forHTTPHeaderField: "X-Clipfan-Sig")
        _ = try? await URLSession.shared.data(for: req)
    }
```

- [ ] **Step 2: Add HMAC signing helpers**

Add to `DaemonClient` (or a small `Signing.swift`). The shared key in `config.json` is base64; the Go side HMACs the raw body bytes and hex-encodes. Match exactly:

```swift
import CryptoKit

extension DaemonClient {
    static func sharedKey() -> SymmetricKey? {
        let path = ("~/.config/clipfan/config.json" as NSString).expandingTildeInPath
        guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let b64 = obj["shared_key"] as? String,
              let raw = Data(base64Encoded: b64) else { return nil }
        return SymmetricKey(data: raw)
    }

    static func sign(_ body: Data, key: SymmetricKey) -> String {
        let mac = HMAC<SHA256>.authenticationCode(for: body, using: key)
        return mac.map { String(format: "%02x", $0) }.joined()
    }
}
```

> VERIFY the key encoding against `internal/transport/auth.go` and `internal/config/config.go` during implementation: confirm `shared_key` is base64 of 32 raw bytes and that `Auth.Sign` HMACs the raw request body and hex-encodes. If the Go side base64-encodes the signature instead of hex, switch `sign` to base64. Add a round-trip sanity check by signing a known body and comparing against a value produced by the Go `auth.Sign` for the same key+body (compute once via a tiny Go scratch test and hardcode the expected hex in a Swift test).

- [ ] **Step 3: Build**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds.

- [ ] **Step 4: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift
git commit -m "feat(mac): DaemonClient history fetch/restore/pin/delete with HMAC"
```

---

## Task 13: Swift — HistoryRow + HistoryWindow (two-pane)

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/HistoryRow.swift`
- Create: `apps/mac/Clipfan/Sources/Clipfan/HistoryWindow.swift`

- [ ] **Step 1: Implement the row view**

Create `apps/mac/Clipfan/Sources/Clipfan/HistoryRow.swift`:

```swift
import SwiftUI

struct HistoryRow: View {
    let entry: HistoryEntry

    var body: some View {
        HStack(spacing: 10) {
            thumbnail
                .frame(width: 30, height: 30)
                .clipShape(RoundedRectangle(cornerRadius: 6))
            VStack(alignment: .leading, spacing: 2) {
                Text(entry.preview)
                    .lineLimit(1)
                    .font(.system(size: 13))
                HStack(spacing: 6) {
                    Text(entry.kind.rawValue)
                    Text(entry.origin)
                        .padding(.horizontal, 5)
                        .background(Color.secondary.opacity(0.2))
                        .clipShape(Capsule())
                    Text(entry.ts, style: .relative)
                }
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            }
            Spacer()
            if entry.pinned { Image(systemName: "pin.fill").font(.system(size: 10)) }
        }
        .padding(.vertical, 3)
    }

    @ViewBuilder private var thumbnail: some View {
        if entry.kind == .image, let path = entry.imagePath,
           let img = NSImage(contentsOfFile: path) {
            Image(nsImage: img).resizable().scaledToFill()
        } else {
            ZStack {
                Color.secondary.opacity(0.18)
                Image(systemName: entry.kind == .link ? "link" : "doc.text")
                    .font(.system(size: 13))
            }
        }
    }
}
```

- [ ] **Step 2: Implement the two-pane window**

Create `apps/mac/Clipfan/Sources/Clipfan/HistoryWindow.swift`:

```swift
import SwiftUI

struct HistoryWindow: View {
    @ObservedObject var daemon: DaemonClient
    @State private var search = ""
    @State private var filter: TypeFilter = .all
    @State private var selection: HistoryEntry.ID?

    private var items: [HistoryEntry] {
        filteredHistory(daemon.history, search: search, typeFilter: filter)
    }
    private var selected: HistoryEntry? {
        items.first { $0.id == selection } ?? items.first
    }

    var body: some View {
        VStack(spacing: 0) {
            searchBar
            Divider()
            HStack(spacing: 0) {
                list.frame(width: 260)
                Divider()
                preview.frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .frame(width: 640, height: 420)
        .task { await daemon.refreshHistory() }
        .onChange(of: items.map(\.id)) { _ in
            if selection == nil { selection = items.first?.id }
        }
    }

    private var searchBar: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
            TextField("Search clipboard…", text: $search)
                .textFieldStyle(.plain)
            Picker("", selection: $filter) {
                ForEach(TypeFilter.allCases) { f in Text(f.label).tag(f) }
            }
            .pickerStyle(.segmented)
            .frame(width: 220)
        }
        .padding(10)
    }

    private var list: some View {
        List(selection: $selection) {
            ForEach(items) { e in
                HistoryRow(entry: e)
                    .tag(e.id)
                    .contextMenu {
                        Button(e.pinned ? "Unpin" : "Pin") {
                            Task { await daemon.setPinned(e.id, !e.pinned) }
                        }
                        Button("Delete", role: .destructive) {
                            Task { await daemon.deleteEntry(e.id) }
                        }
                    }
            }
        }
        .listStyle(.sidebar)
        .onKeyPress(.return) {
            if let id = selection { Task { await daemon.restore(id) }; return .handled }
            return .ignored
        }
    }

    @ViewBuilder private var preview: some View {
        if let e = selected {
            VStack(alignment: .leading, spacing: 0) {
                Group {
                    if e.kind == .image, let p = e.imagePath, let img = NSImage(contentsOfFile: p) {
                        Image(nsImage: img).resizable().scaledToFit().padding()
                    } else {
                        ScrollView { Text(e.text ?? e.preview).textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading).padding() }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                Divider()
                HStack(spacing: 8) {
                    Text(e.kind.rawValue)
                    Text("\(e.sizeBytes) B")
                    Text("from \(e.origin)")
                    Text(e.ts, style: .relative)
                    Spacer()
                    Button("Paste") { Task { await daemon.restore(e.id) } }
                }
                .font(.system(size: 11)).foregroundStyle(.secondary).padding(10)
            }
        } else {
            Text("No clipboard history").foregroundStyle(.secondary)
        }
    }
}
```

> `onKeyPress` requires macOS 14+. The app targets macOS 13 (`LSMinimumSystemVersion` 13.0). Either (a) bump the deployment target to 14 if acceptable, or (b) gate the `.onKeyPress` modifier with `if #available(macOS 14, *)` and provide the double-click/Enter via a fallback (a `.onSubmit`-driven hidden control or a `Button` with `.keyboardShortcut(.defaultAction)`). Decide during implementation and keep the keyboard-restore behavior working on the actual target OS. Confirm the real target in `Info.plist`/`Package.swift` before choosing.

- [ ] **Step 3: Build**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds (resolve any availability gating from the note above).

- [ ] **Step 4: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/HistoryRow.swift apps/mac/Clipfan/Sources/Clipfan/HistoryWindow.swift
git commit -m "feat(mac): two-pane history window + row view"
```

---

## Task 14: Swift — wire window into app + menu + global hotkey

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/GlobalHotkey.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift`

- [ ] **Step 1: Add the history Window + menu item**

In `ClipfanApp.swift`, add a `Window` scene for history and open it via the environment's `openWindow`. Add alongside the existing settings window:

```swift
        Window("Clipboard History", id: "history") {
            HistoryWindow(daemon: DaemonClient.shared)
        }
        .windowResizability(.contentSize)
```

In `StatusMenuView.swift`, add a menu item near the top (above Settings):

```swift
        Button("Clipboard History…") { openWindow(id: "history") }
            .keyboardShortcut("h")
```

Add `@Environment(\.openWindow) private var openWindow` to `StatusMenuView` if not already present.

- [ ] **Step 2: Implement the global hotkey (⇧⌘V default)**

Create `apps/mac/Clipfan/Sources/Clipfan/GlobalHotkey.swift` using Carbon `RegisterEventHotKey` (no third-party dep — superpowers/zero-dep ethos applies to this repo too):

```swift
import AppKit
import Carbon.HIToolbox

/// Registers a single global hotkey and invokes a callback on press.
final class GlobalHotkey {
    private var ref: EventHotKeyRef?
    private var handler: EventHandlerRef?
    private let onFire: () -> Void

    init(keyCode: UInt32 = UInt32(kVK_ANSI_V),
         modifiers: UInt32 = UInt32(cmdKey | shiftKey),
         onFire: @escaping () -> Void) {
        self.onFire = onFire
        register(keyCode: keyCode, modifiers: modifiers)
    }

    private func register(keyCode: UInt32, modifiers: UInt32) {
        var eventType = EventTypeSpec(eventClass: OSType(kEventClassKeyboard),
                                      eventKind: OSType(kEventHotKeyPressed))
        let selfPtr = Unmanaged.passUnretained(self).toOpaque()
        InstallEventHandler(GetApplicationEventTarget(), { _, event, ctx in
            guard let ctx = ctx else { return noErr }
            Unmanaged<GlobalHotkey>.fromOpaque(ctx).takeUnretainedValue().onFire()
            return noErr
        }, 1, &eventType, selfPtr, &handler)

        let id = EventHotKeyID(signature: OSType(0x43464E48 /* 'CFNH' */), id: 1)
        RegisterEventHotKey(keyCode, modifiers, id, GetApplicationEventTarget(), 0, &ref)
    }

    deinit {
        if let ref { UnregisterEventHotKey(ref) }
        if let handler { RemoveEventHandler(handler) }
    }
}
```

Wire it in `ClipfanApp` so pressing ⇧⌘V opens the history window. In the `App`'s init or an `AppDelegate`, hold a `GlobalHotkey` that calls a notification the window scene observes, or activates the app and opens the window:

```swift
    // Held for the app's lifetime; opens the history window on ⇧⌘V.
    private let hotkey = GlobalHotkey {
        NSApp.activate(ignoringOtherApps: true)
        NotificationCenter.default.post(name: .openClipfanHistory, object: nil)
    }
```

Add the notification name and have the app open the window on receipt (use an `AppDelegate` with `@NSApplicationDelegateAdaptor`, or observe in a small hosting view). Define:

```swift
extension Notification.Name { static let openClipfanHistory = Notification.Name("openClipfanHistory") }
```

> The cleanest wiring for `MenuBarExtra` apps: add an `@NSApplicationDelegateAdaptor(AppDelegate.self)`, create the `GlobalHotkey` in `applicationDidFinishLaunching`, and in its callback call the SwiftUI `openWindow` via an injected closure or post `.openClipfanHistory` and observe it where `openWindow` is available. Pick one approach and keep it minimal. Confirm `Info.plist` has no setting that blocks global hotkeys; LSUIElement apps can register them.

- [ ] **Step 3: Build + run**

Run: `cd apps/mac/Clipfan && swift build && ./build-app.sh`
Expected: `Clipfan.app` builds. Launch it (`open .build/Clipfan.app`).

- [ ] **Step 4: Manual verification (live)**

1. Copy several text items and an image on the Mac.
2. Click the menubar icon → "Clipboard History…" (or press ⇧⌘V). The two-pane window opens.
3. Confirm: text rows show snippets, the image row shows a thumbnail, each row shows an origin-host badge, selecting the image shows a large preview.
4. Copy something on a Linux fleet host (e.g. `ssh magic-kingdom 'echo from-mk | clipfan copy'` if available, or copy in a GUI). Confirm it appears in the Mac history with origin `magic-kingdom`.
5. Select an older item, press Enter (or click Paste). Confirm it becomes the current clipboard (⌘V pastes it) and syncs (check another host or `/v1/peers`).
6. Pin an item, push >200 new clips (script a loop), confirm the pinned item survives.
7. Copy from a password manager; confirm it does NOT appear in history.

Record results in the commit message.

- [ ] **Step 5: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/GlobalHotkey.swift apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift
git commit -m "feat(mac): open history window from menu + global hotkey (shift-cmd-V)"
```

---

## Task 15: Full verification + docs sync + deploy

**Files:**
- Modify: `docs/ARCHITECTURE.md`, `docs/ROADMAP.md`, `README.md` (only if drift vs. final implementation)

- [ ] **Step 1: Full backend gate**

Run:
```
PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go test ./...
PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc go vet ./...
PATH=/opt/homebrew/bin:/usr/bin:/bin CC=/usr/bin/cc GOOS=linux CGO_ENABLED=0 go build ./...
cd apps/mac/Clipfan && swift test && swift build
```
Expected: all green.

- [ ] **Step 2: Reconcile docs with what shipped**

Re-read `docs/ARCHITECTURE.md`'s history section and the README history section. Correct any divergence between the design doc and the actual endpoints/fields/behavior implemented (e.g. signature encoding, deployment-target note, exact endpoint paths). Keep them evergreen (present tense, no changelog language). Update `ROADMAP.md` to move the history browser from "planned" to "done", stated plainly.

- [ ] **Step 3: Rebuild + deploy + verify on fleet**

Rebuild binaries (`dist/build-all.sh`) and the app, deploy the daemon to all four hosts (m4 shell-launched; paradise-park launchd; flower-garden + magic-kingdom `systemctl --user`), restart, and re-run the live history checks from Task 14 Step 4 across hosts. This mirrors the existing deploy/verify pattern. Verify binaries by SHA-256.

- [ ] **Step 4: Commit any doc reconciliation**

```bash
git add docs/ README.md
git commit -m "docs: reconcile history docs with shipped implementation"
```

- [ ] **Step 5: Update Linear PRI-1875**

Post a comment summarizing the implemented feature and move the ticket to In Review.

---

## Self-Review notes

- **Spec coverage:** two-pane window (T13), local-per-host recording (T6), 200-item count cap + pinned-exempt GC (T2/T3), image-GC reference protection (T4), restore=re-copy+sync (T7), menubar+hotkey (T14), search/type-filter/pin/delete (T11/T13), origin badge (T11/T13), concealed-clip privacy (T5/T6), HMAC-signed mutating endpoints + loopback GET (T8), config cap (T1). All spec sections map to a task.
- **Deferred items** (merged history, auto-paste, rich link cards, paste-stack, OCR) are intentionally absent.
- **Verification-required points flagged inline** (do not fabricate): concealed-type detection (T5), HMAC key/signature encoding (T12), and the macOS-14 `onKeyPress` availability gate (T13). Each says: verify against the real artifact; if unverifiable, degrade honestly rather than claim a guarantee.
