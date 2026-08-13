#!/usr/bin/env bash
# bootstrap-self-ssh.sh — ensure this host can authenticate to itself over SSH
# using its own default key. clipfan's mesh provisioning requires this: the
# orchestrator (the Mac app) SSHes to every host in the pair — including the
# Mac itself — to install known_hosts entries BEFORE it installs any
# authorized_keys (pair_executor.go: UpsertKnownHostPin runs before
# InstallAuthorizedKey). That ordering only works if the host's own pubkey is
# already in its own ~/.ssh/authorized_keys.
#
# Most people never SSH into their own laptop, so this is rarely set up out of
# the box. This script closes that gap, idempotently:
#   1. ensures ~/.ssh exists with safe perms,
#   2. generates ~/.ssh/id_ed25519 if no default key exists,
#   3. appends that pubkey to ~/.ssh/authorized_keys (deduped),
#   4. verifies self-SSH end-to-end; if it fails (typically because Remote
#      Login / sshd is off on macOS), prints exact enable instructions.
#
# Best-effort: never exits non-zero (a warning is printed instead), so it can't
# abort a wider install. Safe to re-run.
set -uo pipefail

ssh_dir="${HOME}/.ssh"
key="${ssh_dir}/id_ed25519"
auth="${ssh_dir}/authorized_keys"

mkdir -p "$ssh_dir" 2>/dev/null || true
chmod 700 "$ssh_dir" 2>/dev/null || true

# 1. Ensure a default keypair. Use ed25519 with no passphrase so provisioning
#    (which runs non-interactively under BatchMode=yes) can use it directly.
if [[ ! -f "$key" ]]; then
    echo ">> Generating SSH keypair: $key"
    if ssh-keygen -t ed25519 -N "" -C "clipfan@$(hostname -s 2>/dev/null || echo host)" -f "$key" >/dev/null 2>&1; then
        :
    else
        echo "bootstrap-self-ssh: could not generate $key (ssh-keygen failed)" >&2
    fi
fi

# 2. Ensure the pubkey is authorized for self-login (idempotent append).
if [[ -f "$key.pub" ]]; then
    pub=$(cat "$key.pub")
    touch "$auth" 2>/dev/null || true
    chmod 600 "$auth" 2>/dev/null || true
    if ! grep -qF "$pub" "$auth" 2>/dev/null; then
        echo ">> Authorizing $key.pub for self-SSH"
        printf '%s\n' "$pub" >> "$auth"
    fi
fi

# 3. End-to-end verify: can this host actually SSH to itself? This catches
#    "sshd not running" (common on a fresh macOS) as well as pubkey-auth being
#    disabled. Do not enable sshd for the user (that's a System Settings
#    decision and needs sudo); just tell them exactly what to do.
verify_self_ssh() {
    ssh \
        -o BatchMode=yes \
        -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o GlobalKnownHostsFile=/dev/null \
        -o ConnectTimeout=5 \
        -o LogLevel=ERROR \
        "$USER@127.0.0.1" 'true' >/dev/null 2>&1
}

if verify_self_ssh; then
    echo ">> self-SSH OK"
else
    echo "bootstrap-self-ssh: self-SSH check failed." >&2
    case "$(uname -s)" in
        Darwin)
            cat >&2 <<'EOF'
   Remote Login (the macOS SSH server) is probably off, or public-key auth is
   disabled. clipfan's mesh sync needs peers (and the Mac itself) to reach the
   Mac over SSH. Enable Remote Login:
     System Settings → General → Sharing → Remote Login
   or:
     sudo systemsetup -setremotelogin on
   Then re-run the clipfan installer or this script to confirm.
EOF
            ;;
        *)
            echo "   Is sshd running and PubkeyAuthentication enabled on this host?" >&2
            ;;
    esac
fi

exit 0
