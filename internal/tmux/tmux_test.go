package tmux

import (
	"os"
	"testing"
)

func TestSocketsMissingDirIsNoError(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir()) // empty
	socks, err := Sockets()
	if err != nil {
		t.Fatalf("Sockets() error: %v", err)
	}
	if len(socks) != 0 {
		t.Fatalf("expected zero sockets, got %v", socks)
	}
}

func TestSocketsIgnoresRegularFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMUX_TMPDIR", tmp)
	dir := tmp + "/tmux-" + uid()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/regularfile", []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	socks, err := Sockets()
	if err != nil {
		t.Fatalf("Sockets() error: %v", err)
	}
	if len(socks) != 0 {
		t.Fatalf("regular files should be ignored, got %v", socks)
	}
}

func uid() string {
	// strconv.Itoa(os.Getuid()) but kept inline to keep the test small
	return func() string {
		u := os.Getuid()
		if u < 0 {
			u = 0
		}
		var s []byte
		if u == 0 {
			return "0"
		}
		for u > 0 {
			s = append([]byte{byte('0' + u%10)}, s...)
			u /= 10
		}
		return string(s)
	}()
}
