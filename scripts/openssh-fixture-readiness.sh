#!/usr/bin/env bash
set -euo pipefail

target="${1:-}"
if [ "$target" != "macos" ] && [ "$target" != "ubuntu" ]; then
  echo "usage: $0 macos|ubuntu" >&2
  exit 2
fi

required="${CLIPFAN_OPENSSH_FIXTURE_REQUIRED:-0}"
artifact="${CLIPFAN_OPENSSH_FIXTURE_ARTIFACT:-openssh-fixture-${target}.json}"
tmp="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/clipfan-openssh-fixture.$$"
mkdir -p "$tmp"

sshd_pid=""
daemon_probe_pid=""
started_sshd_with_sudo=0
cleanup_completed=0
created_users=()

stop_daemon_probe_listener() {
  if [ -z "$daemon_probe_pid" ]; then
    return 0
  fi
  local pid="$daemon_probe_pid"
  daemon_probe_pid=""
  if kill -0 "$pid" >/dev/null 2>&1; then
    kill "$pid" >/dev/null 2>&1 || true
  fi
  wait "$pid" >/dev/null 2>&1 || true
}

process_is_alive() {
  local pid="$1"
  if [ "$started_sshd_with_sudo" = "1" ]; then
    sudo kill -0 "$pid" >/dev/null 2>&1
  else
    kill -0 "$pid" >/dev/null 2>&1
  fi
}

cleanup() {
  if [ "$cleanup_completed" = "1" ]; then
    return 0
  fi
  local status=0
  if [ -n "$daemon_probe_pid" ]; then
    stop_daemon_probe_listener || status=1
  fi
  if [ -n "$sshd_pid" ]; then
    kill "$sshd_pid" >/dev/null 2>&1 || true
    wait "$sshd_pid" >/dev/null 2>&1 || true
    if kill -0 "$sshd_pid" >/dev/null 2>&1; then
      status=1
    fi
  fi
  if [ -f "$tmp/sshd.pid" ]; then
    local server_pid
    server_pid="$(cat "$tmp/sshd.pid" 2>/dev/null || true)"
    if [ -n "$server_pid" ] && process_is_alive "$server_pid"; then
      if [ "$started_sshd_with_sudo" = "1" ]; then
        sudo kill "$server_pid" >/dev/null 2>&1 || true
      else
        kill "$server_pid" >/dev/null 2>&1 || true
      fi
      sleep 0.1
      if process_is_alive "$server_pid"; then
        printf 'fixture cleanup failed to stop sshd pid: %s\n' "$server_pid" >&2
        status=1
      fi
    fi
  fi
  if [ "${#created_users[@]}" -gt 0 ]; then
    for user in "${created_users[@]}"; do
      if command -v dscl >/dev/null 2>&1; then
        sudo dscl . -delete "/Users/$user" >/dev/null 2>&1 || true
        if sudo dscl . -read "/Users/$user" >/dev/null 2>&1; then
          printf 'fixture cleanup failed to delete user: %s\n' "$user" >&2
          status=1
        fi
      elif command -v userdel >/dev/null 2>&1; then
        sudo userdel -r "$user" >/dev/null 2>&1 || true
        if id "$user" >/dev/null 2>&1; then
          printf 'fixture cleanup failed to delete user: %s\n' "$user" >&2
          status=1
        fi
      fi
    done
  fi
  if [ "${#created_users[@]}" -gt 0 ] || [ "$started_sshd_with_sudo" = "1" ]; then
    sudo rm -rf "$tmp" >/dev/null 2>&1 || status=1
  else
    rm -rf "$tmp" || status=1
  fi
  if [ -e "$tmp" ]; then
    status=1
  fi
  if [ "$status" -eq 0 ]; then
    cleanup_completed=1
  fi
  return "$status"
}
trap 'cleanup || true' EXIT

json_escape() {
  if command -v perl >/dev/null 2>&1; then
    printf '%s' "$1" | perl -0pe 's/\\/\\\\/g; s/"/\\"/g; s/\n/\\n/g; s/\r/\\r/g; s/\t/\\t/g; s/([\x00-\x08\x0b\x0c\x0e-\x1f])/sprintf("\\u%04x", ord($1))/ge'
    return 0
  fi
  if command -v python3 >/dev/null 2>&1; then
    JSON_ESCAPE_VALUE="$1" python3 -c 'import json, os; print(json.dumps(os.environ["JSON_ESCAPE_VALUE"])[1:-1], end="")'
    return 0
  fi
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

write_status() {
  local status="$1"
  local reason="$2"
  local escaped_reason
  escaped_reason="$(json_escape "$reason")"
  mkdir -p "$(dirname "$artifact")"
  cat > "$artifact" <<JSON
{
  "target": "$target",
  "status": "$status",
  "reason": "$escaped_reason"
}
JSON
}

unavailable() {
  local reason="$1"
  local code="ssh_fixture_unavailable"
  if [ "$target" = "macos" ]; then
    code="macos_ssh_fixture_unavailable"
  fi
  write_status "$code" "$reason"
  echo "$code: $reason" >&2
  if [ "$required" = "1" ]; then
    exit 1
  fi
  exit 0
}

finish_success() {
  local reason="$1"
  if ! cleanup; then
    unavailable "fixture cleanup failed"
  fi
  write_status "ok" "$reason"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || unavailable "missing required command: $1"
}

run_with_timeout() {
  local seconds="$1"
  shift
  if command -v perl >/dev/null 2>&1; then
    perl -e 'alarm shift @ARGV; exec @ARGV' "$seconds" "$@"
    return $?
  fi

  "$@" &
  local child="$!"
  local waited=0
  while kill -0 "$child" >/dev/null 2>&1; do
    if [ "$waited" -ge "$seconds" ]; then
      kill "$child" >/dev/null 2>&1 || true
      wait "$child" >/dev/null 2>&1 || true
      return 124
    fi
    sleep 1
    waited=$((waited + 1))
  done
  wait "$child"
}

pick_port() {
  python3 - <<'PY'
import socket
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

make_keys() {
  ssh-keygen -q -t ed25519 -N "" -f "$tmp/client_key" -C "clipfan-fixture-client" >/dev/null
  ssh-keygen -q -t ed25519 -N "" -f "$tmp/host_key" -C "clipfan-fixture-host" >/dev/null
}

forced_command_path() {
  local path="$tmp/clipfan-forced-command.sh"
  cat > "$path" <<'SH'
#!/bin/sh
printf 'clipfan-fixture:%s:%s\n' "$USER" "$SSH_ORIGINAL_COMMAND"
SH
  chmod 0755 "$path"
  printf '%s\n' "$path"
}

install_authorized_key() {
  local home="$1"
  local owner="$2"
  local forced="$3"
  local pub
  pub="$(cat "$tmp/client_key.pub")"
  if [ -n "$owner" ]; then
    sudo mkdir -p "$home/.ssh"
    printf 'command="%s",no-agent-forwarding,no-X11-forwarding,no-port-forwarding,no-pty %s\n' "$forced" "$pub" | sudo tee "$home/.ssh/authorized_keys" >/dev/null
    sudo chmod 0700 "$home/.ssh"
    sudo chmod 0600 "$home/.ssh/authorized_keys"
    sudo chown -R "$owner" "$home/.ssh"
  else
    mkdir -p "$home/.ssh"
    printf 'command="%s",no-agent-forwarding,no-X11-forwarding,no-port-forwarding,no-pty %s\n' "$forced" "$pub" > "$home/.ssh/authorized_keys"
    chmod 0700 "$home/.ssh"
    chmod 0600 "$home/.ssh/authorized_keys"
  fi
}

write_sshd_config() {
  local port="$1"
  local authorized_keys="$2"
  cat > "$tmp/sshd_config" <<EOF
Port $port
ListenAddress 127.0.0.1
HostKey $tmp/host_key
PidFile $tmp/sshd.pid
AuthorizedKeysFile $authorized_keys
PasswordAuthentication no
KbdInteractiveAuthentication no
ChallengeResponseAuthentication no
PubkeyAuthentication yes
PermitRootLogin no
AllowTcpForwarding no
X11Forwarding no
PermitTTY no
StrictModes no
LogLevel VERBOSE
EOF
}

start_sshd() {
  local sshd_path="$1"
  local as_root="$2"
  local port="$3"
  if [ "$as_root" = "1" ]; then
    started_sshd_with_sudo=1
    sudo "$sshd_path" -D -e -f "$tmp/sshd_config" >"$tmp/sshd.log" 2>&1 &
  else
    "$sshd_path" -D -e -f "$tmp/sshd_config" >"$tmp/sshd.log" 2>&1 &
  fi
  sshd_pid="$!"
  for _ in $(seq 1 50); do
    if kill -0 "$sshd_pid" >/dev/null 2>&1 && nc -z 127.0.0.1 "$port" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  cat "$tmp/sshd.log" >&2 || true
  unavailable "sshd did not become ready on localhost port $port"
}

run_ssh_probe() {
  local user="$1"
  local port="$2"
  local expected_prefix="clipfan-fixture:$user:"
  local out
  out="$(ssh \
    -o BatchMode=yes \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile="$tmp/known_hosts" \
    -o GlobalKnownHostsFile=/dev/null \
    -o LogLevel=ERROR \
    -i "$tmp/client_key" \
    -p "$port" \
    "$user@127.0.0.1" \
    "clipfan-fixture-version" 2>"$tmp/ssh.err")" || {
      cat "$tmp/ssh.err" >&2 || true
      unavailable "regular SSH probe failed"
    }
  case "$out" in
    "$expected_prefix"*) ;;
    *)
      printf 'unexpected forced-command output: %s\n' "$out" >&2
      unavailable "forced-command authorized_keys fixture did not execute"
      ;;
  esac
}

start_daemon_probe_listener() {
  local ready="$tmp/daemon7853.ready"
  local hit="$tmp/daemon7853.hit"
  rm -f "$ready" "$hit"
  python3 - "$ready" "$hit" <<'PY' &
import socket
import sys

ready_path = sys.argv[1]
hit_path = sys.argv[2]

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        sock.bind(("127.0.0.1", 7853))
    except OSError as exc:
        print(f"bind 127.0.0.1:7853 failed: {exc}", file=sys.stderr)
        sys.exit(2)
    sock.listen(1)
    with open(ready_path, "w", encoding="utf-8") as ready_file:
        ready_file.write("ready\n")
    sock.settimeout(10)
    try:
        conn, _addr = sock.accept()
    except socket.timeout:
        sys.exit(0)
    with conn:
        with open(hit_path, "w", encoding="utf-8") as hit_file:
            hit_file.write("hit\n")
        try:
            conn.sendall(b"clipfan-daemon-probe\n")
        except OSError:
            pass
PY
  daemon_probe_pid="$!"
  for _ in $(seq 1 50); do
    if [ -f "$ready" ]; then
      return 0
    fi
    if ! kill -0 "$daemon_probe_pid" >/dev/null 2>&1; then
      wait "$daemon_probe_pid" >/dev/null 2>&1 || true
      unavailable "daemon TCP probe port 7853 is unavailable"
    fi
    sleep 0.1
  done
  unavailable "daemon TCP probe listener did not become ready"
}

run_forward_block_probe() {
  local user="$1"
  local port="$2"
  start_daemon_probe_listener
  if printf 'clipfan-forward-probe\n' | run_with_timeout 8 ssh \
    -o BatchMode=yes \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile="$tmp/known_hosts" \
    -o GlobalKnownHostsFile=/dev/null \
    -o LogLevel=ERROR \
    -i "$tmp/client_key" \
    -p "$port" \
    -W "127.0.0.1:7853" \
    "$user@127.0.0.1" >/dev/null 2>"$tmp/forward.err"; then
    unavailable "fixture SSH unexpectedly allowed TCP forwarding toward daemon port 7853"
  fi
  if [ -f "$tmp/daemon7853.hit" ]; then
    unavailable "fixture SSH delivered traffic to daemon TCP port 7853"
  fi
  if ! stop_daemon_probe_listener; then
    unavailable "daemon TCP probe listener cleanup failed"
  fi
}

create_macos_user() {
  local user="$1"
  local home="$2"
  local uid
  uid=$((60000 + RANDOM))
  while dscl . -search /Users UniqueID "$uid" >/dev/null 2>&1; do
    uid=$((uid + 1))
  done
  sudo dscl . -create "/Users/$user"
  sudo dscl . -create "/Users/$user" UserShell /bin/bash
  sudo dscl . -create "/Users/$user" RealName "$user"
  sudo dscl . -create "/Users/$user" UniqueID "$uid"
  sudo dscl . -create "/Users/$user" PrimaryGroupID 20
  sudo dscl . -create "/Users/$user" NFSHomeDirectory "$home"
  sudo dscl . -passwd "/Users/$user" "$(openssl rand -base64 18)"
  sudo mkdir -p "$home"
  sudo chown "$user":staff "$home"
  created_users+=("$user")
}

run_macos_fixture() {
  [ "$(uname -s)" = "Darwin" ] || unavailable "macOS fixture requested on non-Darwin runner"
  require_cmd ssh
  require_cmd sshd
  require_cmd ssh-keygen
  require_cmd nc
  require_cmd python3
  require_cmd dscl
  require_cmd openssl
  sudo -n true >/dev/null 2>&1 || unavailable "passwordless sudo is required to create temporary non-admin fixture users"

  make_keys
  port="$(pick_port)"
  local user_a="clipfanfixa$$"
  local user_b="clipfanfixb$$"
  create_macos_user "$user_a" "$tmp/$user_a"
  create_macos_user "$user_b" "$tmp/$user_b"
  forced="$(forced_command_path)"
  install_authorized_key "$tmp/$user_a" "$user_a:staff" "$forced"
  install_authorized_key "$tmp/$user_b" "$user_b:staff" "$forced"
  write_sshd_config "$port" ".ssh/authorized_keys"
  start_sshd "$(command -v sshd)" 1 "$port"
  run_ssh_probe "$user_a" "$port"
  run_ssh_probe "$user_b" "$port"
  run_forward_block_probe "$user_a" "$port"
  run_forward_block_probe "$user_b" "$port"
  finish_success "macOS OpenSSH fixture readiness passed"
}

run_ubuntu_fixture() {
  [ "$(uname -s)" = "Linux" ] || unavailable "Ubuntu fixture requested on non-Linux runner"
  require_cmd ssh
  require_cmd sshd
  require_cmd ssh-keygen
  require_cmd nc
  require_cmd python3
  sudo -n true >/dev/null 2>&1 || unavailable "passwordless sudo is required to start isolated OpenSSH server"

  make_keys
  port="$(pick_port)"
  forced="$(forced_command_path)"
  install_authorized_key "$tmp/home" "" "$forced"
  write_sshd_config "$port" "$tmp/home/.ssh/authorized_keys"
  start_sshd "$(command -v sshd)" 1 "$port"
  run_ssh_probe "$(id -un)" "$port"
  run_forward_block_probe "$(id -un)" "$port"
  finish_success "Ubuntu OpenSSH fixture readiness passed"
}

case "$target" in
  macos) run_macos_fixture ;;
  ubuntu) run_ubuntu_fixture ;;
esac
