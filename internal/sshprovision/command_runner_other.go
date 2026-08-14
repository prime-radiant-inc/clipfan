//go:build !windows

package sshprovision

import "os"

func sanitizedSSHEnv() []string {
	return os.Environ()
}
