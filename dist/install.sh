#!/usr/bin/env bash
# Installs clipfan (and the xclip / wl-paste shim on Linux) and registers
# a launchd / systemd-user unit so the daemon runs in the background.
#
# Usage from a freshly-extracted dist tarball:
#     ./install.sh
#
# Environment overrides:
#     DEST       — install dir for binaries (default: $HOME/.local/bin)
#     UNIT_NAME  — basename of the unit (default: clipfan / com.primeradiant.clipfan)

set -euo pipefail

DEST=${DEST:-$HOME/.local/bin}
mkdir -p "$DEST"

here=$(cd "$(dirname "$0")" && pwd)

uname_s=$(uname -s)
uname_m=$(uname -m)
case "$uname_m" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "Unsupported arch: $uname_m" >&2; exit 1 ;;
esac
case "$uname_s" in
    Darwin) goos=darwin ;;
    Linux)  goos=linux ;;
    *) echo "Unsupported OS: $uname_s" >&2; exit 1 ;;
esac

bin_src="$here/clipfan-$goos-$arch"
if [[ ! -x "$bin_src" ]]; then
    echo "Missing binary: $bin_src" >&2
    exit 1
fi

echo "Installing $bin_src -> $DEST/clipfan"
install -m 0755 "$bin_src" "$DEST/clipfan"

if [[ "$goos" == "linux" ]]; then
    shim_src="$here/clipfan-shim-$goos-$arch"
    if [[ -x "$shim_src" ]]; then
        echo "Installing $shim_src -> $DEST/clipfan-shim"
        install -m 0755 "$shim_src" "$DEST/clipfan-shim"
        ln -sf "$DEST/clipfan-shim" "$DEST/xclip"
        ln -sf "$DEST/clipfan-shim" "$DEST/wl-paste"
        echo "Symlinked: xclip -> clipfan-shim, wl-paste -> clipfan-shim"
        echo "(Make sure $DEST is ahead of /usr/bin in your PATH for Claude Code to pick up the shim.)"
    fi
fi

case "$goos" in
    darwin)
        plist_dir="$HOME/Library/LaunchAgents"
        log_dir="$HOME/Library/Logs"
        plist="$plist_dir/com.primeradiant.clipfan.plist"
        mkdir -p "$plist_dir" "$log_dir"
        echo "Writing $plist"
        # Resolve the shell PATH so the daemon can find pngpaste, tailscale, etc.
        run_path=$(/bin/zsh -lc 'echo $PATH' 2>/dev/null || echo "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin")
        sed -e "s|__BIN__|$DEST/clipfan|g" \
            -e "s|__LOG__|$log_dir|g" \
            -e "s|__PATH__|$run_path|g" \
            "$here/com.primeradiant.clipfan.plist" > "$plist"
        launchctl unload "$plist" 2>/dev/null || true
        launchctl load "$plist"
        echo "Loaded launchd job: com.primeradiant.clipfan"
        echo "Logs: $log_dir/clipfan.{out,err}.log"
        ;;
    linux)
        unit_dir="$HOME/.config/systemd/user"
        unit="$unit_dir/clipfan.service"
        mkdir -p "$unit_dir"
        echo "Writing $unit"
        install -m 0644 "$here/clipfan.service" "$unit"
        systemctl --user daemon-reload
        systemctl --user enable --now clipfan.service
        systemctl --user status clipfan.service --no-pager | head -10 || true
        ;;
esac

cat <<EOF

clipfan installed.

Next steps:
  1. Inspect/edit \${XDG_CONFIG_HOME:-\$HOME/.config}/clipfan/config.json
     - SharedKey was generated on first launch; copy it to every other host.
     - Discovery is "tailscale" by default; switch to "static" + static_peers if needed.
  2. Restart the daemon after editing:
       $( [[ \$goos == darwin ]] && echo 'launchctl kickstart -k gui/$UID/com.primeradiant.clipfan' \
          || echo 'systemctl --user restart clipfan' )
  3. Verify health: curl http://localhost:7853/v1/health
EOF
