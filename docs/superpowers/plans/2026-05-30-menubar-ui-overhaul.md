# Menubar UI Overhaul Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make clipfan's menubar app — clipboard window, dropdown, and Settings — read as one coherent native macOS app, and expose the daemon's version over the protocol.

**Architecture:** Mostly SwiftUI/AppKit presentation changes in `apps/mac/Clipfan`, plus one small Go change adding a `version` field to the daemon's `/v1/peers` response. A new shared `FleetRow` SwiftUI component (driven by a pure `fleetRows(...)` function) unifies the dropdown and Settings → Fleet, and is the one unit-tested seam on the Swift side.

**Tech Stack:** Go 1.26 (daemon), Swift / SwiftPM / SwiftUI + AppKit (menubar app), XCTest, `go test`.

**Spec:** `docs/superpowers/specs/2026-05-30-menubar-ui-overhaul-design.md`

**Conventions:**
- Go tests: `go test ./...` (run from repo root).
- Swift tests: `cd apps/mac/Clipfan && swift test --filter <Suite>`.
- Commit after every green step. Commit messages follow the repo's
  `type(scope): summary` convention (e.g. `feat(daemon): ...`).

---

## File Structure

- `internal/version/version.go` (new) — single `Version` var, default `"dev"`.
- `internal/daemon/daemon.go` (modify) — add `version` to `peersHandler`.
- `internal/daemon/peers_version_test.go` (new) — assert the field is present.
- `dist/build-all.sh` (modify) — inject version via ldflags.
- `apps/mac/Clipfan/Sources/Clipfan/Models.swift` (modify) — `PeersResponse.version`.
- `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift` (modify) — publish `version`.
- `apps/mac/Clipfan/Sources/Clipfan/FleetRow.swift` (new) — `FleetRowModel`, `fleetRows(...)`, `HealthDot`, `FleetRow`.
- `apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift` (new) — unit tests for `fleetRows(...)`.
- `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift` (modify) — merged fleet list.
- `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift` (modify) — sidebar, Fleet self card, General/Diagnostics split.
- `apps/mac/Clipfan/Sources/Clipfan/CommandPanel.swift` (modify) — real title bar.

---

## Task 1: Daemon version package

**Files:**
- Create: `internal/version/version.go`

- [ ] **Step 1: Create the version package**

```go
// Package version carries the daemon's build version. The default is "dev";
// release builds override it with -ldflags "-X .../internal/version.Version=...".
package version

var Version = "dev"
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./internal/version/`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/version/version.go
git commit -m "feat(version): add daemon version var, default dev"
```

---

## Task 2: Expose version in the peers response

**Files:**
- Modify: `internal/daemon/daemon.go` (imports + `peersHandler`, ~line 202)
- Test: `internal/daemon/peers_version_test.go` (new)

- [ ] **Step 1: Write the failing test**

`newTestDaemon` already exists in `echo_test.go` and builds a `*Daemon` with
`origin: "self"`. Use it.

```go
package daemon

import "testing"

func TestPeersHandlerIncludesVersion(t *testing.T) {
	d, _, _ := newTestDaemon(t)
	out, ok := d.peersHandler().(map[string]any)
	if !ok {
		t.Fatalf("peersHandler returned %T, want map[string]any", d.peersHandler())
	}
	v, ok := out["version"]
	if !ok {
		t.Fatal("peers response missing \"version\" field")
	}
	if v != "dev" {
		t.Fatalf("version = %v, want \"dev\" (default)", v)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestPeersHandlerIncludesVersion -v`
Expected: FAIL — `peers response missing "version" field`.

- [ ] **Step 3: Add the version import and field**

In `internal/daemon/daemon.go`, add to the import block:

```go
	"github.com/prime-radiant-inc/clipfan/internal/version"
```

Change `peersHandler` from:

```go
func (d *Daemon) peersHandler() any {
	return map[string]any{
		"origin": d.origin,
		"peers":  d.Snapshot(context.Background()),
	}
}
```

to:

```go
func (d *Daemon) peersHandler() any {
	return map[string]any{
		"origin":  d.origin,
		"peers":   d.Snapshot(context.Background()),
		"version": version.Version,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -run TestPeersHandlerIncludesVersion -v`
Expected: PASS.

- [ ] **Step 5: Run the full Go suite**

Run: `go test ./...`
Expected: PASS (no regressions).

- [ ] **Step 6: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/peers_version_test.go
git commit -m "feat(daemon): report version in /v1/peers response"
```

---

## Task 3: Inject version at build time

**Files:**
- Modify: `dist/build-all.sh`

- [ ] **Step 1: Edit the ldflags line**

Change:

```bash
ldflags="-s -w"
```

to:

```bash
version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
ldflags="-s -w -X github.com/prime-radiant-inc/clipfan/internal/version.Version=$version"
```

- [ ] **Step 2: Verify the script runs and stamps the version**

Run: `bash dist/build-all.sh >/dev/null 2>&1 && ./dist/clipfan-darwin-arm64 --version 2>&1 | head -1 || echo "no --version flag"`

Note: clipfan may not have a `--version` flag. If it prints "no --version flag",
that is acceptable — the ldflags injection is verified structurally instead:

Run: `grep -c 'internal/version.Version=' dist/build-all.sh`
Expected: `1`.

- [ ] **Step 3: Commit**

```bash
git add dist/build-all.sh
git commit -m "build: stamp daemon version from git describe"
```

---

## Task 4: Swift — decode the version field

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/Models.swift` (`PeersResponse`)
- Test: `apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift` (new, started here)

- [ ] **Step 1: Write the failing test**

Create `apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift`:

```swift
import XCTest
@testable import Clipfan

final class FleetRowTests: XCTestCase {
    func testDecodePeersResponseVersion() throws {
        let json = """
        {"origin":"paradise-park","version":"v1.2.3","peers":[]}
        """.data(using: .utf8)!
        let resp = try JSONDecoder.clipfan.decode(PeersResponse.self, from: json)
        XCTAssertEqual(resp.origin, "paradise-park")
        XCTAssertEqual(resp.version, "v1.2.3")
        XCTAssertTrue(resp.peers.isEmpty)
    }

    func testDecodePeersResponseWithoutVersion() throws {
        let json = """
        {"origin":"paradise-park","peers":[]}
        """.data(using: .utf8)!
        let resp = try JSONDecoder.clipfan.decode(PeersResponse.self, from: json)
        XCTAssertNil(resp.version)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd apps/mac/Clipfan && swift test --filter FleetRowTests`
Expected: FAIL — `value of type 'PeersResponse' has no member 'version'` (compile error).

- [ ] **Step 3: Add the field**

In `Models.swift`, change:

```swift
struct PeersResponse: Codable {
    let origin: String
    let peers: [Peer]
}
```

to:

```swift
struct PeersResponse: Codable {
    let origin: String
    let peers: [Peer]
    let version: String?
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd apps/mac/Clipfan && swift test --filter FleetRowTests`
Expected: PASS (both decode tests).

- [ ] **Step 5: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/Models.swift apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift
git commit -m "feat(app): decode daemon version from peers response"
```

---

## Task 5: Publish version on DaemonClient

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift`

- [ ] **Step 1: Add the published property**

After `@Published var origin: String = "—"` add:

```swift
    @Published var version: String?
```

- [ ] **Step 2: Set it in refresh()**

In `refresh()`, after `self.origin = resp.origin`, add:

```swift
            self.version = resp.version
```

- [ ] **Step 3: Verify it builds**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds.

- [ ] **Step 4: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift
git commit -m "feat(app): publish daemon version on DaemonClient"
```

---

## Task 6: Shared FleetRow component + fleetRows() logic

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/FleetRow.swift`
- Test: `apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift` (extend)

This is the one real piece of logic in the overhaul: a pure function that builds
the ordered fleet rows with the local host first.

- [ ] **Step 1: Write the failing tests**

Append to `FleetRowTests.swift`:

```swift
extension FleetRowTests {
    private func peer(_ name: String) -> Peer {
        Peer(hostname: name, port: 7853,
             last_push_ts: nil, last_push_ok: true,
             last_push_err: nil, last_recv_ts: nil)
    }

    func testSelfRowIsFirst() {
        let rows = fleetRows(origin: "paradise-park", connected: true,
                             peers: [peer("flower-garden"), peer("linux-box")])
        XCTAssertEqual(rows.count, 3)
        XCTAssertTrue(rows[0].isSelf)
        XCTAssertEqual(rows[0].name, "paradise-park")
        XCTAssertFalse(rows[1].isSelf)
        XCTAssertEqual(rows[1].name, "flower-garden")
        XCTAssertEqual(rows[2].name, "linux-box")
    }

    func testSelfRowHasNoSyncTimesAndReflectsConnected() {
        let up = fleetRows(origin: "me", connected: true, peers: [])
        XCTAssertNil(up[0].pushTS)
        XCTAssertNil(up[0].recvTS)
        XCTAssertEqual(up[0].health, .healthy)
        XCTAssertTrue(up[0].subtitle.contains("running"))

        let down = fleetRows(origin: "me", connected: false, peers: [])
        XCTAssertEqual(down[0].health, .down)
        XCTAssertTrue(down[0].subtitle.contains("not running"))
    }

    func testPeerRowsCarryPeer() {
        let p = peer("flower-garden")
        let rows = fleetRows(origin: "me", connected: true, peers: [p])
        XCTAssertEqual(rows[1].peer, p)
        XCTAssertFalse(rows[1].isSelf)
    }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd apps/mac/Clipfan && swift test --filter FleetRowTests`
Expected: FAIL — `cannot find 'fleetRows' in scope`.

- [ ] **Step 3: Implement FleetRow.swift**

```swift
import SwiftUI

/// Health of a fleet member, mapped to a dot color. Colors mirror PeerHealth:
/// green synced, orange stale-or-failed, gray idle/down.
enum FleetHealth {
    case healthy, attention, down

    var color: Color {
        switch self {
        case .healthy:   return .green
        case .attention: return .orange
        case .down:      return .gray
        }
    }
}

/// Map a peer's push state to FleetHealth, mirroring Peer.healthColor exactly
/// (avoids fragile SwiftUI Color equality checks).
private func health(for p: Peer) -> FleetHealth {
    if p.last_push_ok { return .healthy }
    if let ts = p.last_push_ts, ts > Date.distantPast { return .attention }
    return .down
}

/// One row in the unified fleet list — either the local host (isSelf) or a peer.
struct FleetRowModel: Identifiable {
    let id: String          // hostname
    let name: String
    let subtitle: String
    let health: FleetHealth
    let isSelf: Bool
    let pushTS: Date?
    let recvTS: Date?
    let peer: Peer?         // nil for the self row
}

/// Build the ordered fleet rows: the local host first, then peers in order.
/// The self row carries no sync times (the local host never pushes to itself).
func fleetRows(origin: String, connected: Bool, peers: [Peer]) -> [FleetRowModel] {
    let selfRow = FleetRowModel(
        id: origin,
        name: origin,
        subtitle: connected ? "this Mac · running" : "this Mac · daemon not running",
        health: connected ? .healthy : .down,
        isSelf: true,
        pushTS: nil,
        recvTS: nil,
        peer: nil
    )
    let peerRows = peers.map { p in
        FleetRowModel(
            id: p.hostname,
            name: p.hostname,
            subtitle: p.last_push_ok ? "port \(p.port) · synced" : "port \(p.port)",
            health: health(for: p),
            isSelf: false,
            pushTS: p.last_push_ts,
            recvTS: p.last_recv_ts,
            peer: p
        )
    }
    return [selfRow] + peerRows
}

/// A small colored health dot.
struct HealthDot: View {
    let health: FleetHealth
    var size: CGFloat = 9
    var body: some View {
        Circle().fill(health.color).frame(width: size, height: size)
    }
}

/// One fleet row rendered identically in the dropdown and Settings → Fleet.
struct FleetRow: View {
    let model: FleetRowModel

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            HealthDot(health: model.health)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(model.name).font(.system(size: 13, weight: .semibold))
                    if model.isSelf {
                        Text("you")
                            .font(.system(size: 9.5))
                            .padding(.horizontal, 6).padding(.vertical, 1)
                            .background(Color.accentColor.opacity(0.18))
                            .clipShape(Capsule())
                            .foregroundStyle(Color.accentColor)
                    }
                }
                Text(model.subtitle).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
            if let push = model.pushTS, let recv = model.recvTS {
                Text("↑ \(peerTimeAgo(push))   ↓ \(peerTimeAgo(recv))")
                    .font(.system(size: 10)).foregroundStyle(.secondary)
            }
        }
    }
}
```

Note: `peerTimeAgo` already exists (see `PeerHealth.swift`). The `health(for:)`
helper mirrors `Peer.healthColor`'s logic (green if `last_push_ok`, orange if a
real `last_push_ts` exists, else gray) so the dot colors stay consistent.

- [ ] **Step 4: Run to verify it passes**

Run: `cd apps/mac/Clipfan && swift test --filter FleetRowTests`
Expected: PASS (all FleetRowTests).

- [ ] **Step 5: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/FleetRow.swift apps/mac/Clipfan/Tests/ClipfanTests/FleetRowTests.swift
git commit -m "feat(app): shared FleetRow component + fleetRows() logic"
```

---

## Task 7: Dropdown adopts the merged fleet list

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift`

- [ ] **Step 1: Replace the header + fleet with the merged list**

Replace the `header` usage and the `fleet` computed property. The new body keeps
the action rows, then renders one list via `FleetRow`.

In `body`, replace:

```swift
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()

            menuButton(...) { ... }   // keep the three action buttons unchanged
            ...
            Divider()

            fleet
        }
```

so that `header` is removed and `fleet` is replaced by the merged list. Concretely,
change `body` to:

```swift
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            menuButton("Open Clipboard", systemImage: "doc.on.clipboard", shortcut: "⇧⌘V") {
                CommandPanelController.shared.show()
            }
            menuButton("Settings…", systemImage: "gearshape", shortcut: "⌘,") {
                NSApp.activate(ignoringOtherApps: true)
                openWindow(id: "settings")
            }
            menuButton("Quit", systemImage: "power", shortcut: "⌘Q") {
                NSApp.terminate(nil)
            }

            Divider()

            fleet
        }
        .padding(8)
        .frame(width: 280)
    }
```

- [ ] **Step 2: Rewrite the `fleet` property to use fleetRows**

Replace the entire `fleet` computed property with:

```swift
    @ViewBuilder private var fleet: some View {
        Text("FLEET")
            .font(.system(size: 9, weight: .semibold))
            .foregroundStyle(.tertiary)
            .padding(.horizontal, 8)
            .padding(.top, 6).padding(.bottom, 2)
        ForEach(fleetRows(origin: daemon.origin,
                          connected: daemon.connected,
                          peers: daemon.peers)) { row in
            Button {
                NSApp.activate(ignoringOtherApps: true)
                openWindow(id: "settings")
            } label: {
                FleetRow(model: row)
                    .contentShape(Rectangle())
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
            }
            .buttonStyle(MenuRowButtonStyle())
        }
    }
```

Delete the now-unused `header` property.

- [ ] **Step 3: Build**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds. (If `header` is referenced elsewhere, remove those references.)

- [ ] **Step 4: Run the app and verify by eye**

Run: `cd apps/mac/Clipfan && bash build-app.sh && open .build/Clipfan.app`
Expected: the menu-bar dropdown shows the three actions, then a FLEET list whose
first row is this Mac with a "you" pill and subtitle "this Mac · running", peers
below. (Requires `dist/build-all.sh` to have been run so the bundle payload exists.)

- [ ] **Step 5: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift
git commit -m "feat(app): merge this-Mac into the dropdown fleet list"
```

---

## Task 8: Settings sidebar (side tabs)

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift`

- [ ] **Step 1: Add the Diagnostics tab and switch to NavigationSplitView**

Replace the `SettingsView` struct's `Tab` enum and `body`:

```swift
struct SettingsView: View {
    @EnvironmentObject var daemon: DaemonClient

    enum Tab: String, CaseIterable, Hashable, Identifiable {
        case fleet = "Fleet"
        case general = "General"
        case diagnostics = "Diagnostics"
        var id: String { rawValue }

        var systemImage: String {
            switch self {
            case .fleet:       return "network"
            case .general:     return "gearshape"
            case .diagnostics: return "stethoscope"
            }
        }
    }

    @State private var selection: Tab = .fleet

    var body: some View {
        NavigationSplitView {
            List(Tab.allCases, selection: $selection) { tab in
                Label(tab.rawValue, systemImage: tab.systemImage).tag(tab)
            }
            .navigationSplitViewColumnWidth(170)
        } detail: {
            switch selection {
            case .fleet:       FleetTab()
            case .general:     GeneralTab()
            case .diagnostics: DiagnosticsTab()
            }
        }
    }
}
```

`DiagnosticsTab` is created in Task 10. To keep the build green between tasks,
add a temporary stub now at the bottom of the file:

```swift
struct DiagnosticsTab: View {
    var body: some View { Text("Diagnostics").padding() }
}
```

- [ ] **Step 2: Build**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift
git commit -m "feat(app): settings sidebar with Fleet/General/Diagnostics"
```

---

## Task 9: Fleet pane includes the local host

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift` (`FleetTab`, `PeerCard`)

- [ ] **Step 1: Render the unified list via FleetRow cards**

In `FleetTab.body`, replace the `if daemon.peers.isEmpty { … } else { ScrollView … }`
block so the local host always shows first and peers follow. Replace:

```swift
            if daemon.peers.isEmpty {
                VStack(spacing: 6) {
                    Image(systemName: "network.slash").font(.system(size: 28)).foregroundStyle(.tertiary)
                    Text("No peers yet").foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    VStack(spacing: 10) {
                        ForEach(daemon.peers) { peer in PeerCard(peer: peer) }
                    }
                }
            }
```

with:

```swift
            ScrollView {
                VStack(spacing: 10) {
                    ForEach(fleetRows(origin: daemon.origin,
                                      connected: daemon.connected,
                                      peers: daemon.peers)) { row in
                        FleetRow(model: row)
                            .padding(12)
                            .background(Color.secondary.opacity(0.06))
                            .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.secondary.opacity(0.12)))
                            .clipShape(RoundedRectangle(cornerRadius: 10))
                    }
                    if daemon.peers.isEmpty {
                        Text("No peers yet — add one in Settings")
                            .font(.system(size: 11)).foregroundStyle(.secondary)
                            .frame(maxWidth: .infinity)
                            .padding(.top, 4)
                    }
                }
            }
```

`PeerCard` is now unused. Delete the `PeerCard` struct.

- [ ] **Step 2: Build**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds. (If the compiler reports `PeerCard` still referenced,
remove the stray reference.)

- [ ] **Step 3: Run and verify by eye**

Run: `cd apps/mac/Clipfan && bash build-app.sh && open .build/Clipfan.app`
Expected: Settings → Fleet shows this Mac as the first card (with "you" pill, no
sync arrows), peers below; with zero peers the self card still shows plus the
"No peers yet" line.

- [ ] **Step 4: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift
git commit -m "feat(app): show this Mac as the first card in Settings Fleet"
```

---

## Task 10: General + Diagnostics split

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift` (`GeneralTab`, replace stub `DiagnosticsTab`)

- [ ] **Step 1: Trim GeneralTab to preferences only**

Replace the `GeneralTab` struct body so it keeps Startup + Clipboard and drops
the Status and Developer sections:

```swift
struct GeneralTab: View {
    @EnvironmentObject var daemon: DaemonClient
    @StateObject private var loginItem = LoginItemManager.shared

    var body: some View {
        Form {
            Section("Startup") {
                Toggle(isOn: Binding(get: { loginItem.isEnabled },
                                     set: { loginItem.setEnabled($0) })) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Launch clipfan at login")
                        Text("Start syncing automatically").font(.caption).foregroundStyle(.secondary)
                    }
                }
                if let err = loginItem.lastError {
                    Text(err).font(.caption).foregroundStyle(.red)
                }
                if loginItem.status == .requiresApproval {
                    Button("Open Login Items settings…") {
                        SMAppService.openSystemSettingsLoginItems()
                    }
                }
            }

            Section("Clipboard") {
                LabeledContent("History limit", value: "200 items")
                LabeledContent("Global shortcut", value: "⇧⌘V")
            }
        }
        .formStyle(.grouped)
    }
}
```

- [ ] **Step 2: Replace the DiagnosticsTab stub with the real pane**

Replace the temporary `DiagnosticsTab` stub with:

```swift
struct DiagnosticsTab: View {
    @EnvironmentObject var daemon: DaemonClient

    var body: some View {
        Form {
            Section {
                HStack(spacing: 12) {
                    HealthDot(health: daemon.connected ? .healthy : .down, size: 11)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(daemon.connected ? "Daemon running" : "Daemon not running")
                            .font(.system(size: 13, weight: .semibold))
                        Text("this Mac · \(daemon.origin) · \(daemonVersion)")
                            .font(.system(size: 11, design: .monospaced))
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Restart") {
                        daemon.restartDaemon()
                        Task { await daemon.refresh() }
                    }
                }
                .padding(.vertical, 4)
            }

            Section("Setup") {
                Button("Re-run setup…") {
                    WelcomeWindowController.shared.show(startInstall: true)
                }
                Text("Reinstalls and restarts the background service.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("Developer") {
                LabeledContent("Config") { Text(configPath).font(.system(.caption, design: .monospaced)) }
                LabeledContent("Share dir") { Text(shareDirPath).font(.system(.caption, design: .monospaced)) }
                Button("Reveal config in Finder") {
                    NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: configPath)])
                }
                Button("Open daemon log") {
                    NSWorkspace.shared.open(URL(fileURLWithPath: logPath))
                }
            }
        }
        .formStyle(.grouped)
    }

    /// Daemon-reported version, falling back to the app bundle version.
    var daemonVersion: String {
        if let v = daemon.version, !v.isEmpty { return v }
        return Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "—"
    }

    var configPath: String {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/clipfan/config.json").path
    }
    var shareDirPath: String { Installer.shareDir.path }
    var logPath: String { "/tmp/clipfan-shell.log" }
}
```

- [ ] **Step 3: Build**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds. The old `GeneralTab` references to `configPath`/
`shareDirPath`/`logPath`/`showDeveloper` are gone; confirm no dangling references.

- [ ] **Step 4: Run and verify by eye**

Run: `cd apps/mac/Clipfan && bash build-app.sh && open .build/Clipfan.app`
Expected: General shows only Startup + Clipboard. Diagnostics shows the daemon
status banner (with version), Setup, and Developer. Restart works.

- [ ] **Step 5: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift
git commit -m "feat(app): split General prefs from new Diagnostics tab"
```

---

## Task 11: Clipboard window real title bar

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/CommandPanel.swift`

- [ ] **Step 1: Give the panel a real title bar**

In `show()`, change the panel construction. Replace:

```swift
        let panel = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 660, height: 440),
            styleMask: [.titled, .fullSizeContentView, .nonactivatingPanel, .resizable],
            backing: .buffered,
            defer: true
        )
        panel.titleVisibility = .hidden
        panel.titlebarAppearsTransparent = true
        panel.isMovableByWindowBackground = true
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.isReleasedWhenClosed = false
        panel.standardWindowButton(.closeButton)?.isHidden = true
        panel.standardWindowButton(.miniaturizeButton)?.isHidden = true
        panel.standardWindowButton(.zoomButton)?.isHidden = true
        panel.contentView = hosting
        panel.delegate = self
        panel.backgroundColor = .clear
```

with:

```swift
        let panel = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 660, height: 440),
            styleMask: [.titled, .closable, .nonactivatingPanel, .resizable],
            backing: .buffered,
            defer: true
        )
        panel.title = "Clipboard"
        panel.titleVisibility = .visible
        panel.titlebarAppearsTransparent = false
        panel.isMovableByWindowBackground = true
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.isReleasedWhenClosed = false
        // Close hides the panel (handled in windowShouldClose); minimize and zoom
        // are meaningless on a transient HUD, so present-but-disabled.
        panel.standardWindowButton(.miniaturizeButton)?.isEnabled = false
        panel.standardWindowButton(.zoomButton)?.isEnabled = false
        panel.contentView = hosting
        panel.delegate = self
```

Note: `backgroundColor = .clear` and the corner-radius masking are removed/kept
as follows — with a real title bar the window draws its own opaque chrome, so
drop the `panel.backgroundColor = .clear` line (done above) but KEEP the
`hosting.wantsLayer` / `cornerRadius` block as-is below it.

- [ ] **Step 2: Make the close button hide instead of destroy**

Add this delegate method to `CommandPanelController`:

```swift
    // The close button hides the panel (like Esc / click-away) rather than
    // destroying it, so re-summoning is instant.
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        hide()
        return false
    }
```

- [ ] **Step 3: Build**

Run: `cd apps/mac/Clipfan && swift build`
Expected: build succeeds.

- [ ] **Step 4: Run and verify by eye**

Run: `cd apps/mac/Clipfan && bash build-app.sh && open .build/Clipfan.app`
Then press ⇧⌘V.
Expected: the clipboard window has a real opaque title bar reading "Clipboard"
with an active red close button (greyed minimize/zoom). Clicking close hides the
panel; ⇧⌘V re-opens it instantly; Esc and click-away still dismiss.

- [ ] **Step 5: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/CommandPanel.swift
git commit -m "feat(app): give the clipboard window a real title bar"
```

---

## Task 12: Full verification pass

- [ ] **Step 1: Run all tests**

Run: `go test ./... && (cd apps/mac/Clipfan && swift test)`
Expected: all PASS.

- [ ] **Step 2: Build the app bundle clean**

Run: `bash dist/build-all.sh && (cd apps/mac/Clipfan && bash build-app.sh)`
Expected: bundle builds.

- [ ] **Step 3: Manual smoke checklist** (run `open apps/mac/Clipfan/.build/Clipfan.app`)

  - Dropdown: actions, then FLEET list with this-Mac first ("you", "this Mac · running"), peers below.
  - Settings sidebar: Fleet / General / Diagnostics on the left.
  - Fleet pane: this Mac first card, peers below; sensible with zero peers.
  - General: Startup + Clipboard only.
  - Diagnostics: status banner with daemon version, Setup, Developer; Restart works.
  - Clipboard window (⇧⌘V): real title bar "Clipboard", close hides, min/zoom greyed.

- [ ] **Step 4: Update the task tracker** — mark tasks #1–4 and #7 (and the
  daemon-version follow-up) done; note history-limit/shortcut editability is Group B.

---

## Self-Review notes

- **Spec coverage:** §0 daemon version → Tasks 1–5; §1 title bar → Task 11; §2
  dropdown → Task 7; §3 sidebar → Task 8; §4 Fleet self card → Task 9; §5
  General/Diagnostics → Task 10; shared component → Task 6. All covered.
- **Type consistency:** `fleetRows`, `FleetRowModel`, `FleetHealth`, `HealthDot`,
  `FleetRow`, and `PeersResponse.version` are used consistently across Tasks 4–10.
