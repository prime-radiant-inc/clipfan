# clipfan implementation plan

PRI-1873. Phased, each phase ends in a working binary.

## Phase 1 — text-only MVP (Mac ⇄ paradise-park)

Smallest end-to-end that proves the transport. No tmux yet, no images.

- `cmd/clipfan/main.go` — flags, config load, run daemon.
- `internal/config` — TOML loader, defaults, shared-key gen on first run.
- `internal/clipboard` — `Content` type, macOS `pbpaste` polling (250ms), Linux `xclip -o` polling.
- `internal/discovery` — `Discoverer` interface, `static.New(hosts []string)`, `tailscale.New()` (shells out).
- `internal/transport` — HTTP server with `POST /v1/clip` + `GET /v1/clip`; SSE on `/v1/watch`; HMAC auth.
- `internal/daemon` — wire it together: on local change, push to peers; on SSE receive, write to local clipboard.
- Build static darwin-arm64 + linux-amd64 binaries.

**Acceptance:** `pbcopy 'hello' && sleep 1` on Mac → `pbpaste` on paradise-park returns `hello`. Reverse direction: `pbcopy 'world'` on paradise-park → `pbpaste` on Mac returns `world`.

## Phase 2 — tmux load-buffer on receive

- `internal/tmux` — enumerate `/tmp/tmux-$UID/*` (or `$TMUX_TMPDIR` if set), call `tmux -S <sock> load-buffer -` with `Content.Bytes`.
- Hook into daemon's "received content" path.
- Tested via: `pbcopy 'tmuxtest'` on Mac → `tmux send-keys ']'` on a remote tmux pane → pane shows `tmuxtest`.

## Phase 3 — image support

- macOS read: `pngpaste` (already on PATH on Mac; we'll bundle it as a dep check).
- macOS write: AppleScript `set the clipboard to (read POSIX file "..." as «class PNGf»)`.
- Linux read/write: `xclip -t image/png -o` / `-i`.
- Linux without X: skip OS clipboard, just write file + path-on-text.
- `internal/store` — `images/<sha256>.png` write, gc oldest beyond N=50 files.
- Daemon: detect image type, transmit as base64 PNG, on receive write to disk, also set TEXT clipboard to abs path, also call `tmux load-buffer <path>`.

**Acceptance:** Cmd-Shift-Ctrl-4 screenshot on Mac → in remote tmux paste with `prefix-]` shows `/Users/jesse/.local/state/clipfan/images/<sha>.png`. Cat that file → valid PNG.

## Phase 4 — xclip/wl-paste shim for Linux

- `cmd/clipfan-shim/main.go` — handles the arg shapes Claude Code calls:
  - `xclip -selection clipboard -t TARGETS -o` → print `image/png\nimage/jpeg\n...` (advertise what we have).
  - `xclip -selection clipboard -t image/png -o` → output PNG bytes from local clipfan state.
  - Same shape for `wl-paste -l` and `wl-paste -t image/png`.
- Install symlinks `~/.local/bin/{xclip,wl-paste}` ahead of system paths.

**Acceptance:** In Claude Code on magic-kingdom, Ctrl-V pastes the image that's on the Mac.

## Phase 5 — launchd + systemd

- macOS: `dist/com.primeradiant.clipfan.plist` — RunAtLoad, KeepAlive, StandardOutPath/StandardErrorPath to `~/Library/Logs/clipfan.{out,err}.log`.
- Linux: `dist/clipfan.service` — user unit, Restart=always.
- `dist/install.sh` — detect OS, install binary to `~/.local/bin/clipfan`, install unit, enable.

## Phase 6 — deploy + live test

- Build static binaries for darwin-arm64 + linux-amd64 + linux-arm64.
- scp + install on paradise-park, flower-garden, magic-kingdom (when online).
- Test matrix:
  - text copy on Mac → tmux paste on each remote
  - text copy on remote → pbpaste on Mac
  - image copy on Mac → Cmd-V into Claude Code on macOS remote
  - image copy on Mac → Cmd-V into Codex on Linux remote (bracketed paste path)
  - image copy on Mac → Ctrl-V into Claude Code on Linux remote (via shim)
  - mosh disconnect from Mac-A, reconnect from another shell → sync still working
