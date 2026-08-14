//go:build windows

package sshprovision

import (
	"os"
	"strings"
)

// sanitizedSSHEnv returns the current environment with HOME corrected for
// child ssh.exe processes. When clipfan is launched from Git Bash / MSYS, the
// inherited HOME is an MSYS-form path ("/c/Users/Will Wade") that Windows
// OpenSSH cannot expand, breaking every "~/"-relative option (known_hosts,
// identity files) and hanging strict host-key checks. Windows ssh.exe uses
// HOME when set — so point it at the real profile directory.
func sanitizedSSHEnv() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return os.Environ()
	}
	env := os.Environ()
	out := make([]string, 0, len(env))
	replaced := false
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "HOME") {
			out = append(out, "HOME="+home)
			replaced = true
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, "HOME="+home)
	}
	return out
}
