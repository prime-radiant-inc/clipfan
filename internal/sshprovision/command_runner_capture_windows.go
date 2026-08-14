//go:build windows

package sshprovision

import (
	"bytes"
	"io"
	"os"
)

// outputCapture gathers a child process's stdout/stderr (and provides stdin)
// for ExecCommandRunner. On Windows it MUST NOT hand the child anonymous
// pipes for stdio: Win32-OpenSSH (ssh.exe / ssh-keyscan.exe) hangs forever at
// process exit when stdout/stderr are pipe handles — the remote command
// completes and its output is delivered, but ssh.exe never terminates, wedging
// every provisioning step. Real file handles (temp files) avoid the bug; see
// dist/test notes in the Windows port. Output is read back bounded by limit.
type outputCapture struct {
	limit    int
	stdout   *os.File
	stderr   *os.File
	stdin    *os.File
	finished CommandOutput
}

func newOutputCapture(limit int) *outputCapture {
	if limit <= 0 {
		limit = 64 * 1024
	}
	return &outputCapture{limit: limit}
}

func (c *outputCapture) tempfile() *os.File {
	f, err := os.CreateTemp("", "clipfan-ssh-*.out")
	if err != nil {
		return nil
	}
	return f
}

func (c *outputCapture) Stdout() io.Writer {
	if c.stdout = c.tempfile(); c.stdout != nil {
		return c.stdout
	}
	return io.Discard
}

func (c *outputCapture) Stderr() io.Writer {
	if c.stderr = c.tempfile(); c.stderr != nil {
		return c.stderr
	}
	return io.Discard
}

func (c *outputCapture) Input(payload []byte) io.Reader {
	f, err := os.CreateTemp("", "clipfan-ssh-*.in")
	if err != nil {
		return bytes.NewReader(payload)
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		return bytes.NewReader(payload)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return bytes.NewReader(payload)
	}
	c.stdin = f
	return f
}

func (c *outputCapture) Finish() CommandOutput {
	c.finished = CommandOutput{
		Stdout: readAllBounded(c.stdout, c.limit, &c.finished.StdoutTruncated),
		Stderr: readAllBounded(c.stderr, c.limit, &c.finished.StderrTruncated),
	}
	return c.finished
}

func readAllBounded(f *os.File, limit int, truncated *bool) []byte {
	if f == nil {
		return nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	buf, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return buf
	}
	if len(buf) > limit {
		*truncated = true
		return buf[:limit]
	}
	return buf
}

func (c *outputCapture) Cleanup() {
	for _, f := range []*os.File{c.stdout, c.stderr, c.stdin} {
		if f != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}
}
