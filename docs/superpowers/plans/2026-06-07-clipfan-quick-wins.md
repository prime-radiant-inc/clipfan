# clipfan Quick-Wins Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the five independent workstreams from the design spec that have no cross-coupling: the macOS tmux-paste fix, the SSH outbound indicator fix, the add-peer success state, the About screen, and the docs overhaul.

**Architecture:** Each task is self-contained. The two Go/Swift bug fixes (Tasks 1–3) and the two Swift UI additions (Tasks 4–5) follow the codebase's existing pattern of pure, free-function logic with unit tests plus a thin view that calls them. Task 6 (docs) is file moves + content + a grep gate.

**Tech Stack:** Go 1.x (daemon, `internal/tmux`), SwiftUI/AppKit (macOS app under `apps/mac/Clipfan`), bash (`dist/install.sh`). Go tests: `go test ./...`. Swift tests: `cd apps/mac/Clipfan && swift test --filter <Name>`.

**Source spec:** `docs/superpowers/specs/2026-06-07-clipfan-mesh-onboarding-docs-design.md` (Workstreams A, B, C, G, H).

**These five tasks are mutually independent** — they can be implemented in parallel in separate worktrees. They do NOT depend on the mesh plan.

---

## File Structure

- `internal/tmux/tmux.go` — add a launch-independent `tmux` binary resolver; `LoadBufferAll` uses it. (Task 1)
- `internal/tmux/tmux_resolve_test.go` — new test file for the resolver. (Task 1)
- `dist/install.sh` — ensure the baked launchd PATH includes Homebrew dirs. (Task 2)
- `apps/mac/Clipfan/Sources/Clipfan/FleetRow.swift` — transport-aware outbound timestamp + render-when-either. (Task 3)
- `apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift` — add outbound-timestamp tests. (Task 3)
- `apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift` — success state + Done/Add-another, drop the silent auto-dismiss. (Task 4)
- `apps/mac/Clipfan/Tests/ClipfanTests/AddPeerSheetButtonsTests.swift` — new test file for the button-phase helper. (Task 4)
- `apps/mac/Clipfan/Sources/Clipfan/AboutView.swift` — new About view + version helper. (Task 5)
- `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift` — add the `about` Window scene. (Task 5)
- `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift` — add the "About clipfan…" menu item. (Task 5)
- `apps/mac/Clipfan/Tests/ClipfanTests/AboutViewTests.swift` — new test for the version helper. (Task 5)
- `README.md`, `docs/development/`, `docs/TROUBLESHOOTING.md`, and ticket-ID scrubs across `docs/`. (Task 6)

---

## Task 1: tmux binary resolver (Workstream B — the paste-bug root cause)

**Files:**
- Modify: `internal/tmux/tmux.go` (the `LoadBufferAll` exec at line 28; add resolver near `socketDir`)
- Test: `internal/tmux/tmux_resolve_test.go` (create)

**Why:** On macOS the launchd-launched daemon's `PATH` omits `/opt/homebrew/bin`, so `exec.Command("tmux", …)` fails and received clips never reach tmux buffers. The resolver finds tmux regardless of inherited PATH.

- [ ] **Step 1: Write the failing test**

Create `internal/tmux/tmux_resolve_test.go`:

```go
package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveTmuxBinaryFallsBackToAbsoluteCandidate(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	got := resolveTmuxBinary(missing, []string{"/nonexistent/tmux", fake})
	if got != fake {
		t.Fatalf("got %q, want %q", got, fake)
	}
}

func TestResolveTmuxBinaryPrefersPathLookup(t *testing.T) {
	found := func(string) (string, error) { return "/from/path/tmux", nil }
	got := resolveTmuxBinary(found, []string{"/abs/tmux"})
	if got != "/from/path/tmux" {
		t.Fatalf("got %q, want /from/path/tmux", got)
	}
}

func TestResolveTmuxBinaryLastResortIsBareName(t *testing.T) {
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	got := resolveTmuxBinary(missing, []string{"/nope/tmux"})
	if got != "tmux" {
		t.Fatalf("got %q, want \"tmux\"", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tmux/ -run TestResolveTmuxBinary -v`
Expected: FAIL — `undefined: resolveTmuxBinary`.

- [ ] **Step 3: Add the resolver and use it**

In `internal/tmux/tmux.go`, add after the `socketDir()` function:

```go
// tmuxBinaryCandidates are absolute fallbacks for hosts whose PATH omits the
// directory tmux lives in — notably the launchd-launched macOS daemon, whose
// baked PATH excludes Homebrew's /opt/homebrew/bin.
var tmuxBinaryCandidates = []string{
	"/opt/homebrew/bin/tmux",
	"/usr/local/bin/tmux",
	"/usr/bin/tmux",
	"/bin/tmux",
}

// resolveTmuxBinary returns the tmux executable to run: PATH lookup first, then
// the first existing executable absolute candidate. lookPath and candidates are
// injected so tests don't depend on the host's real tmux install.
func resolveTmuxBinary(lookPath func(string) (string, error), candidates []string) string {
	if p, err := lookPath("tmux"); err == nil && p != "" {
		return p
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return c
		}
	}
	return "tmux"
}

func tmuxBinary() string {
	return resolveTmuxBinary(exec.LookPath, tmuxBinaryCandidates)
}
```

Then change the exec call in `LoadBufferAll` (currently `exec.Command("tmux", "-S", s, "load-buffer", "-")`) to:

```go
		cmd := exec.Command(tmuxBinary(), "-S", s, "load-buffer", "-")
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tmux/ -v`
Expected: PASS (new resolver tests + existing tmux tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tmux/tmux.go internal/tmux/tmux_resolve_test.go
git commit -m "fix: resolve tmux by absolute path so launchd daemon finds it"
```

---

## Task 2: install.sh PATH secondary fix (Workstream B — defense in depth)

**Files:**
- Modify: `dist/install.sh` (the `run_path=` assignment in the `darwin` case, ~line 130)

**Why:** The launchd plist's baked PATH (from `zsh -lc 'echo $PATH'`) omitted Homebrew in the app's invocation context. Guarantee the Homebrew dirs are present so the plist-launched daemon's PATH is correct even without the resolver.

- [ ] **Step 1: Make the change**

In `dist/install.sh`, replace the line:

```bash
        run_path=$(/bin/zsh -lc 'echo $PATH' 2>/dev/null || echo "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin")
```

with:

```bash
        run_path=$(/bin/zsh -lc 'echo $PATH' 2>/dev/null || echo "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin")
        # Guarantee Homebrew dirs are present even when the captured login PATH
        # omitted them (the launchd daemon must find tmux/pngpaste/etc).
        case ":$run_path:" in *":/opt/homebrew/bin:"*) :;; *) run_path="/opt/homebrew/bin:$run_path";; esac
        case ":$run_path:" in *":/usr/local/bin:"*) :;; *) run_path="/usr/local/bin:$run_path";; esac
```

- [ ] **Step 2: Verify the shell logic**

Run (sanity-checks the dedupe/prepend logic without installing):

```bash
run_path="/Users/jesse/.local/bin:/usr/bin:/bin"
case ":$run_path:" in *":/opt/homebrew/bin:"*) :;; *) run_path="/opt/homebrew/bin:$run_path";; esac
case ":$run_path:" in *":/usr/local/bin:"*) :;; *) run_path="/usr/local/bin:$run_path";; esac
echo "$run_path"
```

Expected output: `/usr/local/bin:/opt/homebrew/bin:/Users/jesse/.local/bin:/usr/bin:/bin`

- [ ] **Step 3: Commit**

```bash
git add dist/install.sh
git commit -m "fix: ensure launchd PATH includes Homebrew dirs"
```

---

## Task 3: SSH outbound indicator (Workstream C — "↑ never")

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/FleetRow.swift` (the `fleetRows` peer mapping ~line 161; the up/down render block ~line 204)
- Test: `apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift` (add tests)

**Why:** `last_push_ts` is only set by the HTTP fanout path; under SSH it's always nil, so the indicator reads "never." Source the outbound time from `ssh_last_ack_ts` (an ack means a clip was sent + accepted) — NOT `ssh_last_connect_ts` (connecting is not sending) — and render the block when either direction has a time.

- [ ] **Step 1: Write the failing tests**

Add to `apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift` (inside the existing `FleetRowTests` class):

```swift
func testSSHPeerOutboundUsesAckTimestamp() {
    let ack = Date(timeIntervalSince1970: 1_000_000)
    let p = Peer(hostname: "m4", port: 7853, last_push_ts: nil, last_push_ok: false,
                 last_push_err: nil, last_recv_ts: nil, transport: "ssh",
                 ssh_last_ack_ts: ack)
    XCTAssertEqual(fleetOutboundTS(p), ack)
}

func testSSHPeerConnectedButNeverSentHasNoOutbound() {
    let p = Peer(hostname: "m4", port: 7853, last_push_ts: nil, last_push_ok: false,
                 last_push_err: nil, last_recv_ts: nil, transport: "ssh",
                 ssh_last_connect_ts: Date(timeIntervalSince1970: 5),
                 ssh_last_ack_ts: nil)
    XCTAssertNil(fleetOutboundTS(p))
}

func testHTTPPeerOutboundUsesPushTimestamp() {
    let push = Date(timeIntervalSince1970: 2_000_000)
    let p = Peer(hostname: "linux-b", port: 7853, last_push_ts: push, last_push_ok: true,
                 last_push_err: nil, last_recv_ts: nil)
    XCTAssertEqual(fleetOutboundTS(p), push)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd apps/mac/Clipfan && swift test --filter FleetRowTests`
Expected: FAIL — `cannot find 'fleetOutboundTS' in scope`.

- [ ] **Step 3: Add the helper and use it**

In `FleetRow.swift`, add a free function near the top (after the imports, before `enum FleetHealth`):

```swift
/// Outbound "last sent" time for the up-arrow. SSH transport never sets
/// last_push_ts (that's the HTTP fanout path), so use the SSH ack time —
/// deliberately NOT ssh_last_connect_ts, since connecting is not sending.
func fleetOutboundTS(_ p: Peer) -> Date? {
    p.isSSHTransport ? p.ssh_last_ack_ts : p.last_push_ts
}
```

In `fleetRows`, change the peer row's `pushTS:` argument from `pushTS: p.last_push_ts,` to:

```swift
            pushTS: fleetOutboundTS(p),
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd apps/mac/Clipfan && swift test --filter FleetRowTests`
Expected: PASS.

- [ ] **Step 5: Render the block when either time exists**

In `FleetRow.swift`, replace the render block (currently `if let push = model.pushTS, let recv = model.recvTS { … ↑ … ↓ … }`) with:

```swift
            if model.pushTS != nil || model.recvTS != nil {
                VStack(alignment: .trailing, spacing: 2) {
                    if let push = model.pushTS { Text("↑ \(peerTimeAgo(push))") }
                    if let recv = model.recvTS { Text("↓ \(peerTimeAgo(recv))") }
                }
                .font(.system(size: 10))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: true, vertical: false)
            }
```

- [ ] **Step 6: Build the app target to confirm the view compiles**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds.

- [ ] **Step 7: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/FleetRow.swift apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift
git commit -m "fix: source fleet outbound indicator from SSH ack time"
```

---

## Task 4: Add-peer success state (Workstream G)

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift` (state vars ~240–243; success tails at ~597 and ~650; button HStack ~368–376)
- Test: `apps/mac/Clipfan/Tests/ClipfanTests/AddPeerSheetButtonsTests.swift` (create)

**Why:** Today the sheet silently `Task.sleep(1s)`-then-`dismiss()`s on success with no success UI. Replace that with an explicit success state and Done / Add another. (G must NOT claim the mesh was healed — that wording is added by the mesh plan's Task D.)

- [ ] **Step 1: Write the failing test**

Create `apps/mac/Clipfan/Tests/ClipfanTests/AddPeerSheetButtonsTests.swift`:

```swift
import XCTest
@testable import Clipfan

final class AddPeerSheetButtonsTests: XCTestCase {
    func testSuccessShowsDoneAndAddAnother() {
        XCTAssertEqual(addPeerSheetButtons(installing: false, installSuccess: true, hasFailure: false), .success)
    }
    func testEditingByDefault() {
        XCTAssertEqual(addPeerSheetButtons(installing: false, installSuccess: false, hasFailure: false), .editing)
    }
    func testInstallingState() {
        XCTAssertEqual(addPeerSheetButtons(installing: true, installSuccess: false, hasFailure: false), .installing)
    }
    func testFailureState() {
        XCTAssertEqual(addPeerSheetButtons(installing: false, installSuccess: false, hasFailure: true), .failed)
    }
    func testSuccessWinsOverFailure() {
        XCTAssertEqual(addPeerSheetButtons(installing: false, installSuccess: true, hasFailure: true), .success)
    }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd apps/mac/Clipfan && swift test --filter AddPeerSheetButtonsTests`
Expected: FAIL — `cannot find 'addPeerSheetButtons' in scope`.

- [ ] **Step 3: Add the phase enum + helper**

In `AddPeerSheet.swift`, add near the other free functions (after `addPeerInstallButtonTitle`):

```swift
enum AddPeerSheetButtons: Equatable {
    case editing      // Cancel + Install
    case installing   // Cancel(disabled) + Installing…
    case success      // Done + Add another
    case failed       // Cancel + Retry
}

/// Which buttons the sheet shows. Success takes precedence so a retry that
/// finally succeeds shows Done, not Retry.
func addPeerSheetButtons(installing: Bool, installSuccess: Bool, hasFailure: Bool) -> AddPeerSheetButtons {
    if installSuccess { return .success }
    if installing { return .installing }
    if hasFailure { return .failed }
    return .editing
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd apps/mac/Clipfan && swift test --filter AddPeerSheetButtonsTests`
Expected: PASS.

- [ ] **Step 5: Add the success state and reset helper to the view**

In `AddPeerSheet.swift`, add a state var alongside the others (~line 244):

```swift
    @State private var installSuccess = false
    @State private var lastInstalledHost = ""
```

Add a private method on `AddPeerSheet` to reset the form for "Add another":

```swift
    private func resetForAnother() {
        installSuccess = false
        installing = false
        progress = ""
        failure = nil
        log = nil
        lastInstalledHost = ""
        remoteDrafts = [AddPeerRemoteHostDraft(user: NSUserName())]
        tailnetSelected = []
    }
```

- [ ] **Step 6: Set success instead of silently auto-dismissing**

In `install(trustKeyscanConfirmed:)`, find the two success tails that read:

```swift
                        installing = false
                        Task { try? await Task.sleep(nanoseconds: 1_000_000_000); dismiss() }
```

(one in the private-mesh branch ~line 596, one at the end ~line 649). Replace **each** with:

```swift
                        installing = false
                        installSuccess = true
```

For the end-of-loop case, capture the host first — change the loop's success bookkeeping so `lastInstalledHost` holds the last target's host (set `lastInstalledHost = t.host` inside the per-target success `MainActor.run { progress = "Installed on \(t.host)." }`). For the private-mesh branch set `lastInstalledHost = "private SSH mesh"`.

- [ ] **Step 7: Render success view + Done/Add-another buttons**

In `body`, replace the trailing button `HStack` (currently `Cancel` + `installButtonTitle`) with a switch on the phase:

```swift
            HStack {
                Spacer()
                switch addPeerSheetButtons(installing: installing, installSuccess: installSuccess, hasFailure: failure != nil) {
                case .success:
                    Button("Add another") { resetForAnother() }
                    Button("Done") { dismiss() }
                        .keyboardShortcut(.defaultAction)
                default:
                    Button("Cancel") { dismiss() }
                    Button(installButtonTitle) { requestInstall() }
                        .keyboardShortcut(.return)
                        .disabled(isAddPeerInstallDisabled(installCount: installCount,
                                                           installing: installing,
                                                           privateDirectMeshRequested: SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled))
                }
            }
```

And add a success banner above `Spacer()` in `body`, shown when `installSuccess`:

```swift
            if installSuccess {
                HStack(spacing: 10) {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                    Text("Added \(lastInstalledHost).").font(.callout)
                }
            }
```

- [ ] **Step 8: Build to confirm the view compiles**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds.

- [ ] **Step 9: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift apps/mac/Clipfan/Tests/ClipfanTests/AddPeerSheetButtonsTests.swift
git commit -m "feat: explicit add-peer success state with Done / Add another"
```

---

## Task 5: About screen (Workstream H)

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/AboutView.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift` (add a Window scene next to the `settings` window ~line 28–33)
- Modify: `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift` (add a menu item after Settings ~line 16–19)
- Test: `apps/mac/Clipfan/Tests/ClipfanTests/AboutViewTests.swift` (create)

- [ ] **Step 1: Write the failing test**

Create `apps/mac/Clipfan/Tests/ClipfanTests/AboutViewTests.swift`:

```swift
import XCTest
@testable import Clipfan

final class AboutViewTests: XCTestCase {
    func testVersionSummaryWithBoth() {
        XCTAssertEqual(aboutVersionSummary(appVersion: "0.3.29", daemonVersion: "0.3.29"),
                       "App 0.3.29 · Daemon 0.3.29")
    }
    func testVersionSummaryMissingDaemon() {
        XCTAssertEqual(aboutVersionSummary(appVersion: "0.3.29", daemonVersion: nil),
                       "App 0.3.29 · Daemon —")
    }
    func testVersionSummaryMissingApp() {
        XCTAssertEqual(aboutVersionSummary(appVersion: nil, daemonVersion: "0.3.29"),
                       "App — · Daemon 0.3.29")
    }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd apps/mac/Clipfan && swift test --filter AboutViewTests`
Expected: FAIL — `cannot find 'aboutVersionSummary' in scope`.

- [ ] **Step 3: Create AboutView with the helper**

Create `apps/mac/Clipfan/Sources/Clipfan/AboutView.swift`:

```swift
import SwiftUI

/// App version + daemon version on one line; "—" when a source is unavailable.
func aboutVersionSummary(appVersion: String?, daemonVersion: String?) -> String {
    "App \(appVersion ?? "—") · Daemon \(daemonVersion ?? "—")"
}

struct AboutView: View {
    @EnvironmentObject private var daemon: DaemonClient

    private var appVersion: String? {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String
    }

    var body: some View {
        VStack(spacing: 14) {
            Image(systemName: "doc.on.clipboard.fill")
                .font(.system(size: 40, weight: .light))
                .foregroundStyle(.tint)
            Text("clipfan").font(.system(size: 20, weight: .semibold))
            Text("One clipboard across your Macs and Linux hosts.")
                .font(.callout).foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            Text(aboutVersionSummary(appVersion: appVersion, daemonVersion: daemon.version))
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(.secondary)
            Link("Documentation", destination: URL(string: "https://github.com/prime-radiant-inc/clipfan")!)
                .font(.callout)
        }
        .padding(28)
        .frame(width: 360, height: 260)
    }
}
```

> Note: confirm the repo URL is correct for this project before committing; if there is no public repo, drop the `Link` line rather than inventing a URL.

- [ ] **Step 4: Run to verify the helper test passes**

Run: `cd apps/mac/Clipfan && swift test --filter AboutViewTests`
Expected: PASS.

- [ ] **Step 5: Register the Window scene**

In `ClipfanApp.swift`, add after the `settings` `Window(...)` scene (after its `.windowResizability(...)`):

```swift
        Window("About clipfan", id: "about") {
            AboutView()
                .environmentObject(daemon)
        }
        .windowResizability(.contentSize)
```

- [ ] **Step 6: Add the menu item**

In `StatusMenuView.swift`, add after the `Settings…` `menuButton(...)` block (before `Check for Updates…`):

```swift
            menuButton("About clipfan…", systemImage: "info.circle", shortcut: "") {
                NSApp.activate(ignoringOtherApps: true)
                openWindow(id: "about")
            }
```

- [ ] **Step 7: Build to confirm it compiles**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds.

- [ ] **Step 8: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/AboutView.swift apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift apps/mac/Clipfan/Tests/ClipfanTests/AboutViewTests.swift
git commit -m "feat: add About clipfan window and menu item"
```

---

## Task 6: Docs overhaul (Workstream A)

**Files:**
- Modify: `README.md` (user-first rewrite)
- Create: `docs/development/building-from-source.md`
- Create: `docs/TROUBLESHOOTING.md`
- Modify (scrub ticket IDs): `README.md`, `docs/PLAN.md`, `docs/superpowers/plans/2026-05-28-clipboard-history.md`, `docs/superpowers/specs/2026-05-28-clipboard-history-design.md`, `docs/superpowers/specs/2026-05-29-clip-id-recirculation-design.md`, `docs/superpowers/specs/2026-05-29-mac-app-ux-redesign-design.md`

**Why:** README leads with build-from-source and carries a ticket id; `docs/` leaks `PRI-####` and ticket references.

- [ ] **Step 1: Inventory the ticket references to scrub**

Run: `grep -rnE 'PRI-[0-9]+|\*\*(Tracks|Ticket):\*\*|Update Linear|move the ticket' README.md docs/ --exclude='2026-06-07-clipfan-mesh-onboarding-docs-design.md'`
Expected: lists the occurrences in the 6 files above (this is the work-list; the design doc is excluded because it legitimately quotes the patterns).

- [ ] **Step 2: Create `docs/development/building-from-source.md`**

Move the README's build-from-source content (the "Build the binaries" steps and any dev/release setup) into this new file. It should contain: prerequisites (Go toolchain, Swift/Xcode for the app), `dist/build-all.sh` usage, where binaries land, and how to install locally for development. Keep it developer-facing.

- [ ] **Step 3: Rewrite `README.md` user-first**

Reorder/rewrite to this outline (move build content out to the new dev doc; link to it):
1. What clipfan is (the existing intro, minus `Tracks PRI-1873.`)
2. How it works (brief — keep the existing "How it works" section)
3. Install (the menubar app / prebuilt binary path first; one line linking `docs/development/building-from-source.md` for source builds)
4. Getting started (set up this Mac; add a host via Settings → Fleet → Add peer…)
5. Daily use (copy/paste across hosts; tmux `prefix-]`; clipboard history panel ⇧⌘V)
6. Configuration (the existing config reference)
7. Security (condensed summary; keep the detail or link it)
8. Troubleshooting (one-line pointer to `docs/TROUBLESHOOTING.md`)
9. Documentation (pointers to `docs/`)

- [ ] **Step 4: Create `docs/TROUBLESHOOTING.md`**

Cover the two most common failures: (a) daemon not running — `curl -s http://localhost:7853/v1/health` and how to restart; (b) peers not syncing — check the Fleet view health dots, the SSH transport, and that the host is reachable. Keep it concise and user-facing.

- [ ] **Step 5: Scrub the ticket references**

Edit each flagged file to remove `PRI-####` ids and `**Tracks:**`/`**Ticket:**` headers, and reword inline references to describe behavior not tickets. Specifically:
- `README.md:10` — delete the `Tracks PRI-1873.` line.
- `docs/PLAN.md:8` — drop the `PRI-1873.` token, keep the surrounding sentence.
- `docs/superpowers/plans/2026-05-28-clipboard-history.md` — reword the "Update Linear PRI-1875" step to "Update the tracking issue" (or delete the step).
- `docs/superpowers/specs/2026-05-28-clipboard-history-design.md:5` — delete the `**Ticket:** PRI-1875` line.
- `docs/superpowers/specs/2026-05-29-clip-id-recirculation-design.md` (3 spots) — drop the `PRI-1920` tokens, keep the sentences.
- `docs/superpowers/specs/2026-05-29-mac-app-ux-redesign-design.md:5` — delete the `**Tracks:** …` line.

- [ ] **Step 6: Verify the scrub (acceptance gate)**

Run: `grep -rnE 'PRI-[0-9]+|\*\*(Tracks|Ticket):\*\*|Update Linear|move the ticket' README.md docs/ --exclude='2026-06-07-clipfan-mesh-onboarding-docs-design.md'`
Expected: **no output**.

- [ ] **Step 7: Verify README links resolve**

Run: `grep -nE '\]\(docs/|\]\(\./docs/' README.md` then confirm each referenced path exists (`ls docs/development/building-from-source.md docs/TROUBLESHOOTING.md`).
Expected: referenced files exist.

- [ ] **Step 8: Commit**

```bash
git add README.md docs/development/building-from-source.md docs/TROUBLESHOOTING.md docs/PLAN.md docs/superpowers/plans/2026-05-28-clipboard-history.md docs/superpowers/specs/2026-05-28-clipboard-history-design.md docs/superpowers/specs/2026-05-29-clip-id-recirculation-design.md docs/superpowers/specs/2026-05-29-mac-app-ux-redesign-design.md
git commit -m "docs: user-first README, dev docs under docs/, scrub ticket IDs"
```

---

## Self-Review (completed by plan author)

- **Spec coverage:** Workstream A → Task 6; B → Tasks 1–2; C → Task 3; G → Task 4; H → Task 5. All five quick-win workstreams covered. (Mesh workstreams D/E/F + Foundations 1/2 are the separate `clipfan-mesh` plan.)
- **Placeholders:** none — every code step shows the code; doc steps give the exact outline, files, and grep gates. (Task 5 flags the repo URL for confirmation rather than inventing one; Task 6 prose is authored by the implementer against a fixed outline, which is content, not logic.)
- **Type consistency:** `fleetOutboundTS(_:)`, `addPeerSheetButtons(installing:installSuccess:hasFailure:)` / `AddPeerSheetButtons`, `aboutVersionSummary(appVersion:daemonVersion:)`, `resolveTmuxBinary(_:_:)` / `tmuxBinary()` — names used consistently across each task's steps and its test. `Peer` initializer arguments match `Models.swift`.
