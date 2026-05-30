# Configurable settings — design

**Date:** 2026-05-30
**Scope:** Group B of the clipfan UI work. Make the history limit and the global
shortcut user-configurable. Builds on Group A (the General settings pane already
shows both as read-only rows).

## Goal

Two settings become editable in **Settings → General → Clipboard**:

1. **History limit** — a stepper that changes the daemon's `MaxHistory`.
2. **Global shortcut** — a recorder that rebinds the show/hide hotkey.

## Decisions (settled in brainstorming)

- History limit persists through a **new authenticated `POST /v1/config`** daemon
  endpoint — the daemon stays the sole writer of `config.json`.
- The history-limit control is a **Stepper + number** (range 50–5000, step 50).
- The global shortcut uses the **`sindresorhus/KeyboardShortcuts`** SwiftPM
  package (clipfan's first external dependency).

## Part 1 — Editable history limit

### Daemon: `POST /v1/config`

`capLimit()` in `store/history.go` already re-reads `config.json` on every history
write, so a changed cap takes effect on the next clip with no restart. The only
new work is a write path plus immediate re-cap.

- `internal/transport/server.go`: add a `SetConfigFunc` hook (mirroring the
  existing `SetHistory` pattern) of type `func(maxHistory int) error`, routed at
  `mux.HandleFunc("POST /v1/config", s.postConfig)`. `postConfig` authenticates
  with the existing `readSigned`, decodes `{"max_history": <int>}`, and calls the
  hook. On nil hook → 503; on hook error → 400/500 as appropriate; success → 200.
- `internal/daemon/daemon.go`: implement the hook. It clamps `max_history` to
  `[50, 5000]` (reject ≤ 0 with an error), does `config.Load` → set
  `MaxHistory` → `config.Save`, then calls `store.Recap()` to trim any now-excess
  unpinned entries immediately. Wire it with `d.sv.SetConfigFunc(d.setMaxHistory)`.
- `internal/store/history.go`: add exported `Recap() error` — load history, trim
  to `capLimit()` (pinned exempt, matching `AppendHistory`), write back.
- `peersHandler` adds `"max_history": <current cap>` to its response so the app
  can show and seed the stepper.

### App

- `Models.swift`: `PeersResponse` gains `max_history: Int?`.
- `DaemonClient.swift`: `@Published var maxHistory: Int = 200`; `refresh()` sets
  it from the response. Add `func setMaxHistory(_ n: Int) async` that POSTs the
  signed `{"max_history": n}` to `/v1/config`, then refreshes. The history fetch
  uses `maxHistory` instead of the hardcoded `?limit=200`.
- `SettingsView.swift` (GeneralTab → Clipboard): replace the read-only
  `LabeledContent("History limit", value: "200 items")` with a `Stepper`
  (`in: 50...5000, step: 50`) bound to a local `@State` seeded from
  `daemon.maxHistory`, committing via `Task { await daemon.setMaxHistory(n) }`.
  Show the current value beside it ("N items").

## Part 2 — Configurable global shortcut

### Dependency

`apps/mac/Clipfan/Package.swift`: add
`.package(url: "https://github.com/sindresorhus/KeyboardShortcuts", from: "2.0.0")`
and the `KeyboardShortcuts` product to the `Clipfan` target.

### Shortcut name + registration

- New `apps/mac/Clipfan/Sources/Clipfan/Shortcuts.swift`:
  ```swift
  import KeyboardShortcuts
  extension KeyboardShortcuts.Name {
      static let toggleClipboard = Self("toggleClipboard",
          initial: .init(.v, modifiers: [.command, .shift]))
  }
  ```
- `ClipfanApp.swift`: remove the `GlobalHotkey` property and its init. Register the
  handler once in `init()`:
  ```swift
  KeyboardShortcuts.onKeyDown(for: .toggleClipboard) {
      CommandPanelController.shared.toggle()
  }
  ```
- Delete `GlobalHotkey.swift` (now unused). The Carbon import goes with it.

### Recorder + dynamic labels

- `SettingsView.swift` (GeneralTab → Clipboard): replace the read-only
  `LabeledContent("Global shortcut", value: "⇧⌘V")` with
  `KeyboardShortcuts.Recorder("Global shortcut", name: .toggleClipboard)`.
- The hardcoded `⇧⌘V` strings elsewhere become dynamic, read from
  `KeyboardShortcuts.getShortcut(for: .toggleClipboard)`:
  - `StatusMenuView.swift` — the "Open Clipboard" row's trailing shortcut.
  - `CommandPanelView.swift` — none required (the footer hints ⏎ / ⌘1–9 / Esc,
    not the global toggle), so it is left unchanged.
  A small helper `toggleClipboardShortcutLabel() -> String` returns the shortcut's
  description, or "" when unset, so the menu row degrades cleanly.

## Testing

**Go (TDD):**
- `postConfig` with a valid `max_history` calls the hook with the decoded value;
  unsigned request → 401; nil hook → 503.
- `setMaxHistory` clamps out-of-range values to `[50, 5000]` and rejects ≤ 0.
- `peersHandler` includes `max_history`.
- `store.Recap()` trims unpinned entries beyond the cap and keeps pinned ones.

**Swift (TDD):**
- `PeersResponse` decodes `max_history` (present and absent).

**Manual (needs the GUI):** stepper changes the limit and the list re-caps; the
recorder rebinds the hotkey and the menu label updates.

## Files touched

- `internal/transport/server.go` — `SetConfigFunc`, `postConfig` route.
- `internal/daemon/daemon.go` — `setMaxHistory` hook + wiring + `max_history` in peers.
- `internal/store/history.go` — `Recap()`.
- New `internal/transport/config_test.go`, `internal/daemon/config_test.go`,
  `internal/store/recap_test.go`.
- `apps/mac/Clipfan/Package.swift` — KeyboardShortcuts dependency.
- New `apps/mac/Clipfan/Sources/Clipfan/Shortcuts.swift`.
- `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift` — register handler, drop GlobalHotkey.
- Delete `apps/mac/Clipfan/Sources/Clipfan/GlobalHotkey.swift`.
- `apps/mac/Clipfan/Sources/Clipfan/Models.swift` — `PeersResponse.max_history`.
- `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift` — `maxHistory`, `setMaxHistory`, fetch limit.
- `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift` — stepper + recorder.
- `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift` — dynamic shortcut label.
