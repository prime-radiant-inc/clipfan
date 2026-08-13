//go:build windows

package storagecheck

import "strings"

// DefaultProbe classifies a Windows path. A path under a drive letter
// (e.g. C:\Users\…) is treated as a local NTFS volume; a UNC path
// (\\server\share) is treated as network/remote so clipfan refuses to keep
// its state on a network share.
func DefaultProbe(path string) (Fact, error) {
	local := !strings.HasPrefix(path, `\\`)
	return Fact{FilesystemType: "ntfs", Local: &local}, nil
}
