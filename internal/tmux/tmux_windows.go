//go:build windows

package tmux

// On Windows tmux does not exist, so there are no sockets to fan clipboard
// content out to. These stubs let the daemon (which calls tmux.LoadBufferAll)
// compile and run unchanged on Windows.

// LoadBufferAll is a no-op on Windows.
func LoadBufferAll(content []byte) error { return nil }

// Sockets returns no sockets on Windows.
func Sockets() ([]string, error) { return nil, nil }
