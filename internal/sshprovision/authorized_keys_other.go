//go:build !windows

package sshprovision

func syncPlatformAuthorizedKeysStore(entry ManagedAuthorizedKey) error {
	return nil
}

func verifyPlatformAuthorizedKeysStore(entry ManagedAuthorizedKey) error {
	return nil
}
