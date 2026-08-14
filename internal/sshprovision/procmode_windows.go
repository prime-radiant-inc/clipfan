//go:build windows

package sshprovision

import (
	"os/exec"
	"syscall"
)

// windowsCreateNoWindow is CREATE_NO_WINDOW: the child may initialize the
// console subsystem with an invisible conhost instead of trying to attach to
// (or flash) a console window.
const windowsCreateNoWindow = 0x08000000

// applyConsoleSpawnMode configures a child process so console-subsystem
// executables (ssh.exe, ssh-keyscan.exe) can start from console-less parents
// — scheduled tasks, the tray app, or processes sshd spawns without an
// interactive console. Without this, the child's console DLL initialization
// fails with 0xc0000142 (STATUS_DLL_INIT_FAILED): the process starts, may
// even deliver output, then dies or never exits — which is what wedged
// console-less add-peer runs at the ssh-apply-direct-config step.
func ApplyConsoleSpawnMode(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windowsCreateNoWindow,
	}
}
