//go:build !darwin && !linux && !windows

package storagecheck

import "fmt"

func LocalSmokeCheck(root string) error {
	return fmt.Errorf("storage smoke check unsupported on %s", currentPlatform())
}
