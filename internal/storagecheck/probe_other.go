//go:build !darwin && !linux

package storagecheck

import "fmt"

func DefaultProbe(path string) (Fact, error) {
	return Fact{}, fmt.Errorf("storage probe unsupported on %s", currentPlatform())
}
