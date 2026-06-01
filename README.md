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

## Installation

clipfan has two pieces: a **daemon** that runs on every host (macOS + Linux),
and a **menubar app** on the Mac that gives you the clipboard history panel and
a one-click installer for adding more hosts.

### 1. Build the binaries

From a clone of this repo on your Mac:

```sh
bash dist/build-all.sh        # cross-compiles the daemon for darwin + linux, amd64 + arm64
```

### 2. Install the daemon on this Mac

**The menubar app does this for you on first launch** — it bundles the daemon and
runs `install.sh` itself, showing progress in a Welcome window. If you're going
straight to the app, skip to step 3.

To install by hand instead (or to see what the app runs):

```sh
cd dist && ./install.sh
```

`install.sh` installs the daemon to `~/.local/bin/clipfan`, installs the macOS
pasteboard helper (for real image paste), registers a launchd user service, and
stages the cross-arch binaries in `~/.local/share/clipfan/` so the menubar app
can install other hosts for you. It generates a `shared_key` in
`~/.config/clipfan/config.json` on first launch.

**tmux integration is opt-in.** By default `install.sh` sets it up only if `tmux`
is installed on the host. Force it either way:

```sh
./install.sh --with-tmux      # always install the tmux copy snippet + source it
./install.sh --no-tmux        # never touch ~/.tmux.conf
```

See [Copying from a remote](#copying-from-a-remote-tmux-integration) for what the
snippet does.

### 3. Build and run the menubar app

```sh
cd apps/mac/Clipfan && ./build-app.sh
open .build/Clipfan.app          # or: cp -R .build/Clipfan.app /Applications && open /Applications/Clipfan.app
```

On first launch the app installs and starts the background daemon for you (a
Welcome window shows the progress), then tells you the two things you need:
press **⇧⌘V** to open the clipboard panel, and paste = clipfan re-copies the
item you pick so you press **⌘V** yourself. No Accessibility permission is
required. Turn on *Launch at login* in Settings → General to start it
automatically.

The only macOS permission clipfan needs is **Local Network**, and only once you
add LAN peers in step 4 — if a peer can't be reached, the app points you to the
right System Settings pane. (See the
[Local Network caveat](#macos-launchd-vs-local-network-privacy).)

### 4. Add the rest of your fleet

Open the menubar icon → **Settings… → Fleet → Add peer…**. If Tailscale is
running, pick hosts from the tailnet list; otherwise type a host + SSH user. The
app scp's the right-arch binary and a config carrying this Mac's `shared_key`,
runs `install.sh` over SSH, and adds the host to your local peer list. Check the
**tmux copy integration** box for hosts you use inside tmux.

To update an existing peer, open **Settings… → Fleet** and click the update
button on that peer's row. The app prompts for SSH details, uploads the current
bundled payload, and runs `install.sh --no-tmux` on the peer. This refreshes the
binary and restarts the launchd/systemd user service without rewriting the
peer's `~/.config/clipfan/config.json` or touching tmux config. After a Mac app
update, clipfan probes peers through their signed version endpoint and offers to
open Fleet settings when a peer is older or lacks the version endpoint. App
UI-only releases do not force peer updates; peers are compared against the
daemon version stamped from `DAEMON_VERSION`.

You can also install a host by hand: copy this Mac's `shared_key` into the new
host's `~/.config/clipfan/config.json` and run `./install.sh` there.

### 5. Verify

```sh
curl http://localhost:7853/v1/health                          # → ok
launchctl kickstart -k gui/$UID/com.primeradiant.clipfan      # restart (macOS)
systemctl --user restart clipfan                              # restart (Linux)
```

Copy something on one host; it lands on the others. (See the
[Local Network caveat](#macos-launchd-vs-local-network-privacy) if LAN peers
don't sync on macOS Sequoia.)

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
tmux buffer, it remembers that content as the current clip, so the hook-fired
re-copy — which arrives under a fresh clip-ID — is recognised as its own write
and dropped, rather than bouncing around the fleet. See `docs/ARCHITECTURE.md`
for the detail.

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

`static_peers` is the explicit Clipfan fleet allowlist. `discovery: "static"`
uses it as the hostname list. `discovery: "tailscale"` shells out to
`tailscale status --json`, but only syncs with online hosts whose short names
are listed in `static_peers`. Empty `static_peers` with Tailscale discovery
returns only the local host and does no non-self fanout.
`max_history` caps the clipboard history (default 200).

## Security model

clipfan is designed for a fleet of hosts you already trust with one shared
clipboard. The `shared_key` is the fleet credential: any host that has it can
send clipboard updates and participate as a trusted peer, and any local process
that has it can use the signed local API. Do not copy it to a host you would
not trust with your clipboard.

What clipfan protects:

- **Clipboard payload confidentiality on the wire.** Peer clipboard bytes are
  encrypted with AES-GCM using a key derived from `shared_key` before they are
  sent over HTTP.
- **Request integrity and replay resistance.** Signed requests bind the method,
  request URI, timestamp, nonce, and body. Stale timestamps and repeated request
  nonces are rejected. Mixed-version fleets fail closed; upgrade all peers
  together.
- **Recipient binding.** Peer clip envelopes include the intended recipient in
  the signed body, so a captured clip push for one peer is rejected if replayed
  to another peer.
- **Local control endpoints.** History, config, restore, and peer-status
  endpoints require a valid signature and are loopback-only. Successful local
  control responses are also signed and bound to the request nonce, so GUI
  clients can reject a spoofed loopback listener that does not know
  `shared_key`. The unauthenticated health endpoint returns only `ok`.
- **Signed peer version probes.** `/v1/version` is intentionally reachable from
  peers so the Mac app can detect stale remote installs. It still requires a
  valid signed request, returns a signed response, and exposes only the daemon
  version string. The daemon version is intentionally separate from the Mac app
  version so UI-only app releases do not mark peers stale.
- **Local file permissions.** Config, state, history, and image storage are kept
  under the current user's XDG config/state directories. clipfan creates and
  repairs those directories as `0700` and the files as `0600`.
- **User service scope.** The Linux service is a `systemd --user` unit and the
  macOS service is a LaunchAgent. The daemon is not intended to run as root.
- **Concealed pasteboard items.** Password-manager-style concealed or transient
  pasteboard items are not synced or recorded.
- **Peer timestamp bounds.** Peer envelopes are rejected if their clipboard
  timestamp is more than two minutes ahead of the receiver's clock. This
  prevents a trusted peer from poisoning local ordering state with an arbitrary
  future clip.
- **Release-time tooling.** CI builds Sparkle's `generate_appcast` from the
  pinned Sparkle revision in this repo, then checks out that exact revision
  before using the appcast signing key. Sparkle release notes are extracted from
  `CHANGELOG.md` and embedded in the signed appcast.

What clipfan does **not** protect against:

- **A compromised trusted peer.** A host with the `shared_key` is inside the
  trust boundary. It can send clipboard contents and disrupt sync. HMAC and
  encryption protect against outsiders; they do not make an untrusted key holder
  safe.
- **The same Unix user on the same host.** A process running as the same user can
  read the config, state, history, image files, and process environment. clipfan
  does not try to sandbox the user's own processes from each other.
- **Root or physical access.** A root user, device owner, or someone with
  physical access to an unlocked desktop can read or change clipboard state.
- **Internet exposure.** The daemon listens on `:7853` by default because peer
  sync needs a network-reachable listener. Do not expose that port to the public
  internet. Use a trusted LAN, a tailnet, firewall rules, or explicit static
  peers.
- **Traffic metadata.** Payload bytes are encrypted, but HTTP metadata such as
  hostnames, peer addresses, timing, and message sizes can still be visible to
  the network path unless you run over an encrypted underlay such as Tailscale.
- **Large local clips.** The macOS app caps how much history text it searches and
  renders at once, but clipfan still stores clipboard history up to your
  configured history limit. A same-user process can still create large local
  clips and consume local disk or memory.

Multi-user Linux notes:

- Other Unix users should not be able to read clipfan config, history, state, or
  image files when the XDG directories are owned by the clipfan user and the
  permissions above are intact. If a backup job, shared home directory, unusual
  ACL, or admin policy makes those files readable to other users, that is outside
  clipfan's guarantees.
- Other Unix users may be able to connect to the TCP listener, but they still
  need `shared_key` to read or change clipboard state. Without the key, the only
  intended unauthenticated endpoint is `/v1/health`.
- tmux integration verifies that the tmux socket directory is not a symlink, is
  owned by the clipfan user, and is not accessible by group or other users
  (normally `/tmp/tmux-$UID` mode `0700`). Discovered socket files must also be
  owned by the clipfan user and not group/world-writable before clipfan calls
  `tmux -S <socket> load-buffer -`.
- Local daemon identity is authenticated with signed responses, but the local
  HTTP API still uses separate loopback HTTP requests. That protects against a
  different local Unix user who cannot read `shared_key`; it is not a sandbox
  against the same user, root, or a host configuration that exposes the key.
- Deleting a history item or clearing unpinned history also removes image files
  that are no longer referenced by remaining history entries.

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

Click the menubar icon for **Open Clipboard**, **Settings…**, **Quit**, and a
**Fleet** section that shows each peer with a colored health dot (green synced /
orange offline / gray idle) and its last **↑ push / ↓ recv** times — so you can
see at a glance whether your fleet is in sync. Click a peer to jump to its detail.

### Settings

- **Fleet** — every peer as a card (health, address, sync direction), plus
  **Add peer…** (see [Installation](#4-add-the-rest-of-your-fleet)).
- **General** — *Launch at login*, the history limit, the global shortcut, and
  daemon health. Developer bits (config path, daemon log, restart) live in a
  collapsed **Developer** section.

The app signs its `localhost:7853/v1/peers` loopback poll, so it needs no Local
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

- `docs/ARCHITECTURE.md` — module layout, wire format, HTTP API, recirculation
  prevention, the image-on-receive flow, clipboard history, and the tmux
  copy-capture path.
- `docs/ROADMAP.md` — what's shipped and what's planned.

## License

Proprietary — Prime Radiant, Inc.
