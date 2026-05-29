# clipfan macOS App UX Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform the clipfan menubar app from stock-SwiftUI to a designed, native macOS utility — a hotkey-summoned command panel for clipboard history, an at-a-glance fleet menubar, humanized Settings, an adaptive Add-Peer sheet, a real app icon, and opt-in tmux integration.

**Architecture:** The Go daemon, HTTP API, and data models are unchanged. All work is in the SwiftUI app (`apps/mac/Clipfan`) plus one shell-installer behavior change (`dist/install.sh`). New pure-logic helpers (`ClipMetadata.swift`, panel-selection functions) are unit-tested with `swift test`. SwiftUI views are verified by building the app and running it. The clipboard history moves from a hand-rolled `NSWindow` (`HistoryWindow.swift` / `HistoryWindowController.swift`) to a floating `NSPanel` (`CommandPanel.swift` / `CommandPanelView.swift`).

**Tech Stack:** Swift 5.9, SwiftUI + AppKit, SwiftPM (`.executableTarget`), macOS 13+ deployment target. Tests: XCTest via `swift test`. Build: `./build-app.sh`. Bash for the installer.

---

## Working directory & commands

All Swift commands run from `apps/mac/Clipfan`:

```bash
cd apps/mac/Clipfan
swift test          # run unit tests
swift build         # compile-check
./build-app.sh      # produce .build/Clipfan.app for manual verification
open .build/Clipfan.app
```

Installer/bash work runs from the repo root (`dist/`).

**Branching:** work happens directly on `main` per Jesse's repo workflow (see the
project's direct-to-main convention). Commit after every green step.

---

## Scope note: ⌥⏎ plain-text paste and auto-paste are out

The approved spec listed `⌥⏎ Plain` and "paste into the frontmost app". On reading
the code: the history store holds only plain text / image / link (no rich-text
pasteboard types), and `DaemonClient.restore(_:)` already re-copies plain text and
syncs. So a "plain" variant is a no-op distinction — **dropped** (YAGNI, and adding
a daemon API for it would violate the no-API-changes non-goal). Likewise, synthesizing
a real `⌘V` keystroke needs Accessibility and was already deferred, so **`⏎` and
`⌘1`–`⌘9` call `restore` (clipboard + fleet sync) and dismiss the panel**; the user
presses `⌘V` themselves. This matches Raycast's behavior without Accessibility.

---

## File Structure

**New files:**
- `apps/mac/Clipfan/Sources/Clipfan/ClipMetadata.swift` — pure humanization helpers (size, dimensions, monospace detection).
- `apps/mac/Clipfan/Sources/Clipfan/PanelSelection.swift` — pure keyboard-selection helpers (move, clamp, number→id).
- `apps/mac/Clipfan/Sources/Clipfan/CommandPanelView.swift` — the SwiftUI panel UI (search, list, preview, footer, key shortcuts).
- `apps/mac/Clipfan/Sources/Clipfan/CommandPanel.swift` — `NSPanel` subclass + controller (summon/dismiss/focus/resign).
- `apps/mac/Clipfan/Sources/Clipfan/VisualEffectBackground.swift` — `NSVisualEffectView` bridge for panel translucency.
- `apps/mac/Clipfan/Tests/ClipfanTests/ClipMetadataTests.swift`
- `apps/mac/Clipfan/Tests/ClipfanTests/PanelSelectionTests.swift`
- `apps/mac/Clipfan/Tests/ClipfanTests/InstallerFlagTests.swift`
- `dist/test-tmux-gating.sh` — bash test for the installer tmux decision.
- `apps/mac/Clipfan/AppIcon.iconset/` (generated) + `apps/mac/Clipfan/Resources/AppIcon.icns` — app icon.
- `apps/mac/Clipfan/make-icon.sh` — regenerates the icon from source art.

**Modified files:**
- `Sources/Clipfan/ClipfanApp.swift` — hotkey → command panel; menubar icon stays template SF Symbol.
- `Sources/Clipfan/StatusMenuView.swift` — at-a-glance fleet menu.
- `Sources/Clipfan/SettingsView.swift` — Fleet cards + General/Developer.
- `Sources/Clipfan/AddPeerSheet.swift` — adaptive Tailnet-first sheet + tmux checkbox + friendly progress.
- `Sources/Clipfan/Installer.swift` — `withTmux` param + flag passthrough.
- `Info.plist` — `CFBundleIconFile`.
- `build-app.sh` — copy the icon into the bundle.
- `dist/install.sh` — auto-detect tmux + `--with-tmux`/`--no-tmux`.

**Deleted at the end (after the panel is verified):**
- `Sources/Clipfan/HistoryWindow.swift`
- `Sources/Clipfan/HistoryWindowController.swift`

---

## Task 1: Metadata humanization helpers

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/ClipMetadata.swift`
- Test: `apps/mac/Clipfan/Tests/ClipfanTests/ClipMetadataTests.swift`

- [ ] **Step 1: Write the failing test**

Create `apps/mac/Clipfan/Tests/ClipfanTests/ClipMetadataTests.swift`:

```swift
import XCTest
@testable import Clipfan

final class ClipMetadataTests: XCTestCase {
    func testHumanSizeBytes() {
        XCTAssertEqual(humanSize(0), "0 B")
        XCTAssertEqual(humanSize(13), "13 B")
        XCTAssertEqual(humanSize(1023), "1023 B")
    }

    func testHumanSizeKBandMB() {
        XCTAssertEqual(humanSize(1234), "1 KB")        // 1234/1024 = 1.2 -> 1
        XCTAssertEqual(humanSize(248_213), "242 KB")   // 248213/1024 = 242.4 -> 242
        XCTAssertEqual(humanSize(5 * 1024 * 1024), "5 MB")
    }

    func testFormatDimensions() {
        XCTAssertEqual(formatDimensions(width: 1920, height: 1080), "1920×1080")
        XCTAssertEqual(formatDimensions(width: 0, height: 0), "0×0")
    }

    func testMonospacePreferredForPaths() {
        XCTAssertTrue(isMonospacePreferred("/Users/jesse/.config/clipfan/config.json"))
        XCTAssertTrue(isMonospacePreferred("~/.ssh/id_ed25519"))
    }

    func testMonospacePreferredForCode() {
        XCTAssertTrue(isMonospacePreferred("func chooseBackend(wayland string) {"))
        XCTAssertTrue(isMonospacePreferred("brew install --cask clipfan"))
    }

    func testMonospaceNotPreferredForProse() {
        XCTAssertFalse(isMonospacePreferred("Remember to call the dentist tomorrow"))
        XCTAssertFalse(isMonospacePreferred("jesse@fsck.com"))
    }
}
```

- [ ] **Step 2: Run the test, verify it fails to compile**

Run: `cd apps/mac/Clipfan && swift test 2>&1 | tail -20`
Expected: build failure — `cannot find 'humanSize' in scope` (and the other three).

- [ ] **Step 3: Write the implementation**

Create `apps/mac/Clipfan/Sources/Clipfan/ClipMetadata.swift`:

```swift
import Foundation

/// humanSize renders a byte count as a short human string ("242 KB").
/// Uses a fixed 1024 base and no decimals so output is deterministic across
/// locales (ByteCountFormatter is locale-dependent and unsuitable for tests).
func humanSize(_ bytes: Int) -> String {
    if bytes < 1024 { return "\(bytes) B" }
    let units = ["KB", "MB", "GB", "TB"]
    var value = Double(bytes) / 1024
    var unit = 0
    while value >= 1024 && unit < units.count - 1 {
        value /= 1024
        unit += 1
    }
    return String(format: "%.0f %@", value, units[unit])
}

/// formatDimensions renders pixel dimensions with a × separator.
func formatDimensions(width: Int, height: Int) -> String {
    "\(width)×\(height)"
}

/// imageDimensions reads a PNG/image file and returns its pixel size as a
/// formatted string, or nil if it can't be read. Thin glue over NSImage;
/// the formatting is covered by formatDimensions tests.
func imageDimensions(path: String) -> String? {
    guard let img = NSImage(contentsOfFile: path),
          let rep = img.representations.first else { return nil }
    return formatDimensions(width: rep.pixelsWide, height: rep.pixelsHigh)
}

/// isMonospacePreferred returns true when a clip's text reads like code or a
/// filesystem path and should render in a monospaced font. Conservative
/// heuristic — favors false for ordinary prose.
func isMonospacePreferred(_ s: String) -> Bool {
    let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
    if t.isEmpty { return false }
    // Filesystem paths.
    if t.hasPrefix("/") || t.hasPrefix("~/") { return true }
    if t.range(of: #"^[A-Za-z]:\\"#, options: .regularExpression) != nil { return true }
    // Code-ish tokens / shell commands.
    let codeMarkers = ["{", "}", ";", "()", "=>", "func ", "def ", "class ",
                       "import ", "const ", "var ", "--", "sudo ", "git ", "::"]
    if codeMarkers.contains(where: { t.contains($0) }) {
        // But an email contains none of these except possibly "--"; guard emails.
        if t.range(of: #"^\S+@\S+\.\S+$"#, options: .regularExpression) != nil { return false }
        return true
    }
    return false
}

#if canImport(AppKit)
import AppKit
#endif
```

Note: move the `import AppKit` to the top of the file (Swift requires imports
before use). Final import order:

```swift
import Foundation
import AppKit
```

- [ ] **Step 4: Run the test, verify it passes**

Run: `cd apps/mac/Clipfan && swift test --filter ClipMetadataTests 2>&1 | tail -20`
Expected: `Executed 6 tests, with 0 failures`.

- [ ] **Step 5: Run the full suite to confirm nothing else broke**

Run: `cd apps/mac/Clipfan && swift test 2>&1 | tail -5`
Expected: all tests pass (existing `HistoryFilterTests`, `HistoryEntryTests`, `SigningTests` still green).

- [ ] **Step 6: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/ClipMetadata.swift apps/mac/Clipfan/Tests/ClipfanTests/ClipMetadataTests.swift
git commit -m "feat(mac): metadata humanization helpers (size, dimensions, monospace)"
```

---

## Task 2: Keyboard-selection helpers for the panel

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/PanelSelection.swift`
- Test: `apps/mac/Clipfan/Tests/ClipfanTests/PanelSelectionTests.swift`

These are pure functions the panel uses for ↑/↓ movement and ⌘1–9 selection.
They operate on the already-filtered list (from the existing `filteredHistory`).

- [ ] **Step 1: Write the failing test**

Create `apps/mac/Clipfan/Tests/ClipfanTests/PanelSelectionTests.swift`:

```swift
import XCTest
@testable import Clipfan

final class PanelSelectionTests: XCTestCase {
    private func ids(_ xs: [String]) -> [HistoryEntry] {
        xs.map { HistoryEntry(id: $0, kind: .text, preview: $0, text: $0,
                              imagePath: nil, sizeBytes: 1, origin: "m4",
                              ts: Date(timeIntervalSince1970: 0), pinned: false) }
    }

    func testMoveSelectionDown() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(movedSelection(from: "a", in: list, delta: 1), "b")
        XCTAssertEqual(movedSelection(from: "b", in: list, delta: 1), "c")
    }

    func testMoveSelectionUp() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(movedSelection(from: "c", in: list, delta: -1), "b")
    }

    func testMoveClampsAtEnds() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(movedSelection(from: "c", in: list, delta: 1), "c")
        XCTAssertEqual(movedSelection(from: "a", in: list, delta: -1), "a")
    }

    func testMoveFromNilSelectsFirst() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(movedSelection(from: nil, in: list, delta: 1), "a")
        XCTAssertEqual(movedSelection(from: nil, in: list, delta: -1), "a")
    }

    func testMoveInEmptyListIsNil() {
        XCTAssertNil(movedSelection(from: nil, in: ids([]), delta: 1))
    }

    func testIdForNumber() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(idForNumber(1, in: list), "a")
        XCTAssertEqual(idForNumber(3, in: list), "c")
    }

    func testIdForNumberOutOfRange() {
        let list = ids(["a", "b"])
        XCTAssertNil(idForNumber(3, in: list))
        XCTAssertNil(idForNumber(0, in: list))
    }

    func testClampedSelectionKeepsValid() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(clampedSelection("b", in: list), "b")
    }

    func testClampedSelectionFallsBackToFirst() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(clampedSelection("gone", in: list), "a")
        XCTAssertEqual(clampedSelection(nil, in: list), "a")
        XCTAssertNil(clampedSelection("x", in: ids([])))
    }
}
```

- [ ] **Step 2: Run the test, verify it fails to compile**

Run: `cd apps/mac/Clipfan && swift test 2>&1 | tail -20`
Expected: `cannot find 'movedSelection' in scope` etc.

- [ ] **Step 3: Write the implementation**

Create `apps/mac/Clipfan/Sources/Clipfan/PanelSelection.swift`:

```swift
import Foundation

/// movedSelection returns the id `delta` positions from the current selection
/// within `list`, clamped to the ends. A nil current selection (or a selection
/// no longer present) starts at the first item. Returns nil only for an empty list.
func movedSelection(from current: HistoryEntry.ID?, in list: [HistoryEntry], delta: Int) -> HistoryEntry.ID? {
    guard !list.isEmpty else { return nil }
    guard let current, let idx = list.firstIndex(where: { $0.id == current }) else {
        return list.first?.id
    }
    let next = max(0, min(list.count - 1, idx + delta))
    return list[next].id
}

/// idForNumber maps a 1-based quick-paste number (⌘1…⌘9) to the id at that
/// position, or nil if out of range.
func idForNumber(_ n: Int, in list: [HistoryEntry]) -> HistoryEntry.ID? {
    guard n >= 1, n <= list.count else { return nil }
    return list[n - 1].id
}

/// clampedSelection returns `current` if it still exists in `list`, otherwise
/// the first item's id (nil for an empty list). Used after the list refreshes.
func clampedSelection(_ current: HistoryEntry.ID?, in list: [HistoryEntry]) -> HistoryEntry.ID? {
    if let current, list.contains(where: { $0.id == current }) { return current }
    return list.first?.id
}
```

- [ ] **Step 4: Run the test, verify it passes**

Run: `cd apps/mac/Clipfan && swift test --filter PanelSelectionTests 2>&1 | tail -20`
Expected: `Executed 9 tests, with 0 failures`.

- [ ] **Step 5: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/PanelSelection.swift apps/mac/Clipfan/Tests/ClipfanTests/PanelSelectionTests.swift
git commit -m "feat(mac): pure keyboard-selection helpers for command panel"
```

---

## Task 3: Visual-effect background bridge

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/VisualEffectBackground.swift`

A small `NSViewRepresentable` so SwiftUI can use real behind-window blur. No unit
test (it's a thin AppKit bridge); verified when the panel renders in Task 5.

- [ ] **Step 1: Write the implementation**

Create `apps/mac/Clipfan/Sources/Clipfan/VisualEffectBackground.swift`:

```swift
import SwiftUI
import AppKit

/// VisualEffectBackground bridges NSVisualEffectView so SwiftUI views get true
/// behind-window vibrancy. macOS automatically falls back to a solid material
/// when the user enables Reduce Transparency.
struct VisualEffectBackground: NSViewRepresentable {
    var material: NSVisualEffectView.Material = .hudWindow
    var blendingMode: NSVisualEffectView.BlendingMode = .behindWindow

    func makeNSView(context: Context) -> NSVisualEffectView {
        let v = NSVisualEffectView()
        v.material = material
        v.blendingMode = blendingMode
        v.state = .active
        return v
    }

    func updateNSView(_ v: NSVisualEffectView, context: Context) {
        v.material = material
        v.blendingMode = blendingMode
    }
}
```

- [ ] **Step 2: Compile-check**

Run: `cd apps/mac/Clipfan && swift build 2>&1 | tail -10`
Expected: `Compiling` … `Build complete!` (no errors).

- [ ] **Step 3: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/VisualEffectBackground.swift
git commit -m "feat(mac): NSVisualEffectView background bridge for panel"
```

---

## Task 4: Command panel SwiftUI view

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/CommandPanelView.swift`

The view: search row with type chips, a single-column `List(selection:)` of
comfortable rows, a preview pane, and a footer. Keyboard: instant search, ↑/↓
(native to `List`), ⏎ paste+dismiss, ⌘1–9 quick paste, Esc dismiss. Uses
`filteredHistory`, the Task 1/2 helpers, and `DaemonClient`.

- [ ] **Step 1: Write the view**

Create `apps/mac/Clipfan/Sources/Clipfan/CommandPanelView.swift`:

```swift
import SwiftUI

struct CommandPanelView: View {
    @ObservedObject var daemon: DaemonClient
    /// Called after a paste so the controller can dismiss the panel.
    var onPaste: () -> Void
    /// Called when the user presses Esc.
    var onDismiss: () -> Void

    @State private var search = ""
    @State private var filter: TypeFilter = .all
    @State private var selection: HistoryEntry.ID?
    @FocusState private var searchFocused: Bool

    private var items: [HistoryEntry] {
        filteredHistory(daemon.history, search: search, typeFilter: filter)
    }
    private var selected: HistoryEntry? {
        items.first { $0.id == selection } ?? items.first
    }

    var body: some View {
        VStack(spacing: 0) {
            searchRow
            Divider()
            if items.isEmpty {
                emptyState
            } else {
                HStack(spacing: 0) {
                    listPane.frame(width: 300)
                    Divider()
                    previewPane.frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            Divider()
            footer
        }
        .frame(width: 660, height: 440)
        .background(VisualEffectBackground())
        .background(hiddenShortcuts)
        .task {
            await daemon.refreshHistory()
            searchFocused = true
        }
        .onChange(of: items.map(\.id)) { ids in
            selection = clampedSelection(selection, in: items)
        }
    }

    // MARK: search

    private var searchRow: some View {
        HStack(spacing: 10) {
            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
            TextField("Type to search clipboard…", text: $search)
                .textFieldStyle(.plain)
                .font(.system(size: 15))
                .focused($searchFocused)
            Picker("", selection: $filter) {
                ForEach(TypeFilter.allCases) { f in Text(f.label).tag(f) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(width: 240)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
    }

    // MARK: list

    private var listPane: some View {
        List(selection: $selection) {
            ForEach(Array(items.enumerated()), id: \.element.id) { idx, e in
                CommandRow(entry: e, number: idx < 9 ? idx + 1 : nil)
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
        .listStyle(.plain)
    }

    // MARK: preview

    @ViewBuilder private var previewPane: some View {
        if let e = selected {
            VStack(alignment: .leading, spacing: 0) {
                Group {
                    if e.kind == .image, let p = e.imagePath, let img = NSImage(contentsOfFile: p) {
                        Image(nsImage: img).resizable().scaledToFit().padding(16)
                    } else {
                        ScrollView {
                            Text(e.text ?? e.preview)
                                .font(.system(size: 13,
                                               design: isMonospacePreferred(e.text ?? e.preview) ? .monospaced : .default))
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(16)
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                Divider()
                previewBadge(e).padding(.horizontal, 16).padding(.vertical, 10)
            }
        } else {
            Color.clear
        }
    }

    private func previewBadge(_ e: HistoryEntry) -> some View {
        HStack(spacing: 8) {
            Text(metaLine(e))
            if e.pinned { Image(systemName: "pin.fill") }
            Spacer()
        }
        .font(.system(size: 11))
        .foregroundStyle(.secondary)
    }

    private func metaLine(_ e: HistoryEntry) -> String {
        var parts = [e.kind.rawValue]
        if e.kind == .image, let p = e.imagePath, let dims = imageDimensions(path: p) {
            parts.append(dims)
        }
        parts.append(humanSize(e.sizeBytes))
        parts.append("from \(e.origin)")
        return parts.joined(separator: " · ")
    }

    // MARK: footer + empty

    private var footer: some View {
        HStack(spacing: 10) {
            Text("\(items.count) of \(daemon.history.count)")
            Spacer()
            keyHint("return", "Paste")
            keyHint("command", "1–9 quick")
            keyHint("escape", "Close")
        }
        .font(.system(size: 11))
        .foregroundStyle(.secondary)
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private func keyHint(_ symbol: String, _ label: String) -> some View {
        HStack(spacing: 4) {
            Image(systemName: symbol == "return" ? "return" :
                              symbol == "escape" ? "escape" : "command")
            Text(label)
        }
    }

    private var emptyState: some View {
        VStack(spacing: 8) {
            Image(systemName: "doc.on.clipboard")
                .font(.system(size: 34))
                .foregroundStyle(.tertiary)
            Text(search.isEmpty ? "No clipboard history yet" : "No matches")
                .foregroundStyle(.secondary)
            if search.isEmpty {
                Text("Copy something on any host to get started")
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: keyboard

    /// Hidden buttons carrying ⏎, Esc, and ⌘1–9 so the whole panel is keyboard-driven.
    private var hiddenShortcuts: some View {
        ZStack {
            Button("") { pasteSelected() }
                .keyboardShortcut(.return, modifiers: [])
            Button("") { onDismiss() }
                .keyboardShortcut(.cancelAction)
            ForEach(1...9, id: \.self) { n in
                Button("") { pasteNumber(n) }
                    .keyboardShortcut(KeyEquivalent(Character("\(n)")), modifiers: .command)
            }
        }
        .opacity(0)
        .allowsHitTesting(false)
    }

    private func pasteSelected() {
        guard let id = selection ?? items.first?.id else { return }
        Task { await daemon.restore(id); onPaste() }
    }

    private func pasteNumber(_ n: Int) {
        guard let id = idForNumber(n, in: items) else { return }
        Task { await daemon.restore(id); onPaste() }
    }
}

/// One comfortable row: thumbnail · title · metadata · ⌘-number.
struct CommandRow: View {
    let entry: HistoryEntry
    let number: Int?

    var body: some View {
        HStack(spacing: 11) {
            thumbnail
                .frame(width: 32, height: 32)
                .clipShape(RoundedRectangle(cornerRadius: 7))
            VStack(alignment: .leading, spacing: 2) {
                Text(entry.preview)
                    .lineLimit(1)
                    .font(.system(size: 13,
                                  design: isMonospacePreferred(entry.preview) ? .monospaced : .default))
                HStack(spacing: 6) {
                    Text(entry.kind.rawValue)
                    Text(entry.origin)
                        .padding(.horizontal, 6)
                        .background(Color.secondary.opacity(0.18))
                        .clipShape(Capsule())
                    Text(entry.ts, style: .relative)
                }
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            }
            Spacer()
            if entry.pinned {
                Image(systemName: "pin.fill").font(.system(size: 10)).foregroundStyle(.secondary)
            }
            if let number {
                Text("⌘\(number)")
                    .font(.system(size: 10))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 5).padding(.vertical, 1)
                    .background(Color.secondary.opacity(0.12))
                    .clipShape(RoundedRectangle(cornerRadius: 4))
            }
        }
        .padding(.vertical, 5)
    }

    @ViewBuilder private var thumbnail: some View {
        if entry.kind == .image, let path = entry.imagePath,
           let img = NSImage(contentsOfFile: path) {
            Image(nsImage: img).resizable().scaledToFill()
        } else {
            ZStack {
                Color.secondary.opacity(0.15)
                Image(systemName: entry.kind == .link ? "link" : "doc.text")
                    .font(.system(size: 13))
                    .foregroundStyle(.secondary)
            }
        }
    }
}
```

- [ ] **Step 2: Compile-check**

Run: `cd apps/mac/Clipfan && swift build 2>&1 | tail -15`
Expected: `Build complete!`. Fix any compile errors (e.g. `KeyEquivalent` import is part of SwiftUI). Do NOT proceed until it builds.

- [ ] **Step 3: Run the full test suite**

Run: `cd apps/mac/Clipfan && swift test 2>&1 | tail -5`
Expected: all green (this task added no tests but must not break the build).

- [ ] **Step 4: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/CommandPanelView.swift
git commit -m "feat(mac): command panel view (search, list, preview, key shortcuts)"
```

---

## Task 5: Command panel NSPanel + controller, wired to the hotkey

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/CommandPanel.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift` (point "Open Clipboard" at the panel)

- [ ] **Step 1: Write the panel + controller**

Create `apps/mac/Clipfan/Sources/Clipfan/CommandPanel.swift`:

```swift
import SwiftUI
import AppKit

/// Floating HUD panel that hosts the command panel. Becomes key without
/// activating the whole app, and closes when it loses key (click-away) or on Esc.
final class CommandPanelController: NSObject, NSWindowDelegate {
    static let shared = CommandPanelController()

    private var panel: NSPanel?

    private override init() { super.init() }

    var isVisible: Bool { panel?.isVisible ?? false }

    func toggle() { isVisible ? hide() : show() }

    func show() {
        if let panel {
            position(panel)
            panel.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            return
        }

        let view = CommandPanelView(
            daemon: .shared,
            onPaste: { [weak self] in self?.hide() },
            onDismiss: { [weak self] in self?.hide() }
        )
        let hosting = NSHostingView(rootView: view)

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

        // Rounded corners on the panel content.
        hosting.wantsLayer = true
        hosting.layer?.cornerRadius = 12
        hosting.layer?.masksToBounds = true

        self.panel = panel
        position(panel)
        panel.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func hide() {
        panel?.orderOut(nil)
    }

    private func position(_ panel: NSPanel) {
        guard let screen = NSScreen.main else { panel.center(); return }
        let f = panel.frame
        let visible = screen.visibleFrame
        let x = visible.midX - f.width / 2
        // Slightly above vertical center reads better, Spotlight-style.
        let y = visible.midY - f.height / 2 + visible.height * 0.08
        panel.setFrameOrigin(NSPoint(x: x, y: y))
    }

    // Close on click-away.
    func windowDidResignKey(_ notification: Notification) {
        hide()
    }
}
```

- [ ] **Step 2: Point the hotkey at the panel**

In `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift`, change the hotkey closure
(currently lines 16–19) from opening the history window to toggling the panel:

```swift
        historyHotkey = GlobalHotkey {
            CommandPanelController.shared.toggle()
        }
```

(Leave the rest of `ClipfanApp.swift` unchanged for now — the menubar icon stays
`systemImage: "doc.on.clipboard"`, which is already a template SF Symbol.)

- [ ] **Step 3: Point the menu item at the panel**

In `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift`, change the "Clipboard
History…" button (currently lines 26–30) to:

```swift
        Button("Open Clipboard") {
            CommandPanelController.shared.show()
        }
        .keyboardShortcut("v", modifiers: [.command, .shift])
```

(The full menu redesign is Task 8; this keeps it working in the meantime.)

- [ ] **Step 4: Build the app bundle**

Run: `cd apps/mac/Clipfan && ./build-app.sh 2>&1 | tail -5`
Expected: `Built .build/Clipfan.app`.

- [ ] **Step 5: Manual verification**

```bash
cd apps/mac/Clipfan && open .build/Clipfan.app
```

Verify (the daemon must be running — if the menu shows "daemon not running", start it first):
- Press ⇧⌘V anywhere → the panel appears centered, translucent, search field focused.
- Type → list filters live.
- ↑/↓ → selection moves; preview updates.
- ⏎ → panel closes (selected clip is now on the clipboard; ⌘V to confirm).
- ⇧⌘V → reopen; ⌘2 → closes (2nd item copied).
- Click another window → panel dismisses.
- Esc → panel dismisses.
- Menu → "Open Clipboard" opens the same panel.

If the panel doesn't take focus for typing, confirm `.nonactivatingPanel` + the
`NSApp.activate` call are present and that `searchFocused = true` runs in `.task`.

- [ ] **Step 6: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/CommandPanel.swift apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift
git commit -m "feat(mac): floating command panel summoned by hotkey, replaces history window"
```

---

## Task 6: Installer tmux gating (auto-detect + --with-tmux / --no-tmux)

**Files:**
- Modify: `dist/install.sh`
- Create: `dist/test-tmux-gating.sh`

The current tmux block (`dist/install.sh` lines 63–86) runs unconditionally when
`tmux.conf.snippet` is present. Make it: default **auto** (run only if `tmux` is on
PATH), with `--with-tmux` forcing on and `--no-tmux` forcing off. Factor the
decision into a `want_tmux` function and add a source guard so it's testable.

- [ ] **Step 1: Write the failing bash test**

Create `dist/test-tmux-gating.sh`:

```bash
#!/usr/bin/env bash
# Tests the tmux-gating decision in install.sh by sourcing it (the source guard
# stops the imperative installer body from running) and exercising want_tmux
# under each mode with tmux present/absent on PATH.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
fails=0
check() { # desc expected actual
    if [[ "$2" == "$3" ]]; then echo "ok   - $1"; else echo "FAIL - $1 (want $2 got $3)"; fails=$((fails+1)); fi
}

# Source install.sh; the source guard must prevent the installer from running.
# shellcheck disable=SC1090
source "$here/install.sh"

# Fake PATH with a tmux binary present.
fake_with=$(mktemp -d); : > "$fake_with/tmux"; chmod +x "$fake_with/tmux"
# Fake PATH with no tmux.
fake_without=$(mktemp -d)

# mode=off -> never, regardless of tmux presence
TMUX_MODE=off PATH="$fake_with" want_tmux; check "off + tmux present -> skip" 1 "$?"

# mode=on -> always
TMUX_MODE=on PATH="$fake_without" want_tmux; check "on + no tmux -> run" 0 "$?"

# mode=auto -> follows tmux presence
TMUX_MODE=auto PATH="$fake_with" want_tmux; check "auto + tmux present -> run" 0 "$?"
TMUX_MODE=auto PATH="$fake_without" want_tmux; check "auto + no tmux -> skip" 1 "$?"

rm -rf "$fake_with" "$fake_without"
if [[ $fails -eq 0 ]]; then echo "ALL PASS"; else echo "$fails FAILED"; exit 1; fi
```

Make it executable:

```bash
chmod +x dist/test-tmux-gating.sh
```

- [ ] **Step 2: Run the test, verify it fails**

Run: `bash dist/test-tmux-gating.sh`
Expected: failure — sourcing today's `install.sh` runs the whole installer (no
source guard, no `want_tmux`). You'll see installer output / errors, not `ALL PASS`.

- [ ] **Step 3: Add arg-parse, want_tmux, and a source guard to install.sh**

In `dist/install.sh`, immediately after the header comment block and `set -euo
pipefail` (after line 12), add argument parsing and the decision function:

```bash
# tmux integration mode: auto (default; on iff tmux is installed), on, or off.
TMUX_MODE=auto
_args=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --with-tmux) TMUX_MODE=on ;;
        --no-tmux)   TMUX_MODE=off ;;
        *) _args+=("$1") ;;
    esac
    shift
done
set -- "${_args[@]:-}"

# want_tmux decides whether to install the tmux copy integration.
want_tmux() {
    case "$TMUX_MODE" in
        on)  return 0 ;;
        off) return 1 ;;
        *)   command -v tmux >/dev/null 2>&1 ;;
    esac
}
```

- [ ] **Step 4: Gate the tmux block on want_tmux**

In `dist/install.sh`, change the tmux block condition (currently line 66) from:

```bash
if [[ -f "$here/tmux.conf.snippet" ]]; then
```

to:

```bash
if [[ -f "$here/tmux.conf.snippet" ]] && want_tmux; then
```

- [ ] **Step 5: Add the source guard**

Wrap the imperative installer body so sourcing the file for tests does not run it.
The simplest non-invasive guard: at the point where real work begins (the
`DEST=${DEST:-$HOME/.local/bin}` line, currently line 14), guard the remainder.

Replace line 14 onward's execution by adding this guard right after the `want_tmux`
function you added in Step 3, and indent nothing — instead, return early when
sourced:

```bash
# When sourced (e.g. by dist/test-tmux-gating.sh) stop here: expose the
# functions/vars above but don't run the installer.
(return 0 2>/dev/null) && return 0
```

`(return 0 2>/dev/null)` succeeds only when the script is being sourced, so the
`&& return 0` short-circuits the installer body in that case and is a no-op when
executed normally.

- [ ] **Step 6: Run the test, verify it passes**

Run: `bash dist/test-tmux-gating.sh`
Expected:
```
ok   - off + tmux present -> skip
ok   - on + no tmux -> run
ok   - auto + tmux present -> run
ok   - auto + no tmux -> skip
ALL PASS
```

- [ ] **Step 7: Lint the installer still parses**

Run: `bash -n dist/install.sh && echo "syntax ok"`
Expected: `syntax ok`.

- [ ] **Step 8: Commit**

```bash
git add dist/install.sh dist/test-tmux-gating.sh
git commit -m "feat(install): tmux integration auto-detects, gated by --with-tmux/--no-tmux"
```

---

## Task 7: Installer.swift passes the tmux flag

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/Installer.swift`
- Test: `apps/mac/Clipfan/Tests/ClipfanTests/InstallerFlagTests.swift`

`Installer.install(...)` runs `bash install.sh` remotely (Installer.swift line 144).
Add a `withTmux: Bool` parameter and pass the matching flag. Add a tiny pure helper
`Installer.tmuxFlag(_:)` so the mapping is unit-tested.

- [ ] **Step 1: Write the failing test**

Create `apps/mac/Clipfan/Tests/ClipfanTests/InstallerFlagTests.swift`:

```swift
import XCTest
@testable import Clipfan

final class InstallerFlagTests: XCTestCase {
    func testTmuxFlagOn() {
        XCTAssertEqual(Installer.tmuxFlag(true), "--with-tmux")
    }
    func testTmuxFlagOff() {
        XCTAssertEqual(Installer.tmuxFlag(false), "--no-tmux")
    }
}
```

- [ ] **Step 2: Run the test, verify it fails to compile**

Run: `cd apps/mac/Clipfan && swift test 2>&1 | tail -20`
Expected: `type 'Installer' has no member 'tmuxFlag'`.

- [ ] **Step 3: Add the helper and the parameter**

In `apps/mac/Clipfan/Sources/Clipfan/Installer.swift`, add the helper inside the
`actor Installer` (next to the other `static func` helpers, e.g. after
`shortName`):

```swift
    /// tmuxFlag maps the Add-Peer tmux checkbox to the install.sh flag. The GUI
    /// always passes an explicit flag so installs are never subject to auto-detect.
    static func tmuxFlag(_ withTmux: Bool) -> String {
        withTmux ? "--with-tmux" : "--no-tmux"
    }
```

Change the `install` signature (line 38) to add the parameter:

```swift
    static func install(user: String, host: String, port: Int, sshKey: String,
                        withTmux: Bool,
                        onProgress: @MainActor @escaping (InstallProgress) -> Void) async throws {
```

Change the remote install command (lines 140–145) so `install.sh` receives the flag:

```swift
        let cmd = """
        set -e
        mkdir -p ~/.config/clipfan
        install -m 0600 /tmp/clipfan-install/config.json ~/.config/clipfan/config.json
        cd /tmp/clipfan-install && bash install.sh \(tmuxFlag(withTmux))
        """
```

- [ ] **Step 4: Update the two existing callers so the app still compiles**

In `apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift`, the two `Installer.install(...)`
calls (ManualForm line ~71 and TailscalePickerView line ~157) currently omit
`withTmux`. Add `withTmux: false` to both for now — Task 9 wires the real checkbox:

ManualForm:
```swift
                try await Installer.install(
                    user: user, host: host, port: port, sshKey: sshKey,
                    withTmux: false,
                    onProgress: { p in status = "\(p.step): \(p.detail)" }
                )
```

TailscalePickerView:
```swift
                    try await Installer.install(user: user, host: peer.hostName, port: 22, sshKey: "",
                                                withTmux: false,
                                                onProgress: { p in
                        statusByHost[peer.hostName] = "\(p.step): \(p.detail)"
                    })
```

- [ ] **Step 5: Run the test + build, verify green**

Run: `cd apps/mac/Clipfan && swift test --filter InstallerFlagTests 2>&1 | tail -10`
Expected: `Executed 2 tests, with 0 failures`.

Run: `cd apps/mac/Clipfan && swift build 2>&1 | tail -5`
Expected: `Build complete!`.

- [ ] **Step 6: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/Installer.swift apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift apps/mac/Clipfan/Tests/ClipfanTests/InstallerFlagTests.swift
git commit -m "feat(mac): Installer passes --with-tmux/--no-tmux to remote install.sh"
```

---

## Task 8: Menubar — at-a-glance fleet

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift`

Replace the `●✗○` glyph-strings and redundant items with: a header, **Open
Clipboard**, a **Fleet** section (one row per peer with a colored dot + last-sync),
**Settings**, **Quit**. Try the native `.menu` style first.

- [ ] **Step 1: Rewrite StatusMenuView**

Replace the entire contents of `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift`:

```swift
import SwiftUI

struct StatusMenuView: View {
    @EnvironmentObject var daemon: DaemonClient
    @Environment(\.openWindow) var openWindow

    var body: some View {
        Text(daemon.connected ? "clipfan · \(daemon.origin)" : "clipfan · daemon not running")

        Divider()

        Button("Open Clipboard") {
            CommandPanelController.shared.show()
        }
        .keyboardShortcut("v", modifiers: [.command, .shift])

        Divider()

        if daemon.peers.isEmpty {
            Text("No peers yet — add one in Settings")
        } else {
            Section("Fleet") {
                ForEach(daemon.peers) { peer in
                    Button {
                        NSApp.activate(ignoringOtherApps: true)
                        openWindow(id: "settings")
                    } label: {
                        Label("\(peer.hostname) — \(lastSync(peer))",
                              systemImage: dotSymbol(peer))
                    }
                }
            }
        }

        Divider()

        Button("Settings…") {
            NSApp.activate(ignoringOtherApps: true)
            openWindow(id: "settings")
        }
        .keyboardShortcut(",", modifiers: .command)

        Button("Quit") {
            NSApp.terminate(nil)
        }
        .keyboardShortcut("q", modifiers: .command)
    }

    /// Health dot as an SF Symbol name. Green when the last push succeeded,
    /// amber when it failed/stale, hollow when never contacted.
    private func dotSymbol(_ peer: Peer) -> String {
        if peer.last_push_ok { return "circle.fill" }
        if let ts = peer.last_push_ts, ts > Date.distantPast { return "exclamationmark.circle.fill" }
        return "circle"
    }

    /// Human last-sync summary for the menu row.
    private func lastSync(_ peer: Peer) -> String {
        let recv = relative(peer.last_recv_ts)
        let push = relative(peer.last_push_ts)
        if !peer.last_push_ok, peer.last_push_ts != nil, peer.last_push_ts! > Date.distantPast {
            return "offline"
        }
        // Prefer the most recent of push/recv as "last synced".
        let latest = [peer.last_push_ts, peer.last_recv_ts]
            .compactMap { $0 }
            .filter { $0 > Date.distantPast }
            .max()
        guard let latest else { return "never" }
        return relativeShort(latest)
        // (recv/push kept above for future detail; not shown inline)
        _ = (recv, push)
    }

    private func relative(_ date: Date?) -> String {
        guard let date, date > Date.distantPast else { return "never" }
        return relativeShort(date)
    }

    private func relativeShort(_ date: Date) -> String {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .short
        return f.localizedString(for: date, relativeTo: Date())
    }
}
```

Note: remove the dead `_ = (recv, push)` line and the two unused locals if the
compiler warns — they're vestigial. Final `lastSync` should be:

```swift
    private func lastSync(_ peer: Peer) -> String {
        if !peer.last_push_ok, let ts = peer.last_push_ts, ts > Date.distantPast {
            return "offline"
        }
        let latest = [peer.last_push_ts, peer.last_recv_ts]
            .compactMap { $0 }
            .filter { $0 > Date.distantPast }
            .max()
        guard let latest else { return "never" }
        return relativeShort(latest)
    }
```

(Use this clean version; the verbose one above is shown only to explain intent.)

- [ ] **Step 2: Build the app bundle**

Run: `cd apps/mac/Clipfan && ./build-app.sh 2>&1 | tail -5`
Expected: `Built .build/Clipfan.app`.

- [ ] **Step 3: Manual verification (decides .menu vs .window)**

```bash
cd apps/mac/Clipfan && open .build/Clipfan.app
```

Click the menubar icon and verify:
- Header reads `clipfan · <hostname>`.
- "Open Clipboard" (⇧⌘V) opens the panel.
- A "Fleet" section lists each peer with a status icon + relative last-sync.
- "Settings…" (⌘,) opens Settings; "Quit" (⌘Q) quits.
- No `●✗○` strings; no duplicate Add-peer/Settings.

**Decision point (Jesse's "decide during impl"):** if the SF Symbol health icons
render acceptably (even if monochrome in the menu), keep `.menu`. If glanceable
color is essential and the menu renders them flat/uncolored, switch the app to a
custom window-style menu: in `ClipfanApp.swift` change `.menuBarExtraStyle(.menu)`
to `.menuBarExtraStyle(.window)` and wrap `StatusMenuView` body in a `VStack`
with padding so it renders as a popover (colored `Circle()` dots become possible).
Record which path you took in the commit message.

- [ ] **Step 4: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift
git commit -m "feat(mac): at-a-glance fleet menubar menu (replaces glyph strings)"
```

---

## Task 9: Add Peer — adaptive Tailnet-first sheet with tmux opt-in

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift`

Collapse the two-tab sheet into one adaptive view: a Tailnet checklist that appears
only when `TailscaleClient.status()` returns peers, manual fields below, a shared
**tmux opt-in checkbox** (off by default), and friendly progress (no raw step strings).

- [ ] **Step 1: Rewrite AddPeerSheet.swift**

Replace the entire contents of `apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift`:

```swift
import SwiftUI

struct AddPeerSheet: View {
    @Environment(\.dismiss) private var dismiss

    @State private var user: String = NSUserName()
    @State private var host: String = ""
    @State private var port: Int = 22
    @State private var sshKey: String = ""
    @State private var withTmux = false

    @State private var tailnet: [TailscalePeer] = []
    @State private var tailnetSelected: Set<String> = []
    @State private var tailnetAvailable = false

    @State private var installing = false
    @State private var progress: String = ""

    private var installCount: Int {
        tailnetSelected.count + (host.isEmpty ? 0 : 1)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Add a peer").font(.title3).bold()
            Text("Install clipfan on another host over SSH")
                .font(.callout).foregroundStyle(.secondary)

            if tailnetAvailable {
                tailnetSection
                dividerLabel("or add manually")
            }

            manualSection

            Toggle(isOn: $withTmux) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Set up tmux copy integration")
                    Text("Edits ~/.tmux.conf so copies inside tmux (incl. Claude Code) sync to the fleet.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }

            if !progress.isEmpty {
                Text(progress).font(.callout).foregroundStyle(.secondary)
            }

            Spacer()

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button(installing ? "Installing…" : installLabel) { install() }
                    .keyboardShortcut(.return)
                    .disabled(installCount == 0 || installing)
            }
        }
        .padding(20)
        .frame(width: 560, height: tailnetAvailable ? 560 : 420)
        .task { await loadTailnet() }
    }

    private var installLabel: String {
        installCount <= 1 ? "Install" : "Install on \(installCount) hosts"
    }

    // MARK: tailnet

    private var tailnetSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("From your tailnet", systemImage: "network").font(.headline)
            ForEach(tailnet) { peer in
                Button {
                    toggle(peer.id)
                } label: {
                    HStack(spacing: 10) {
                        Image(systemName: tailnetSelected.contains(peer.id) ? "checkmark.square.fill" : "square")
                            .foregroundStyle(tailnetSelected.contains(peer.id) ? Color.accentColor : .secondary)
                        Circle().fill(peer.online ? Color.green : Color.gray).frame(width: 8, height: 8)
                        Text(peer.hostName)
                        Text(peer.os).font(.caption).foregroundStyle(.secondary)
                        Spacer()
                        Text(peer.ip).font(.caption).foregroundStyle(.secondary)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .padding(.vertical, 4).padding(.horizontal, 8)
                .background(tailnetSelected.contains(peer.id) ? Color.accentColor.opacity(0.12) : Color.clear)
                .clipShape(RoundedRectangle(cornerRadius: 7))
            }
        }
    }

    private func toggle(_ id: String) {
        if tailnetSelected.contains(id) { tailnetSelected.remove(id) } else { tailnetSelected.insert(id) }
    }

    // MARK: manual

    private var manualSection: some View {
        Form {
            TextField("Host", text: $host, prompt: Text("host.local or 192.168.1.42"))
            TextField("User", text: $user)
            TextField("SSH port", value: $port, format: .number)
            if !tailnetAvailable {
                TextField("SSH key (optional)", text: $sshKey, prompt: Text("~/.ssh/id_ed25519"))
            }
        }
        .formStyle(.grouped)
    }

    private func dividerLabel(_ text: String) -> some View {
        HStack {
            VStack { Divider() }
            Text(text).font(.caption).foregroundStyle(.secondary).fixedSize()
            VStack { Divider() }
        }
    }

    // MARK: actions

    private func loadTailnet() async {
        if let peers = try? await TailscaleClient.status(), !peers.isEmpty {
            tailnet = peers
            tailnetAvailable = true
        } else {
            tailnetAvailable = false
        }
    }

    private func install() {
        installing = true
        progress = ""
        Task {
            var targets: [(user: String, host: String, port: Int, key: String)] = []
            for peer in tailnet where tailnetSelected.contains(peer.id) {
                targets.append((NSUserName(), peer.hostName, 22, ""))
            }
            if !host.isEmpty {
                targets.append((user, host, port, sshKey))
            }

            for t in targets {
                await MainActor.run { progress = "Installing on \(t.host)…" }
                do {
                    try await Installer.install(
                        user: t.user, host: t.host, port: t.port, sshKey: t.key,
                        withTmux: withTmux,
                        onProgress: { p in progress = friendly(p, host: t.host) }
                    )
                    await MainActor.run { progress = "Installed on \(t.host)." }
                } catch {
                    await MainActor.run { progress = "Failed on \(t.host): \(error.localizedDescription)" }
                    await MainActor.run { installing = false }
                    return
                }
            }
            await MainActor.run {
                installing = false
                Task { try? await Task.sleep(nanoseconds: 1_000_000_000); dismiss() }
            }
        }
    }

    /// friendly maps Installer's internal step names to user-facing phrases,
    /// so raw playbook strings never reach the UI.
    private func friendly(_ p: InstallProgress, host: String) -> String {
        let phrase: String
        switch p.step {
        case "Probe":   phrase = "Connecting"
        case "Config":  phrase = "Preparing keys"
        case "Upload":  phrase = "Copying clipfan"
        case "Install": phrase = "Installing"
        case "Local", "Restart": phrase = "Finishing up"
        default:        phrase = "Working"
        }
        return "\(host): \(phrase)…"
    }
}
```

(This deletes the old `ManualForm` and `TailscalePickerView` structs — they're
fully replaced by this single adaptive view.)

- [ ] **Step 2: Build**

Run: `cd apps/mac/Clipfan && swift build 2>&1 | tail -10`
Expected: `Build complete!`. (No references to the deleted `ManualForm` /
`TailscalePickerView` remain — `SettingsView` only presents `AddPeerSheet()`.)

- [ ] **Step 3: Manual verification**

Run: `cd apps/mac/Clipfan && ./build-app.sh && open .build/Clipfan.app`, open
Settings → Fleet → "Add peer…", and verify:
- With Tailscale running: a "From your tailnet" checklist appears above the manual
  fields; rows toggle selection; the button reads "Install on N hosts".
- The tmux checkbox is **unchecked** by default, with its explanatory subtitle.
- With Tailscale stopped (`tailscale down` or quit): the tailnet section is absent;
  the manual form (with the SSH-key field) shows alone.
- Installing shows friendly phrases ("Copying clipfan…"), not "Upload: scp 6 files".

(If you can't toggle Tailscale, at minimum verify the tailnet-absent layout by
temporarily forcing `tailnetAvailable = false` in a scratch build, then revert.)

- [ ] **Step 4: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift
git commit -m "feat(mac): adaptive Tailnet-first Add Peer sheet with tmux opt-in"
```

---

## Task 10: Settings — Fleet cards + humanized General

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift`

Rename the "Peers" tab to "Fleet" with peer **cards** (not a `Table`), and move
config paths / log / restart into a collapsed **Developer** disclosure on General.

- [ ] **Step 1: Rewrite SettingsView.swift**

Replace the entire contents of `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift`:

```swift
import ServiceManagement
import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var daemon: DaemonClient

    enum Tab: String, CaseIterable, Hashable {
        case fleet = "Fleet"
        case general = "General"
    }

    @State private var selection: Tab = .fleet

    var body: some View {
        TabView(selection: $selection) {
            FleetTab()
                .tabItem { Label("Fleet", systemImage: "network") }
                .tag(Tab.fleet)
            GeneralTab()
                .tabItem { Label("General", systemImage: "gear") }
                .tag(Tab.general)
        }
        .padding()
    }
}

struct FleetTab: View {
    @EnvironmentObject var daemon: DaemonClient
    @State private var showAdd = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Fleet").font(.headline)
                Spacer()
                Button("Refresh") { Task { await daemon.refresh() } }
            }
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
            Button {
                showAdd = true
            } label: {
                Label("Add peer…", systemImage: "plus")
            }
            .buttonStyle(.borderedProminent)
        }
        .sheet(isPresented: $showAdd) { AddPeerSheet() }
    }
}

struct PeerCard: View {
    let peer: Peer

    var body: some View {
        HStack(spacing: 13) {
            Circle().fill(dotColor).frame(width: 9, height: 9)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(peer.hostname).font(.system(size: 13.5, weight: .semibold))
                }
                Text(stateLine).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 3) {
                Text("↑ \(timeAgo(peer.last_push_ts))   ↓ \(timeAgo(peer.last_recv_ts))")
                    .font(.system(size: 10)).foregroundStyle(.secondary)
                Text(healthWord).font(.system(size: 10)).foregroundStyle(healthColor)
            }
        }
        .padding(12)
        .background(Color.secondary.opacity(0.06))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.secondary.opacity(0.12)))
        .clipShape(RoundedRectangle(cornerRadius: 10))
    }

    private var dotColor: Color { healthColor }

    private var healthColor: Color {
        if peer.last_push_ok { return .green }
        if let ts = peer.last_push_ts, ts > Date.distantPast { return .orange }
        return .gray
    }

    private var healthWord: String {
        if peer.last_push_ok { return "healthy" }
        if let ts = peer.last_push_ts, ts > Date.distantPast { return "offline" }
        return "idle"
    }

    private var stateLine: String {
        if peer.last_push_ok { return "port \(peer.port) · synced" }
        if let err = peer.last_push_err, !err.isEmpty { return "last error: \(err)" }
        return "port \(peer.port)"
    }

    private func timeAgo(_ date: Date?) -> String {
        guard let date, date > Date.distantPast else { return "never" }
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .short
        return f.localizedString(for: date, relativeTo: Date())
    }
}

struct GeneralTab: View {
    @EnvironmentObject var daemon: DaemonClient
    @StateObject private var loginItem = LoginItemManager.shared
    @State private var showDeveloper = false

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

            Section("Status") {
                LabeledContent("Daemon", value: daemon.connected ? "running" : "down")
            }

            Section {
                DisclosureGroup("Developer", isExpanded: $showDeveloper) {
                    LabeledContent("Config") { Text(configPath).font(.system(.caption, design: .monospaced)) }
                    LabeledContent("Share dir") { Text(shareDirPath).font(.system(.caption, design: .monospaced)) }
                    Button("Reveal config in Finder") {
                        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: configPath)])
                    }
                    Button("Open daemon log") {
                        NSWorkspace.shared.open(URL(fileURLWithPath: logPath))
                    }
                    Button("Restart daemon") {
                        daemon.restartDaemon()
                        Task { await daemon.refresh() }
                    }
                }
            }
        }
        .formStyle(.grouped)
    }

    var configPath: String {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/clipfan/config.json").path
    }
    var shareDirPath: String { Installer.shareDir.path }
    var logPath: String { "/tmp/clipfan-shell.log" }
}
```

Note: "History limit" is shown read-only (200) to match the current behavior —
the daemon's `max_history` is config-driven and not yet writable from the app;
making it editable is out of scope (no API for it). Keep it as a labeled value.

- [ ] **Step 2: Build**

Run: `cd apps/mac/Clipfan && swift build 2>&1 | tail -10`
Expected: `Build complete!`.

- [ ] **Step 3: Manual verification**

Run: `cd apps/mac/Clipfan && ./build-app.sh && open .build/Clipfan.app`, open
Settings:
- "Fleet" tab shows peer **cards** (dot, hostname, state line, ↑/↓ times, health
  word), an accent "Add peer…" button, and an empty state when there are none.
- "General" tab shows Startup / Clipboard / Status sections; config paths, log,
  and "Restart daemon" are hidden inside a collapsed **Developer** disclosure.

- [ ] **Step 4: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift
git commit -m "feat(mac): fleet-card Settings + humanized General with Developer disclosure"
```

---

## Task 11: App icon + bundle wiring

**Files:**
- Create: `apps/mac/Clipfan/make-icon.sh`
- Create (generated): `apps/mac/Clipfan/Resources/AppIcon.icns`
- Modify: `apps/mac/Clipfan/Info.plist`
- Modify: `apps/mac/Clipfan/build-app.sh`

Per Jesse's "spike during impl": this plan takes the **reliable path** — generate
a real `.icns` from CoreGraphics-drawn art and copy it into the bundle via
`build-app.sh` (no SwiftPM `.xcassets` dependency, which is unreliable for
executable targets on the SPM macOS-13 toolchain). The accent color stays the
**system accent** (no AccentColor asset needed — `Color.accentColor` already
follows the system).

- [ ] **Step 1: Write the icon generator**

Create `apps/mac/Clipfan/make-icon.sh`:

```bash
#!/usr/bin/env bash
# Renders the clipfan app icon (graphite cards fanned out, one accent card) at all
# required sizes and packs them into Resources/AppIcon.icns via iconutil.
set -euo pipefail
cd "$(dirname "$0")"

work=$(mktemp -d)
iconset="$work/AppIcon.iconset"
mkdir -p "$iconset" Resources

# Draw a 1024px master with Swift + CoreGraphics, then downscale with sips.
cat > "$work/draw.swift" <<'SWIFT'
import AppKit

let size = 1024
let img = NSImage(size: NSSize(width: size, height: size))
img.lockFocus()
let ctx = NSGraphicsContext.current!.cgContext

// Background: rounded graphite gradient (macOS icon grid is handled by the mask
// at pack time; we draw a full-bleed rounded square).
let rect = CGRect(x: 0, y: 0, width: size, height: size)
let bgPath = CGPath(roundedRect: rect.insetBy(dx: 80, dy: 80),
                    cornerWidth: 180, cornerHeight: 180, transform: nil)
ctx.addPath(bgPath)
let colors = [NSColor(calibratedWhite: 0.22, alpha: 1).cgColor,
              NSColor(calibratedWhite: 0.12, alpha: 1).cgColor] as CFArray
let grad = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(), colors: colors, locations: [0, 1])!
ctx.saveGState(); ctx.clip()
ctx.drawLinearGradient(grad, start: CGPoint(x: 0, y: size), end: CGPoint(x: size, y: 0), options: [])
ctx.restoreGState()

// Three fanned cards. Two graphite, one accent (system blue).
func card(cx: CGFloat, cy: CGFloat, angle: CGFloat, fill: NSColor) {
    ctx.saveGState()
    ctx.translateBy(x: cx, y: cy)
    ctx.rotate(by: angle * .pi / 180)
    let w: CGFloat = 300, h: CGFloat = 380
    let r = CGRect(x: -w/2, y: -h/2, width: w, height: h)
    let p = CGPath(roundedRect: r, cornerWidth: 40, cornerHeight: 40, transform: nil)
    ctx.addPath(p); ctx.setFillColor(fill.cgColor); ctx.fillPath()
    ctx.restoreGState()
}
card(cx: 430, cy: 520, angle: 14, fill: NSColor(calibratedWhite: 0.80, alpha: 1))
card(cx: 512, cy: 512, angle: 0,  fill: NSColor(calibratedWhite: 0.92, alpha: 1))
card(cx: 594, cy: 504, angle: -14, fill: NSColor.systemBlue)

img.unlockFocus()
let tiff = img.tiffRepresentation!
let rep = NSBitmapImageRep(data: tiff)!
let png = rep.representation(using: .png, properties: [:])!
try! png.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
SWIFT

master="$work/master.png"
swift "$work/draw.swift" "$master"

# Generate the iconset sizes Apple requires.
gen() { sips -z "$2" "$2" "$master" --out "$iconset/icon_$1.png" >/dev/null; }
gen 16x16        16
gen 16x16@2x     32
gen 32x32        32
gen 32x32@2x     64
gen 128x128      128
gen 128x128@2x   256
gen 256x256      256
gen 256x256@2x   512
gen 512x512      512
gen 512x512@2x   1024

iconutil -c icns "$iconset" -o Resources/AppIcon.icns
echo "Wrote Resources/AppIcon.icns"
```

Make it executable:

```bash
chmod +x apps/mac/Clipfan/make-icon.sh
```

- [ ] **Step 2: Generate the icon**

Run: `cd apps/mac/Clipfan && ./make-icon.sh`
Expected: `Wrote Resources/AppIcon.icns`, and `Resources/AppIcon.icns` exists.

Verify it's a real icns:
Run: `file apps/mac/Clipfan/Resources/AppIcon.icns`
Expected: `... Mac OS X icon`.

- [ ] **Step 3: Reference the icon in Info.plist**

In `apps/mac/Clipfan/Info.plist`, add (inside the top-level `<dict>`, after the
`CFBundleExecutable` block):

```xml
    <key>CFBundleIconFile</key>
    <string>AppIcon</string>
```

- [ ] **Step 4: Copy the icon into the bundle in build-app.sh**

In `apps/mac/Clipfan/build-app.sh`, after the line `cp "$BIN" "$APP/Contents/MacOS/Clipfan"`
(line 19), add:

```bash
cp Resources/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
```

- [ ] **Step 5: Build and verify the icon shows**

Run: `cd apps/mac/Clipfan && ./build-app.sh 2>&1 | tail -5`
Expected: `Built .build/Clipfan.app`.

Verify the icon is in the bundle:
Run: `ls apps/mac/Clipfan/.build/Clipfan.app/Contents/Resources/AppIcon.icns && echo present`
Expected: `present`.

Manual: `open apps/mac/Clipfan/.build/Clipfan.app` — the app icon (fanned cards)
appears in the Dock/⌘-Tab is suppressed (LSUIElement), but the icon shows in
Finder's Get Info and in the About panel. Confirm in Finder:
`open apps/mac/Clipfan/.build/` and look at `Clipfan.app`'s icon. If it's still
generic, run `touch apps/mac/Clipfan/.build/Clipfan.app` and re-check (Finder
icon cache can lag), or copy to /Applications.

- [ ] **Step 6: Commit**

```bash
git add apps/mac/Clipfan/make-icon.sh apps/mac/Clipfan/Resources/AppIcon.icns apps/mac/Clipfan/Info.plist apps/mac/Clipfan/build-app.sh
git commit -m "feat(mac): app icon (fanned cards) generated + bundled"
```

---

## Task 12: Retire the old history window

**Files:**
- Delete: `apps/mac/Clipfan/Sources/Clipfan/HistoryWindow.swift`
- Delete: `apps/mac/Clipfan/Sources/Clipfan/HistoryWindowController.swift`

The command panel fully replaces both. Confirm there are no remaining references
before deleting.

- [ ] **Step 1: Confirm no references remain**

Run: `cd apps/mac/Clipfan && grep -rn "HistoryWindowController\|HistoryWindow(" Sources/ || echo "no references"`
Expected: `no references` (Tasks 5 and 8 repointed the only two callers to
`CommandPanelController`).

If any references remain, repoint them to `CommandPanelController.shared.show()`
before continuing.

- [ ] **Step 2: Delete the files**

Run:
```bash
cd apps/mac/Clipfan
git rm Sources/Clipfan/HistoryWindow.swift Sources/Clipfan/HistoryWindowController.swift
```

- [ ] **Step 3: Build + test**

Run: `cd apps/mac/Clipfan && swift build 2>&1 | tail -5 && swift test 2>&1 | tail -5`
Expected: `Build complete!` and all tests pass.

- [ ] **Step 4: Commit**

```bash
git commit -m "refactor(mac): remove history window, superseded by command panel"
```

---

## Task 13: Full build + end-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Clean build the app**

Run: `cd apps/mac/Clipfan && rm -rf .build/Clipfan.app && ./build-app.sh 2>&1 | tail -5`
Expected: `Built .build/Clipfan.app`.

- [ ] **Step 2: Run the whole test suite**

Run: `cd apps/mac/Clipfan && swift test 2>&1 | tail -8`
Expected: all suites green — `ClipMetadataTests`, `PanelSelectionTests`,
`InstallerFlagTests`, `HistoryFilterTests`, `HistoryEntryTests`, `SigningTests`.

- [ ] **Step 3: Run the installer test**

Run: `bash dist/test-tmux-gating.sh`
Expected: `ALL PASS`.

- [ ] **Step 4: Manual smoke of the whole app**

```bash
cp -R apps/mac/Clipfan/.build/Clipfan.app /Applications/   # replace the running copy
open /Applications/Clipfan.app
```

Walk the full flow:
- Menubar: header, Open Clipboard, Fleet list with health, Settings, Quit.
- ⇧⌘V: panel summons, search, ↑/↓, ⏎ paste + dismiss, ⌘1–9, Esc, click-away.
- Settings → Fleet cards; General with collapsed Developer.
- Add peer sheet: tailnet section present/absent by Tailscale state; tmux checkbox off.
- App icon visible in Finder Get Info.

- [ ] **Step 5: Commit any final tweaks, then update memory**

If verification surfaced small fixes, commit them. Then note completion (the
implementer should update the project memory file `project_clipfan.md` and the
session journal per house rules — record that the UX redesign shipped, the panel
replaced the window, and tmux is now opt-in).

```bash
git add -A   # only after git status confirms the change set
git commit -m "chore(mac): finalize UX redesign"
```

---

## Self-Review (completed by plan author)

**Spec coverage:**
- Command panel → Tasks 4, 5 ✓
- Adaptive Native identity (system accent, graphite) → applied across Tasks 4/8/9/10; icon Task 11 ✓
- Single column + preview → Task 4 ✓
- At-a-glance fleet menubar (template SF Symbol; menu) → Task 8; icon already template (Task 5 note) ✓
- Fleet cards + humanized General/Developer → Task 10 ✓
- Tailnet-first adaptive Add Peer + tmux opt-in → Task 9 ✓
- tmux opt-in installer (auto-detect + flags) → Tasks 6, 7 ✓
- Metadata humanization helpers → Task 1 (used in Task 4) ✓
- Tests: humanization, selection, installer flag, install.sh gating → Tasks 1, 2, 7, 6 ✓
- Build/packaging (icon, Info.plist, build-app.sh) → Task 11 ✓
- Retire old window → Task 12 ✓

**Deviations from spec (documented, with rationale):**
- ⌥⏎ plain-text paste dropped (store has no rich text; would need an API change). See scope note.
- Auto-paste (synthetic ⌘V) not implemented (needs Accessibility; already deferred). ⏎ = restore + dismiss.
- History-limit shown read-only in General (no daemon API to set `max_history` from the app).

**Type/name consistency check:** `humanSize`, `formatDimensions`, `imageDimensions`,
`isMonospacePreferred`, `movedSelection`, `idForNumber`, `clampedSelection`,
`CommandPanelController.shared.show()/toggle()/hide()`, `Installer.tmuxFlag(_:)`,
`Installer.install(..., withTmux:, onProgress:)`, `want_tmux`, `TMUX_MODE` — all
used consistently across tasks. `Peer` fields (`hostname`, `port`, `last_push_ok`,
`last_push_ts`, `last_push_err`, `last_recv_ts`) and `HistoryEntry` fields match
the real models read from source.

**Placeholder scan:** none — every code step contains complete code; every run
step has an exact command + expected output.
