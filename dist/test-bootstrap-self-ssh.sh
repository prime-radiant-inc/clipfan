#!/usr/bin/env bash
# Tests bootstrap-self-ssh.sh against a fake $HOME: verifies it (1) creates
# ~/.ssh with safe perms, (2) generates a default key when none exists,
# (3) is idempotent (re-running doesn't duplicate the authorized_keys line),
# (4) is best-effort (always exits 0 even when self-SSH can't be verified).
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
fails=0
check() { # desc expected actual
    if [[ "$2" == "$3" ]]; then echo "ok   - $1"; else echo "FAIL - $1 (want $2 got $3)" >&2; fails=$((fails+1)); fi
}

fake_home=$(mktemp -d)
trap 'rm -rf "$fake_home"' EXIT

run_bootstrap() { HOME="$fake_home" bash "$here/bootstrap-self-ssh.sh"; }

# 1. First run: ~/.ssh + key + authorized_keys should all appear.
run_bootstrap >/dev/null 2>&1
check "~/.ssh created" yes "$([[ -d "$fake_home/.ssh" ]] && echo yes || echo no)"
check "~/.ssh perms 700" "$(stat -f '%Lp' "$fake_home/.ssh")" "700"
check "default key generated" yes "$([[ -f "$fake_home/.ssh/id_ed25519" ]] && echo yes || echo no)"
check "authorized_keys created" yes "$([[ -f "$fake_home/.ssh/authorized_keys" ]] && echo yes || echo no)"
check "authorized_keys perms 600" "$(stat -f '%Lp' "$fake_home/.ssh/authorized_keys")" "600"

lines_after_first=$(wc -l < "$fake_home/.ssh/authorized_keys" | tr -d ' ')

# 2. Second run: idempotent — the pubkey line must not be appended again.
run_bootstrap >/dev/null 2>&1
lines_after_second=$(wc -l < "$fake_home/.ssh/authorized_keys" | tr -d ' ')
check "re-run is idempotent (no duplicate line)" "$lines_after_first" "$lines_after_second"

# 3. The single authorized_keys line must match the pubkey.
pub=$(cat "$fake_home/.ssh/id_ed25519.pub")
auth_line=$(head -1 "$fake_home/.ssh/authorized_keys")
check "authorized_keys holds the pubkey" "$pub" "$auth_line"

# 4. Best-effort: the script exits 0 even though self-SSH to the fake home's
#    nonexistent sshd can't possibly succeed.
if run_bootstrap >/dev/null 2>&1; then check "exits 0 even without sshd" 0 0; else check "exits 0 even without sshd" 0 1; fi

if [[ $fails -eq 0 ]]; then echo "ALL PASS"; else echo "$fails FAILED" >&2; exit 1; fi
