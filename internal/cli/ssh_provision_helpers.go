package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
	"github.com/prime-radiant-inc/clipfan/internal/storagecheck"
)

func RunSSHEnsureSyncKey(args []string, stdout io.Writer, stderr io.Writer) error {
	return runSSHEnsureSyncKey(args, stdout, stderr, storagecheck.Checker{}, nil)
}

func runSSHEnsureSyncKey(args []string, stdout io.Writer, stderr io.Writer, checker storagecheck.Checker, generator config.SyncKeyGenerator) error {
	fs := flag.NewFlagSet("ssh-ensure-sync-key", flag.ContinueOnError)
	fs.SetOutput(stderr)
	hostID := fs.String("host-id", "", "host id")
	keyPath := fs.String("key-path", "", "sync key path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected ssh-ensure-sync-key argument")
	}
	if err := config.ValidateHostID(*hostID); err != nil {
		return err
	}

	result, changed, err := loadOrCreateSyncKey(*keyPath, *hostID, checker, generator)
	if err != nil {
		return err
	}
	material, err := sshprovision.SyncKeyMaterialFromConfig(result)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"status":           "ok",
		"changed":          changed,
		"host_id":          *hostID,
		"key_id":           material.KeyID,
		"public_key":       material.PublicKey,
		"private_key_path": material.PrivateKeyPath,
	})
}

func loadOrCreateSyncKey(keyPath string, hostID string, checker storagecheck.Checker, generator config.SyncKeyGenerator) (config.SyncKeyCreateResult, bool, error) {
	if err := ensureSyncKeyParentDirectory(keyPath); err != nil {
		return config.SyncKeyCreateResult{}, false, err
	}
	return loadOrCreateSyncKeyWithOps(keyPath, hostID, checker, generator, config.LoadLocalSyncKey, config.CreateLocalSyncKey)
}

type syncKeyLoadFunc func(config.SyncKeyLoadOptions) (config.SyncKeyCreateResult, error)
type syncKeyCreateFunc func(config.SyncKeyCreateOptions) (config.SyncKeyCreateResult, error)

func loadOrCreateSyncKeyWithOps(keyPath string, hostID string, checker storagecheck.Checker, generator config.SyncKeyGenerator, load syncKeyLoadFunc, create syncKeyCreateFunc) (config.SyncKeyCreateResult, bool, error) {
	result, err := load(config.SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  hostID,
		Checker: checker,
	})
	if err == nil {
		return result, false, nil
	}
	if !errors.Is(err, config.ErrMissingSyncKey) {
		return config.SyncKeyCreateResult{}, false, err
	}
	result, err = create(config.SyncKeyCreateOptions{
		KeyPath:   keyPath,
		HostID:    hostID,
		Checker:   checker,
		Generator: generator,
	})
	if errors.Is(err, config.ErrSyncKeyExists) {
		result, loadErr := load(config.SyncKeyLoadOptions{
			KeyPath: keyPath,
			HostID:  hostID,
			Checker: checker,
		})
		if loadErr == nil {
			return result, false, nil
		}
		return config.SyncKeyCreateResult{}, false, loadErr
	}
	if err != nil {
		return config.SyncKeyCreateResult{}, false, err
	}
	return result, true, nil
}

func ensureSyncKeyParentDirectory(keyPath string) error {
	if err := config.ValidateSyncKeyPath(keyPath); err != nil {
		return fmt.Errorf("invalid_sync_key: %w", err)
	}
	dir := filepath.Dir(keyPath)
	if dir == "." || !filepath.IsAbs(dir) {
		return fmt.Errorf("%w: invalid sync key parent: %s", config.ErrSyncKeyDirectoryUnsafe, dir)
	}
	current := string(os.PathSeparator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(dir), string(os.PathSeparator)), string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: sync key parent ancestry uses symlink: %s", config.ErrSyncKeyDirectoryUnsafe, current)
		}
		if !info.Mode().IsDir() {
			return fmt.Errorf("%w: sync key parent ancestry is not a directory: %s", config.ErrSyncKeyDirectoryUnsafe, current)
		}
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("%w: sync key parent path is unsafe: %s", config.ErrSyncKeyDirectoryUnsafe, dir)
	}
	if filepath.Clean(resolved) != filepath.Clean(dir) {
		return fmt.Errorf("%w: sync key parent ancestry uses symlink: %s", config.ErrSyncKeyDirectoryUnsafe, dir)
	}
	return nil
}

func RunSSHInstallKnownHost(args []string, stdout io.Writer, stderr io.Writer) error {
	return runSSHInstallKnownHost(args, stdout, stderr, sshprovision.UpsertKnownHostPin)
}

func runSSHInstallKnownHost(args []string, stdout io.Writer, stderr io.Writer, upsert func(string, sshprovision.KnownHostPin) error) error {
	fs := flag.NewFlagSet("ssh-install-known-host", flag.ContinueOnError)
	fs.SetOutput(stderr)
	knownHostsPath := fs.String("known-hosts", "", "known_hosts path")
	host := fs.String("host", "", "ssh host")
	port := fs.Int("port", 22, "ssh port")
	keyType := fs.String("key-type", "", "host key type")
	publicKey := fs.String("public-key", "", "base64 host public key blob")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected ssh-install-known-host argument")
	}
	if upsert == nil {
		return fmt.Errorf("missing known host upsert")
	}
	pin, err := sshprovision.NewKnownHostPin(*host, *port, *keyType, *publicKey)
	if err != nil {
		return err
	}
	if err := upsert(*knownHostsPath, pin); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"status":   "ok",
		"pattern":  pin.Pattern,
		"key_type": pin.KeyType,
	})
}
