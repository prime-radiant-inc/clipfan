# clipfan

> One clipboard across a fleet of macOS + Linux hosts so remote image paste into Claude Code/Codex "just works" without OSC 52 or Xvfb.

**Family:** dev-tools · **Type:** service · **Lifecycle:** production · **Owner:** obra

## What it does
A daemon runs on every host, polls the local OS clipboard, exposes a signed loopback HTTP API for the app and CLI, and syncs peer clipboard updates over authenticated SSH streams. Receivers write text to the local OS clipboard, write image bytes to disk and put the path on the text clipboard, and call `tmux load-buffer` on every running tmux socket. Each daemon relays received clips onward to its own peers, so hosts that can't see each other converge through one that sees both (typically the Mac). The Mac menubar app is the control surface: clipboard history panel, one-click SSH provisioning of new peers (Tailscale host picker or manual host+user), and in-place peer updates. Conflict policy is last-write-wins by monotonic timestamp with no central server.

## How it fits
- Depends on: — (no internal prime-radiant-inc code/service dependencies; Go module has no requires, Swift app uses external KeyboardShortcuts and Sparkle)
- Used by: —
- External: SSH (peer transport and provisioning), Tailscale (tailnet host picker when adding peers), tmux, Sparkle (updates), targets Claude Code and Codex CLI image paste

## Runtime & data
- Runs: per-host Go daemon (macOS + Linux) plus a macOS SwiftUI menubar app
- Data in: local OS clipboard changes; clip updates from peer daemons over SSH streams
- Data out: local OS clipboard, tmux paste buffers, image files under `$XDG_STATE_HOME/clipfan/images`, searchable local history

<!-- Maintained by the maintaining-project-map skill. Do not hand-edit; regenerated. -->
