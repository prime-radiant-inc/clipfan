# clipfan macOS App — UX Redesign

**Date:** 2026-05-29
**Status:** Approved design (pending spec review)
**Tracks:** PRI-1873 / PRI-1875 follow-on

## Problem

The SwiftUI menubar app is ~90% stock SwiftUI. It works, but it reads as
engineer-built: no app icon, no accent color, no typography system. Sync status
shows as unicode glyphs (`●✗○`) embedded in menu strings; clip sizes render as
raw bytes (`248213 B`); filesystem paths, a log-file opener, and "Restart
daemon" are surfaced as first-class UI; the peer list is a spreadsheet `Table`;
the clipboard history is a fixed-size hand-rolled `NSWindow`. The functional
bones are good — two-pane history, search, type filters, pin/delete, Tailscale
picker. What is missing is entirely the polish and interaction layer.

This redesign keeps the daemon, the HTTP API, and the data models unchanged. It
is a UI/UX rework of the Mac app plus one installer behavior change (tmux opt-in).

## Goals

- Make the app feel like a native, designed macOS utility, not a dev tool.
- Make the clipboard history fast and keyboard-driven (the primary daily action).
- Make fleet sync status glanceable.
- Give clipfan a real visual identity (icon + accent) that follows macOS conventions.
- Stop forcing tmux config edits during peer install.

## Non-Goals

- No daemon, wire-format, or HTTP API changes.
- No new history features (merged cross-fleet history, OCR, paste-stack, link
  card fetching) — deferred. Pinning, plain-text paste, and per-type filters
  already exist or are cheap and stay in scope.
- No change to the sync/relay model or conflict policy.
- No codesigning/notarization work (separate roadmap item).

## Design Decisions (all approved via visual brainstorming)

1. **History becomes a hotkey-summoned command panel** (Raycast/Spotlight model),
   not a window. (Decision: B)
2. **Visual identity: Adaptive Native** — graphite chrome, follows the user's
   system accent color for selection; brand color only on the app icon and a
   small status dot. (Decision: C)
3. **Panel interior: single column of clips + persistent preview pane**,
   "comfortable" row density. (Decision: B, clarified to one vertical list)
4. **Menubar: at-a-glance fleet** — template SF Symbol icon; menu shows Open
   Clipboard, a quiet Fleet peer list with colored dots + last-sync, Settings,
   Quit. (Decision: B)
5. **Settings: fleet-as-cards + humanized General** — peers as cards (not a
   table); General shows human settings, with config paths / log / restart
   collapsed into a "Developer" disclosure. (Approved)
6. **Add Peer: Tailnet-first adaptive sheet** — Tailnet multi-select checklist
   shows only when `tailscale status` succeeds, manual host entry below it, one
   shared tmux opt-in checkbox. (Approved)
7. **tmux integration becomes opt-in**, off by default, per install. (Jesse flag)

---

## Component 1 — Command Panel (the headline change)

### Behavior

- Summoned by the global hotkey **⇧⌘V** from anywhere. Replaces the current
  "open the history NSWindow" behavior.
- Appears centered on the screen with the active app (focus follows the user),
  as a floating panel above other windows.
- The search field is **focused on open** — the user types immediately.
- Dismisses on **Esc**, on click-away (resign key), and after a paste.
- Translucent background via `NSVisualEffectView` (material `.hudWindow` or
  `.popover`, `blendingMode = .behindWindow`). Must remain legible with Reduce
  Transparency on — fall back to a solid graphite background.
- Rounded window corners (~12pt), no standard title bar.

### Layout

```
┌──────────────────────────────────────────────┐
│ ⌕  Type to search…        [All Text Image Link]│   search row
├───────────────────────┬──────────────────────┤
│ ▦ Screenshot 2026… ⌘1 │  Preview of selected  │
│ ⌗ func chooseBack… ⌘2 │  clip (image/text/    │   list (single col)  +  preview pane
│ 🔗 github.com/pri… ⌘3 │  link), with metadata │
│ ⌗ jesse@fsck.com   ⌘4 │  badge                │
├───────────────────────┴──────────────────────┤
│ 5 of 200      ⏎ Paste  ⌥⏎ Plain  ⌘K actions   │   footer
└──────────────────────────────────────────────┘
```

- **Search row:** magnifier icon, plain search field, type-filter chips on the
  right (All / Text / Image / Link). Active chip uses the system accent.
- **List (single vertical column):** each row is icon · title · metadata line ·
  ⌘-number (1–9 for the top items). Row uses "comfortable" density (~44pt:
  thumbnail + two text lines). Selected row uses the system accent at low opacity.
- **Preview pane (right):** full preview of the selected clip — rendered image,
  scrollable text, or link. A metadata badge shows humanized type + size +
  origin host + pinned state.
- **Footer:** item count, and key hints: ⏎ Paste, ⌥⏎ Plain (paste without
  formatting), ⌘K actions.

### Keyboard map

| Key | Action |
|-----|--------|
| ⇧⌘V | Summon / dismiss the panel |
| (type) | Instant search |
| ↑ / ↓ | Move selection |
| ⏎ | Paste selected clip into the frontmost app (restore + sync) |
| ⌥⏎ | Paste as plain text |
| ⌘1–⌘9 | Paste the Nth item directly |
| ⌘K | Open the per-item action menu (pin/unpin, delete, copy without paste) |
| Esc | Dismiss |

`⏎` and `⌘1`–`⌘9` call the existing `POST /v1/restore`. Plain-text paste
(`⌥⏎`) re-copies the text content without rich types before restore.

### Metadata humanization

- Sizes: `242 KB`, not `248213 B` (ByteCountFormatter).
- Images: show dimensions (`1920×1080`) when known.
- Timestamps: relative (`now`, `2m`, `1h`, `3h`) — already used.
- Origin host: a small badge, not "from <host>" prose.
- Code-like text and file paths: rendered in a monospaced font.

### Implementation notes

- Replace `HistoryWindowController`'s `NSWindow` with an `NSPanel` subclass
  (`.nonactivatingPanel`, `.hudWindow` style, `isFloatingPanel = true`,
  `becomesKeyOnlyIfNeeded`). Handle `resignKey` → close. `animationBehavior =
  .utilityWindow` for a smooth fade.
- The panel hosts an `NSHostingView` of a new `CommandPanelView`.
- Reuse `HistoryViewModel.filteredHistory()` for search + filter + pinned-first
  ordering. Extend it only as needed for keyboard selection state.
- The ⌘K action menu reuses existing `setPinned` / `deleteEntry` / `restore`.

---

## Component 2 — Visual Identity (Adaptive Native)

- **App icon:** a custom `.icns` showing clipboard cards *fanned out* (the name:
  clipboard, fanned across the fleet). Graphite cards with one accent card.
  Standard macOS rounded-rect icon grid.
- **Accent:** use the **system accent color** (`Color.accentColor` / control
  tint) for selection and active chips — do not hardcode a brand hue. The brand
  color appears only in the app icon and the menubar status dot.
- **Chrome:** neutral graphite surfaces, native materials, 8pt spacing grid,
  ~6–8pt row corner radii, ~12pt panel/card radii.
- **Typography:** system font (SF Pro) throughout; a clear size/weight hierarchy
  (titles ~13–15pt semibold, body ~13pt, metadata ~11pt); SF Mono for code/paths.
- **Asset catalog:** the SPM build currently has no asset catalog. Add an
  `Assets.xcassets` (AppIcon + AccentColor) and wire it into the build —
  either as an SPM resource processed by the toolchain, or by extending
  `build-app.sh` to compile/copy the icon into `Contents/Resources` and set
  `CFBundleIconName`/`CFBundleIconFile` in `Info.plist`. (Spike: confirm SPM
  `.copy`/`.process` handles `.xcassets` for an executable target on the macOS
  13 toolchain; if not, use the `actool`/`build-app.sh` path.)

---

## Component 3 — Menubar (at-a-glance fleet)

### Icon

- A **template** SF Symbol (`isTemplate = true`) so macOS auto-tints for
  light/dark/active. ~16×16 optical weight in the 22pt menubar area. No color.

### Menu (replaces the `●✗○` strings + redundant items)

```
clipfan                       this Mac · m4
─────────────────────────────────────────
▤  Open Clipboard                     ⇧⌘V
─────────────────────────────────────────
FLEET
●  paradise-park                       now
●  magic-kingdom                        2m
●  flower-garden                   offline   (amber dot)
─────────────────────────────────────────
⚙  Settings…                           ⌘,
⏻  Quit
```

- Header: app name + this host's origin.
- **Open Clipboard** (⇧⌘V) is the primary item.
- **Fleet** section: one clean row per peer — colored dot (green = synced
  recently, amber = offline/stale), hostname, last-sync time. Clicking a peer
  opens its detail in Settings → Fleet.
- **Settings…** (⌘,) and **Quit**.
- Removed: the `origin:` text line, the `●✗○` glyph strings, the duplicate
  "Add peer…"/"Settings…" pair, and the top-level "Restart daemon" (now under
  Settings → Developer).

If `MenuBarExtra(.menu)` cannot render the peer rows with colored dots well, use
`.menuBarExtraStyle(.window)` with a small custom SwiftUI view. Decide during
implementation; prefer the simpler `.menu` if dots can be represented acceptably.

---

## Component 4 — Settings

Two tabs: **Fleet** and **General** (rename "Peers" → "Fleet").

### Fleet tab — cards, not a table

Each peer is a card:

- Colored status dot (green healthy / amber stale / gray offline).
- Hostname + OS badge (macOS / Linux).
- Address + plain-language state (`synced both ways`, `last seen 3h ago`).
- Sync direction summary (↑ push / ↓ recv with relative times).
- Click to expand for detail (errors in plain language, not raw status strings).

A primary **+ Add peer…** button (accent-colored) opens the Add Peer sheet.

### General tab — humanized, Developer tucked away

Grouped form:

- **Startup:** "Launch clipfan at login" toggle with a subtitle.
- **Clipboard:** History limit (maps to `max_history`), Global shortcut (⇧⌘V).
- **Status:** Daemon health (● running / down).
- **Developer** (disclosure, collapsed by default): config path, share-dir path,
  open daemon log, Restart daemon — i.e., everything currently shown inline.

---

## Component 5 — Add Peer (Tailnet-first adaptive sheet)

One sheet, no tabs. Content adapts to the environment.

**When `tailscale status` succeeds:**

- **From your tailnet** — a multi-select checklist of tailnet peers (online dot,
  hostname, OS badge, tailnet IP).
- A quiet "or add manually" divider.
- Manual fields: Host, User, SSH port (SSH key optional).
- One **tmux opt-in checkbox** (off) applying to all hosts being installed.
- Action: "Install on N host(s)".

**When Tailscale is not running:**

- The Tailnet section is hidden entirely.
- Manual fields only (Host, User, SSH port, optional SSH key) + the tmux checkbox.
- A subtle "Tailscale not detected" hint.

**Install progress** uses friendly step labels (Copied binary → Wrote service &
key → Starting daemon → Verifying sync), not the raw playbook step strings that
leak today. The tmux step reads "skipped" when the checkbox is off.

---

## Component 6 — tmux integration becomes opt-in (installer change)

**Today:** `dist/install.sh` lines 63–86 run unconditionally whenever
`tmux.conf.snippet` is present in the payload — they install the snippet to
`~/.config/clipfan/tmux.conf` and append `source-file ~/.config/clipfan/tmux.conf`
to the user's `~/.tmux.conf`, then reload any running server. Every host gets its
shell config edited, including hosts that don't use tmux.

**Change:**

- Add a `--with-tmux` flag to `install.sh` (default **off**). Gate the entire
  tmux block (lines 63–86) on it.
- The menubar installer (`Installer.swift`) passes `--with-tmux` only when the
  Add Peer tmux checkbox is checked.
- Re-running install without the flag never touches `~/.tmux.conf`.

**Compatibility note for review:** this changes behavior for anyone running
`install.sh` directly — they stop getting tmux unless they pass `--with-tmux`.
This is the intended behavior (forcing the edit was the bug). No interactive
prompt is added; the flag is the single switch. *(Jesse to confirm the bare CLI
needs no prompt.)*

---

## Testing

The daemon and API are unchanged, so tests focus on the app's pure logic plus
the installer flag. The existing test targets (`HistoryViewModelTests`,
`HistoryEntryTests`, `SigningTests`) stay green.

- **Metadata humanization** (new pure helpers): byte→human size, image dimension
  formatting, code/path detection for monospace. Unit-tested with table cases.
- **`HistoryViewModel`** keyboard-selection / ordering extensions: unit tests for
  filter + pinned-first + selection-clamping behavior.
- **Installer flag:** `install.sh` with and without `--with-tmux` — assert the
  tmux block runs only with the flag (test by pointing `HOME` at a temp dir and
  checking whether `~/.tmux.conf` was modified and the snippet installed).
- **Signing** parity test stays as the Swift==Go known-answer guard.
- UI/AppKit panel behavior (focus, dismiss, hotkey) is validated by Jesse
  running the built app — not unit-tested. Verification is manual and explicit.

Per house rules: no mocked-behavior tests, real logic only; test output must be
pristine.

## Build / packaging impact

- `build-app.sh` gains an icon/accent step (asset catalog → `Contents/Resources`,
  `Info.plist` icon keys) — see Component 2.
- Minimum macOS stays 13.0 unless an API forces a bump; if a panel/material API
  needs 14, raise the floor deliberately and note it. (Spike during impl.)
- No new third-party dependencies.

## Module layout (additive, follows existing one-file-per-responsibility style)

- `CommandPanel.swift` — the `NSPanel` subclass + summon/dismiss/focus.
- `CommandPanelView.swift` — the SwiftUI panel UI (search, list, preview, footer).
- `ClipMetadata.swift` — pure humanization helpers (size, dimensions, mono detect).
- Restyle in place: `StatusMenuView.swift` (menubar), `SettingsView.swift`
  (Fleet cards + General/Developer), `AddPeerSheet.swift` (adaptive sheet),
  `HistoryRow.swift` (comfortable row). Retire `HistoryWindow.swift` /
  `HistoryWindowController.swift` in favor of the panel (confirm before deleting).
- `Installer.swift` — pass `--with-tmux` conditionally.
- `dist/install.sh` — `--with-tmux` flag gating the tmux block.

## Open questions for review

1. Bare-CLI `install.sh`: flag-only (no prompt) acceptable? (Component 6)
2. `MenuBarExtra` `.menu` vs `.window` for the fleet dots — defer to impl, or
   decide now?
3. Asset-catalog-in-SPM vs `build-app.sh` `actool` path — spike, or pick now?
