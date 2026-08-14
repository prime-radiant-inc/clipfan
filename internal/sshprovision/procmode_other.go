//go:build !windows

package sshprovision

import "os/exec"

// ApplyConsoleSpawnMode is a no-op on Unix: children inherit the parent's
// normal process context and need no console handling.
func ApplyConsoleSpawnMode(*exec.Cmd) {}
