#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
backup="$tmp/backup"
mkdir -p "$backup" "$tmp/bin"

helpers=(
  "$repo/dist/clipfan-pasteboard-helper-darwin-amd64"
  "$repo/dist/clipfan-pasteboard-helper-darwin-arm64"
)

go_payloads=(
  "$repo/dist/clipfan-darwin-amd64"
  "$repo/dist/clipfan-darwin-arm64"
  "$repo/dist/clipfan-linux-amd64"
  "$repo/dist/clipfan-linux-arm64"
  "$repo/dist/clipfan-shim-linux-amd64"
  "$repo/dist/clipfan-shim-linux-arm64"
)

payloads=("${helpers[@]}" "${go_payloads[@]}")

for f in "${payloads[@]}"; do
  if [[ -e "$f" ]]; then
    cp -p "$f" "$backup/$(basename "$f")"
  fi
done

cleanup() {
  for f in "${payloads[@]}"; do
    rm -f "$f"
    if [[ -e "$backup/$(basename "$f")" ]]; then
      cp -p "$backup/$(basename "$f")" "$f"
    fi
  done
  rm -rf "$tmp"
}
trap cleanup EXIT

cat > "$tmp/bin/swiftc" <<'SH'
#!/usr/bin/env bash
out=""
target=""
inputs=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      shift
      out="$1"
      ;;
    -target)
      shift
      target="$1"
      ;;
    -*)
      ;;
    *)
      inputs+=("$1")
      ;;
  esac
  shift
done
[[ -n "$out" ]] || exit 2
found_input=0
for input in "${inputs[@]}"; do
  if [[ "$input" == "$FAKE_SWIFTC_EXPECTED_INPUT" ]]; then
    found_input=1
  fi
done
[[ "$found_input" == "1" ]] || {
  echo "missing expected swift input: $FAKE_SWIFTC_EXPECTED_INPUT" >&2
  exit 3
}
mkdir -p "$(dirname "$out")"
printf '#!/bin/sh\n# fake swiftc target=%s\n' "$target" > "$out"
chmod 0755 "$out"
printf '%s\n' "$target" >> "$FAKE_SWIFTC_LOG"
SH
chmod 0755 "$tmp/bin/swiftc"

cat > "$tmp/bin/go" <<'SH'
#!/usr/bin/env bash
out=""
pkg=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      shift
      out="$1"
      ;;
    -ldflags)
      shift
      ;;
    ./*)
      pkg="$1"
      ;;
  esac
  shift
done
[[ -n "$out" ]] || exit 2
[[ "$pkg" == "./cmd/clipfan" || "$pkg" == "./cmd/clipfan-shim" ]] || {
  echo "unexpected go package: $pkg" >&2
  exit 3
}
mkdir -p "$(dirname "$out")"
printf '#!/bin/sh\n# fake go package=%s target=%s/%s\n' "$pkg" "${GOOS:-}" "${GOARCH:-}" > "$out"
chmod 0755 "$out"
printf '%s %s %s\n' "$out" "$pkg" "${GOOS:-}/${GOARCH:-}" >> "$FAKE_GO_LOG"
SH
chmod 0755 "$tmp/bin/go"

export FAKE_SWIFTC_LOG="$tmp/swiftc.log"
export FAKE_SWIFTC_EXPECTED_INPUT="dist/clipfan-pasteboard-helper.swift"
export FAKE_GO_LOG="$tmp/go.log"
PATH="$tmp/bin:$PATH" bash "$repo/dist/build-all.sh"

for f in "${helpers[@]}"; do
  [[ -x "$f" ]] || { echo "missing executable helper: $f" >&2; exit 1; }
  grep -q "fake swiftc target=" "$f" || { echo "helper was not produced by swiftc: $f" >&2; exit 1; }
done

for f in "${go_payloads[@]}"; do
  [[ -x "$f" ]] || { echo "missing executable go payload: $f" >&2; exit 1; }
  grep -q "fake go package=" "$f" || { echo "go payload was not produced by go build: $f" >&2; exit 1; }
done

grep -q "./cmd/clipfan-shim" "$repo/dist/clipfan-shim-linux-amd64" || {
  echo "linux amd64 shim was not built from cmd/clipfan-shim" >&2
  exit 1
}
grep -q "./cmd/clipfan-shim" "$repo/dist/clipfan-shim-linux-arm64" || {
  echo "linux arm64 shim was not built from cmd/clipfan-shim" >&2
  exit 1
}

grep -qx "x86_64-apple-macos13.0" "$FAKE_SWIFTC_LOG" || {
  echo "missing x86_64 helper build" >&2
  exit 1
}
grep -qx "arm64-apple-macos13.0" "$FAKE_SWIFTC_LOG" || {
  echo "missing arm64 helper build" >&2
  exit 1
}

for f in "${go_payloads[@]}"; do
  rm -f "$f"
done

if CLIPFAN_TEST_HELPER_ONLY=1 CLIPFAN_BUILD_HELPER_ONLY=1 CLIPFAN_VERIFY_ONLY=1 bash "$repo/dist/build-all.sh" 2> "$tmp/verify.err"; then
  echo "incomplete release payload passed verification" >&2
  exit 1
fi

grep -q "missing executable payload: dist/clipfan-darwin-amd64" "$tmp/verify.err" || {
  cat "$tmp/verify.err" >&2
  exit 1
}
