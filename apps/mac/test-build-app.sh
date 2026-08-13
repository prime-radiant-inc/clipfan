#!/usr/bin/env bash
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
app="$here/build/Clipfan.app"

bash "$here/build-app.sh"

[[ -d "$app" ]] || { echo "missing app bundle: $app" >&2; exit 1; }
[[ -d "$app/Contents/Frameworks/Sparkle.framework" ]] || {
    echo "missing embedded Sparkle.framework" >&2
    exit 1
}
[[ -d "$app/Contents/Resources/KeyboardShortcuts_KeyboardShortcuts.bundle" ]] || {
    echo "missing embedded KeyboardShortcuts resource bundle" >&2
    exit 1
}

echo "ALL PASS"
