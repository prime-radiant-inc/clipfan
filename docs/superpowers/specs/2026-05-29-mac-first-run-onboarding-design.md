# Mac first-run onboarding — design

## Problem

A new user (Drew) installed `Clipfan.app`, and:

1. The daemon was **not running**.
2. He **never saw a permission prompt**.
3. The app **did not appear** in System Settings → Privacy & Security → Accessibility.

### Root causes

These three symptoms have two distinct causes:

- **Daemon not running (real bug).** `Clipfan.app` ships only the Swift
  executable, `Info.plist`, and icon (`build-app.sh`). It contains no daemon
  payload and never installs one. `DaemonClient.ensureDaemonRunning()` checks for
  `~/.local/bin/clipfan` and **silently returns** if it is absent. The daemon is
  only ever installed by running `dist/install.sh` from a terminal — a step a
  GUI user never performs. The failure is silent: nothing tells the user setup
  is incomplete.

- **No permission prompt / not in Accessibility list (stale docs).** The app
  intentionally uses **no** Accessibility-gated API. Auto-paste via synthetic
  ⌘V (the thing that would need Accessibility) was deliberately deferred
  (`ROADMAP.md`, `docs/superpowers/plans/2026-05-29-mac-app-ux-redesign.md`).
  Paste = the daemon re-copies the entry to the clipboard and the user presses
  ⌘V themselves (Raycast-style). The global hotkey uses Carbon
  `RegisterEventHotKey`, which needs no Accessibility grant. Yet `README.md:77`
  tells users to "grant Accessibility / Input Monitoring when prompted." Drew
  followed the README, waited for a prompt that never fires, and searched a list
  the app will never appear in.

  The **only** macOS permission clipfan actually needs is **Local Network**, and
  only once the user adds LAN peers to sync with. A solo first-run user
  correctly sees no prompts.

## Goal

A user double-clicks `Clipfan.app` and — with no terminal and no manual steps —
ends up with a running daemon and a clear understanding of how to use it.
Replace the silent failure with a visible, guided first run. Fix the stale
README. Add an actionable Local Network nudge for multi-Mac users.

## Non-goals

- Implementing auto-paste / synthetic ⌘V (still deferred; out of scope).
- Code signing / notarization (roadmap Phase 8). The app remains ad-hoc signed;
  we mitigate the download-quarantine case but do not solve distribution signing.
- Changing the daemon's launchd-vs-child-process Local Network workaround.

## Approach

Decisions taken during brainstorming:

- **Daemon source:** bundle the payload inside the `.app` (network-free,
  self-contained).
- **Install mechanism:** shell out to the bundled `install.sh` (reuse tested
  logic — PATH-baking, pasteboard-helper install, share-dir staging that
  "Add Peer" depends on — with zero divergence).
- **First-run UX:** a dedicated Welcome window with live progress.
- **Permissions:** docs fix + an actionable Local Network nudge.

## Components

### 1. Bundle the dist payload into the app — `build-app.sh`

`build-app.sh` copies the **full** `dist/` payload into
`Clipfan.app/Contents/Resources/dist/`: all cross-arch `clipfan-*` binaries,
both `clipfan-pasteboard-helper-darwin-*`, the Linux shims, `install.sh`,
`com.primeradiant.clipfan.plist`, `clipfan.service`, and `tmux.conf.snippet`.

This is exactly the file set `install.sh` stages into `~/.local/share/clipfan`
and that `Installer.swift` later reads from for "Add Peer." Bundling all arches
(~54 MB) is what keeps remote install working after a GUI-only first run.

The build **fails loudly** if `dist/` is missing required binaries (so we never
ship an app that can't bootstrap).

### 2. First-run detection + bootstrap orchestrator — new `Bootstrap.swift`

On launch, classify state by signal and act:

- **No `~/.local/bin/clipfan`** → first run → open the Welcome window and run the
  bundled `install.sh` for the host arch.
- **Binary present, daemon not answering** → existing kickstart / child
  shell-launch path (`DaemonClient`), unchanged.
- **Daemon answering** → normal launch, no window.

Bootstrap responsibilities:

- Resolve the bundled payload at `Bundle.main.resourceURL/dist`.
- **Strip the `com.apple.quarantine` xattr** from the bundled payload before
  running `install.sh` (defends against the app arriving via download / AirDrop,
  which would otherwise make launchd refuse the binaries).
- Run `install.sh` via `/bin/bash` as a subprocess, capturing combined
  stdout/stderr to a log file (`~/Library/Logs/clipfan-install.log`).
- Report `installing` progress lines, then `success` or `failed(error, logPath)`.
- No `sudo`: everything `install.sh` touches is user-owned (`~/.local/bin`,
  `~/Library/LaunchAgents`, `~/.config`, `~/.local/share`).

The orchestrator exposes an observable state enum the Welcome window renders:

```
enum SetupState { case idle, installing(progress: [String]), success, failed(message: String, logPath: String) }
```

`install.sh` is run with an explicit tmux flag (it already defaults to auto;
first-run passes `--no-tmux` unless tmux is detected — matches current
auto behavior but explicit, mirroring `Installer.tmuxFlag`).

### 3. Welcome window — new `WelcomeView.swift` + a `Window` scene

A small state machine driven by `SetupState`:

- **welcome / installing:** brief intro, then "Setting up the background
  service…" with the live progress lines from `install.sh`.
- **success:** tells the user the two things they need —
  - press **⇧⌘V** to open clipboard history;
  - paste = clipfan re-copies the item, you press **⌘V** (sets correct
    expectations, directly countering the old README confusion).
  - a "Done" button that closes the window.
- **failed:** the error message, a **Retry** button (re-runs bootstrap), and a
  **View log** button (opens the install log).

The window is opened programmatically on first run and is **reopenable** from
Settings → General via a "Re-run setup / Reinstall daemon" button — the recovery
path for users already in a broken state (Drew today, without a fresh download).

### 4. Local Network nudge — Fleet view / Settings

No public API reports Local Network permission state, so this is heuristic: when
a peer is **configured but has never been reachable** (red health, empty
`last_recv_ts`), show a one-time explainer card in the Fleet tab:

> Sync needs macOS **Local Network** permission. Open System Settings → Privacy &
> Security → Local Network and enable Clipfan.

with a button that deep-links to the Local Network settings pane
(`x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork`).
Solo users with no peers never see it.

### 5. Docs fix — `README.md`

Replace the "grant Accessibility / Input Monitoring when prompted" instruction
(README.md:77-79) with the real model:

- The app self-installs the background service on first launch — no terminal step.
- Paste = clipfan re-copies the item; you press ⌘V.
- The only OS permission is **Local Network**, and only once you add peers; the
  app will point you to it if sync is blocked.

Also update the "Build and run the menubar app" section to reflect that
double-clicking the app is now sufficient.

## Data flow

```
launch
  └─ Bootstrap.classify()
       ├─ daemon answering ............ normal launch (no window)
       ├─ binary present, not running . DaemonClient kickstart/child-launch
       └─ no binary (FIRST RUN) ....... open Welcome window
                                          └─ Bootstrap.run()
                                               ├─ strip quarantine on Resources/dist
                                               ├─ bash install.sh (host arch)  ──► progress lines
                                               ├─ install.sh: copy bin, write PATH-baked plist,
                                               │   install helper, stage share dir, launchctl load,
                                               │   generate config + SharedKey
                                               └─ poll /v1/health ──► success | failed
```

## Error handling

- **Missing bundled payload** (developer build error) → Welcome window shows a
  `failed` state naming the missing path; `build-app.sh` should have caught this
  at build time.
- **install.sh non-zero exit** → capture combined output to the log; show
  `failed(message, logPath)` with Retry + View log.
- **Daemon installed but never becomes healthy** → after a bounded poll on
  `/v1/health`, show `failed` with a hint to view the daemon log.
- **Quarantine strip fails** → log and proceed (best-effort; only relevant for
  downloaded apps).

## Testing

- **Unit (SwiftPM `ClipfanTests`):**
  - `Bootstrap.classify()` returns the right state for each of:
    binary-absent, binary-present, daemon-answering — using injected
    filesystem/health probes (no real install).
  - `SetupState` transitions render the expected Welcome view branch.
  - The Local Network nudge predicate (peer configured + never received) fires
    only for the unreachable-peer case.
- **install.sh:** existing `dist/test-tmux-gating.sh` style coverage continues to
  apply; add a check that a clean local install populates `~/.local/share/clipfan`
  with all expected payloads (the Add-Peer precondition).
- **Manual end-to-end (documented in the plan):** on a clean account with no
  `~/.local/bin/clipfan`, launching the built app opens the Welcome window,
  installs, and reaches `success`; menubar then shows "this Mac · <host>".

## Risks

- **Bundle size** grows ~54 MB (all-arch binaries). Acceptable for internal
  distribution; a future optimization could strip non-host arches but that breaks
  Add-Peer, so it is out of scope.
- **Ad-hoc signing + quarantine:** a downloaded app's nested binaries inherit
  quarantine. `install -m 0755` (used by install.sh) writes fresh files without
  the source xattrs, which strips quarantine on the installed copy; we also strip
  quarantine on the bundled payload before running. Proper notarization remains a
  Phase 8 concern.
- **Local Network nudge is heuristic** (no permission API). It can theoretically
  show for a genuinely-offline peer; the copy is worded as a possibility, not a
  certainty.
