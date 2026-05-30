# Configurable Settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the history limit and the global shortcut user-configurable in Settings → General → Clipboard.

**Architecture:** A new authenticated `POST /v1/config` daemon endpoint persists `MaxHistory` (the daemon stays the sole writer of `config.json`); the cap is already read live so it applies immediately, and `store.Recap()` trims excess on decrease. The global shortcut moves from a hardcoded Carbon hotkey to the `sindresorhus/KeyboardShortcuts` SwiftPM package, which supplies a SwiftUI recorder and `UserDefaults` persistence.

**Tech Stack:** Go 1.26 (daemon), Swift/SwiftPM/SwiftUI + KeyboardShortcuts, XCTest, `go test`.

**Spec:** `docs/superpowers/specs/2026-05-30-configurable-settings-design.md`

**Conventions:** Go tests `go test ./...` from repo root; Swift tests `cd apps/mac/Clipfan && swift test`. Commit after each green step with `type(scope): summary`.

---

## File Structure

- `internal/store/history.go` (modify) — exported `Recap()` and `CapLimit()`.
- `internal/store/recap_test.go` (new) — Recap trims unpinned beyond cap, keeps pinned.
- `internal/transport/server.go` (modify) — `setConfigFn` field, `SetConfigFunc`, `postConfig` route.
- `internal/transport/config_test.go` (new) — endpoint auth + decode + dispatch.
- `internal/daemon/daemon.go` (modify) — `setMaxHistory` hook, wiring, `max_history` in peers.
- `internal/daemon/config_test.go` (new) — clamp/reject + peers field.
- `apps/mac/Clipfan/Package.swift` (modify) — KeyboardShortcuts dependency.
- `apps/mac/Clipfan/Sources/Clipfan/Shortcuts.swift` (new) — shortcut name.
- `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift` (modify) — register handler, drop GlobalHotkey.
- `apps/mac/Clipfan/Sources/Clipfan/GlobalHotkey.swift` (delete).
- `apps/mac/Clipfan/Sources/Clipfan/Models.swift` (modify) — `PeersResponse.max_history`.
- `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift` (modify) — `maxHistory`, `setMaxHistory`, fetch limit.
- `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift` (modify) — stepper + recorder.
- `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift` (modify) — dynamic shortcut label.

---

## Task 1: store.Recap() and store.CapLimit()

**Files:**
- Modify: `internal/store/history.go`
- Test: `internal/store/recap_test.go` (new)

Context: `capLimit()` (unexported) reads the configured cap or `DefaultMaxHistory`.
`capTrim(list, max)` trims newest-first, pinned exempt. `writeHistory`/`readHistory`
exist; `historyMu` guards them. `AppendHistory` shows the lock pattern.

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestRecapTrimsUnpinnedKeepsPinned(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "cfg"))

	// Seed 5 entries (one pinned), then set cap to 2 and Recap.
	for i := 0; i < 5; i++ {
		body := []byte{byte('a' + i)}
		c := clipboard.New(clipboard.KindText, body, time.Now().Add(time.Duration(i)*time.Second))
		if err := AppendHistory(c, "self", ""); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := readHistory()
	if len(list) != 5 {
		t.Fatalf("seeded %d entries, want 5", len(list))
	}
	// Pin the oldest so it must survive the trim.
	oldest := list[len(list)-1].ID
	if err := SetPinned(oldest, true); err != nil {
		t.Fatal(err)
	}

	// Write a config with MaxHistory=2.
	cfgDir := filepath.Join(tmp, "cfg", "clipfan")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"shared_key":"k","max_history":2}`), 0o600)

	if got := CapLimit(); got != 2 {
		t.Fatalf("CapLimit() = %d, want 2", got)
	}
	if err := Recap(); err != nil {
		t.Fatal(err)
	}
	after, _ := readHistory()
	if len(after) != 2 {
		t.Fatalf("after Recap len = %d, want 2 (1 pinned + 1 unpinned)", len(after))
	}
	pinnedSurvived := false
	for _, e := range after {
		if e.ID == oldest && e.Pinned {
			pinnedSurvived = true
		}
	}
	if !pinnedSurvived {
		t.Fatal("pinned entry was trimmed by Recap")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run TestRecapTrimsUnpinnedKeepsPinned -v`
Expected: FAIL — `undefined: CapLimit` / `undefined: Recap`.

- [ ] **Step 3: Add the exported functions**

In `internal/store/history.go`, after the `capLimit()` function add:

```go
// CapLimit is the configured history cap (or DefaultMaxHistory). Exported so the
// daemon can report it.
func CapLimit() int { return capLimit() }

// Recap re-trims stored history to the current cap, freeing entries that exceed a
// freshly lowered MaxHistory. Pinned entries are exempt (same rule as AppendHistory).
func Recap() error {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return err
	}
	return writeHistory(capTrim(list, capLimit()))
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/store/ -run TestRecapTrimsUnpinnedKeepsPinned -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/history.go internal/store/recap_test.go
git commit -m "feat(store): Recap() re-trims history to the current cap"
```

---

## Task 2: POST /v1/config endpoint

**Files:**
- Modify: `internal/transport/server.go`
- Test: `internal/transport/config_test.go` (new)

Context: `Server` holds function hooks (`peersFn`, `historyFn`, …). `SetHistory`
wires several at once. Mutating handlers call `s.readSigned(w, r)` (returns nil
after writing 401/400 on bad signature/body), decode JSON, call the fn, write 200.
Look at an existing transport test for how a signed request is constructed.

Confirmed against the codebase: the signer is `auth.Sign(body) -> string` (sets
`X-Clipfan-Sig`), `NewAuth(base64Key)` builds it, and the proven test key is
`"dGVzdC1rZXktMDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI="` (used in
`history_endpoint_test.go`). The server constructor is
`NewServer(listen, auth, onRecv ReceiveFunc, peersFn PeersFunc)`.

- [ ] **Step 1: Write the failing test**

Create `internal/transport/config_test.go`:

```go
package transport

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestPostConfigDispatchesMaxHistory(t *testing.T) {
	auth, err := NewAuth("dGVzdC1rZXktMDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(":0", auth, func(clipboard.Content, string) {}, func() any { return nil })
	var got int
	s.SetConfigFunc(func(n int) error { got = n; return nil })

	body := []byte(`{"max_history":123}`)
	req := httptest.NewRequest("POST", "/v1/config", bytes.NewReader(body))
	req.Header.Set("X-Clipfan-Sig", auth.Sign(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed POST status = %d, want 200", rec.Code)
	}
	if got != 123 {
		t.Fatalf("hook received %d, want 123", got)
	}

	// Unsigned → 401.
	req2 := httptest.NewRequest("POST", "/v1/config", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d, want 401", rec2.Code)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/transport/ -run TestPostConfigDispatchesMaxHistory -v`
Expected: FAIL — `s.SetConfigFunc undefined`.

- [ ] **Step 3: Implement the hook + handler**

In `server.go`, add a field to the `Server` struct:

```go
	configFn  func(maxHistory int) error
```

Add the setter near `SetHistory`:

```go
// SetConfigFunc wires the config-write endpoint. Called by the daemon.
func (s *Server) SetConfigFunc(fn func(maxHistory int) error) { s.configFn = fn }
```

Register the route in `Handler()` after the pin route:

```go
	mux.HandleFunc("POST /v1/config", s.postConfig)
```

Add the handler (mirror `postPin`):

```go
func (s *Server) postConfig(w http.ResponseWriter, r *http.Request) {
	body := s.readSigned(w, r)
	if body == nil {
		return
	}
	if s.configFn == nil {
		http.Error(w, "config endpoint not wired", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		MaxHistory int `json:"max_history"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.configFn(req.MaxHistory); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/transport/ -run TestPostConfigDispatchesMaxHistory -v`
Expected: PASS.

- [ ] **Step 5: Run the full transport suite**

Run: `go test ./internal/transport/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/transport/server.go internal/transport/config_test.go
git commit -m "feat(transport): authenticated POST /v1/config endpoint"
```

---

## Task 3: Daemon setMaxHistory hook + peers field

**Files:**
- Modify: `internal/daemon/daemon.go`
- Test: `internal/daemon/config_test.go` (new)

Context: `New()` wires hooks after constructing the server (the `d.sv.SetHistory(...)`
block). `peersHandler` returns `map[string]any{"origin", "peers", "version"}`.
`newTestDaemon(t)` builds a `*Daemon`. Imports already include `config` and `store`;
add `fmt` if not present (it is).

- [ ] **Step 1: Write the failing test**

```go
package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetMaxHistoryClampsAndRejects(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	d, _, _ := newTestDaemon(t)

	if err := d.setMaxHistory(0); err == nil {
		t.Fatal("setMaxHistory(0) should error")
	}

	if err := d.setMaxHistory(10); err != nil { // below 50 → clamps, no error
		t.Fatal(err)
	}
	if got := readSavedMax(t); got != 50 {
		t.Fatalf("saved max = %d, want clamped 50", got)
	}

	if err := d.setMaxHistory(99999); err != nil { // above 5000 → clamps
		t.Fatal(err)
	}
	if got := readSavedMax(t); got != 5000 {
		t.Fatalf("saved max = %d, want clamped 5000", got)
	}

	if err := d.setMaxHistory(300); err != nil {
		t.Fatal(err)
	}
	if got := readSavedMax(t); got != 300 {
		t.Fatalf("saved max = %d, want 300", got)
	}
}

func TestPeersHandlerIncludesMaxHistory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, _ := newTestDaemon(t)
	out := d.peersHandler().(map[string]any)
	if _, ok := out["max_history"]; !ok {
		t.Fatal("peers response missing max_history")
	}
}

// readSavedMax reads MaxHistory back from the on-disk config.
func readSavedMax(t *testing.T) int {
	t.Helper()
	p := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "clipfan", "config.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		MaxHistory int `json:"max_history"`
	}
	if err := jsonUnmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c.MaxHistory
}
```

Add at the top of the test file the small helper to avoid importing encoding/json
inline noise:

```go
import "encoding/json"

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
```

(Combine the imports into one block when writing the file.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/daemon/ -run 'TestSetMaxHistory|TestPeersHandlerIncludesMaxHistory' -v`
Expected: FAIL — `d.setMaxHistory undefined` and missing `max_history`.

- [ ] **Step 3: Implement the hook, wiring, and peers field**

Add the method (near `peersHandler`):

```go
// setMaxHistory persists a new history cap. Values are clamped to [50, 5000];
// a non-positive request is rejected. After saving, excess history is trimmed.
func (d *Daemon) setMaxHistory(n int) error {
	if n <= 0 {
		return fmt.Errorf("max_history must be positive, got %d", n)
	}
	if n < 50 {
		n = 50
	}
	if n > 5000 {
		n = 5000
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.MaxHistory = n
	if err := config.Save(cfg); err != nil {
		return err
	}
	return store.Recap()
}
```

Wire it after the `SetHistory(...)` block in `New()`:

```go
	d.sv.SetConfigFunc(d.setMaxHistory)
```

Add `max_history` to `peersHandler`:

```go
func (d *Daemon) peersHandler() any {
	return map[string]any{
		"origin":      d.origin,
		"peers":       d.Snapshot(context.Background()),
		"version":     version.Version,
		"max_history": store.CapLimit(),
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/daemon/ -run 'TestSetMaxHistory|TestPeersHandlerIncludesMaxHistory' -v`
Expected: PASS.

- [ ] **Step 5: Full Go suite**

Run: `go test ./...`
Expected: PASS. Then `gofmt -l internal/daemon/daemon.go` → empty.

- [ ] **Step 6: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/config_test.go
git commit -m "feat(daemon): persist clamped MaxHistory; report it in peers"
```

---

## Task 4: Swift — decode max_history, publish, setMaxHistory, fetch limit

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/Models.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift`
- Test: `apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift` (extend)

- [ ] **Step 1: Write the failing decode test**

Append to `FleetRowTests.swift` (inside the existing class or a new extension):

```swift
extension FleetRowTests {
    func testDecodePeersResponseMaxHistory() throws {
        let json = """
        {"origin":"p","peers":[],"max_history":350}
        """.data(using: .utf8)!
        let resp = try JSONDecoder.clipfan.decode(PeersResponse.self, from: json)
        XCTAssertEqual(resp.max_history, 350)
    }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd apps/mac/Clipfan && swift test --filter FleetRowTests`
Expected: FAIL — `PeersResponse` has no member `max_history`.

- [ ] **Step 3: Add the field**

In `Models.swift`, `PeersResponse`:

```swift
struct PeersResponse: Codable {
    let origin: String
    let peers: [Peer]
    let version: String?
    let max_history: Int?
}
```

- [ ] **Step 4: Publish + setter + fetch limit in DaemonClient.swift**

After `@Published var version: String?` add:

```swift
    @Published var maxHistory: Int = 200
```

In `refresh()`, after `self.version = resp.version` add:

```swift
            if let m = resp.max_history { self.maxHistory = m }
```

In `refreshHistory()`, change the hardcoded limit. Replace:

```swift
              let url = URL(string: "\(base.absoluteString)/v1/history?limit=200") else { return }
```

with:

```swift
              let url = URL(string: "\(base.absoluteString)/v1/history?limit=\(maxHistory)") else { return }
```

Add the setter method (next to `clearUnpinned()`):

```swift
    func setMaxHistory(_ n: Int) async {
        await signedRequest(method: "POST", path: "/v1/config", body: ["max_history": n])
        await refresh()
        await refreshHistory()
    }
```

- [ ] **Step 5: Run tests + build**

Run: `cd apps/mac/Clipfan && swift test --filter FleetRowTests && swift build`
Expected: decode test passes; build succeeds.

- [ ] **Step 6: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/Models.swift apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift
git commit -m "feat(app): read/write MaxHistory via daemon; use it as fetch limit"
```

---

## Task 5: Add KeyboardShortcuts and replace the Carbon hotkey

**Files:**
- Modify: `apps/mac/Clipfan/Package.swift`
- Create: `apps/mac/Clipfan/Sources/Clipfan/Shortcuts.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift`
- Delete: `apps/mac/Clipfan/Sources/Clipfan/GlobalHotkey.swift`

- [ ] **Step 1: Add the package dependency**

In `Package.swift`, add to the `Package(...)`:

```swift
    dependencies: [
        .package(url: "https://github.com/sindresorhus/KeyboardShortcuts", from: "2.0.0"),
    ],
```

and add the product to the `Clipfan` executable target:

```swift
        .executableTarget(
            name: "Clipfan",
            dependencies: [
                .product(name: "KeyboardShortcuts", package: "KeyboardShortcuts"),
            ],
            path: "Sources/Clipfan"
        ),
```

- [ ] **Step 2: Resolve packages**

Run: `cd apps/mac/Clipfan && swift package resolve`
Expected: KeyboardShortcuts (2.x) resolves. If 2.0.0 is unavailable, use the
latest 2.x reported by the resolver and update the `from:` accordingly.

- [ ] **Step 3: Define the shortcut name**

Create `apps/mac/Clipfan/Sources/Clipfan/Shortcuts.swift`:

```swift
import KeyboardShortcuts

extension KeyboardShortcuts.Name {
    /// Global show/hide for the clipboard window. Defaults to ⇧⌘V.
    static let toggleClipboard = Self("toggleClipboard",
        initial: .init(.v, modifiers: [.command, .shift]))
}
```

- [ ] **Step 4: Register the handler, drop GlobalHotkey**

In `ClipfanApp.swift`, add `import KeyboardShortcuts`. Remove the
`private let historyHotkey: GlobalHotkey` property and its assignment, and replace
the `init()` body's hotkey setup. The new `init()`:

```swift
    init() {
        DaemonClient.shared.start()
        KeyboardShortcuts.onKeyDown(for: .toggleClipboard) {
            CommandPanelController.shared.toggle()
        }
    }
```

- [ ] **Step 5: Delete the obsolete file**

```bash
git rm apps/mac/Clipfan/Sources/Clipfan/GlobalHotkey.swift
```

- [ ] **Step 6: Build**

Run: `cd apps/mac/Clipfan && swift build`
Expected: builds (KeyboardShortcuts links). No references to `GlobalHotkey` remain.

- [ ] **Step 7: Commit**

```bash
git add apps/mac/Clipfan/Package.swift apps/mac/Clipfan/Package.resolved apps/mac/Clipfan/Sources/Clipfan/Shortcuts.swift apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift
git commit -m "feat(app): global shortcut via KeyboardShortcuts, drop Carbon hotkey"
```

---

## Task 6: Settings — stepper + recorder

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift`

Context: `GeneralTab` → `Section("Clipboard")` currently has two read-only
`LabeledContent` rows. `GeneralTab` has `@EnvironmentObject var daemon`.

- [ ] **Step 1: Add the import and a stepper state**

At the top of `SettingsView.swift` add `import KeyboardShortcuts`.

In `GeneralTab`, add state seeded from the daemon:

```swift
    @State private var historyLimit: Int = 200
```

- [ ] **Step 2: Replace the Clipboard section**

```swift
            Section("Clipboard") {
                Stepper(value: $historyLimit, in: 50...5000, step: 50) {
                    LabeledContent("History limit", value: "\(historyLimit) items")
                }
                .onChange(of: historyLimit) { _, n in
                    Task { await daemon.setMaxHistory(n) }
                }
                KeyboardShortcuts.Recorder("Global shortcut", name: .toggleClipboard)
            }
```

Seed `historyLimit` from the daemon when the view appears and keep it in sync:

```swift
        .onAppear { historyLimit = daemon.maxHistory }
        .onChange(of: daemon.maxHistory) { _, m in
            if m != historyLimit { historyLimit = m }
        }
```

Attach these two modifiers to the `Form` in `GeneralTab.body`.

Note: the `.onChange(of:)` two-parameter closure form requires macOS 14. The
package targets macOS 13. If `swift build` errors on the `onChange` signature, use
the single-parameter form `.onChange(of: historyLimit) { n in … }` and
`.onChange(of: daemon.maxHistory) { m in … }` instead.

- [ ] **Step 3: Build**

Run: `cd apps/mac/Clipfan && swift build`
Expected: builds. (Apply the onChange fallback from the note if needed.)

- [ ] **Step 4: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift
git commit -m "feat(app): editable history limit stepper + shortcut recorder"
```

---

## Task 7: Dynamic shortcut label in the dropdown

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift`

Context: the "Open Clipboard" `menuButton` passes a hardcoded `shortcut: "⇧⌘V"`.

- [ ] **Step 1: Add the import and use the live shortcut**

At the top add `import KeyboardShortcuts`. Change the Open Clipboard row's
`shortcut:` argument from `"⇧⌘V"` to a computed value:

```swift
            menuButton("Open Clipboard", systemImage: "doc.on.clipboard",
                       shortcut: toggleShortcutLabel) {
                CommandPanelController.shared.show()
            }
```

Add the helper to `StatusMenuView`:

```swift
    /// The current global toggle shortcut as a display string, or "" if unset.
    private var toggleShortcutLabel: String {
        KeyboardShortcuts.getShortcut(for: .toggleClipboard).map { "\($0)" } ?? ""
    }
```

- [ ] **Step 2: Build**

Run: `cd apps/mac/Clipfan && swift build`
Expected: builds.

- [ ] **Step 3: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift
git commit -m "feat(app): dropdown shows the live global shortcut"
```

---

## Task 8: Full verification pass

- [ ] **Step 1: All tests**

Run: `go test ./... && (cd apps/mac/Clipfan && swift test)`
Expected: all PASS.

- [ ] **Step 2: Build the bundle**

Run: `bash dist/build-all.sh && (cd apps/mac/Clipfan && bash build-app.sh)`
Expected: builds.

- [ ] **Step 3: Manual smoke (controller will run + screenshot)**

  - General → Clipboard: stepper changes "N items"; history list re-caps to the new size.
  - Global shortcut recorder rebinds; the new chord opens the clipboard window; the dropdown's Open Clipboard label updates.
  - Default ⇧⌘V still works on a fresh launch.

---

## Self-Review notes

- **Spec coverage:** endpoint → Tasks 2–3; Recap → Task 1; app read/write → Task 4;
  KeyboardShortcuts → Tasks 5–7; stepper → Task 6; dynamic label → Task 7. Covered.
- **During execution:** confirm the existing transport tests' signing helper name
  (Task 2 Step 1) before writing the signed-request test — do not invent
  `auth.Sign` if the real method differs. Confirm the `onChange` arity against the
  macOS 13 target (Task 6 note).
- **Type consistency:** `setMaxHistory`, `CapLimit`, `Recap`, `SetConfigFunc`,
  `max_history`/`maxHistory`, and `.toggleClipboard` are used consistently.
