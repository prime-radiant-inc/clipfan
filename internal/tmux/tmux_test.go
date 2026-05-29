package tmux

import (
	"net"
	"os"
	"strings"
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

// TestLoadBufferAllUsesLoadBufferNotSetBuffer locks in the invariant that the
// daemon writes received clips back with `load-buffer`, NOT `set-buffer`. The
// dotfiles `after-set-buffer` tmux hook (which pipes Claude-Code-style buffer
// writes into clipfan) relies on this asymmetry: if the writeback ever used
// `set-buffer`, it would re-trigger that hook and create a sync loop. A fake
// `tmux` on PATH records the subcommand actually invoked.
func TestLoadBufferAllUsesLoadBufferNotSetBuffer(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMUX_TMPDIR", tmp)

	// A real unix socket so Sockets() returns it (it filters on os.ModeSocket).
	sockDir := tmp + "/tmux-" + uid()
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sockDir+"/default")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// A fake `tmux` on PATH that records its argv to a file.
	binDir := t.TempDir()
	argLog := tmp + "/tmux-args.log"
	shim := "#!/bin/sh\necho \"$@\" >> " + argLog + "\n"
	if err := os.WriteFile(binDir+"/tmux", []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := LoadBufferAll([]byte("hello")); err != nil {
		t.Fatalf("LoadBufferAll: %v", err)
	}

	out, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("fake tmux was never invoked: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "load-buffer") {
		t.Fatalf("writeback must use load-buffer; got args: %q", got)
	}
	if strings.Contains(got, "set-buffer") {
		t.Fatalf("writeback must NOT use set-buffer (would loop the after-set-buffer hook); got args: %q", got)
	}
}
