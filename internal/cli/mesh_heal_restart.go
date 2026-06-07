package cli

import (
	"context"
	"strconv"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

// restartShell is the single platform-agnostic script mesh-heal runs on a host
// to restart its clipfan daemon after a config change. Ported from the Swift
// installer: try the Linux systemd user unit, then the macOS launchd agent
// (resolving the uid remotely via `id -u`, falling back to $UID and finally the
// report-provided uid), and unconditionally fall back to `nohup … daemon &` —
// reachable on both platforms when neither service manager is present. It does
// not branch on a platform argument; the remote shell self-selects.
func restartShell(uid int, installPath string) string {
	return `if command -v systemctl >/dev/null 2>&1; then
    systemctl --user daemon-reload >/dev/null 2>&1 || true
    systemctl --user enable clipfan.service >/dev/null 2>&1 || true
    systemctl --user restart clipfan.service >/dev/null 2>&1 && exit 0
fi
if command -v launchctl >/dev/null 2>&1; then
    user_uid="$(id -u 2>/dev/null || printf '%s' "${UID:-}")"
    [ -n "$user_uid" ] || user_uid=` + shellSingleQuote(strconv.Itoa(uid)) + `
    plist="$HOME/Library/LaunchAgents/com.primeradiant.clipfan.plist"
    launchctl enable "gui/$user_uid/com.primeradiant.clipfan" >/dev/null 2>&1 || true
    launchctl bootstrap "gui/$user_uid" "$plist" >/dev/null 2>&1 || \
        launchctl load "$plist" >/dev/null 2>&1 || true
    launchctl kickstart -k "gui/$user_uid/com.primeradiant.clipfan" >/dev/null 2>&1 && exit 0
fi
nohup ` + shellSingleQuote(installPath) + ` daemon >/tmp/clipfan-daemon.log 2>&1 &
`
}

// shellSingleQuote wraps s in POSIX single quotes, escaping any embedded single
// quote, so an attacker-influenced value (e.g. a peer's reported install path)
// cannot break out of the restart script.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// restartDaemon runs restartShell on a host over the user's regular SSH. adminHost
// is the original admin endpoint, uid and installPath come from the host's
// roster-read self-report.
func restartDaemon(ctx context.Context, runner sshprovision.CommandRunner, adminHost sshprovision.DirectPairHost, regularKnownHostsPath string, uid int, installPath string) error {
	command, err := sshprovision.RegularSSHShellCommand(sshprovision.RegularSSHShellSpec{
		User:           adminHost.SSHUser,
		Host:           adminHost.SSHHost,
		Port:           adminHost.SSHPort,
		KnownHostsPath: regularKnownHostsPath,
		Script:         restartShell(uid, installPath),
	})
	if err != nil {
		return err
	}
	_, err = runner.Run(ctx, command)
	return err
}
