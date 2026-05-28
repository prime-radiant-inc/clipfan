# clipfan architecture

## Goals

- Mac clipboard ↔ every remote OS clipboard kept in sync (text + images).
- Every remote's tmux paste-buffer (`prefix-]`) mirrors the Mac clipboard.
- Image paste into Claude Code and Codex CLI on a remote works without OSC 52, Xvfb, or per-SSH session state.
- Survives mosh-from-multiple-Macs.
- Transport pluggable (Tailscale today, static peer list as a fallback, room for mDNS later).
- XDG-conformant on every platform, including macOS.

## Module layout

```
cmd/
  clipfan/             daemon entrypoint
  clipfan-shim/        xclip / wl-paste replacement on Linux remotes
internal/
  clipboard/           per-platform OS clipboard read/write (NSPasteboard / xclip / wl-clipboard)
    macos.go           uses pbpaste/pngpaste shellouts (no cgo, keeps cross-compile easy)
    linux.go           wraps xclip / wl-clipboard binaries
    types.go           Content { kind=text|image, bytes, hash, ts }
  store/               XDG state dir: ~/.local/state/clipfan/{current.txt,images/<sha>.png}
  config/              TOML config under $XDG_CONFIG_HOME/clipfan/config.toml
  discovery/           interface { Peers() []Peer }; impls: tailscale, static
  transport/           HTTP server + client; HMAC-signed JSON envelopes
    server.go          POST /v1/clip, GET /v1/clip, SSE /v1/watch
    client.go          push + subscribe
    auth.go            shared-key HMAC (SHA-256)
  tmux/                load-buffer-all: enumerate /tmp/tmux-$UID/* and call tmux -S sock load-buffer -
  daemon/              wires everything together: poll local clipboard, broadcast on change, write on receive
  log/                 thin slog wrapper, structured to stderr (launchd/systemd captures)
```

## Wire format

```json
{
  "id": "01J...",          // ULID, monotonic-ish
  "ts": "2026-05-28T12:34:56.789Z",
  "origin": "magic-kingdom",
  "kind": "text",          // "text" or "image"
  "mime": "text/plain; charset=utf-8",
  "size": 412,
  "sha256": "abc...",
  "body": "<base64>"       // for images, the PNG bytes
}
```

Signed with `X-Clipfan-Sig: hmac-sha256(shared_key, sha256(body))`. Receiving daemon ignores any envelope whose `sha256` matches the current local content (prevents echo loops).

## Discovery

```go
type Peer struct {
    Hostname string  // tailnet name or static-config name
    Port     int     // default 7853
    Self     bool    // true if this is us
}

type Discoverer interface {
    Peers(ctx context.Context) ([]Peer, error)
}
```

`tailscale.Discoverer` shells out to `tailscale status --json` and filters to online peers in the configured tailnet, optionally restricted to specific tags. `static.Discoverer` reads a hostname list from config.

## Image flow on receive (the load-bearing trick)

When an image arrives at a remote:

1. Hash-dedup against `store/current` — skip if same content.
2. Write bytes to `$XDG_STATE_HOME/clipfan/images/<sha256>.png`.
3. Set OS clipboard:
   - macOS: shell out to `osascript -e 'set the clipboard to ...'` for text path, or write PNG via pbpaste analog (we'll use a small AppleScript: `osascript -e 'set the clipboard to (read POSIX file "..." as «class PNGf»)'`).
   - Linux: `xclip -selection clipboard -t image/png -i < file` if X11 is available. **Skip silently** if no X server — the path-on-text fallback covers it.
4. Set the TEXT clipboard to the file's absolute path (string). This is what makes Codex and Claude Code's bracketed-paste image-attach work without arboard / xclip / Xvfb.
5. Call `tmux load-buffer <path>` on every tmux socket.

After this, the remote user pastes via either Cmd-V (passes through terminal → bracketed paste → Codex/Claude Code sees a file path and attaches it) or `prefix-]` (tmux pastes the path string).

## XDG paths

| Purpose | Path |
|--|--|
| Config  | `${XDG_CONFIG_HOME:-$HOME/.config}/clipfan/config.toml` |
| State (current.txt, images/) | `${XDG_STATE_HOME:-$HOME/.local/state}/clipfan/` |
| Logs (launchd/systemd capture stderr; rotated by them) | n/a |

## Auth model

Single shared HMAC key per fleet, in `config.toml`. The daemon refuses any HTTP request without a valid `X-Clipfan-Sig` header. This is plenty for a Tailscale-only or LAN deployment — the network layer already authenticates the peer; HMAC is belt-and-suspenders against accidental misrouting.

## Non-goals (initial)

- Multi-user / multi-tenant. One user per host.
- Selection-clipboard (Linux primary selection). Only the regular clipboard.
- Rich types beyond text + PNG (no RTF, no PDF, no file lists).
- Conflict resolution beyond last-write-wins.
