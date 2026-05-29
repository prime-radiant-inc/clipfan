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
  clipfan/             daemon entrypoint (also dispatches `copy`/`paste` subcommands)
  clipfan-shim/        xclip / wl-paste replacement on Linux remotes
internal/
  cli/                 `clipfan copy` / `clipfan paste` subcommands (+ OSC 52 emit)
  clipboard/           per-platform OS clipboard read/write
    clipboard.go       Content { Kind=text|image, Bytes, Hash, TS } + Backend interface
    clipboard_darwin.go  pbpaste/pngpaste read; pbcopy + pasteboard helper write
    clipboard_linux.go   wraps xclip / wl-clipboard binaries; headless fallback
    selection.go         chooseBackend: wayland/xclip/headless from $DISPLAY, $WAYLAND_DISPLAY, PATH
  config/              JSON config under $XDG_CONFIG_HOME/clipfan/config.json
  discovery/           interface { Peers() []Peer }; impls: tailscale, static
  store/               XDG state dir
    store.go           images/<sha>.png write + history-aware image GC
    state.go           state.json + current.txt (the shim's view of the clipboard)
    history.go         history.json: append/load, pin/delete, retention GC
  transport/           HTTP server + client; HMAC-signed JSON envelopes
    server.go          POST /v1/clip, GET /v1/peers, GET /v1/health, history endpoints
    client.go          push (PushAs stamps a chosen origin for relay)
    envelope.go        the wire envelope
    auth.go            shared-key HMAC (SHA-256)
  tmux/                load-buffer-all: enumerate /tmp/tmux-$UID/* and call tmux -S sock load-buffer -
  daemon/              wires everything together: poll local clipboard, broadcast on change, write on receive
    daemon.go          poll / onReceive / fanout / relay + peer-status tracking
    seen.go            bounded content-hash set used for echo-loop prevention
apps/
  mac/Clipfan/         SwiftUI menubar app (Clipfan.app); supervises the daemon
```

## Wire format

A clipboard event is sent as a JSON envelope:

```json
{
  "origin": "magic-kingdom",
  "ts": "2026-05-28T12:34:56.789Z",
  "kind": "text",
  "sha256": "abc...",
  "body": "<base64>"
}
```

`kind` is `"text"` or `"image"`. `body` is the base64-encoded payload — the UTF-8
text for text, or the PNG bytes for an image. `sha256` is the hex digest of the
raw (pre-base64) payload.

Every request carries `X-Clipfan-Sig: hex(hmac-sha256(shared_key, request_body))`,
computed over the exact JSON request body. The receiving daemon recomputes the
HMAC and rejects any request whose signature doesn't match.

## HTTP API

The daemon listens on `:7853` by default and serves these endpoints:

| Method & path        | Auth          | Purpose |
|----------------------|---------------|---------|
| `POST /v1/clip`      | `X-Clipfan-Sig` | Accept a clipboard envelope from a peer (or from `clipfan copy`) and apply it locally. Returns `204 No Content`. |
| `GET /v1/peers`      | none (loopback) | Return `{ "origin": "<this host>", "peers": [PeerState, ...] }` for the menubar app. |
| `GET /v1/health`     | none          | Liveness check. Returns `200` with body `ok`. |
| `GET /v1/history`    | `X-Clipfan-Sig` | Return `{ "entries": [HistoryEntry, ...] }`; `?limit=<n>` caps the count. |
| `POST /v1/restore`   | `X-Clipfan-Sig` | Re-copy a history entry as the current clipboard and fan it out to the fleet. |
| `POST /v1/history/pin` | `X-Clipfan-Sig` | Pin or unpin a history entry. |
| `DELETE /v1/history` | `X-Clipfan-Sig` | Delete one entry, or all unpinned entries. |

`PeerState` carries the hostname, port, last push timestamp + outcome, last push
error, and last receive timestamp. The menubar app polls `GET /v1/peers` over
loopback (loopback is exempt from the Local Network privacy gate, so the app
needs no special permission to read it).

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

`tailscale.Discoverer` shells out to `tailscale status --json` and filters to
online peers in the configured tailnet. `static.Discoverer` reads a hostname
list from config. The active discoverer is chosen by the config's `discovery`
field; the default is `tailscale`.

## Echo-loop prevention

Both the local poll and the receive path funnel through a bounded set of
recently-seen content hashes (`daemon/seen.go`). The same hash is never acted on
twice:

- On a local clipboard change, the poll loop checks the seen set before
  broadcasting; if the hash is already present (e.g. because the daemon just
  wrote it after a receive) it skips.
- On receive, the daemon checks the seen set before applying, and ignores
  envelopes whose timestamp predates the last applied one (last-write-wins).
- After writing a received clip to the OS clipboard, the daemon reads it back
  and records the readback hash too. On text-only backends an incoming image
  becomes a path-on-text, whose hash differs from the image's; remembering the
  readback hash stops that path from being re-broadcast.

The Mac is the hub: a received clip is also relayed to every peer except its
origin, so peers that can't see each other directly (e.g. one on the LAN, one on
the tailnet) still converge through the Mac. The hash dedup — not origin
filtering — is what keeps relay from looping.

## Image flow on receive (the load-bearing trick)

When an image arrives at a host:

1. Hash-dedup against the seen set — skip if already applied.
2. Write the PNG bytes to `$XDG_STATE_HOME/clipfan/images/<sha256>.png` and
   record metadata in `state.json` (kind=image, the image path).
3. Set the OS clipboard:
   - macOS: shell out to the bundled `clipfan-pasteboard-helper`, which writes a
     single `NSPasteboardItem` carrying BOTH the PNG bytes (`public.png`) and the
     file path as text (`public.utf8-plain-text`). Cmd-V in Preview/Keynote/Slack
     pastes the image; Cmd-V in a TUI app pastes the path. Falls back to
     text-only (the path) if the helper isn't installed.
   - Linux with a display: text-only — write the file's absolute path to the
     clipboard (`xclip` has no clean multi-target write). On a headless host the
     backend is the no-op headless fallback (no `$DISPLAY`/`$WAYLAND_DISPLAY`), so
     this step does nothing — but the path still reaches tmux and the shim.
4. Set `current.txt` to the text representation (the file's absolute path for an
   image), which is what the shim serves.
5. Call `tmux load-buffer <path>` on every tmux socket.

After this, the remote user pastes via either Cmd-V (terminal → bracketed paste →
Codex/Claude Code sees a file path and attaches it) or `prefix-]` (tmux pastes
the path string). On Linux, Claude Code's `Ctrl-V` image paste is served by the
xclip/wl-paste shim, which answers `image/png` queries out of the `images/`
directory — no display server required.

## tmux copy capture

The daemon keeps the OS clipboard in sync on its own. Capturing a copy made
*inside tmux on a remote* and getting it onto the fleet is the job of the tmux
snippet (`dist/tmux.conf.snippet`), installed by `dist/install.sh` to
`~/.config/clipfan/tmux.conf` and sourced from `~/.tmux.conf`. It pipes tmux
copies through `clipfan copy`, which posts them to the local daemon (and emits
OSC 52 to the client tty as a fallback).

tmux exposes a copy through more than one path, so the snippet covers all of
them:

- **Copy-mode yanks** — `y`, `Enter`, and `MouseDragEnd1Pane`, bound in *both*
  the `copy-mode-vi` and `copy-mode` (emacs) tables, because the active table
  depends on the resolved `mode-keys` and a default binding in the other table
  would otherwise copy only to the tmux buffer.
- **Paste-buffer writes** — full-screen TUIs that capture the mouse (e.g. Claude
  Code) run their own selection and write it straight into the tmux paste buffer,
  bypassing copy-mode. The `after-set-buffer` and `after-load-buffer` hooks fire
  on those writes and pipe the buffer through `clipfan copy`.

### Why both buffer hooks, and why it doesn't loop

Different tools use different commands to set the buffer — Claude Code uses
`load-buffer`, others use `set-buffer` — so the snippet hooks both. tmux's own
docs describe `load-buffer` and `set-buffer` as the same operation differing only
in data source (a file/stdin vs an inline argument), so the *verb* is not a
reliable signal for "who wrote this."

That matters because the daemon itself writes every received clip into the tmux
buffer (via `tmux.LoadBufferAll`, which uses `load-buffer`) so `prefix-]` mirrors
the OS clipboard. Naively, the `after-load-buffer` hook would then fire on the
daemon's own writeback and re-broadcast it — an echo loop. The guard is content
identity, not the verb: right where the daemon loads a received clip into the
buffer, it records that payload's SHA-256 in the seen set (`daemon/seen.go`).
When the hook re-submits the same bytes through `clipfan copy`, the daemon
recognizes the hash and drops it. For an image the buffer holds the on-disk path
(whose hash differs from the image bytes), so that path hash is registered
explicitly; on a headless host the OS-clipboard readback is empty, so this
explicit registration is the only thing standing between the hook and a loop.
`daemon/hookloop_test.go` covers the image-path case.

## Persistence

State lives under the XDG state dir (`$XDG_STATE_HOME/clipfan/`, default
`~/.local/state/clipfan/`):

| File                | Contents |
|---------------------|----------|
| `state.json`        | metadata about the current clipboard (kind, timestamp, image path) |
| `current.txt`       | the text representation of the current clipboard — the literal text, or an image's absolute path |
| `images/<sha>.png`  | content-addressed PNGs; the same image is never written twice |

The state files exist so the `clipfan-shim` and the `clipfan paste` subcommand
can answer clipboard queries from anywhere on the system without IPC to the
daemon. Image storage is content-addressed by SHA-256 and garbage-collected: a
background sweep keeps the most recent images and removes the oldest beyond the
retention bound.

## Clipboard history

Each daemon records the clips that pass through its own clipboard into a
newest-first history. Because the Mac is the relay hub, its history naturally
sees nearly every clip on the fleet, each tagged with the host it originated on.
History is local per-host; no new sync protocol is introduced — the daemon
appends to history at the same two points it already persists the current clip
(a clip copied locally, and a clip pushed by a peer).

### Data model

A history entry:

```
HistoryEntry {
  id          // sha256 hex of the content (stable identity, dedup key)
  kind        // "text" | "image" | "link"
  preview     // short display string: first ~140 chars, or image filename
  text        // full text payload, inline, for text/link (empty for image)
  image_path  // absolute path to images/<sha>.png, for image (empty otherwise)
  size_bytes
  origin      // host the clip originated on
  ts
  pinned
}
```

Entries persist as a JSON list in `$XDG_STATE_HOME/clipfan/history.json`, newest
first. Short text is stored inline; images stay on disk and are referenced by
path, so `history.json` stays small while the UI renders thumbnails from the
existing `images/<sha>.png` files. A `link` is text whose content matches a URL
pattern; the daemon classifies it so the API is self-describing. `id` is the
content hash: re-copying identical content moves the existing entry to the top
(updating its timestamp) rather than creating a duplicate.

### Retention

History is capped by entry count (default 200, configurable). GC trims the
oldest unpinned entries beyond the cap; pinned entries are exempt. The image GC
is history-aware: it never deletes a PNG still referenced by a retained or pinned
history entry. The reference set is computed from `history.json` before trimming.

### Privacy — concealed clips

The daemon does not record concealed clips. macOS password managers mark
pasteboard items with `org.nspasteboard.ConcealedType`; the macOS clipboard
backend detects that type and skips the history append (the item may still sync
as the current clipboard, but it is never written to `history.json`). This keeps
secrets out of history.

### History API

These endpoints are HMAC-SHA256 signed with the shared key, exactly like
`POST /v1/clip`:

- `GET /v1/history?limit=<n>` → `{ "entries": [HistoryEntry, ...] }`.
  Pinned floated to the top, then newest first. `limit` caps the count; without
  it, the full retained history is returned (itself bounded by the retention cap).
- `POST /v1/restore` `{ "id": "<sha>" }` → loads that entry, makes it the current
  clipboard (text → pbcopy; image → the pasteboard helper), fans it out to peers
  so the fleet converges, and moves the entry to the top of history.
- `POST /v1/history/pin` `{ "id": "<sha>", "pinned": <bool> }`.
- `DELETE /v1/history` `{ "id": "<sha>" }` (one) or `{ "all_unpinned": true }`
  (clear unpinned).

## XDG paths

| Purpose | Path |
|--|--|
| Config  | `${XDG_CONFIG_HOME:-$HOME/.config}/clipfan/config.json` |
| State (state.json, current.txt, images/, history.json) | `${XDG_STATE_HOME:-$HOME/.local/state}/clipfan/` |
| Logs (launchd/systemd capture stderr; rotated by them) | n/a |

## Auth model

Single shared HMAC key per fleet, in `config.json`. The daemon refuses any
`POST /v1/clip` request without a valid `X-Clipfan-Sig` header. This is plenty
for a Tailscale-only or LAN deployment — the network layer already authenticates
the peer; HMAC is belt-and-suspenders against accidental misrouting. The
loopback-only `GET /v1/peers` and `GET /v1/health` endpoints are unauthenticated.

## Non-goals

- Multi-user / multi-tenant. One user per host.
- Selection-clipboard (Linux primary selection). Only the regular clipboard.
- Rich types beyond text + PNG (no RTF, no PDF, no file lists).
- Conflict resolution beyond last-write-wins.
