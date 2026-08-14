package sshprovision

// Windows OpenSSH's shipped sshd_config routes administrator accounts to
// C:\ProgramData\ssh\administrators_authorized_keys and ignores
// ~/.ssh/authorized_keys for them. When that routing is active, the managed
// entry must be mirrored into the administrators file (with sshd's strict
// ACLs) or key auth for admin accounts silently fails.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	windowsSSHDConfigPath   = `ssh\sshd_config`
	windowsAdminKeysRelPath = `ssh\administrators_authorized_keys`
	windowsAdminKeysMarker  = "administrators_authorized_keys"
)

// syncPlatformAuthorizedKeysStore mirrors entry into the Windows
// administrators_authorized_keys store when sshd routes admins there.
//
// Skip conditions (return nil): no ProgramData/sshd_config (not a Windows
// OpenSSH server), config without the admins routing, or access-denied on
// the ProgramData store (the account is not an administrator, so sshd reads
// the user file and the mirror is unnecessary).
func syncPlatformAuthorizedKeysStore(entry ManagedAuthorizedKey) error {
	path, ok, err := windowsAdminAuthorizedKeysTarget()
	if err != nil || !ok {
		return err
	}
	data, _, err := readWindowsAdminAuthorizedKeys(path)
	if err != nil {
		if isWindowsAccessDenied(err) {
			return nil
		}
		return err
	}
	updated, err := UpsertManagedAuthorizedKeyLine(data, entry)
	if err != nil {
		return err
	}
	changed := !bytes.Equal(updated, data)
	if changed {
		if err := writeWindowsAdminAuthorizedKeys(path, updated); err != nil {
			if isWindowsAccessDenied(err) {
				return nil
			}
			return err
		}
	}
	// sshd refuses the file unless only Administrators/SYSTEM can write it.
	if err := repairWindowsAdminAuthorizedKeysACLs(path); err != nil && changed {
		return err
	}
	return nil
}

// verifyPlatformAuthorizedKeysStore checks the mirrored store is in sync.
func verifyPlatformAuthorizedKeysStore(entry ManagedAuthorizedKey) error {
	path, ok, err := windowsAdminAuthorizedKeysTarget()
	if err != nil || !ok {
		return err
	}
	data, exists, err := readWindowsAdminAuthorizedKeys(path)
	if err != nil {
		if isWindowsAccessDenied(err) {
			return nil
		}
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrAuthorizedKeyNotFound, path)
	}
	updated, err := UpsertManagedAuthorizedKeyLine(data, entry)
	if err != nil {
		return err
	}
	if !bytes.Equal(updated, data) {
		return fmt.Errorf("%w: %s", ErrAuthorizedKeyNotFound, entry.PeerID)
	}
	return nil
}

// windowsAdminAuthorizedKeysTarget reports the administrators_authorized_keys
// path and whether the installed sshd_config actually routes administrators
// there. Missing config or ProgramData means the routing cannot apply.
func windowsAdminAuthorizedKeysTarget() (string, bool, error) {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		return "", false, nil
	}
	configPath := filepath.Join(programData, "ssh", "sshd_config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || isWindowsAccessDenied(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if !bytes.Contains(data, []byte(windowsAdminKeysMarker)) {
		return "", false, nil
	}
	return filepath.Join(programData, windowsAdminKeysRelPath), true, nil
}

func readWindowsAdminAuthorizedKeys(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

func writeWindowsAdminAuthorizedKeys(path string, data []byte) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "clipfan-authorized-keys-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

// repairWindowsAdminAuthorizedKeysACLs applies the ACL set sshd requires
// (inheritance removed; Administrators and SYSTEM full control). Idempotent.
func repairWindowsAdminAuthorizedKeysACLs(path string) error {
	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant", "Administrators:F", "/grant", "SYSTEM:F")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: icacls %s: %v", ErrAuthorizedKeysUnsafe, path, err)
	}
	return nil
}

func isWindowsAccessDenied(err error) bool {
	if err == nil {
		return false
	}
	// os.PathError from the syscall layer; ERROR_ACCESS_DENIED = 5,
	// ERROR_SHARING_VIOLATION = 32. Match on the textual form to stay
	// independent of the errno mapping.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "access_denied")
}
