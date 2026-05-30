# Clip-ID Recirculation Prevention — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every clip a stable random ID assigned once at its origin, dedup by that ID across the mesh, and suppress echoes of our own clipboard writes — so a clip is processed exactly once per host and never recirculates.

**Architecture:** `Envelope` and `clipboard.Content` carry an `ID`. Origins (`pollOnce` for GUI copies, `clipfan copy` for CLI, `Restore`) mint a random 128-bit ID; relays preserve it. The daemon dedups by ID in `seenSet` (now string-keyed). A `currentClip` record of what the daemon last wrote to the clipboard (id + canonical hash + image path) lets `pollOnce` recognise and suppress echoes that come back re-represented.

**Tech Stack:** Go (daemon, transport, store, cli), `crypto/rand`, standard `testing`.

**Spec:** `docs/superpowers/specs/2026-05-29-clip-id-recirculation-design.md`

**Run all tests with:** `go test ./...` from repo root `/Users/jesse/Documents/GitHub/prime-radiant-inc/clipfan`.

---

## Task 1: Carry a clip-ID on the wire and in-process

**Files:**
- Modify: `internal/transport/envelope.go` (add `ID` field + `NewClipID`)
- Modify: `internal/clipboard/clipboard.go` (add `ID` to `Content`)
- Modify: `internal/transport/client.go:38-45` (`PushAs` stamps `env.ID`)
- Modify: `internal/transport/server.go:114-116` (`postClip` copies `env.ID` onto the Content)
- Test: `internal/transport/clipid_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/transport/clipid_test.go`:

```go
package transport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestNewClipID(t *testing.T) {
	a := NewClipID()
	if len(a) != 32 {
		t.Fatalf("NewClipID len = %d, want 32 hex chars", len(a))
	}
	if a == NewClipID() {
		t.Fatal("two NewClipID() calls returned the same value")
	}
}

func TestEnvelopeIDRoundTrip(t *testing.T) {
	in := Envelope{Origin: "m4", Kind: "text", ID: "abc123", Body: EncodeBody([]byte("hi"))}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"id":"abc123"`) {
		t.Fatalf("marshaled envelope missing id: %s", raw)
	}
	var out Envelope
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "abc123" {
		t.Fatalf("round-trip ID = %q, want abc123", out.ID)
	}
}

// PushAs must stamp content.ID into the wire envelope, and the receiving
// server must surface it on the Content passed to onRecv.
func TestPushCarriesIDToReceiver(t *testing.T) {
	auth, err := NewAuth("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	var got clipboard.Content
	recv := func(c clipboard.Content, origin string) { got = c }
	srv := NewServer("", auth, recv, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	host, port := splitHostPort(t, ts.URL)
	cl := NewClient(auth, "m4")
	content := clipboard.Content{Kind: clipboard.KindText, Bytes: []byte("hello"), ID: "clip-xyz"}
	if err := cl.PushAs(context.Background(), host, port, content, "m4"); err != nil {
		t.Fatal(err)
	}
	if got.ID != "clip-xyz" {
		t.Fatalf("receiver Content.ID = %q, want clip-xyz", got.ID)
	}
}
```

Add this helper at the bottom of the same file (the test server URL is `http://127.0.0.1:PORT`):

```go
func splitHostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	hp := strings.TrimPrefix(rawURL, "http://")
	host, portStr, found := strings.Cut(hp, ":")
	if !found {
		t.Fatalf("no port in %s", rawURL)
	}
	port := 0
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}
	return host, port
}
```

- [ ] **Step 2: Run the test, watch it fail**

Run: `go test ./internal/transport/ -run 'TestNewClipID|TestEnvelopeIDRoundTrip|TestPushCarriesIDToReceiver' 2>&1 | tail`
Expected: build failure — `undefined: NewClipID`, `unknown field ID in struct literal of type Envelope`, `unknown field ID ... clipboard.Content`.

- [ ] **Step 3: Add the `ID` field and `NewClipID` to the envelope**

In `internal/transport/envelope.go`, add `ID` to the struct and the constructor. Final file:

```go
package transport

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"time"
)

type Envelope struct {
	ID     string    `json:"id"`
	Origin string    `json:"origin"`
	TS     time.Time `json:"ts"`
	Kind   string    `json:"kind"`
	SHA256 string    `json:"sha256"`
	Body   string    `json:"body"`
}

// NewClipID returns a random 128-bit hex token identifying one logical clip.
// Assigned once at a clip's origin and preserved verbatim through every relay,
// so the mesh can dedup by identity rather than by mutable content bytes.
func NewClipID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

func (e *Envelope) Bytes() ([]byte, error) {
	return base64.StdEncoding.DecodeString(e.Body)
}

func EncodeBody(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
```

- [ ] **Step 4: Add `ID` to `clipboard.Content`**

In `internal/clipboard/clipboard.go`, add the field to the `Content` struct (leave `New` untouched — ID stays empty unless explicitly set):

```go
type Content struct {
	ID        string // clip identity; empty until minted at the origin
	Kind      Kind
	Bytes     []byte
	Hash      [32]byte
	TS        time.Time
	Concealed bool // true if the source marked the clip as transient/secret
}
```

- [ ] **Step 5: Stamp and surface the ID in transport**

In `internal/transport/client.go`, add `ID` to the envelope built by `PushAs` (line ~39):

```go
	env := Envelope{
		ID:     content.ID,
		Origin: origin,
		TS:     content.TS,
		Kind:   string(content.Kind),
		SHA256: hex.EncodeToString(content.Hash[:]),
		Body:   EncodeBody(content.Bytes),
	}
```

In `internal/transport/server.go`, in `postClip`, set the ID on the Content before dispatch (line ~114):

```go
	c := clipboard.New(clipboard.Kind(env.Kind), raw, env.TS)
	c.ID = env.ID
	slog.Debug("clip received", "id", env.ID, "origin", env.Origin, "kind", env.Kind, "bytes", len(raw))
	s.onRecv(c, env.Origin)
```

- [ ] **Step 6: Run the tests, watch them pass**

Run: `go test ./internal/transport/ ./internal/clipboard/ 2>&1 | tail`
Expected: `ok` for both packages.

- [ ] **Step 7: Commit**

```bash
git add internal/transport/envelope.go internal/transport/client.go internal/transport/server.go internal/clipboard/clipboard.go internal/transport/clipid_test.go
git commit -m "feat(transport): carry a clip-ID on the envelope and Content"
```

---

## Task 2: Mint clip-IDs at every origin, preserve through relay

**Files:**
- Modify: `internal/daemon/daemon.go` (`pollOnce` mints for new local clips; `Restore` mints)
- Modify: `internal/cli/cli.go:127-135` (`pushToDaemon` stamps `env.ID`)
- Modify: `internal/daemon/echo_test.go:50-55` (record `id` in `pushCall`)
- Test: `internal/daemon/clipid_test.go` (new)

Context: `fanout` → `PushAs` already serialises `content.ID` (Task 1). Relays call `fanout(c, origin)` with the *received* `c`, whose `ID` is already set, so relays preserve it for free. This task only needs the *origins* to populate the ID, and a test harness that can observe it.

- [ ] **Step 1: Extend the test pusher to record the clip-ID**

In `internal/daemon/echo_test.go`, add an `id` field to `pushCall` and record it. Change the `pushCall` struct (line ~50) and `PushAs` (line ~54):

```go
type pushCall struct {
	host string
	id   string
	kind clipboard.Kind
	hash [32]byte
}

func (p *fakePusher) PushAs(ctx context.Context, host string, port int, content clipboard.Content, origin string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, pushCall{host: host, id: content.ID, kind: content.Kind, hash: content.Hash})
	return nil
}
```

- [ ] **Step 2: Write the failing tests**

Create `internal/daemon/clipid_test.go`:

```go
package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

// A genuine local copy detected by pollOnce must be broadcast with a freshly
// minted, non-empty clip-ID.
func TestPollMintsClipID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)
	cb.current = clipboard.New(clipboard.KindText, []byte("hello world"), fixedTime)

	d.pollOnce(context.Background())
	waitForPushes(t, push, 1)

	if id := push.snapshot()[0].id; id == "" {
		t.Fatal("pollOnce broadcast a clip with an empty ID")
	}
}

// A relayed clip keeps the original sender's ID; the relay must not re-mint.
func TestRelayPreservesClipID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, push := newTestDaemon(t)
	c := clipboard.New(clipboard.KindText, []byte("relay me"), fixedTime)
	c.ID = "origin-assigned-id"

	d.onReceive(c, "some-origin")
	waitForPushes(t, push, 1)

	if id := push.snapshot()[0].id; id != "origin-assigned-id" {
		t.Fatalf("relay changed the clip ID to %q, want origin-assigned-id", id)
	}
}
```

- [ ] **Step 3: Run the tests, watch them fail**

Run: `go test ./internal/daemon/ -run 'TestPollMintsClipID|TestRelayPreservesClipID' 2>&1 | tail`
Expected: `TestPollMintsClipID` FAILS ("broadcast a clip with an empty ID"); `TestRelayPreservesClipID` PASSES already (relays pass `c` through unchanged).

- [ ] **Step 4: Mint an ID for new local clips in `pollOnce`**

In `internal/daemon/daemon.go`, in `pollOnce`, after the dedup bookkeeping and before `fanout`, set the ID on the local clip. Locate the block that ends with `d.fanout(ctx, c, "" /* skipOrigin = none */)` and stamp `c` before recording/broadcasting:

```go
	c.ID = transport.NewClipID()
	slog.Debug("local clip changed", "id", c.ID, "kind", c.Kind, "bytes", len(c.Bytes))
```

(Replace the existing `slog.Debug("local clip changed", ...)` line with the two lines above; `c` is a local variable so mutating it is safe. `transport` is already imported.)

- [ ] **Step 5: Mint an ID in `Restore`**

In `internal/daemon/daemon.go`, `Restore` builds a fresh `clipboard.Content` (`c = clipboard.New(...)`). After constructing `c` in **both** the image and text branches, set its ID. The simplest single edit: right after the `if e.Kind == "image" { ... } else { ... }` block closes (before `d.mu.Lock()`), add:

```go
	c.ID = transport.NewClipID()
```

- [ ] **Step 6: Mint an ID in the `clipfan copy` CLI**

In `internal/cli/cli.go`, `pushToDaemon` builds the `transport.Envelope`. Add the ID (line ~128):

```go
	env := transport.Envelope{
		ID:     transport.NewClipID(),
		Origin: origin,
		TS:     time.Now().UTC(),
		Kind:   kind,
		SHA256: hex.EncodeToString(sum[:]),
		Body:   transport.EncodeBody(body),
	}
```

- [ ] **Step 7: Run the tests, watch them pass**

Run: `go test ./internal/daemon/ -run 'TestPollMintsClipID|TestRelayPreservesClipID' 2>&1 | tail`
Expected: both PASS.

- [ ] **Step 8: Run the full daemon + cli suites (no regressions)**

Run: `go test ./internal/daemon/ ./internal/cli/ ./internal/transport/ 2>&1 | tail`
Expected: `ok` for all three.

- [ ] **Step 9: Commit**

```bash
git add internal/daemon/daemon.go internal/cli/cli.go internal/daemon/echo_test.go internal/daemon/clipid_test.go
git commit -m "feat(daemon): mint a clip-ID at every origin, preserve through relay"
```

---

## Task 3: Track the current clip and suppress echoes in `pollOnce`

**Files:**
- Modify: `internal/daemon/daemon.go` (add `currentClip` to `Daemon`; set it in `onReceive`/`Restore`; check it in `pollOnce`)
- Test: `internal/daemon/clipid_test.go` (append)

Context: when the daemon writes the clipboard (applying a received clip, or `Restore`), it records what it wrote. `pollOnce` then recognises a re-read of that write — including the image re-represented as its store path — and suppresses it instead of broadcasting a new clip.

- [ ] **Step 1: Write the failing tests**

Append to `internal/daemon/clipid_test.go` (no new imports needed — `context`, `testing`, `time`, and `clipboard` are already imported from Task 2):

```go
// After applying a received image, a pollOnce that reads the same image bytes
// back must be recognised as an echo and not broadcast.
func TestPollSuppressesImageBytesEcho(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)

	img := clipboard.New(clipboard.KindImage, []byte("PNGDATA"), fixedTime)
	img.ID = "img-1"
	d.onReceive(img, "peer")
	waitForPushes(t, push, 1) // the relay
	relayed := len(push.snapshot())

	// Clipboard now reads back the exact image bytes we applied.
	cb.current = clipboard.New(clipboard.KindImage, []byte("PNGDATA"), fixedTime)
	d.pollOnce(context.Background())
	time.Sleep(50 * time.Millisecond)

	if extra := len(push.snapshot()) - relayed; extra != 0 {
		t.Fatalf("pollOnce re-broadcast the image echo: %d extra pushes", extra)
	}
}

// A genuinely new local copy after a received clip is NOT an echo: it mints a
// new ID and broadcasts.
func TestPollBroadcastsGenuineNewCopy(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)

	first := clipboard.New(clipboard.KindText, []byte("received text"), fixedTime)
	first.ID = "txt-1"
	d.onReceive(first, "peer")
	waitForPushes(t, push, 1)
	relayed := len(push.snapshot())

	cb.current = clipboard.New(clipboard.KindText, []byte("brand new user copy"), fixedTime.Add(time.Second))
	d.pollOnce(context.Background())
	waitForPushes(t, push, relayed+1)

	got := push.snapshot()
	last := got[len(got)-1]
	if last.id == "" {
		t.Fatal("new copy broadcast with empty ID")
	}
}
```

- [ ] **Step 2: Run the tests, watch them fail**

Run: `go test ./internal/daemon/ -run 'TestPollSuppressesImageBytesEcho|TestPollBroadcastsGenuineNewCopy' 2>&1 | tail`
Expected: `TestPollSuppressesImageBytesEcho` FAILS (the image bytes echo is re-broadcast — 1 extra push). `TestPollBroadcastsGenuineNewCopy` likely PASSES (it already broadcasts), but keep it as a guard.

- [ ] **Step 3: Add the `currentClip` record to the `Daemon` struct**

In `internal/daemon/daemon.go`, add a type and field. After the `Daemon` struct's `seen`/`lastTS` fields (inside the `mu`-guarded group), add the field; define the type above `New`:

```go
// currentClip is the daemon's record of what it last wrote to the local
// clipboard, so pollOnce can recognise echoes of our own write — even when the
// content comes back re-represented (an image read back as its store path).
type currentClip struct {
	id        string
	kind      clipboard.Kind
	hash      [32]byte // canonical bytes hash (text bytes, or image bytes)
	imagePath string   // set for image clips
}
```

Add to the `Daemon` struct, in the `mu`-guarded block:

```go
	mu      sync.Mutex
	seen    *seenSet
	lastTS  time.Time
	current currentClip
```

- [ ] **Step 4: Record `currentClip` whenever we write the clipboard**

In `onReceive`, after the `WriteImage`/`WriteText` block (right after the `else { ... WriteText ... }`), record what we wrote:

```go
	d.mu.Lock()
	if c.Kind == clipboard.KindImage {
		d.current = currentClip{id: c.ID, kind: clipboard.KindImage, hash: c.Hash, imagePath: imagePath}
	} else {
		d.current = currentClip{id: c.ID, kind: clipboard.KindText, hash: c.Hash}
	}
	d.mu.Unlock()
```

In `Restore`, after the write block and after `c.ID = transport.NewClipID()` (Task 2), add the same record (image branch uses `e.ImagePath`):

```go
	d.mu.Lock()
	if e.Kind == "image" {
		d.current = currentClip{id: c.ID, kind: clipboard.KindImage, hash: c.Hash, imagePath: e.ImagePath}
	} else {
		d.current = currentClip{id: c.ID, kind: clipboard.KindText, hash: c.Hash}
	}
	d.mu.Unlock()
```

- [ ] **Step 5: Suppress echoes in `pollOnce`**

In `internal/daemon/daemon.go`, add an `isEcho` method and call it early in `pollOnce`. Add the method:

```go
// isEcho reports whether a freshly read clipboard content `c` is just our own
// last write coming back — possibly re-represented as the current image's store
// path — rather than a new user copy.
func (d *Daemon) isEcho(c clipboard.Content) bool {
	d.mu.Lock()
	cur := d.current
	d.mu.Unlock()
	if cur.id == "" {
		return false
	}
	if c.Kind == cur.kind && c.Hash == cur.hash {
		return true
	}
	if cur.kind == clipboard.KindImage && c.Kind == clipboard.KindText {
		if strings.TrimSpace(string(c.Bytes)) == cur.imagePath {
			return true
		}
	}
	return false
}
```

In `pollOnce`, after the existing `store.IsImageStorePath` guard and before the `d.seen` bookkeeping, add:

```go
	if d.isEcho(c) {
		return
	}
```

- [ ] **Step 6: Run the tests, watch them pass**

Run: `go test ./internal/daemon/ -run 'TestPollSuppressesImageBytesEcho|TestPollBroadcastsGenuineNewCopy|TestReceiveImageStorePathDoesNotClobber|TestPollDoesNotBroadcastImageStorePath' 2>&1 | tail`
Expected: all PASS.

- [ ] **Step 7: Run the full daemon suite (no regressions)**

Run: `go test ./internal/daemon/ 2>&1 | tail`
Expected: `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/clipid_test.go
git commit -m "feat(daemon): track current clip, suppress echoes of our own writes"
```

---

## Task 4: Dedup by clip-ID, drop ID-less envelopes, adopt the startup clipboard

**Files:**
- Modify: `internal/daemon/seen.go` (string-keyed set, cap 256)
- Modify: `internal/daemon/seen_test.go` (string keys)
- Modify: `internal/daemon/daemon.go` (`onReceive` dedup by ID + drop empty ID; `Run` adopts the startup clipboard; remove the now-redundant content-hash echo registrations)
- Test: `internal/daemon/clipid_test.go` (append)

Context: this is the dedup switch. `seenSet` moves from `[32]byte` content hashes to `string` clip-IDs. `pollOnce`'s old `d.seen.has(c.Hash)` check and `onReceive`'s content-hash registrations (the lines registering `loaded`/readback hashes) are removed — echo suppression is now `currentClip`'s job (Task 3), and mesh dedup is by ID.

- [ ] **Step 1: Write the failing tests**

Append to `internal/daemon/clipid_test.go`:

```go
// The same clip-ID arriving twice (e.g. via two relay paths) is applied and
// relayed only once.
func TestReceiveDedupsByID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, push := newTestDaemon(t)

	mk := func(body string) clipboard.Content {
		c := clipboard.New(clipboard.KindText, []byte(body), fixedTime)
		c.ID = "same-id"
		return c
	}
	d.onReceive(mk("first bytes"), "peer-a")
	waitForPushes(t, push, 1)
	// Second delivery: SAME id, DIFFERENT bytes — must be deduped by ID.
	d.onReceive(mk("different bytes but same id"), "peer-b")
	time.Sleep(50 * time.Millisecond)

	if got := len(push.snapshot()); got != 1 {
		t.Fatalf("same clip-ID applied/relayed %d times, want 1", got)
	}
}

// An envelope with no clip-ID is dropped, not applied or relayed.
func TestReceiveDropsEmptyID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)
	cb.current = clipboard.New(clipboard.KindText, []byte("SENTINEL"), fixedTime)

	c := clipboard.New(clipboard.KindText, []byte("no id here"), fixedTime)
	// c.ID intentionally empty
	d.onReceive(c, "peer")
	time.Sleep(50 * time.Millisecond)

	if got := len(push.snapshot()); got != 0 {
		t.Fatalf("ID-less envelope relayed %d times, want 0", got)
	}
	if cb.current.Kind != clipboard.KindText || string(cb.current.Bytes) != "SENTINEL" {
		t.Fatal("ID-less envelope was applied to the clipboard")
	}
}
```

- [ ] **Step 2: Run the tests, watch them fail**

Run: `go test ./internal/daemon/ -run 'TestReceiveDedupsByID|TestReceiveDropsEmptyID' 2>&1 | tail`
Expected: `TestReceiveDedupsByID` FAILS (deduped by content hash, but bytes differ → applied twice → 2 pushes). `TestReceiveDropsEmptyID` FAILS (ID-less clip is applied + relayed).

- [ ] **Step 3: Re-key `seenSet` to strings**

Replace `internal/daemon/seen.go` entirely:

```go
package daemon

// seenCap bounds how many recent clip-IDs the daemon remembers for mesh dedup.
const seenCap = 256

// seenSet is a bounded, insertion-ordered set of recently-seen clip-IDs. The
// oldest entry is evicted once the set exceeds seenCap. Not safe for concurrent
// use; callers must hold their own lock.
type seenSet struct {
	members map[string]struct{}
	order   []string
}

func newSeenSet() *seenSet {
	return &seenSet{members: make(map[string]struct{})}
}

func (s *seenSet) has(id string) bool {
	_, ok := s.members[id]
	return ok
}

func (s *seenSet) add(id string) {
	if s.has(id) {
		return
	}
	s.members[id] = struct{}{}
	s.order = append(s.order, id)
	if len(s.order) > seenCap {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.members, oldest)
	}
}
```

- [ ] **Step 4: Update `seen_test.go` to string keys**

Replace the contents of `internal/daemon/seen_test.go` with string-keyed equivalents of its existing cases:

```go
package daemon

import "testing"

func TestSeenSetAddHas(t *testing.T) {
	s := newSeenSet()
	if s.has("a") {
		t.Fatal("empty set reported membership")
	}
	s.add("a")
	if !s.has("a") {
		t.Fatal("add then has = false")
	}
	s.add("a") // idempotent
	if !s.has("a") {
		t.Fatal("re-add lost membership")
	}
}

func TestSeenSetEviction(t *testing.T) {
	s := newSeenSet()
	first := "id-0"
	s.add(first)
	for i := 1; i <= seenCap; i++ {
		s.add(string(rune('a'+i%26)) + string(rune('0'+i/26)) + "-pad-" + itoa(i))
	}
	if s.has(first) {
		t.Fatal("oldest entry not evicted past capacity")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
```

- [ ] **Step 5: Dedup `onReceive` by clip-ID and drop ID-less clips**

In `internal/daemon/daemon.go` `onReceive`: keep the leading `IsImageStorePath` guard, then replace the hash-based dedup block. The current head is:

```go
	d.mu.Lock()
	if d.seen.has(c.Hash) {
		d.mu.Unlock()
		return
	}
	if !d.lastTS.IsZero() && c.TS.Before(d.lastTS) {
		d.mu.Unlock()
		return
	}
	d.seen.add(c.Hash)
	d.lastTS = c.TS
	d.mu.Unlock()
```

Replace it with:

```go
	if c.ID == "" {
		slog.Debug("dropping clip with no ID", "origin", origin)
		return
	}
	d.mu.Lock()
	if d.seen.has(c.ID) {
		d.mu.Unlock()
		return
	}
	if !d.lastTS.IsZero() && c.TS.Before(d.lastTS) {
		d.mu.Unlock()
		return
	}
	d.seen.add(c.ID)
	d.lastTS = c.TS
	d.mu.Unlock()
```

- [ ] **Step 6: Remove the now-redundant content-hash echo registrations in `onReceive`**

Still in `onReceive`, delete the two blocks that registered written/readback hashes into `seen` (echo suppression is now `currentClip`'s job). Delete the block beginning with the comment `// Register the hash of exactly what we loaded into the tmux buffer.` through its `d.seen.add(loaded)` unlock, and the following block beginning `// Register what we just wrote so our own poll loop doesn't re-broadcast it.` through its `d.seen.add(readback.Hash)` unlock. (The `tmux.LoadBufferAll` call between/around them stays; only the `seen.add` bookkeeping is removed. Remove the now-unused `loaded := sha256.Sum256(...)` line too.)

- [ ] **Step 7: Fix `pollOnce` and `Run` for ID-keyed seen + startup adoption**

In `pollOnce`, the old `d.seen.has(c.Hash)` / `d.seen.add(c.Hash)` bookkeeping referenced content hashes. After Task 2/3, `pollOnce` mints `c.ID` and suppresses echoes via `isEcho`. Replace the `d.mu.Lock(); if d.seen.has(c.Hash) {...} d.seen.add(c.Hash); d.lastTS = c.TS; d.mu.Unlock()` block with ID bookkeeping placed **after** minting `c.ID` (Task 2 added `c.ID = transport.NewClipID()`):

```go
	d.mu.Lock()
	d.seen.add(c.ID)
	d.lastTS = c.TS
	d.mu.Unlock()
```

In `Run`, the startup read currently does `d.seen.add(c.Hash)` to avoid broadcasting the clip already present at launch. Replace that block so the startup clipboard is *adopted* as the current clip — `isEcho` keys off a non-empty `current.id`, so use the sentinel `"startup"` to mark "we already know this clipboard content":

```go
	if c, err := d.cb.Read(); err == nil && len(c.Bytes) > 0 {
		d.mu.Lock()
		d.lastTS = c.TS
		if c.Kind == clipboard.KindImage {
			d.current = currentClip{id: "startup", kind: clipboard.KindImage, hash: c.Hash}
		} else {
			d.current = currentClip{id: "startup", kind: clipboard.KindText, hash: c.Hash}
		}
		d.mu.Unlock()
	}
```

- [ ] **Step 8: Run the new tests, watch them pass**

Run: `go test ./internal/daemon/ -run 'TestReceiveDedupsByID|TestReceiveDropsEmptyID|TestSeenSet' 2>&1 | tail`
Expected: all PASS.

- [ ] **Step 9: Run the full suite (no regressions)**

Run: `go test ./... 2>&1 | tail -20`
Expected: `ok` for every package; in particular the existing `TestImageReceiveDoesNotEchoPath`, `TestRelayDedup`, `TestPollDoesNotBroadcastImageStorePath`, `TestReceiveImageStorePathDoesNotClobber` still pass. If `TestImageReceiveDoesNotEchoPath` or `TestRelayDedup` reference content-hash assumptions that the ID switch changes, update them to set `c.ID` on their input clips (they construct `clipboard.New(...)` without an ID; give each a fixed ID like `"img-1"`), since post-switch an ID-less clip is dropped.

- [ ] **Step 10: Commit**

```bash
git add internal/daemon/seen.go internal/daemon/seen_test.go internal/daemon/daemon.go internal/daemon/clipid_test.go internal/daemon/echo_test.go
git commit -m "feat(daemon): dedup by clip-ID, drop ID-less clips, adopt startup clipboard"
```

---

## Task 5: Whole-suite verification and build

**Files:** none (verification only)

- [ ] **Step 1: Full test suite**

Run: `go test ./... 2>&1 | tail -20`
Expected: exit 0, every package `ok`, no `FAIL`/`panic`.

- [ ] **Step 2: Vet and build all targets**

Run: `go vet ./... && bash dist/build-all.sh 2>&1 | tail -3`
Expected: vet clean; all four `dist/clipfan-*` binaries rebuilt.

- [ ] **Step 3: Confirm no stray content-hash dedup remains**

Run: `grep -n "seen.has(c.Hash)\|seen.add(c.Hash)\|seen.add(loaded)\|seen.add(readback" internal/daemon/daemon.go || echo "clean: all seen ops are ID-keyed"`
Expected: `clean: all seen ops are ID-keyed`.

- [ ] **Step 4: Commit any final touch-ups**

```bash
git add -A
git commit -m "chore(daemon): clip-ID recirculation pass — vet + build green" || echo "nothing to commit"
```

---

## Notes for the implementer

- **Repo root:** `/Users/jesse/Documents/GitHub/prime-radiant-inc/clipfan`. Run `go test` from there.
- **Keep the build green at every commit.** Tasks are ordered so each commit compiles and passes: Task 1 is additive, Task 2 populates IDs without changing dedup, Task 3 adds echo tracking, Task 4 flips dedup to IDs.
- **`transport` is already imported** in `daemon.go` and `cli.go`; no new imports there except `store` already present in `daemon.go`. Task 1 adds `crypto/rand` + `encoding/hex` to `envelope.go`.
- **Do not** remove `store.IsImageStorePath` or its guards — they remain defense-in-depth beneath the clip-ID mechanism.
- After Task 4, the deploy is fleet-wide (no ID-less compat); that rollout is performed separately by the user, not in this plan.
