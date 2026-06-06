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

func TestSocketsRejectsSharedSocketDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMUX_TMPDIR", tmp)
	dir := tmp + "/tmux-" + uid()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}

	if _, err := Sockets(); err == nil {
		t.Fatal("expected shared tmux socket directory to be rejected")
	}
}

func TestSocketsRejectsSymlinkSocketDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMUX_TMPDIR", tmp)
	target := tmp + "/target"
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, tmp+"/tmux-"+uid()); err != nil {
		t.Fatal(err)
	}

	if _, err := Sockets(); err == nil {
		t.Fatal("expected symlinked tmux socket directory to be rejected")
	}
}

func TestSocketsIgnoresSharedSocket(t *testing.T) {
	tmp := shortTempDir(t)
	t.Setenv("TMUX_TMPDIR", tmp)
	dir := tmp + "/tmux-" + uid()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", dir+"/default")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chmod(dir+"/default", 0o777); err != nil {
		t.Fatal(err)
	}

	socks, err := Sockets()
	if err != nil {
		t.Fatalf("Sockets() error: %v", err)
	}
	if len(socks) != 0 {
		t.Fatalf("shared socket should be ignored, got %v", socks)
	}
}

func TestSocketsIncludesGroupWritableTmuxSocketInPrivateDir(t *testing.T) {
	tmp := shortTempDir(t)
	t.Setenv("TMUX_TMPDIR", tmp)
	dir := tmp + "/tmux-" + uid()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", dir+"/default")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chmod(dir+"/default", 0o770); err != nil {
		t.Fatal(err)
	}

	socks, err := Sockets()
	if err != nil {
		t.Fatalf("Sockets() error: %v", err)
	}
	if len(socks) != 1 || !strings.HasSuffix(socks[0], "/default") {
		t.Fatalf("expected default socket, got %v", socks)
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

// TestLoadBufferAllUsesLoadBuffer characterizes how the daemon writes received
// clips into tmux: via `load-buffer` reading from stdin. (tmux treats
// load-buffer and set-buffer as the same operation differing only in data
// source, so this is documentation of the chosen mechanism, not a loop guard.
// Loop-safety for the tmux hook bridge lives in the daemon's seen-set: see
// TestImagePathWritebackDedupedHeadless.) A fake `tmux` on PATH records the
// subcommand actually invoked.
func TestLoadBufferAllUsesLoadBuffer(t *testing.T) {
	tmp := shortTempDir(t)
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
		t.Fatalf("writeback should use load-buffer; got args: %q", got)
	}
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "clipfan-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
