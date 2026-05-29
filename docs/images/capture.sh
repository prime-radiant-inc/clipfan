#!/usr/bin/env bash
# Capture the two README screenshots. Run from anywhere; writes into docs/images/.
# Uses interactive window capture: when prompted, click the target window.
#
#   1. Command panel  — press ⇧⌘V first so the panel is open, then run this and
#      click the panel.
#   2. Menubar fleet  — click the clipfan menubar icon so the dropdown is open,
#      then run this and click the dropdown.
#
# Tip: `screencapture -iW` waits for you to click a window and grabs just that
# window with its shadow. Run each capture separately.
set -euo pipefail
cd "$(dirname "$0")"

shot() { # name prompt
    echo ">>> $2"
    echo "    (clicking captures that window; Esc cancels)"
    screencapture -iW -o "$1.png"
    echo "    wrote docs/images/$1.png"
}

case "${1:-all}" in
    panel)   shot command-panel "Open the panel with ⇧⌘V, then click it" ;;
    menubar) shot menubar-fleet "Open the clipfan menubar dropdown, then click it" ;;
    all)
        shot command-panel "Open the panel with ⇧⌘V, then click it"
        shot menubar-fleet "Open the clipfan menubar dropdown, then click it"
        ;;
    *) echo "usage: capture.sh [panel|menubar|all]" >&2; exit 1 ;;
esac
