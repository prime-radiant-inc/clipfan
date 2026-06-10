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
  transport/           signed local HTTP APIs + SSH sync stream framing
    server.go          GET/POST /v1/current, GET /v1/peers, GET /v1/fleet, GET /v1/version,
                       GET /v1/health, history/config endpoints
    client.go          signed loopback client used by app, CLI, and SSH gateway
    envelope.go        encrypted clip payload carried by SSH stream state frames
    auth.go            shared-key request HMAC (SHA-256)
    crypto.go          AES-GCM clip body encryption
  tmux/                load-buffer-all: enumerate /tmp/tmux-$UID/* and call tmux -S sock load-buffer -
  daemon/              wires everything together: poll local clipboard, broadcast on change, write on receive
    daemon.go          poll / onReceive / SSH publish + peer-status tracking;
                       currentClip + isEcho for our-own-write echo suppression
    seen.go            bounded clip-ID set used for mesh dedup
apps/
  mac/Clipfan/         SwiftUI menubar app (Clipfan.app); supervises the daemon
```

## SSH sync payload

A clipboard event carried over the authenticated SSH sync stream is encoded as a
JSON envelope:

```json
{
  "id": "9f3a1c7b8e2d4a06b15c0f9e7d2a4b13",
  "origin": "magic-kingdom",
  "recipient": "paradise-park",
  "ts": "2026-05-28T12:34:56.789Z",
  "kind": "text",
  "body": "<base64 ciphertext>",
  "nonce": "<base64 AES-GCM nonce>",
  "concealed": false
}
```

`id` is a random 128-bit hex token (`transport.NewClipID`) minted once at the
clip's true origin and preserved verbatim through every relay — it is the identity
the mesh dedups on (see Recirculation prevention). `kind` is `"text"` or
`"image"`. `body` is the AES-GCM ciphertext encoded as base64; the AES-GCM key
is derived from `shared_key`, and `nonce` is the base64 nonce needed to decrypt
that body. `concealed` marks password-manager or transient pasteboard items so
receivers drop them without writing clipboard, state, history, or relay output.
`recipient` is the daemon identity the SSH stream payload is intended for. It is
inside the encrypted clip envelope so stream handlers can reject payloads that do
not match the local identity after short-name normalization (`.local` and FQDN
forms normalize to the same short host). Suffix aliases are not accepted at this
security boundary. A clip payload captured for one peer therefore fails closed
when replayed to another peer, even when both peers share the same `shared_key`.
The envelope does not carry a payload `sha256`; content hashes are computed from
raw payload bytes inside the daemon where needed for echo suppression, history
identity, and image filenames.

The SSH sync stream wraps envelopes in newline-delimited JSON frames. A signed
hello frame authenticates the stream purpose and host IDs; state frames carry the
current encrypted clip envelope or an explicit null reason; ack/error frames
report per-frame status.

Every signed request carries `X-Clipfan-Ts`, `X-Clipfan-Nonce`, and
`X-Clipfan-Sig`. The signature is
`hex(hmac-sha256(shared_key, method + "\n" + request_uri + "\n" + timestamp +
"\n" + nonce + "\n" + body))`, where `request_uri` includes the query string.
The server rejects missing or bad signatures, stale timestamps, and replayed
request nonces. There is no legacy body-only HMAC compatibility path, so
mixed-version fleets fail closed.

## Local HTTP API

The daemon listens on `:7853` by default and serves these endpoints:

| Method & path        | Auth          | Purpose |
|----------------------|---------------|---------|
| `GET /v1/current`    | signed request, loopback only | Return the latest visible current clipboard payload for the local SSH gateway. |
| `POST /v1/current`   | signed request, loopback only | Apply a local current clipboard payload from `clipfan copy` or the SSH gateway. Returns `204 No Content`. |
| `GET /v1/peers`      | signed request, loopback only | Return `{ "origin": "<this host>", "peers": [PeerState, ...] }` for the menubar app. |
| `GET /v1/fleet`      | signed request, loopback only | Return the app's aggregated SSH fleet view. |
| `GET /v1/version`    | signed request | Return `{ "version": "<daemon version>" }` for signed diagnostics and local callers. |
| `GET /v1/health`     | none          | Liveness check. Returns `200` with body `ok`. |
| `GET /v1/history`    | signed request, loopback only | Return `{ "entries": [HistoryEntry, ...] }`; `?limit=<n>` caps the count. |
| `POST /v1/restore`   | signed request, loopback only | Re-copy a history entry as the current clipboard and publish it through SSH sync. |
| `POST /v1/history/pin` | signed request, loopback only | Pin or unpin a history entry. |
| `DELETE /v1/history` | signed request, loopback only | Delete one entry, or all unpinned entries. |
| `POST /v1/config`    | signed request, loopback only | Update local daemon configuration currently exposed through the app, such as `max_history`. |
| `GET/PUT/DELETE/PATCH /v1/config/ssh/...` | signed request, loopback only | Read and update SSH peer config from the app. |

`PeerState` carries hostname, port, SSH runtime state, endpoint diagnostics, and
last receive timestamp. The menubar app polls the local endpoints over loopback
(loopback is exempt from the Local Network privacy gate, so the app needs no
special permission to read them).

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

`static_peers` is the explicit Clipfan fleet allowlist. `static.Discoverer`
reads it directly as the hostname list. `tailscale.Discoverer` shells out to
`tailscale status --json`, but filters online tailnet peers through the same
short-name allowlist before SSH publish. An empty `static_peers` list in
Tailscale mode returns only the local host and produces no non-self sync. The
active discoverer is chosen by the config's `discovery` field; the default is
`tailscale`.

## Recirculation prevention

Every clip carries a random clip-`id` minted once at its origin (a genuine local
copy seen by the poll loop, a `clipfan copy` injection, or a history restore) and
preserved through every relay. Two layers keep a clip from being acted on twice or
looping the mesh, with a third content-based guard beneath them:

- **Clip-ID dedup (mesh identity).** `daemon/seen.go` is a bounded set of
  recently-seen clip-IDs. On receive, a clip whose `id` is already in the set is
  dropped before it is applied or relayed; a clip with no `id` is dropped outright
  (the fleet runs a single version — there is no ID-less fallback). The `lastTS`
  check additionally ignores envelopes older than the last applied one
  (last-write-wins).

- **Current-clip echo suppression (content identity).** A clip-ID names a logical
  clip, but the OS clipboard stores no ID — so a clip read back off the clipboard,
  possibly *re-represented*, has no ID to match. The daemon therefore records the
  `currentClip` (id, kind, content hash, image-store path) every time it writes
  the clipboard **or** broadcasts a local copy. `isEcho` then recognises a
  subsequent clipboard read, or an inbound clip, whose content matches the current
  clip — including an image that comes back as its store-path text — and
  suppresses it. This catches the re-representations and re-originations that
  clip-ID dedup structurally cannot see: a path has a fresh hash, and the tmux
  hook re-submits content under a fresh ID.

- **Image-path guard.** A clip whose bytes are one of our own content-addressed
  image-store paths (`store.IsImageStorePath`) is never broadcast as text and
  never written over a real image, on either the poll or the receive path.

The Mac is the hub: a received clip is published over SSH to every configured
peer except its origin, so peers that can't see each other directly still
converge through the Mac. Clip-ID dedup, not origin filtering, is what keeps
publish loops from applying the same logical clip twice.

## Image flow on receive (the load-bearing trick)

When an image arrives at a host:

1. Clip-ID dedup against the seen set — skip if already applied.
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
copies through `clipfan copy`, which applies them through the signed loopback
current endpoint on the local daemon (and emits OSC 52 to the client tty as a
fallback).

The snippet covers copy-mode yanks only: `y`, `Enter`, and `MouseDragEnd1Pane`,
bound in *both* the `copy-mode-vi` and `copy-mode` (emacs) tables, because the
active table depends on the resolved `mode-keys` and a default binding in the
other table would otherwise copy only to the tmux buffer.

The snippet intentionally does not install global `after-set-buffer` or
`after-load-buffer` hooks. The daemon itself writes every received clip into the
tmux buffer (via `tmux.LoadBufferAll`, which uses `load-buffer`) so `prefix-]`
mirrors the OS clipboard. Treating every tmux buffer write as a new local copy
can re-submit daemon writebacks, stale buffers from another tmux socket, or
temporary test data as fresh fleet updates.

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
(a clip copied locally, and a clip received from SSH sync).

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

The daemon does not record or sync concealed clips. macOS password managers mark
pasteboard items with concealed or transient pasteboard types; the macOS
clipboard backend detects those types, SSH publish skips the clip, and receivers
drop any sync payload marked `concealed` before writing peer state, history, the
clipboard, tmux, or relay output.

### History API

These endpoints are signed with the shared key and are accepted only from
loopback:

- `GET /v1/history?limit=<n>` → `{ "entries": [HistoryEntry, ...] }`.
  Pinned floated to the top, then newest first. `limit` caps the count; without
  it, the full retained history is returned (itself bounded by the retention cap).
- `POST /v1/restore` `{ "id": "<sha>" }` → loads that entry, makes it the current
  clipboard (text → pbcopy; image → the pasteboard helper), publishes it over
  SSH so the fleet converges, and moves the entry to the top of history.
- `POST /v1/history/pin` `{ "id": "<sha>", "pinned": <bool> }`.
- `DELETE /v1/history` `{ "id": "<sha>" }` (one) or `{ "all_unpinned": true }`
  (clear unpinned).

## XDG paths

| Purpose | Path |
|--|--|
| Config  | `${XDG_CONFIG_HOME:-$HOME/.config}/clipfan/config.json` |
| State (state.json, current.txt, images/, history.json) | `${XDG_STATE_HOME:-$HOME/.local/state}/clipfan/` |
| Logs (launchd/systemd capture stderr; rotated by them) | n/a |

The config directory, state directory, image directory, and clipboard-bearing
files are private to the current user (`0700` directories and `0600` files).
Config, state, history, current text, and image writes validate and repair
private paths, and avoid following unsafe symlinked temporary or final paths.

## Auth model

Single shared key per fleet, in `config.json`. The daemon derives the envelope
encryption key from it and also uses it for canonical request HMAC signatures
and SSH stream hello signatures. Local admin endpoints (`/v1/current`,
`/v1/peers`, `/v1/fleet`, history, restore, pin/delete, and config) require
valid signatures and loopback source addresses. `GET /v1/health` remains
unauthenticated for liveness checks.

## Non-goals

- Multi-user / multi-tenant. One user per host.
- Selection-clipboard (Linux primary selection). Only the regular clipboard.
- Rich types beyond text + PNG (no RTF, no PDF, no file lists).
- Conflict resolution beyond last-write-wins.
