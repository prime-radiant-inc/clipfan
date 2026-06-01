#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/dist" "$tmp/home" "$tmp/fakebin"
cp "$repo/dist/install.sh" "$tmp/dist/install.sh"
cp "$repo/dist/clipfan.service" "$tmp/dist/clipfan.service"
printf '#!/usr/bin/env bash\necho fake clipfan\n' > "$tmp/dist/clipfan-linux-amd64"
chmod 0644 "$tmp/dist/clipfan-linux-amd64"

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
    bash "$tmp/dist/install.sh" --no-tmux >"$tmp/install.out"

test -x "$tmp/home/.local/bin/clipfan"
"$tmp/home/.local/bin/clipfan" | grep -qx 'fake clipfan'
