#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/dist" "$tmp/home" "$tmp/fakebin"
cp "$repo/dist/install.sh" "$tmp/dist/install.sh"
cp "$repo/dist/clipfan.service" "$tmp/dist/clipfan.service"
printf '#!/usr/bin/env bash\necho fake clipfan\n' > "$tmp/dist/clipfan-linux-amd64"
chmod +x "$tmp/dist/install.sh" "$tmp/dist/clipfan-linux-amd64"

cat > "$tmp/fakebin/uname" <<'SH'
#!/usr/bin/env bash
case "$1" in
  -s) printf 'Linux\n' ;;
  -m) printf 'x86_64\n' ;;
  *) exit 1 ;;
esac
SH
chmod +x "$tmp/fakebin/uname"

cat > "$tmp/fakebin/systemctl" <<SH
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "$tmp/systemctl.log"
if [[ "\$*" == *" status "* ]]; then
  printf 'clipfan.service active\n'
fi
SH
chmod +x "$tmp/fakebin/systemctl"

HOME="$tmp/home" \
DEST="$tmp/home/.local/bin" \
PATH="$tmp/fakebin:/usr/bin:/bin" \
    "$tmp/dist/install.sh" --no-tmux >"$tmp/install.out"

grep -qx -- '--user daemon-reload' "$tmp/systemctl.log"
grep -qx -- '--user enable clipfan.service' "$tmp/systemctl.log"
grep -qx -- '--user restart clipfan.service' "$tmp/systemctl.log"
grep -qx -- '--user status clipfan.service --no-pager' "$tmp/systemctl.log"
