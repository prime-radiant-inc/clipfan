# clipfan: what it is, who it's for

clipfan is one clipboard for every Mac and Linux machine you work on: copy on
any of them and paste on any other, including screenshots into Claude Code and
Codex CLI on a headless remote.

## What you get

Every machine you work on keeps its own clipboard. The Mac has one. The Linux
box you SSH into has another. tmux holds its own paste buffers on top of both.
Copy a line on one machine and it exists only there. A screenshot is the worst
case: Claude Code waits in an SSH session on a headless server, the picture
sits on your Mac, and no clipboard connects them. So you copy the file over by
hand, or you wire up OSC 52 and learn that Apple Terminal ignores it, or you
give the server a fake X display to move one picture.
([README § Copying from a remote](../README.md#copying-from-a-remote-tmux-integration),
[README § Codex CLI image paste](../README.md#codex-cli-image-paste))

clipfan removes the boundary. Your machines share one clipboard as a fleet.

- **Copy anywhere, paste anywhere.** Copy on one host and it is already on the
  others: ⌘V in the terminal on a remote, prefix-] inside tmux.
  ([README § Daily use](../README.md#daily-use))
- **Screenshots reach the AI tools on your servers.** Take a screenshot on the
  Mac and paste it into Claude Code or Codex CLI running on a headless remote.
  The image lands ready to attach, with no display server, no Xvfb, and no
  OSC 52 support required.
  ([README § Linux Ctrl-V image paste](../README.md#linux-ctrl-v-image-paste),
  [ARCHITECTURE § Goals](ARCHITECTURE.md#goals),
  [ARCHITECTURE § Image flow on receive](ARCHITECTURE.md#image-flow-on-receive-the-load-bearing-trick))
- **Copies made inside tmux sync back.** Yank in tmux copy-mode on a remote and
  the selection lands on your Mac clipboard.
  ([README § Copying from a remote](../README.md#copying-from-a-remote-tmux-integration),
  [ARCHITECTURE § tmux copy capture](ARCHITECTURE.md#tmux-copy-capture))
- **Recent clips, searchable.** Press ⇧⌘V for the clipboard panel: type to
  search, arrows to move, Enter puts the clip back on your clipboard and sends
  it to the fleet. History stays on each host, capped at a limit you set
  (200 by default).
  ([README § Menubar app](../README.md#menubar-app-macos),
  [README § Configuration](../README.md#configuration))
- **Fleet health at a glance.** The menu bar icon lists each peer machine with
  a health dot and its last send and receive times.
  ([README § Menubar app](../README.md#menubar-app-macos))
- **Password manager copies stay private.** Clips marked concealed or transient
  are never synced and never recorded.
  ([SECURITY § What clipfan protects](../SECURITY.md#what-clipfan-protects))

## Using it

Day to day you press the keys you already press: ⌘C where the text is, ⌘V or
prefix-] where you want it, ⇧⌘V when you want something from earlier.

The same clipboard is scriptable. `clipfan copy` reads stdin and sends it to
the fleet; `clipfan paste` prints the current clip; the daemon answers a plain
health check on its loopback port:

```console
$ echo "one clipboard, every host" | clipfan copy
$ clipfan paste
one clipboard, every host
$ curl -s http://127.0.0.1:7853/v1/health
ok
```

Captured from a live fleet host on 2026-06-11.
([ROADMAP § Done](ROADMAP.md#done),
[ARCHITECTURE § Local HTTP API](ARCHITECTURE.md#local-http-api))

## Running it

clipfan is two pieces. A Go daemon runs on every host: it watches the local
clipboard, serves a signed API on loopback, and syncs with its peers.
Clipfan.app, the menubar app, is the control surface on the Mac: it installs
the local daemon on first launch, adds hosts over SSH, and shows fleet health.
([README § How it works](../README.md#how-it-works))

There is no central server. Clips travel between your hosts over authenticated
SSH streams, encrypted with AES-GCM inside the stream, and every host relays
clips onward, so machines that cannot reach each other still converge through
one that can.
([SECURITY § What clipfan protects](../SECURITY.md#what-clipfan-protects),
[ARCHITECTURE § SSH sync payload](ARCHITECTURE.md#ssh-sync-payload),
[README § How it works](../README.md#how-it-works))

One shared key defines the fleet. Every host holding it is a full peer: it can
send clips, read clips, and call the local API. Give the key only to machines
you already trust with your clipboard. ([SECURITY.md](../SECURITY.md))

The daemon's control surface stays on the machine. Out of the box the HTTP API
binds to 127.0.0.1:7853, control endpoints take signed, replay-checked
requests only, and the one unauthenticated endpoint answers `ok`. Config,
history, and images live under your user's XDG directories as 0700 directories
and 0600 files. The daemon runs as your user: a systemd user unit on Linux, a
LaunchAgent on macOS.
([SECURITY § What clipfan protects](../SECURITY.md#what-clipfan-protects),
[ARCHITECTURE § Local HTTP API](ARCHITECTURE.md#local-http-api))

Adding a host is one dialog. Settings → Fleet → Add peer lists your Tailscale
hosts, or takes a hostname and SSH user; the app stages the right binary for
the host's architecture, installs the service over SSH, and the peer joins.
Releases are Developer ID signed and notarized, the app updates itself over
Sparkle, and you push daemon updates to peers from the same Fleet pane.
([README § Getting started](../README.md#getting-started),
[ROADMAP § Done](ROADMAP.md#done))

What it costs you: a daemon on every host; one trust domain per fleet, because
the key is all or nothing; upgrades done together, because mixed-version fleets
fail closed; and on macOS LAN fleets, the Local Network permission gate that
Sequoia puts on background daemons. The app holds that grant and relaunches the
daemon under it, and fleets routed over Tailscale never hit the gate.
([SECURITY § What clipfan protects](../SECURITY.md#what-clipfan-protects),
[README § macOS launchd vs Local Network privacy](../README.md#macos-launchd-vs-local-network-privacy))

## Who it's for and who it's not

clipfan fits a developer who works several machines at once: a Mac in front of
you, Linux hosts over SSH, tmux everywhere, and AI coding tools running on the
remotes. It assumes every machine in the fleet is one you already trust.

It is the wrong tool for sharing a clipboard between people or tenants: one
fleet is one trust domain. It does not run on Windows. It is proprietary
software with public source, so a team that requires an OSI license should
pass. ([SECURITY.md](../SECURITY.md), [README § Install](../README.md#install),
[LICENSE](../LICENSE))

## Limitations

- (user) History is local to each host. The panel shows what reached that
  machine; there is no merged fleet history.
  ([README § Menubar app](../README.md#menubar-app-macos),
  [ROADMAP § Planned](ROADMAP.md#planned--clipboard-history-follow-ups))
- (user) Text and PNG images sync; RTF, HTML, and file lists do not travel.
  ([ROADMAP § Stretch](ROADMAP.md#stretch--post-10))
- (user) On a headless remote, Codex CLI takes images through bracketed paste
  (⌘V or prefix-]); its own Ctrl-V needs a display server, which a headless
  host lacks. ([README § Codex CLI image paste](../README.md#codex-cli-image-paste))
- (user) Each host's own clipboard is polled every 250 ms, so sync adds up to
  a quarter second before the network hop.
  ([ROADMAP § Planned](ROADMAP.md#planned))
- (owner) Any host holding the shared key sits fully inside the trust boundary;
  a compromised peer can read and write the fleet's clipboard.
  ([SECURITY.md](../SECURITY.md),
  [SECURITY § What clipfan does not protect against](../SECURITY.md#what-clipfan-does-not-protect-against))
- (owner) Mixed-version fleets fail closed; plan to upgrade all peers together.
  ([SECURITY § What clipfan protects](../SECURITY.md#what-clipfan-protects))
- (owner) Two hosts with no direct edge converge through a host that reaches
  both, typically the Mac; when no such host is online, they wait for one.
  ([README § Topology](../README.md#topology))

## Getting started

- **Use it.** Download the latest release from
  [github.com/prime-radiant-inc/clipfan/releases](https://github.com/prime-radiant-inc/clipfan/releases),
  move Clipfan.app to Applications, and launch it. The Welcome window installs
  the daemon and walks you to your first peer. Press ⇧⌘V.
  ([README § Install](../README.md#install))
- **Run a fleet.** Add each host from Settings → Fleet → Add peer. Read
  [SECURITY.md](../SECURITY.md) before you give a machine the key.
  ([README § Getting started](../README.md#getting-started))
- **Work on clipfan.** Build the daemon and the app from source:
  [docs/development/building-from-source.md](development/building-from-source.md).

<!-- The brochure site (docs/index.html) is rendered from this file by
the superpowers-docs documentation skill; re-render on stamp-SHA mismatch
(see the sentinel in docs/index.html). -->

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-11 · commit `9b9970e` · verified against code (3 claims deferred to review)._
