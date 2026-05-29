# clipfan

One clipboard across a fleet of macOS + Linux hosts. Copy on any machine —
your Mac, a Linux box over SSH, inside tmux, inside Claude Code — and it lands
everywhere: the Mac pasteboard, every remote's OS clipboard, and every running
tmux paste buffer. Built so remote image paste into Claude Code and Codex CLI
"just works" without OSC 52 support, without Xvfb, and without per-SSH session
state.

Tracks PRI-1873.

## How it works

A daemon runs on every host. Peers discover each other (Tailscale `tailscale
status` by default; a static peer list as a fallback). When a host's clipboard
changes, its daemon broadcasts the content to every peer over HTTP. Each
receiving daemon:

- Writes text to the local OS clipboard (a no-op on a headless host with no
  display server — there's nothing to write to, and that's fine).
- For images, writes the bytes to `$XDG_STATE_HOME/clipfan/images/<sha>.png`
  and puts the absolute path on the **text** clipboard. (This is the trick that
  lets Codex and Claude Code attach images via bracketed paste, no X server
  required. On macOS the clipboard also carries the real image bytes, so Cmd-V
  into Preview/Slack pastes the picture.)
- Calls `tmux load-buffer` on every running tmux socket so `prefix-]` works.
- Records the clip in a local, searchable history (see the menubar app).

Conflict policy: last-write-wins by monotonic timestamp. There is no central
server. The Mac acts as a relay hub, so peers that can't see each other directly
(one on the LAN, one on the tailnet) still converge through it.

## Quickstart

```sh
# 1. Build cross-platform binaries
bash dist/build-all.sh

# 2. Install on each host (binary + service + tmux integration)
cd dist && ./install.sh

# 3. Edit ~/.config/clipfan/config.json — copy the shared key to every host,
#    set static_peers, or leave discovery="tailscale" to auto-fan to the tailnet.

# 4. Restart and verify
launchctl kickstart -k gui/$UID/com.primeradiant.clipfan      # macOS
systemctl --user restart clipfan                              # Linux
curl http://localhost:7853/v1/health                          # → ok
```

`install.sh` installs the daemon (and, on macOS, the pasteboard helper; on
Linux, the xclip/wl-paste shim), registers a launchd/systemd-user service, and
wires the tmux integration (below).

## Copying from a remote (tmux integration)

The daemon keeps the *OS clipboard* in sync on its own. Getting a **copy you
make inside tmux on a remote** back to your Mac is what the tmux integration
does. `install.sh` writes the snippet to `~/.config/clipfan/tmux.conf` and adds
`source-file ~/.config/clipfan/tmux.conf` to your `~/.tmux.conf` (idempotently —
re-running won't duplicate it). Reload with `tmux source-file ~/.tmux.conf` or
restart tmux.

The snippet captures every way a copy happens in tmux:

- **Copy-mode yanks** — `y`, `Enter`, and mouse-drag, bound in both the vi and
  emacs copy-mode tables, pipe the selection through `clipfan copy`.
- **Full-screen TUIs that grab the mouse** (e.g. Claude Code) do their own
  selection and push it straight into the tmux paste buffer, bypassing
  copy-mode. `after-set-buffer` and `after-load-buffer` hooks catch those and
  pipe them through `clipfan copy` too.

Each path also emits OSC 52 to the client tty as a fallback, so a terminal
connected from a non-clipfan host (Blink on a phone, kitty on a laptop without
clipfan) still grabs the text. Apple Terminal ignores OSC 52 — the daemon path
is what makes it work there.

Loop-safety is structural: when the daemon writes a *received* clip into the
tmux buffer, it records that content's hash, so the hook-fired re-copy is
deduped at the daemon rather than bouncing around the fleet. See
`docs/ARCHITECTURE.md` for the detail.

The snippet lives in clipfan (`dist/tmux.conf.snippet`) and is the single source
of truth; dotfiles should `source-file` the installed path rather than keep a
copy.

## Configuration

`$XDG_CONFIG_HOME/clipfan/config.json` (default `~/.config/clipfan/config.json`):

```json
{
  "listen": ":7853",
  "shared_key": "base64-32-bytes-shared-across-fleet",
  "discovery": "static",
  "static_peers": ["mac-host", "paradise-park", "flower-garden.local"],
  "port": 7853,
  "max_history": 200
}
```

The `shared_key` must be identical on every host. It's generated automatically
on first launch — copy it to the other hosts after the first daemon starts. It
is host-local state and never belongs in a dotfiles repo.

`discovery: "tailscale"` shells out to `tailscale status --json` and pushes to
every online peer. `discovery: "static"` uses the `static_peers` hostname list.
`max_history` caps the clipboard history (default 200).

## Menubar app (macOS)

`Clipfan.app` (`apps/mac/Clipfan`, a SwiftUI app) runs alongside the daemon and
shows an `NSStatusItem` menu:

- the daemon's origin (short hostname);
- one line per peer — `●` if the last push succeeded, `✗` if it failed, `(rx)`
  if we've received from them recently;
- **Add Peer…** — install on a remote over SSH: scp's the right-arch binary,
  shim, service unit, and a config sharing this Mac's `shared_key`, runs
  `install.sh`, and adds the new host to the local `static_peers`. The payload
  lives in `~/.local/share/clipfan/` (staged by `install.sh`);
- **Clipboard History…** — a two-pane window: a searchable list on the left, a
  preview on the right. Filter by type (All / Text / Image / Link), move with
  the arrow keys. Each row shows a thumbnail or text preview, the host the clip
  came from, a relative timestamp, and a pin indicator. Press Enter (or the
  Paste button) to make the selected clip the current clipboard and sync it to
  the fleet; pin or delete from the row's context menu. Opens from this menu or
  the **⇧⌘V** global hotkey. History is local to each host, and password-manager
  pastes (concealed clips) are never recorded;
- Open config, open daemon log, restart daemon.

The app polls `localhost:7853/v1/peers` over loopback, so it needs no Local
Network privacy grant. Build it from `apps/mac/Clipfan` (`./build-app.sh`).

## Known caveats

### macOS launchd vs Local Network privacy

Starting in macOS Sequoia, processes launched by launchd are gated by the
"Local Network" privacy permission for any connection to an RFC1918 IP
(10.x / 172.16-31.x / 192.168.x). A foreground app gets a permission prompt;
launchd-managed background daemons get a silent `EHOSTUNREACH` and no prompt.

Until a Local Network permission entry exists for clipfan, run the daemon from
your shell instead of via launchd:

```sh
launchctl unload ~/Library/LaunchAgents/com.primeradiant.clipfan.plist
nohup ~/.local/bin/clipfan > /tmp/clipfan.log 2>&1 &
disown
```

Tailscale (100.x) traffic is unaffected — only LAN/mDNS peers trigger the gate.
If your fleet is entirely Tailscale-routed, launchd works fine.

### Topology

There's no relay between non-Mac peers. If host A reaches the Mac and host B
reaches the Mac but A and B can't see each other, an update originating at A
reaches the Mac (and from there B, via the Mac's relay) but A→B direct does not.
Mac-originated updates reach everyone. For a hub-and-spoke fleet (the Mac is the
primary source) this is the right shape.

### Linux Ctrl-V image paste

On a headless Linux remote, the xclip/wl-paste shim symlinks (`install.sh`
creates them) answer Claude Code's `Ctrl-V` image paste. Claude Code shells out
to `xclip -t TARGETS -o` and `xclip -t image/png -o`; the shim serves those out
of clipfan's state directory, no display server required. Keep `~/.local/bin`
ahead of `/usr/bin` in PATH so the shim is found first.

### Codex CLI image paste

Codex's `Ctrl-V` calls `arboard`, which needs X11/Wayland; clipfan does not make
Ctrl-V work for Codex on a headless remote. But **bracketed paste** does:
`Cmd-V` from your Mac terminal (or `prefix-]` in tmux) sends the synced text —
the image's absolute path on the remote — into Codex's prompt, and Codex's
bracketed-paste handler detects the path and attaches the image. So you paste
with `Cmd-V` / `prefix-]` and Codex picks it up from disk. No Xvfb, no
x11-bridge, no sudo on the remote.

## Documentation

- `docs/ARCHITECTURE.md` — module layout, wire format, HTTP API, echo-loop
  prevention, the image-on-receive flow, clipboard history, and the tmux
  copy-capture path.
- `docs/ROADMAP.md` — what's shipped and what's planned.

## License

Proprietary — Prime Radiant, Inc.
