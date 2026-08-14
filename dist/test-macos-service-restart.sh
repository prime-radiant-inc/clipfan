#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/dist" "$tmp/home" "$tmp/fakebin"
cp "$repo/dist/install.sh" "$tmp/dist/install.sh"
cat > "$tmp/dist/com.primeradiant.clipfan.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>Label</key><string>com.primeradiant.clipfan</string></dict></plist>
PLIST
printf '#!/usr/bin/env bash\nexit 0\n' > "$tmp/dist/clipfan-darwin-arm64"
chmod +x "$tmp/dist/install.sh" "$tmp/dist/clipfan-darwin-arm64"

cat > "$tmp/fakebin/uname" <<'SH'
#!/usr/bin/env bash
case "$1" in
  -s) printf 'Darwin\n' ;;
  -m) printf 'arm64\n' ;;
  *) exit 1 ;;
esac
SH
chmod +x "$tmp/fakebin/uname"

cat > "$tmp/fakebin/launchctl" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$LAUNCHCTL_LOG"
case "$1" in
  unload|enable) exit 0 ;;
  bootstrap) exit 1 ;;
  load)
    touch "$LAUNCHCTL_STATE"
    exit 0
    ;;
  kickstart|print)
    test -f "$LAUNCHCTL_STATE"
    ;;
  *) exit 1 ;;
esac
SH
chmod +x "$tmp/fakebin/launchctl"

service="gui/$(id -u)/com.primeradiant.clipfan"
HOME="$tmp/home" \
DEST="$tmp/home/.local/bin" \
PATH="$tmp/fakebin:/usr/bin:/bin" \
LAUNCHCTL_LOG="$tmp/launchctl.log" \
LAUNCHCTL_STATE="$tmp/launchctl.state" \
    "$tmp/dist/install.sh" --no-tmux >"$tmp/install.out"

grep -qx -- "enable $service" "$tmp/launchctl.log"
grep -qx -- "bootstrap gui/$(id -u) $tmp/home/Library/LaunchAgents/com.primeradiant.clipfan.plist" "$tmp/launchctl.log"
grep -qx -- "load $tmp/home/Library/LaunchAgents/com.primeradiant.clipfan.plist" "$tmp/launchctl.log"
grep -qx -- "kickstart -k $service" "$tmp/launchctl.log"
grep -qx -- "print $service" "$tmp/launchctl.log"
grep -qx -- 'Loaded launchd job: com.primeradiant.clipfan' "$tmp/install.out"

: > "$tmp/launchctl.log"
rm -f "$tmp/launchctl.state"
HOME="$tmp/home" \
DEST="$tmp/home/.local/bin" \
PATH="$tmp/fakebin:/usr/bin:/bin" \
LAUNCHCTL_LOG="$tmp/launchctl.log" \
LAUNCHCTL_STATE="$tmp/launchctl.state" \
    "$tmp/dist/install.sh" --no-tmux --no-restart >"$tmp/install-no-restart.out"

test ! -s "$tmp/launchctl.log"
grep -qx -- 'Skipped launchd load/restart (--no-restart)' "$tmp/install-no-restart.out"

echo "ALL PASS"
