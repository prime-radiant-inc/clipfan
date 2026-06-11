# clipfan

clipfan is one clipboard for every Mac and Linux machine you work on: copy on
any of them and paste on any other, including screenshots into Claude Code and
Codex CLI on a headless remote.

The brochure, [docs/BROCHURE.md](docs/BROCHURE.md), covers what you get, the
trust model, and who it's for.

## How it works

A daemon runs on every host. The daemon polls the local OS clipboard, exposes a
signed loopback HTTP API for the app and CLI, and syncs peer clipboard updates
over authenticated SSH streams. The Mac app is the control surface: it installs
the local daemon, provisions peers over SSH, and keeps the fleet view current.

When a host receives a clipboard update, its daemon:

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
server. Every daemon relays a received clip onward to its configured peers
(except the clip's origin), so hosts that can't see each other directly still
converge through a host that sees both — typically the Mac, which holds an
edge to every peer it installs.

## Install

clipfan has two pieces: a **daemon** that runs on every host (macOS + Linux),
and a **menubar app** on the Mac that gives you the clipboard panel and a
one-click installer for adding more hosts. Start on your Mac with the menubar
app — it bundles the daemon and installs everything for you.

1. Get `Clipfan.app` and move it to `/Applications` (or run it from wherever you
   downloaded it).
2. Launch it. On first launch the app installs and starts the background daemon
   for you, showing progress in a Welcome window. It then tells you the two
   things you need: press **⇧⌘V** to open the clipboard panel, and paste =
   clipfan re-copies the item you pick so you press **⌘V** yourself.
3. No Accessibility permission is required. The only macOS permission clipfan
   needs is **Local Network**, and only once you add LAN peers — if a peer can't
   be reached, the app points you to the right System Settings pane. (See the
   [Local Network caveat](#macos-launchd-vs-local-network-privacy).)

Building from a source clone instead? See
[docs/development/building-from-source.md](docs/development/building-from-source.md).

## Getting started

1. **Set up this Mac.** Launching the app (above) installs and starts the daemon.
   Turn on *Launch at login* in **Settings → General** to start it automatically.
2. **Add the rest of your fleet.** Open the menu bar icon →
   **Settings… → Fleet → Add peer…**. If Tailscale is running, pick hosts from
   the tailnet list; otherwise type a host + SSH user. The app stages the
   right-arch binary, installs the service over SSH, and adds the host to your
   local peer list. Check the
   **tmux copy integration** box for hosts you use inside tmux.

To update an existing peer, open **Settings… → Fleet** and click the update
button on that peer's row. The app prompts for SSH details, uploads the current
bundled payload, and refreshes the peer in place. This updates the binary and
restarts the launchd/systemd user service without rewriting the peer's
`~/.config/clipfan/config.json` or touching tmux config. App UI-only releases do
not require peer updates; daemon-affecting releases carry a bundled daemon
version stamped from `DAEMON_VERSION`.

You can also install a host by hand from a source clone. See
[docs/development/building-from-source.md](docs/development/building-from-source.md).

## Daily use

- **Copy anywhere, paste anywhere.** Copy on one host and it lands on the others.
  On a remote you can paste the synced item with **⌘V** from your Mac terminal or
  **prefix-]** in tmux.
- **Clipboard panel — ⇧⌘V.** A keyboard-driven panel (Spotlight/Raycast
  style) of your recent clips, local to each host. Type to search, **↑ / ↓** to
  move, **⏎** to put the selected clip on your clipboard and sync it to the fleet.
  See [Menubar app](#menubar-app-macos) for the full controls.
- **tmux `prefix-]`.** Received clips land in every running tmux paste buffer, so
  `prefix-]` pastes the latest synced clip. For getting a copy you make *inside*
  tmux on a remote back to your Mac, see
  [Copying from a remote](#copying-from-a-remote-tmux-integration).

## Copying from a remote (tmux integration)

The daemon keeps the *OS clipboard* in sync on its own. Getting a **copy you
make inside tmux on a remote** back to your Mac is what the tmux integration
does. The installer writes the snippet to `~/.config/clipfan/tmux.conf` and adds
`source-file ~/.config/clipfan/tmux.conf` to your `~/.tmux.conf` (idempotently —
re-running won't duplicate it). Reload with `tmux source-file ~/.tmux.conf` or
restart tmux.

The snippet captures tmux copy-mode yanks — `y`, `Enter`, and mouse-drag in the
vi table; `M-w` and mouse-drag in the emacs table — and pipes the selection
through `clipfan copy`.

That path also emits OSC 52 to the client tty as a fallback, so a terminal
connected from a non-clipfan host (Blink on a phone, kitty on a laptop without
clipfan) still grabs the text. Apple Terminal ignores OSC 52 — the daemon path
is what makes it work there.

The snippet intentionally does not install global tmux buffer hooks. Received
clips already get written to tmux for `prefix-]`, and treating every tmux buffer
change as a new local copy can turn daemon writebacks or unrelated tmux servers
into duplicate fleet updates.

The snippet lives in clipfan (`dist/tmux.conf.snippet`) and is the single source
of truth; dotfiles should `source-file` the installed path rather than keep a
copy.

## Configuration

`$XDG_CONFIG_HOME/clipfan/config.json` (default `~/.config/clipfan/config.json`):

```json
{
  "listen": "127.0.0.1:7853",
  "shared_key": "base64-32-bytes-shared-across-fleet",
  "discovery": "static",
  "static_peers": ["mac-host", "paradise-park", "flower-garden.local"],
  "port": 7853,
  "max_history": 200
}
```

The app writes and updates this file for normal use. `shared_key` is host-local
state and never belongs in a dotfiles repo.

`static_peers` is the explicit clipfan fleet allowlist. `discovery: "static"`
uses it as the hostname list; `discovery: "tailscale"` shells out to
`tailscale status --json` and filters online tailnet hosts through the same
allowlist (with empty `static_peers` it lists only the local host). Discovery
feeds the peer and fleet views; clip sync itself runs over the SSH peers the
app provisions. `max_history` caps the clipboard history (default 200).

## Security

clipfan is for hosts you already trust with one shared clipboard. Clipboard
payloads are encrypted inside the SSH sync stream, local control endpoints are
signed and loopback-only, and generated configs bind the daemon HTTP API to
`127.0.0.1:7853`. Read [SECURITY.md](SECURITY.md) for the full trust model and
non-goals.

## Troubleshooting

Common failures (daemon not running, peers not syncing) and their fixes are in
[docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md).

## Menubar app (macOS)

`Clipfan.app` (`apps/mac/Clipfan`, a SwiftUI app) runs alongside the daemon. It
follows your system accent color and stays out of the way until you summon it.

### Clipboard panel — ⇧⌘V

A translucent, keyboard-driven panel (Spotlight/Raycast style) that appears at
the center of the screen:

- **Type to search** the moment it opens — it filters as you type.
- **Type chips** (All / Text / Image / Link) narrow by kind.
- A single list on the left, a live **preview** on the right. Rows show a
  thumbnail (or a type glyph), the clip, the origin host, and a relative time;
  images show their dimensions, code and file paths render monospaced.
- **↑ / ↓** to move, **⏎** to put the selected clip on your clipboard and sync
  it to the fleet, **⌘1–9** to grab one of the top items directly, **Esc** (or
  click away) to dismiss. Right-click a row to **pin** or **delete**.

History is local to each host, capped at `max_history` (default 200). Password
manager pastes (concealed or transient clips) are never synced or recorded.

### Menubar dropdown

Click the menu bar icon for **Open Clipboard**, **Install Update…** (shown when
an update is available), **Settings…**, **Quit**, and a
**Fleet** section that shows each peer with a colored health dot (green synced /
orange offline / gray idle) and its last **↑ send / ↓ recv** times — so you can
see at a glance whether your fleet is in sync. Click a peer to open the
Settings window.

### Settings

- **Fleet** — every peer as a card (health, address, sync direction), plus
  **Add peer…** (see [Getting started](#getting-started)).
- **General** — *Launch at login*, the history limit, the global shortcut, and
  **Check for Updates…**.
- **Diagnostics** — daemon health, setup recovery, config path, daemon log, and
  restart controls.
- **About** — app and daemon versions plus a GitHub link.

The app signs its `localhost:7853/v1/peers` loopback poll, so it needs no Local
Network privacy grant.

## Known caveats

### macOS launchd vs Local Network privacy

Starting in macOS Sequoia, processes launched by launchd are gated by the
"Local Network" privacy permission for any connection to an RFC1918 IP
(10.x / 172.16-31.x / 192.168.x). A foreground app gets a permission prompt;
launchd-managed background daemons get a silent `EHOSTUNREACH` and no prompt.

When the menubar app is running it helps two ways: once sends to a LAN peer
start failing it points you to the right System Settings pane (see
[Install](#install)), and at app launch, if no daemon answers on the loopback
port, it shell-launches the daemon as a child, which inherits the app's Local
Network grant. Running the daemon without the app, work around the gate by hand — run
the daemon from your shell instead of via launchd:

```sh
launchctl unload ~/Library/LaunchAgents/com.primeradiant.clipfan.plist
nohup ~/.local/bin/clipfan > /tmp/clipfan.log 2>&1 &
disown
```

Tailscale (100.x) traffic is unaffected — only LAN/mDNS peers trigger the gate.
If your fleet is entirely Tailscale-routed, launchd works fine.

### Topology

Sync follows the provisioned SSH edges, and any host relays received clips
onward to its other peers — relaying is not special to the Mac. The Mac holds
an edge to every peer it installs, so with that star topology host A and host
B converge through the Mac even when they can't see each other. Adding a peer
also provisions direct mesh edges between peers that can reach each other
(mesh-heal repairs these), so such peers keep converging when the Mac is down;
a pair with no edge and no common reachable host waits until one returns.

### Linux Ctrl-V image paste

On a headless Linux remote, the xclip/wl-paste shim symlinks (the installer
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

- [docs/development/building-from-source.md](docs/development/building-from-source.md)
  — build the daemon and the menubar app from a source clone.
- [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) — common failures and fixes.
- [SECURITY.md](SECURITY.md) — trust boundary, protections, and non-goals.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — module layout, wire format,
  HTTP API, recirculation prevention, the image-on-receive flow, clipboard
  history, and the tmux copy-capture path.
- [docs/ROADMAP.md](docs/ROADMAP.md) — what's shipped and what's planned.
- [docs/INDEX.md](docs/INDEX.md) — the full documentation index.

## License

Proprietary — Prime Radiant, Inc.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-10 · commit `5ed989c` · verified against code (9 claims deferred to review)._
