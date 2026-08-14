//go:build !darwin && !linux && !windows

package storagecheck

import "fmt"

func DefaultProbe(path string) (Fact, error) {
	return Fact{}, fmt.Errorf("storage probe unsupported on %s", currentPlatform())
}
