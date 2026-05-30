// Package version carries the daemon's build version. The default is "dev";
// release builds override it with -ldflags "-X .../internal/version.Version=...".
package version

var Version = "dev"
