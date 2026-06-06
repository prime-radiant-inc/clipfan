# clipfan

> One clipboard across a fleet of macOS + Linux hosts so remote image paste into Claude Code/Codex "just works" without OSC 52 or Xvfb.

**Family:** dev-tools · **Type:** service · **Lifecycle:** production · **Owner:** obra

## What it does
A daemon runs on every host and peers discover each other (Tailscale status by default, static peer list fallback). When a host's clipboard changes its daemon broadcasts the content to every peer over HTTP; receivers write text to the local OS clipboard, write image bytes to disk and put the path on the text clipboard, and call `tmux load-buffer` on every running tmux socket. The Mac also ships a menubar app that provides clipboard history and a one-click installer for adding hosts. Conflict policy is last-write-wins by monotonic timestamp with no central server.

## How it fits
- Depends on: — (no internal prime-radiant-inc code/service dependencies; Go module has no requires, Swift app uses external KeyboardShortcuts and Sparkle)
- Used by: —
- External: Tailscale (peer discovery via `tailscale status`), tmux, Sparkle (updates), targets Claude Code and Codex CLI image paste

## Runtime & data
- Runs: per-host Go daemon (macOS + Linux) plus a macOS SwiftUI menubar app
- Data in: local OS clipboard changes; HTTP broadcasts from peer daemons
- Data out: local OS clipboard, tmux paste buffers, image files under `$XDG_STATE_HOME/clipfan/images`, searchable local history

<!-- Maintained by the maintaining-project-map skill. Do not hand-edit; regenerated. -->
