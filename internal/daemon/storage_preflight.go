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
		Required: releaseflags.ConfigV2WriteEnabled,
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
	if results, err := p.checkStateParentBeforeCreate(configRoot, stateRoot); err != nil {
		return storagePreflightError(results, err)
	}
	if err := ensureStoragePreflightRoot(stateRoot); err != nil {
		return storagePreflightPrepareError(stateRoot, err)
	}
	results, err := p.Checker.CheckRoots(storagecheck.RuntimeRoots(configRoot, stateRoot))
	if err == nil {
		return nil
	}
	return storagePreflightError(results, err)
}

func (p StoragePreflightPolicy) checkStateParentBeforeCreate(configRoot string, stateRoot string) ([]storagecheck.Result, error) {
	if _, err := os.Lstat(stateRoot); err == nil {
		return nil, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %v", storagecheck.ErrStorageCheckInconclusive, err)
	}
	parent, err := nearestExistingParent(stateRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", storagecheck.ErrStorageCheckInconclusive, err)
	}
	return p.Checker.CheckRoots([]storagecheck.Root{
		{Role: storagecheck.RootConfig, Path: configRoot},
		{Role: storagecheck.RootState, Path: parent},
	})
}

func storagePreflightError(results []storagecheck.Result, err error) error {
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

func storagePreflightPrepareError(stateRoot string, err error) error {
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

func nearestExistingParent(path string) (string, error) {
	candidate := filepath.Clean(path)
	for {
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", fmt.Errorf("no existing parent for %s", path)
		}
		if info, err := os.Lstat(parent); err == nil {
			if !info.Mode().IsDir() {
				return "", fmt.Errorf("parent %s is not a directory", parent)
			}
			return parent, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		candidate = parent
	}
}
