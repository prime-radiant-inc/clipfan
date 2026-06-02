package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
	"github.com/prime-radiant-inc/clipfan/internal/storagecheck"
)

type StoragePreflightPolicy struct {
	Required   bool
	ConfigRoot string
	StateRoot  string
	Checker    storagecheck.Checker
}

type StoragePreflightError struct {
	Code    storagecheck.Code
	Results []storagecheck.Result
	Err     error
}

func (e *StoragePreflightError) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code)
}

func (e *StoragePreflightError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func DefaultStoragePreflightPolicy() StoragePreflightPolicy {
	return StoragePreflightPolicy{
		Required: releaseflags.PeerHTTPRuntimeDisabled || releaseflags.ConfigV2WriteEnabled,
	}
}

func (p StoragePreflightPolicy) check() error {
	if !p.Required {
		return nil
	}
	configRoot := p.ConfigRoot
	if configRoot == "" {
		configRoot = filepath.Dir(config.Path())
	}
	stateRoot := p.StateRoot
	if stateRoot == "" {
		stateRoot = config.StateDir()
	}
	if err := ensureStoragePreflightRoot(stateRoot); err != nil {
		return &StoragePreflightError{
			Code: storagecheck.CodeStorageCheckInconclusive,
			Results: []storagecheck.Result{{
				Role:           storagecheck.RootState,
				Path:           stateRoot,
				NormalizedPath: filepath.Clean(stateRoot),
				Code:           storagecheck.CodeStorageCheckInconclusive,
				StorageClass:   storagecheck.ClassInconclusive,
				Reason:         "state_root_prepare_failed",
			}},
			Err: fmt.Errorf("%w: %v", storagecheck.ErrStorageCheckInconclusive, err),
		}
	}
	results, err := p.Checker.CheckRoots(storagecheck.RuntimeRoots(configRoot, stateRoot))
	if err == nil {
		return nil
	}
	code := storagecheck.CodeStorageCheckInconclusive
	if errors.Is(err, storagecheck.ErrUnsupportedRuntimeStorage) {
		code = storagecheck.CodeUnsupportedRuntimeStorage
	}
	return &StoragePreflightError{
		Code:    code,
		Results: results,
		Err:     err,
	}
}

func ensureStoragePreflightRoot(root string) error {
	if _, err := os.Lstat(root); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsDir() {
		return fmt.Errorf("storage root %s is not a real directory", root)
	}
	return os.Chmod(root, 0o700)
}
