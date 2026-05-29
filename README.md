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

## Quickstart

```sh
# 1. Build cross-platform binaries
bash dist/build-all.sh

# 2. Install locally (and on each remote)
cd dist && ./install.sh

# 3. Edit ~/.config/clipfan/config.json — pick a shared key, set static peers
#    or leave discovery="tailscale" to auto-fan to every online tailnet peer.

# 4. Restart and verify
launchctl kickstart -k gui/$UID/com.primeradiant.clipfan      # macOS
systemctl --user restart clipfan                              # Linux
curl http://localhost:7853/v1/health                          # → ok
```

## Configuration

`$XDG_CONFIG_HOME/clipfan/config.json` (default `~/.config/clipfan/config.json`):

```json
{
  "listen": ":7853",
  "shared_key": "base64-32-bytes-shared-across-fleet",
  "discovery": "static",
  "static_peers": ["mac-host", "paradise-park", "flower-garden.local"],
  "port": 7853
}
```

The `shared_key` must be identical on every host. Generated automatically on
first launch — copy it to the other hosts after the first daemon starts.

`discovery: "tailscale"` shells out to `tailscale status --json` and pushes to
every online peer. `discovery: "static"` uses the `static_peers` hostname list.

## Known caveats

### macOS launchd vs Local Network privacy

Starting in macOS Sequoia, processes launched by launchd are gated by the
"Local Network" privacy permission for any connection to an RFC1918 IP
(10.x / 172.16-31.x / 192.168.x). The first time a foreground app tries to
talk to a LAN host, macOS prompts the user; launchd-managed background
daemons get a silent `EHOSTUNREACH` ("no route to host") and no prompt.

Until the Local Network permission entry exists for clipfan, run the daemon
from your shell instead of via launchd:

```sh
launchctl unload ~/Library/LaunchAgents/com.primeradiant.clipfan.plist
nohup ~/.local/bin/clipfan > /tmp/clipfan.log 2>&1 &
disown
```

Push that line into your shell startup if you want it persistent. Tailscale
(100.x) traffic is unaffected — only LAN/mDNS peers trigger the gate. If your
fleet is entirely Tailscale-routed, launchd works fine.

### Topology

There's no relay. If host A can reach Mac and host B can reach Mac but A and
B can't see each other (different networks/tailnets), updates that originate
at A won't reach B. Mac-originated updates reach both. For Jesse's hub-and-
spoke setup (Mac is the primary source) this is the right shape; a relay
hop would invite echo-loop bugs.

### Menubar app (macOS)

`Clipfan.app` (`apps/mac/Clipfan`, a SwiftUI app) runs alongside the daemon
and renders a NSStatusItem menu showing:

- the daemon's origin (short hostname)
- one line per peer, with `●` if last push succeeded, `✗` if it failed,
  `(rx)` if we've received from them recently
- "Add Peer…" — install on a remote over SSH: scp's the right-arch binary,
  shim (linux only), unit file, and a config sharing this Mac's `shared_key`,
  runs install.sh, and adds the new host to the local `static_peers`. The
  install payload lives in `~/.local/share/clipfan/` (staged by
  `dist/install.sh`).
- "Open config", "Open daemon log", "Restart daemon"

The app polls `localhost:7853/v1/peers`. It does NOT need Local Network
privacy (loopback is exempt). Build it from `apps/mac/Clipfan` with SwiftPM /
Xcode.

### Linux Ctrl-V image paste

On a headless Linux remote, install the xclip/wl-paste shim symlinks
(`install.sh` does this on Linux). Claude Code's `Ctrl-V` image-paste shells
out to `xclip -t TARGETS -o` and `xclip -t image/png -o`; the shim answers
those out of clipfan's state directory, no display server required.

Make sure `~/.local/bin` is ahead of `/usr/bin` in PATH so the shim is found
first when Claude Code spawns its subprocess.

### Codex CLI image paste

Codex's `Ctrl-V` (or `Alt-V`) calls `arboard`, which needs X11/Wayland.
clipfan does NOT make Ctrl-V work for Codex on a headless Linux remote.

But **bracketed paste** does: `Cmd-V` from your Mac terminal (or `prefix-]`
in tmux) sends the synced text — which is the image's absolute file path on
the remote — into Codex's prompt, and Codex's bracketed-paste handler
detects the path and attaches the image (see `clipboard_paste.rs:899`). So
you paste with `Cmd-V` / `prefix-]` and Codex picks it up from disk. No Xvfb,
no x11-bridge, no sudo on the remote.

## Status

Active development. See `docs/PLAN.md` for the phased rollout and
`docs/ARCHITECTURE.md` for module-level design.

## License

Proprietary — Prime Radiant, Inc.
