//go:build linux

package storagecheck

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func DefaultProbe(path string) (Fact, error) {
	mounts, err := readLinuxMountInfo("/proc/self/mountinfo")
	if err != nil {
		return Fact{}, err
	}
	path = normalizePath(path)
	var best Fact
	for _, mount := range mounts {
		if mount.MountPoint == "" {
			continue
		}
		if pathWithinMount(path, mount.MountPoint) {
			if len(mount.MountPoint) > len(best.MountPoint) {
				best = mount
			}
		}
	}
	if best.MountPoint == "" {
		return Fact{}, fmt.Errorf("mount not found for %s", path)
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return Fact{}, err
	}
	best.FilesystemMagic = int64(stat.Type)
	if isKnownLocalFilesystem(best.FilesystemType) || isKnownLocalFilesystemMagic(best.FilesystemMagic) {
		local := true
		best.Local = &local
	}
	if isUnsupportedFilesystem(best.FilesystemType) || isUnsupportedFilesystemMagic(best.FilesystemMagic) {
		local := false
		best.Local = &local
	}
	return best, nil
}

func readLinuxMountInfo(path string) ([]Fact, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var mounts []Fact
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, " - ")
		if len(parts) != 2 {
			continue
		}
		left := strings.Fields(parts[0])
		right := strings.Fields(parts[1])
		if len(left) < 5 || len(right) < 1 {
			continue
		}
		mounts = append(mounts, Fact{
			MountPoint:     unescapeMountInfo(left[4]),
			FilesystemType: right[0],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return mounts, nil
}

func pathWithinMount(path string, mountPoint string) bool {
	if mountPoint == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == mountPoint || strings.HasPrefix(path, mountPoint+"/")
}

func unescapeMountInfo(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+3 < len(value) {
			if n, err := strconv.ParseUint(value[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		out.WriteByte(value[i])
	}
	return out.String()
}
