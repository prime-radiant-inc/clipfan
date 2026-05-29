# clipfan 1.0 roadmap

The daemon proves the architecture: HTTP+HMAC mesh, image-as-path, tmux
load-buffer, xclip shim, the launchd Local-Network workaround. The path to 1.0
covers (a) origin working from any host, (b) a real macOS app, (c) frictionless
onboarding of new peers including a Tailscale picker, and (d) a clean
install/distribution story.

---

## Done

These are shipped and are the current behavior. See ARCHITECTURE.md and the
READMEs for how they work.

- **CLI origination.** `clipfan copy` reads stdin and POSTs to the local daemon
  as a fresh broadcast (text/image auto-detect, `--image` to force);
  `clipfan paste` reads the daemon's current state to stdout. `clipfan copy` can
  also emit an OSC 52 sequence to a tty, so a terminal connected from a
  non-clipfan host still gets the bytes. `dist/tmux.conf.snippet` wires tmux
  copy-mode to `clipfan copy`.
- **SwiftUI menubar app.** `Clipfan.app` (`apps/mac/Clipfan`) is a background-only
  (`LSUIElement`) menubar app. It shows this host's origin and a live peer list
  (`●` push ok / `✗` push failed / `(rx)` recent receive), and offers Add Peer…,
  Settings, Restart Daemon, and Open Config / Open Log.
- **Install sheet.** "Add Peer…" installs clipfan on a remote over SSH (scp the
  right-arch binary + shim + unit + a config sharing this Mac's `shared_key`, run
  install.sh), with a Tailscale picker (multi-select from `tailscale status
  --json`) and a manual `user@host:port` mode.
- **Multi-type pasteboard write.** When an image arrives at a Mac host, the
  bundled `clipfan-pasteboard-helper` writes a single `NSPasteboardItem` carrying
  both the PNG bytes (`public.png`) and the file path as text
  (`public.utf8-plain-text`) — Cmd-V pastes the image in GUI apps and the path in
  TUI apps.
- **OSC 52 fallback.** `clipfan copy --osc52 <tty>` emits the standard OSC 52
  clipboard sequence for terminals connected from hosts that don't run clipfan.
- **Login item.** The app registers itself (not the daemon) at login via
  `SMAppService.mainApp`, and shell-launches the daemon as a child so the daemon
  inherits the app's Local Network grant.
- **Image echo prevention.** A received image becomes a path-on-text on
  text-only backends; the daemon records the readback hash so that path is never
  re-broadcast, closing the image echo loop.
- **launchd Local-Network workaround.** The GUI app holds the Local Network
  grant and shell-launches the daemon, sidestepping the Sequoia gate that
  silently breaks launchd-spawned daemons on RFC1918 peers.

---

## Planned — clipboard history browser

A clipboard history browser in the menubar app: a two-pane window that lists
recently-copied items (text, links, images) with a large preview pane, full
keyboard navigation, search, type filters, pinning, and an origin-host badge on
every row. Picking an item re-copies it and syncs it to the fleet.

Scope:

- **Backend.** Each daemon records the clips that pass through its own clipboard
  into a `history.json` ring (newest-first, count-capped, default 200). Entries
  are content-hash identified (re-copy moves to top), reuse the existing
  `images/<sha>.png` files as thumbnails, and carry an origin-host tag. Image GC
  becomes history-aware so it never deletes a PNG a retained or pinned entry
  still references. New endpoints: `GET /v1/history`, `POST /v1/restore`,
  `POST /v1/history/pin`, `DELETE /v1/history` — all HMAC-signed.
- **Privacy.** Clips marked `org.nspasteboard.ConcealedType` (password managers)
  are never written to history.
- **Frontend.** A SwiftUI two-pane window opened from the menubar and a
  configurable global hotkey (default ⇧⌘V); type-to-filter, ↑/↓ + Enter to
  restore, filter chips (All / Text / Image / Link), pin/delete/clear.

Explicitly deferred (not in this version):

- Cross-fleet **merged** history (a union of all hosts). History is local per-host.
- Auto-paste into the frontmost app via Accessibility — we re-copy; the user
  presses ⌘V.
- Rich link cards with fetched favicons / social images.
- Paste-stack (sequential multi-item paste).
- OCR / text recognition on images.

---

## Planned — toward 1.0

### Push-based sync via SSE

Replace the 250ms poll loop with a push model: a `GET /v1/watch` SSE endpoint
peers connect to once and receive events on. The Mac keeps a fast change-count
poll (macOS has no pasteboard-change event); Linux switches to `wl-paste -w` /
`xsel --watch` triggers where available. Goal: sub-100ms peer-to-peer latency,
~0% idle CPU.

### Reliability + observability

- Richer `GET /v1/health`: uptime, build version, last successful push per peer,
  current state hash, image count.
- Structured logs to dated files, rotated and kept for 14 days.
- Integration-test harness: N daemons in one process running round-trip, image,
  relay, conflict, and peer-churn scenarios in CI.
- "Test origination" diagnostic in the menubar: send a canary and report each hop.

### Distribution

- Codesign + notarize `Clipfan.app`; DMG installer.
- Linux `.deb` / `.rpm` via nfpm; Homebrew tap.
- GitHub Releases pipeline: build all targets on tag, sign, notarize, upload.
- Sparkle auto-update on the Mac app.

### Onboarding + first-launch UX

- First-launch onboarding: generate or paste a shared key → add the first peer
  (Tailscale picker or manual) → done. Shared key is QR-shareable.

### Polish

- Designed app icon.
- Notification preferences (per-event opt-in).
- Optional anonymized telemetry; crash reporting.
- Docs site with screenshots.

---

## Stretch / post-1.0

- **Selection clipboard on Linux** (the X11 PRIMARY one).
- **Rich types** beyond text + PNG: RTF, HTML, file lists.
- **iOS / iPadOS companion** via a share extension.
- **Per-app privacy filter** — don't broadcast clipboard from 1Password,
  Bitwarden, Mail; allow/deny by owning application.
- **Browser extension** — push web selection straight into clipfan.
- **Public clipboard mode** — an opt-in peer that exposes its clipboard over
  HTTP for one-shot share with a non-clipfan host.

---

## What 1.0 looks like from the outside

- Download `Clipfan.dmg`, drag to Applications, launch.
- Onboarding wizard: generate or paste a shared key → add peers (Tailscale picker
  or `user@host:port`).
- Each added peer gets clipfan installed over SSH, automatically.
- Menubar icon → live peer list → clipboard history browser.
- `prefix-]` in any tmux on any host mirrors the Mac clipboard. Yank anywhere →
  propagates everywhere within ~200ms. Cmd-V into Claude Code / Codex / Preview —
  Just Works.
- Login-time autostart, signed binary, notarized DMG, optional auto-update.
