# clipfan roadmap

clipfan 1.0 shipped 2026-06-09. The shipped
architecture: per-host daemons sync over authenticated SSH streams (peer HTTP
sync was removed in 1.0.0), a signed HMAC loopback API serves the app and CLI,
images travel as path-on-text, received clips land in every tmux paste buffer,
the xclip shim answers headless image paste, and the GUI app holds the Local
Network grant for the daemon. This doc tracks what shipped and what's next.

---

## Done

These are shipped and are the current behavior. See ARCHITECTURE.md and the
README for how they work.

- **CLI origination.** `clipfan copy` reads stdin and POSTs to the local daemon
  as a fresh broadcast (text/image auto-detect, `--image` to force);
  `clipfan paste` reads the daemon's current state to stdout. `clipfan copy` can
  also emit an OSC 52 sequence to a tty, so a terminal connected from a
  non-clipfan host still gets the bytes. `dist/tmux.conf.snippet` wires tmux
  copy-mode to `clipfan copy`.
- **SwiftUI menubar app.** `Clipfan.app` (`apps/mac/Clipfan`) is a background-only
  (`LSUIElement`) menubar app. The dropdown offers Open Clipboard, Install
  Update…, Settings…, and Quit, plus a Fleet section showing each peer with a
  colored health dot (green synced / orange offline / gray idle) and SSH
  status; Add peer…, Restart, and diagnostics live in Settings.
- **Install sheet.** "Add peer…" installs clipfan on a remote over SSH (stage the
  right-arch binary + shim + unit + a config sharing this Mac's `shared_key`, run
  install.sh), with a single-select Tailscale picker (from `tailscale status
  --json`) and a manual host + SSH user mode; direct-mesh provisioning and
  mesh-heal then fan the new edges across the fleet.
- **Multi-type pasteboard write.** When an image arrives at a Mac host, the
  bundled `clipfan-pasteboard-helper` writes a single `NSPasteboardItem` carrying
  both the PNG bytes (`public.png`) and the file path as text
  (`public.utf8-plain-text`) — Cmd-V pastes the image in GUI apps and the path in
  TUI apps.
- **OSC 52 fallback.** `clipfan copy --osc52 <tty>` emits the standard OSC 52
  clipboard sequence for terminals connected from hosts that don't run clipfan.
- **Login item.** The app registers itself (not the daemon) at login via
  `SMAppService.mainApp`, and falls back to shell-launching the daemon as a
  child — which inherits the app's Local Network grant — when the
  launchd-managed daemon isn't reachable.
- **Image echo prevention.** A received image becomes a path-on-text on
  text-only backends; the daemon records the readback hash so that path is never
  re-broadcast, closing the image echo loop.
- **launchd Local-Network workaround.** The GUI app holds the Local Network
  grant and shell-launches the daemon when the launchd copy can't answer,
  working around the Sequoia gate that silently breaks launchd-spawned daemons
  on RFC1918 peers.
- **Clipboard panel.** A keyboard-driven panel with search, type filters
  (All / Text / Image / Link), keyboard navigation, pinning, origin badges, and
  a ⇧⌘V hotkey; Enter restores an entry to the clipboard and fans it out to the
  fleet. Each daemon records its clips into `history.json` (newest-first,
  count-capped at 200, pinned exempt), content-hash identified, with
  history-aware image GC and concealed-clip exclusion. The model, persistence,
  and HTTP API are in [ARCHITECTURE.md](ARCHITECTURE.md#clipboard-history).
- **Distribution.** GitHub Releases pipeline on every `v*` tag: build all
  targets, Developer ID-sign, notarize + staple, publish `.zip`/`.dmg` and the
  Sparkle appcast ([RELEASING.md](RELEASING.md)).
- **Sparkle auto-update** on the Mac app, including the menu bar Install
  Update… affordance.
- **First-launch onboarding.** A Welcome wizard installs and starts the local
  daemon, then walks you to your first peer.
- **Designed app icon** — the card-fan artwork, shared with the menu bar icon
  and About screen.

---

## Planned — clipboard history follow-ups

Deferred sub-features of the clipboard panel:

- Cross-fleet **merged** history (a union of all hosts). History is local per-host.
- Auto-paste into the frontmost app via Accessibility — we re-copy; the user
  presses ⌘V.
- Rich link cards with fetched favicons / social images.
- Paste-stack (sequential multi-item paste).
- OCR / text recognition on images.

---

## Planned

### Event-driven local clipboard capture

Peer-to-peer delivery is already push-based over persistent SSH streams; what
remains polled is each host's own clipboard (250ms). Replace the poll where the
platform allows: the Mac keeps a fast change-count poll (macOS has no
pasteboard-change event); Linux switches to `wl-paste -w` / `xsel --watch`
triggers where available. Goal: sub-100ms end-to-end latency, ~0% idle CPU.

### Reliability + observability

- Richer `GET /v1/health`: uptime, build version, last successful send per peer,
  current state hash, image count.
- Structured logs to dated files, rotated and kept for 14 days.
- Integration-test harness: N daemons in one process running round-trip, image,
  relay, conflict, and peer-churn scenarios in CI.
- "Test origination" diagnostic in the menu bar: send a canary and report each hop.

### Distribution

- Linux `.deb` / `.rpm` via nfpm; Homebrew tap.

### Onboarding + polish

- QR-shareable `shared_key` for onboarding.
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

## What 1.0 shipped

- Download `Clipfan.dmg` (signed, notarized), drag to Applications, launch.
- The onboarding wizard installs the local daemon and adds your first peers
  (Tailscale picker or manual host + SSH user); each added peer gets clipfan
  installed over SSH automatically.
- Menu bar icon → live fleet health → clipboard panel (⇧⌘V).
- `prefix-]` in any tmux on any host mirrors the fleet clipboard; Cmd-V into
  Claude Code / Codex / Preview works on remotes via image-as-path.
- Login-time autostart, signed and notarized app, Sparkle auto-update.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-10 · commit `5ed989c` · verified against code (5 claims deferred to review)._
