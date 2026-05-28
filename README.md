# clipfan

Clipboard sync daemon for a fleet of macOS + Linux hosts. Keeps your Mac
pasteboard in sync with each remote's OS clipboard *and* every running tmux
paste buffer, so `prefix-]` on any host mirrors what's on your Mac. Designed
to make remote image paste into Claude Code and Codex CLI "just work" without
OSC 52 support, without Xvfb, and without per-SSH session state.

Tracks PRI-1873.

## How it works

A daemon runs on every host. Peers discover each other (Tailscale `tailscale
status` by default; static peer list as a fallback). When the local clipboard
changes, the daemon broadcasts to every peer over HTTP. Each receiving daemon:

- Writes text content to the local OS clipboard.
- For images, writes the bytes to `$XDG_STATE_HOME/clipfan/images/<sha>.png`
  and places the absolute path on the **text** clipboard. (This is the trick
  that lets Codex and Claude Code attach images via bracketed paste, no X
  server required.)
- Calls `tmux load-buffer` on every running tmux socket so `prefix-]` works.

Conflict policy: last-write-wins by monotonic timestamp. There is no central
server.

## Status

Active development. See `docs/PLAN.md` for the phased rollout.
