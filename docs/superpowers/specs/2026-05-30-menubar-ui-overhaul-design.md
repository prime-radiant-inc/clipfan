# Menubar app UI overhaul — design

**Date:** 2026-05-30
**Scope:** Group A of the clipfan UI work. Pure SwiftUI/AppKit presentation changes
in the Mac menubar app. No daemon, transport, or persistence changes.

This is the first of three sub-projects. Group B (editable history limit +
configurable global shortcut) and Group C (GitHub auto-build + Sparkle) are
specified separately.

## Goal

Make the menubar app's three surfaces — the clipboard window, the menu-bar
dropdown, and the Settings window — read as one coherent, native macOS app.
Five changes:

1. Clipboard window gets a real title bar instead of a transparent one.
2. The dropdown folds "this Mac" into a single fleet list.
3. Settings moves from top tabs to a left sidebar.
4. Settings → Fleet includes the local host as the first entry.
5. The old Status and Developer sections are redesigned into a Diagnostics tab.

## Non-goals

- No editable history limit or configurable shortcut yet (Group B). The General
  pane shows both as read-only values.
- No daemon-side change. The daemon's peer snapshot still excludes self; the app
  synthesizes the self entry in the view layer.

## Changes

### 1. Clipboard window title bar

File: `CommandPanel.swift`.

Today the panel is a `.titled` `NSPanel` with `titlebarAppearsTransparent`,
`fullSizeContentView`, hidden title, and hidden window buttons, so the search row
draws to the top edge under a faint separator.

Give it a real, opaque title bar:

- Remove `.fullSizeContentView` from the style mask.
- Set `titlebarAppearsTransparent = false` and `titleVisibility = .visible`.
- Title the window "Clipboard".
- Un-hide the close button; keep it wired so a click hides the panel (same as
  Esc and click-away).
- Disable the minimize and zoom buttons (`isEnabled = false`) — present but
  greyed, the standard treatment for a utility window.
- Keep every HUD behavior: `.nonactivatingPanel`, floating level, click-away
  dismissal via `windowDidResignKey`, centered positioning.

The rounded-corner content layer and `VisualEffectBackground` stay. The search
row, list, preview, and footer in `CommandPanelView.swift` are unchanged; they
simply render below the title bar now.

### 2. Menu-bar dropdown

File: `StatusMenuView.swift`.

Remove the standalone header that shows "this Mac · origin". Build one fleet
list whose first row is the local host:

- Name: `daemon.origin`.
- Tag: a small "you" pill next to the name.
- Subtitle: "this Mac · running" when `daemon.connected`, "this Mac · daemon not
  running" otherwise.
- Health dot: green when connected, grey when not.
- No ↑/↓ sync times (the local host does not push to itself).

Peers follow in the same row style, unchanged in content. The action rows (Open
Clipboard, Settings…, Quit) stay above the list.

### 3. Settings sidebar

File: `SettingsView.swift`.

Replace the top `TabView` with a `NavigationSplitView` (sidebar + detail). Three
sidebar items, each a `Label`:

- **Fleet** (`network`)
- **General** (`gearshape`)
- **Diagnostics** (`stethoscope`)

Selection is the existing `Tab` enum, extended with `.diagnostics`. The detail
column hosts the matching pane.

### 4. Fleet pane includes the local host

File: `SettingsView.swift`.

The Fleet pane renders a uniform list of cards. The first card is the local host,
synthesized from `daemon.origin` and `daemon.connected`:

- "you" pill next to the name.
- Subtitle "this Mac · running" / "this Mac · daemon not running".
- Health dot green/grey.
- No sync arrows.

Peers render below as today via `PeerCard`.

### 5. General and Diagnostics panes

File: `SettingsView.swift`.

**General** holds preferences only:

- **Startup** — Launch clipfan at login (existing toggle and its approval/error
  states).
- **Clipboard** — History limit and Global shortcut, both read-only in Group A
  (`200 items`, `⇧⌘V`).

**Diagnostics** (new) collects everything operational:

- A **daemon status banner**: health dot, "this Mac · `origin` · `version`",
  and a Restart button. Version comes from the app bundle's
  `CFBundleShortVersionString`.
- **Setup** — Re-run setup (existing button + explanatory caption).
- **Developer** — config path, share dir, Reveal config in Finder, Open daemon
  log. No longer a collapsed disclosure; it is the body of this tab.

The "down" state of the status banner and the Restart action reuse
`daemon.connected` and `daemon.restartDaemon()`.

## Shared component

Introduce one small reusable view, `FleetRow`, plus a `HealthDot`, so the
dropdown and the Fleet pane render identical rows. This removes the current
duplication between `StatusMenuView`'s peer rows and `SettingsView`'s `PeerCard`.
`FleetRow` takes a value type describing one host (name, subtitle, health,
optional sync times, isSelf) and renders the row; callers pass either a peer or
the synthesized self entry.

## Self-entry synthesis (testable seam)

A pure function builds the ordered row list:

```
func fleetRows(origin: String, connected: Bool, peers: [Peer]) -> [FleetRowModel]
```

It returns the self row first, then one row per peer. This is the one piece of
real logic in Group A and gets unit tests:

- Self row is always first.
- Self row carries no sync times and the `isSelf` flag.
- Self subtitle reflects `connected`.
- Peer rows preserve order and content.

## Error and empty states

- Daemon down: dropdown and Fleet self row show grey dot + "daemon not running";
  Diagnostics banner shows the down state with Restart still available.
- No peers: the fleet list still shows the self row; the existing "No peers yet —
  add one in Settings" guidance shows below it.
- The existing `LocalNetworkNudge` in the Fleet pane is unchanged.

## Testing

- Unit-test `fleetRows(...)` (ordering, self flags, subtitle, peer passthrough)
  in `ClipfanTests`.
- Keep `PanelSelectionTests`, `HistoryViewModelTests`, and the rest green.
- View chrome (title bar, sidebar, banner layout) is verified by building and
  running the app and confirming each surface by eye, since SwiftUI layout has no
  meaningful unit surface.

## Files touched

- `CommandPanel.swift` — title bar.
- `StatusMenuView.swift` — merged fleet list, adopt `FleetRow`.
- `SettingsView.swift` — sidebar, Fleet self card, General/Diagnostics split.
- New `FleetRow.swift` — shared row + health dot + `fleetRows` + `FleetRowModel`.
- `Tests/ClipfanTests/FleetRowTests.swift` — new.
