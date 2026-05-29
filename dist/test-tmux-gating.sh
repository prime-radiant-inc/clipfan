#!/usr/bin/env bash
# Tests the tmux-gating decision in install.sh by sourcing it (the source guard
# stops the imperative installer body from running) and exercising want_tmux
# under each mode with tmux present/absent on PATH.
# -e is intentionally omitted: want_tmux returns 1 on "skip" and must not abort the script.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
fails=0
check() { # desc expected actual
    if [[ "$2" == "$3" ]]; then echo "ok   - $1"; else echo "FAIL - $1 (want $2 got $3)"; fails=$((fails+1)); fi
}

# Source install.sh; the source guard must prevent the installer from running.
# shellcheck disable=SC1090
source "$here/install.sh"

# The source guard must have stopped install.sh before its imperative body,
# which is what sets $goos. If goos is set, the guard regressed.
check "source guard suppressed installer body" "" "${goos:-}"

# Fake PATH with a tmux binary present.
fake_with=$(mktemp -d); : > "$fake_with/tmux"; chmod +x "$fake_with/tmux"
# Fake PATH with no tmux.
fake_without=$(mktemp -d)

# mode=off -> never, regardless of tmux presence
TMUX_MODE=off PATH="$fake_with" want_tmux; check "off + tmux present -> skip" 1 "$?"

# mode=on -> always
TMUX_MODE=on PATH="$fake_without" want_tmux; check "on + no tmux -> run" 0 "$?"

# mode=auto -> follows tmux presence
TMUX_MODE=auto PATH="$fake_with" want_tmux; check "auto + tmux present -> run" 0 "$?"
TMUX_MODE=auto PATH="$fake_without" want_tmux; check "auto + no tmux -> skip" 1 "$?"

rm -rf "$fake_with" "$fake_without"
if [[ $fails -eq 0 ]]; then echo "ALL PASS"; else echo "$fails FAILED"; exit 1; fi
