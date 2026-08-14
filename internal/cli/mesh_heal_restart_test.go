package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

func TestRestartShellIsPlatformAgnostic(t *testing.T) {
	script := restartShell(501, "/Users/jesse/.local/bin/clipfan")

	for _, want := range []string{
		"systemctl --user restart clipfan.service",
		"launchctl kickstart -k",
		"com.primeradiant.clipfan",
		"id -u",
		"nohup '/Users/jesse/.local/bin/clipfan' daemon",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}

	// The nohup fallback must be the last meaningful line — reachable on BOTH
	// platforms after the systemctl and launchctl blocks fall through.
	trimmed := strings.TrimSpace(script)
	lastLine := trimmed[strings.LastIndex(trimmed, "\n")+1:]
	if !strings.HasPrefix(lastLine, "nohup ") {
		t.Fatalf("script must end with the nohup fallback, last line: %q", lastLine)
	}

	// It must not branch on a platform argument or probe.
	if strings.Contains(script, "$platform") || strings.Contains(script, "uname") {
		t.Fatalf("script should not branch on platform:\n%s", script)
	}
}

func TestRestartShellBakesKnownUIDFallback(t *testing.T) {
	script := restartShell(1234, "/clipfan")
	if !strings.Contains(script, "1234") {
		t.Fatalf("script should bake the report-provided uid as a fallback:\n%s", script)
	}
}

func TestRestartShellSafelyQuotesInstallPath(t *testing.T) {
	// An install path with a single quote must not break out of the nohup line.
	script := restartShell(501, "/weird/cli'pfan")
	if !strings.Contains(script, `'/weird/cli'\''pfan'`) {
		t.Fatalf("install path not safely single-quoted:\n%s", script)
	}
}

func TestRestartDaemonRunsScriptOverRegularSSH(t *testing.T) {
	runner := &fakeRosterRunner{}
	err := restartDaemon(context.Background(), runner, sshprovision.DirectPairHost{
		SSHUser: "jesse", SSHHost: "host.example", SSHPort: 22,
	}, "/Users/jesse/.ssh/known_hosts", 501, "/Users/jesse/.local/bin/clipfan")
	if err != nil {
		t.Fatalf("restartDaemon: %v", err)
	}
	if !strings.Contains(runner.lastCommand, "jesse@host.example") {
		t.Fatalf("did not SSH to the admin host: %q", runner.lastCommand)
	}
	if !strings.Contains(runner.lastCommand, "clipfan.service") || !strings.Contains(runner.lastCommand, "\"sh\" \"-c\"") {
		t.Fatalf("did not run the restart script via sh -c: %q", runner.lastCommand)
	}
}
