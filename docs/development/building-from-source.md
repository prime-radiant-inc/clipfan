# Building clipfan from source

This is the developer path: build the daemon and the menubar app from a clone of
this repo. Most users should install the prebuilt menubar app instead — see the
[README](../../README.md#install).

clipfan has two pieces: a **daemon** that runs on every host (macOS + Linux), and
a **menubar app** on the Mac that gives you the clipboard history panel and a
one-click installer for adding more hosts.

## Prerequisites

- A **Go** toolchain (the daemon, the Linux xclip/wl-paste shim, and the `copy` /
  `paste` CLI are Go).
- **Swift / Xcode** command-line tools on macOS — `swiftc` builds the pasteboard
  helper and `swift build` builds the menubar app.

## 1. Build the binaries

From a clone of this repo on your Mac:

```sh
bash dist/build-all.sh        # cross-compiles the daemon for darwin + linux, amd64 + arm64
```

`dist/build-all.sh` cross-compiles the daemon (`clipfan`), the Linux shim
(`clipfan-shim`), and the macOS pasteboard helper for every supported target, then
stages them — alongside `install.sh` and the launchd/systemd unit files — in
`dist/`. The build version is read from the `DAEMON_VERSION` file (override with
the `CLIPFAN_DAEMON_VERSION` environment variable).

## 2. Install the daemon on this Mac

**The menubar app does this for you on first launch** — it bundles the daemon and
runs `install.sh` itself, showing progress in a Welcome window. If you're going
straight to the app, skip to step 3.

To install by hand instead (or to see what the app runs):

```sh
cd dist && ./install.sh
```

`install.sh` installs the daemon to `~/.local/bin/clipfan`, installs the macOS
pasteboard helper (for real image paste), registers a launchd user service, and
stages the cross-arch binaries in `~/.local/share/clipfan/` so the menubar app can
install other hosts for you. It generates a `shared_key` in
`~/.config/clipfan/config.json` on first launch.

**tmux integration is opt-in.** By default `install.sh` sets it up only if `tmux`
is installed on the host. Force it either way:

```sh
./install.sh --with-tmux      # always install the tmux copy snippet + source it
./install.sh --no-tmux        # never touch ~/.tmux.conf
```

See [Copying from a remote](../../README.md#copying-from-a-remote-tmux-integration)
for what the snippet does.

## 3. Build and run the menubar app

```sh
cd apps/mac/Clipfan && ./build-app.sh
open .build/Clipfan.app          # or: cp -R .build/Clipfan.app /Applications && open /Applications/Clipfan.app
```

`build-app.sh` runs `swift build` and assembles a double-clickable,
menubar-only (`LSUIElement`) `Clipfan.app` bundle under `.build/`. It bundles the
full `dist/` payload (all-arch binaries + helpers + `install.sh`) inside the app,
so **run `dist/build-all.sh` first** — the build fails if the payload is
incomplete.

On first launch the app installs and starts the background daemon for you (a
Welcome window shows the progress), then tells you the two things you need: press
**⇧⌘V** to open the clipboard panel, and paste = clipfan re-copies the item you
pick so you press **⌘V** yourself. No Accessibility permission is required. Turn
on *Launch at login* in Settings → General to start it automatically.

The only macOS permission clipfan needs is **Local Network**, and only once you
add LAN peers — if a peer can't be reached, the app points you to the right System
Settings pane. (See the
[Local Network caveat](../../README.md#macos-launchd-vs-local-network-privacy).)

## Installing a host by hand

You can also install a host without the app: copy this Mac's `shared_key` into the
new host's `~/.config/clipfan/config.json` and run `./install.sh` there.

## Running the tests

```sh
go test ./...                                   # daemon, CLI, shim
cd apps/mac/Clipfan && swift test               # menubar app
```
