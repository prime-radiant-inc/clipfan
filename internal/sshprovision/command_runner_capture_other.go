//go:build !windows

package sshprovision

import (
	"bytes"
	"io"
)

// outputCapture gathers a child process's stdout/stderr (and can provide a
// stdin source) for ExecCommandRunner. On Unix this is the simple in-memory
// pipe buffer.
type outputCapture struct {
	stdout limitedBuffer
	stderr limitedBuffer
}

func newOutputCapture(limit int) *outputCapture {
	return &outputCapture{
		stdout: limitedBuffer{limit: limit},
		stderr: limitedBuffer{limit: limit},
	}
}

func (c *outputCapture) Stdout() io.Writer { return &c.stdout }
func (c *outputCapture) Stderr() io.Writer { return &c.stderr }
func (c *outputCapture) Input(payload []byte) io.Reader {
	return bytes.NewReader(payload)
}

func (c *outputCapture) Finish() CommandOutput {
	return CommandOutput{
		Stdout:          c.stdout.Bytes(),
		Stderr:          c.stderr.Bytes(),
		StdoutTruncated: c.stdout.Truncated(),
		StderrTruncated: c.stderr.Truncated(),
	}
}

func (c *outputCapture) Cleanup() {}
