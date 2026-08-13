//go:build windows

package storagecheck

// LocalSmokeCheck is a no-op on Windows. The Unix permission check (state root
// not group/world writable) does not apply to NTFS, which is ACL-based; the
// state directory lives under %USERPROFILE%, which is user-owned by default.
func LocalSmokeCheck(root string) error {
	return nil
}
