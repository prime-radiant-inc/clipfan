//go:build darwin

package storagecheck

import "syscall"

// MNT_LOCAL is not exported by every Go Darwin target, but Darwin statfs uses
// this stable mount flag to mark local volumes.
const darwinMNTLocal = 0x00001000

func DefaultProbe(path string) (Fact, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return Fact{}, err
	}
	local := stat.Flags&darwinMNTLocal != 0
	return Fact{
		FilesystemType: int8ArrayString(stat.Fstypename[:]),
		MountPoint:     int8ArrayString(stat.Mntonname[:]),
		Local:          &local,
	}, nil
}

func int8ArrayString(chars []int8) string {
	bytes := make([]byte, 0, len(chars))
	for _, ch := range chars {
		if ch == 0 {
			break
		}
		bytes = append(bytes, byte(ch))
	}
	return string(bytes)
}
