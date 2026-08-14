//go:build windows

package daemon

import "time"

// windowsSSHWaitGrace bounds how long the daemon waits for ssh.exe to exit
// after its stream ends. Win32-OpenSSH can hang at process exit when its
// stdio is connected to pipes (which the sync stream requires — the pipes are
// the transport), so after the grace period the process is killed to keep the
// reconnect loop moving. 10s is far above a healthy exit (sub-second).
const windowsSSHWaitGrace = 10 * time.Second

func (p execSSHStartedProcess) Wait() error {
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(windowsSSHWaitGrace):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		return <-done
	}
}
