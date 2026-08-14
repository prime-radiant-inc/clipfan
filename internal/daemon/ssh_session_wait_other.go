//go:build !windows

package daemon

func (p execSSHStartedProcess) Wait() error {
	return p.cmd.Wait()
}
