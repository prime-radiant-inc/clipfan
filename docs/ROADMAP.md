# clipfan 1.0 roadmap

The 0.x daemon proves the architecture: HTTP+HMAC mesh, image-as-path,
tmux load-buffer, xclip shim, launchd workaround. To call it 1.0 we
need (a) origin to actually work from any host, (b) a real macOS app
instead of the systray hack, (c) frictionless onboarding of new peers
including a Tailscale picker, and (d) a clean install/distribution
story without the shell-launched workaround.

Estimated calendar: ~3 weeks of focused work for Phases 1–9. Phase 10
is polish on either side of release.

---

## Phase 1 — Fix origination across the fleet  (P0, ~1 day)

Today every clipboard change has to first land on an OS pasteboard
before clipfan notices it. That breaks on headless Linux and on tmux
configs that emit OSC 52 instead of shelling to `pbcopy`. Bypass it.

- `clipfan copy` subcommand — reads stdin, POSTs to the local daemon
  as a fresh broadcast. Detects text vs image (`--image` for force).
- `clipfan paste` subcommand — symmetric: reads from the local daemon
  and writes to stdout. Useful in scripts and in remote tmux copy-pipe.
- Fix `clipfan-shim -i`/`--in` mode to push into the daemon instead of
  the current `io.Copy(io.Discard, os.Stdin)`. Today it silently drops.
- Ship `dist/tmux.conf.snippet` wiring `copy-mode-vi y` /
  `MouseDragEnd1Pane` to `clipfan copy`. Install path adds it as
  `~/.tmux.conf.clipfan` and prints a `source-file` hint.
- **Acceptance:** yank in tmux on flower-garden → Mac pbpaste updates
  within a second; vice versa.

## Phase 2 — Real SwiftUI menubar app  (P0, ~1 week)

Replace `cmd/clipfan-menu` (Go + getlantern/systray) with a proper
SwiftUI macOS app bundle: `Clipfan.app`.

- New layout: `apps/mac/` as a SwiftPM package, separate from the Go
  module. Go daemon lives in the bundle at `Contents/MacOS/clipfand`.
- `LSUIElement = true` — background-only, no Dock icon.
- NSStatusItem hosting a SwiftUI `Menu`:
  - Origin row (this host's name)
  - Peer list — live status (`●` push ok, `✗` last push failed,
    `(rx)` recent receive), latency badge, last sync timestamp
  - Recent clipboard history (last 10) with click-to-restore
  - Quick actions: Add Peer…, Open Settings, Show Logs, Restart Daemon
- Settings window (Cmd-,) — tabbed:
  - **General** — shared key reveal/regen, polling rate, log level
  - **Peers** — table with add/remove/edit, online status, manual
    "send test ping"
  - **Tailscale** — picker UI for Phase 3
  - **Advanced** — XDG paths, version, "Check for updates"
- Daemon supervisor: app spawns `clipfand` if missing, restarts on
  crash, surfaces stderr to the Logs viewer.

## Phase 3 — Install wizard: user@host:port + Tailscale picker  (P0, ~3 days)

The "Add Peer…" sheet has two modes.

- **Type address** — fields: user, host, port (default 22), SSH key
  path (optional), label. Validates by running `ssh -o BatchMode=yes`
  to probe before install. Shows the parsed-back form so the user can
  catch typos.
- **Pick from Tailnet** — calls `tailscale status --json`, renders a
  table of online peers with hostname, tailnet name, OS, IP, owner.
  Multi-select. Filters out tagged-devices unless "show all" toggle.
  Adding multiple peers is one click.
- Install runs the existing scp + install.sh playbook, with a live
  progress sheet (`Probing arch… → Copying binary… → Running
  install.sh… → Verifying health`) and a cancellable per-step view.
- On success: adds peer to local config, restarts the daemon, shows a
  "ready to sync" toast.

## Phase 4 — Multi-type NSPasteboard write  (Mac remote GUI paste, ~1 day)

When an image arrives at a Mac host, write a single pasteboard item
with both `public.png` (the image bytes) AND `public.utf8-plain-text`
(the path string) types.

- Cmd-V in Preview, Keynote, Slack etc. pastes the image.
- Cmd-V in Claude Code / Codex / a tmux pane pastes the path string
  (TUI apps read text from the clipboard, never image types).
- Replaces the current "text-path only" compromise on receive.

## Phase 5 — Real launchd story (kill the shell-launched workaround, ~2 days)

The 0.x doc says "run clipfan from your shell because launchd gets
EHOSTUNREACH to LAN peers." That's not a 1.0 state. Two paths;
build whichever turns out cleaner:

- **App-bundle path** — `Clipfan.app`'s bundled `clipfand` triggers the
  Local Network privacy prompt on first launch (foreground apps get
  the dialog properly). Once granted, the launchd-spawned daemon
  works for LAN peers.
- **Login-item path** — register `clipfand` via SMAppService.loginItem
  under the app's bundle ID. Login Items inherit user-session
  network privileges.

Verify by revoking Local Network in System Settings, restarting,
re-granting, and confirming launchd reaches flower-garden again.

## Phase 6 — Push-based sync via SSE (drop polling, ~2 days)

The 250ms `pbpaste`/`pngpaste` polling loop is wasteful and adds
latency. Replace with a push model.

- Daemon exposes `GET /v1/watch` (SSE) — peers connect once, receive
  events as they happen.
- The Mac's NSPasteboard change-count poll stays (no OS event for
  pasteboard change on macOS), but we can drop to 50ms and only emit
  on change-count delta — virtually free.
- Linux daemon switches from `xclip -o` polling to `wl-paste -w`
  (Wayland) or `xsel --watch` style triggers when available.
- Sub-100ms peer-to-peer latency, ~0% idle CPU.

## Phase 7 — Reliability + observability  (~2 days)

- `GET /v1/health` returns daemon uptime, build version, last
  successful push per peer, current state hash, image count.
- Structured slog → `~/Library/Logs/clipfan/clipfan-YYYY-MM-DD.log`
  on Mac, `~/.local/state/clipfan/logs/` on Linux, rotated daily,
  kept for 14 days.
- Integration test harness: spin up N daemons in goroutines in one
  process and run scenarios (text round-trip, image, relay, origin
  conflict, peer churn). Runs in CI.
- "Test origination" diagnostic in the menubar: click a peer →
  daemon sends a canary text → returns within 2s with each hop and
  the resulting recv stamp from the target.

## Phase 8 — Distribution  (~3 days)

- Codesign + notarize `Clipfan.app` with the Prime Radiant Developer
  ID; DMG installer with the drag-to-Applications background.
- Linux packages: `nfpm` builds `.deb` + `.rpm` from the binaries.
- Homebrew tap: `brew install clipfan` on Mac, `brew install
  clipfan-cli` on Linux (CLI-only without the .app).
- GitHub Releases pipeline: on tag push, build all targets, sign,
  notarize, upload artifacts, update the Homebrew tap, draft release
  notes from commit messages.
- Sparkle auto-update on the Mac app (in-app "Check for updates"
  hits a feed.xml on a static site).

## Phase 9 — Onboarding + first-launch UX  (~1 day)

- First launch of `Clipfan.app`: full-window onboarding (not just the
  menubar). "Welcome → Generate a shared key OR paste one from another
  host → Add your first peer (Tailscale picker or manual) → Done."
- Skip on subsequent launches.
- The shared key is QR-code-shareable for easy copy to a phone /
  another Mac.

## Phase 10 — Polish  (rolling)

- Designed app icon (the current 22x22 is hand-pixeled placeholder).
- Notification preferences (per-event opt-in: install success/failure,
  daemon down, large clipboard payload received).
- Telemetry opt-in (anonymized: peer count, daily sync count, build
  version). Sentry for crash reports.
- README → mkdocs/docusaurus site on github.io with screenshots.

---

## Stretch / post-1.0

- **Selection clipboard on Linux** (the X11 PRIMARY one).
- **Rich types** beyond text + PNG: RTF, HTML, file lists.
- **iOS / iPadOS companion** via a share extension.
- **Per-app privacy filter** — "don't broadcast clipboard from 1Password,
  Bitwarden, Mail." Mac-side allow/deny by NSRunningApplication ownership.
- **Browser extension** — push web selection straight into clipfan.
- **Public clipboard mode** — opt-in peer that exposes its clipboard via
  HTTP for one-shot share with a non-clipfan host.

---

## What 1.0 looks like from the outside

- Download `Clipfan.dmg`, drag to Applications, launch.
- Onboarding wizard: "Generate or paste shared key" → "Add peers"
  → either Tailscale picker (one-click multi-select) or
  `user@host:port` form.
- Each added peer gets clipfan installed over SSH, automatically.
- Menubar icon → live peer list → recent clipboard history.
- prefix-] in any tmux on any host mirrors the Mac clipboard. Yank
  anywhere → propagates everywhere within ~200ms. Cmd-V into Claude
  Code / Codex / Preview / anything — Just Works.
- No `nohup ~/.local/bin/clipfan &` line in your shell rc. No "you
  have to run from a terminal." Login-time autostart, signed binary,
  notarized DMG, optional auto-update.
