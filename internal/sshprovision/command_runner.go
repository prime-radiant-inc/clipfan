package sshprovision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var ErrSSHCommandFailed = errors.New("ssh_command_failed")

type CommandOutput struct {
	Stdout []byte
	Stderr []byte
}

type CommandRunner interface {
	Run(context.Context, SSHCommand) (CommandOutput, error)
}

type ExecCommandRunner struct {
	MaxOutputBytes int
}

type SSHCommandError struct {
	Err    error
	Args   []string
	Stderr string
}

func (e SSHCommandError) Error() string {
	message := ErrSSHCommandFailed.Error()
	if len(e.Args) > 0 {
		message += ": " + redactSSHCommandArgs(e.Args)
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	if strings.TrimSpace(e.Stderr) != "" {
		message += ": " + e.Stderr
	}
	return message
}

func (e SSHCommandError) Unwrap() error {
	if e.Err == nil {
		return ErrSSHCommandFailed
	}
	return errors.Join(ErrSSHCommandFailed, e.Err)
}

func (r ExecCommandRunner) Run(ctx context.Context, command SSHCommand) (CommandOutput, error) {
	if len(command.Args) == 0 || command.Args[0] == "" {
		return CommandOutput{}, fmt.Errorf("%w: empty command", ErrSSHCommandFailed)
	}
	limit := r.MaxOutputBytes
	if limit <= 0 {
		limit = 64 * 1024
	}
	cmd := exec.CommandContext(ctx, command.Args[0], command.Args[1:]...)
	var stdout, stderr limitedBuffer
	stdout.limit = limit
	stderr.limit = limit
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := CommandOutput{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
	}
	if err != nil {
		return output, SSHCommandError{
			Err:    err,
			Args:   command.Args,
			Stderr: redactSSHDiagnostic(string(output.Stderr), command.Args),
		}
	}
	return output, nil
}

type limitedBuffer struct {
	limit int
	buf   bytes.Buffer
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if w.limit > 0 && w.buf.Len() < w.limit {
		remaining := w.limit - w.buf.Len()
		if len(p) < remaining {
			remaining = len(p)
		}
		_, _ = w.buf.Write(p[:remaining])
	}
	return len(p), nil
}

func (w *limitedBuffer) Bytes() []byte {
	return append([]byte(nil), w.buf.Bytes()...)
}

var (
	absolutePathPattern       = regexp.MustCompile(`/[A-Za-z0-9._/@+-]+`)
	publicKeyBlobPattern      = regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`)
	quotedPublicKeyPattern    = regexp.MustCompile(`'--public-key' '[^']*'`)
	quotedSensitiveArgPattern = regexp.MustCompile(`'--(key-path|known-hosts|gateway-path)' '[^']*'`)
)

func redactSSHCommandArgs(args []string) string {
	redacted := make([]string, len(args))
	for i, arg := range args {
		switch {
		case i > 0 && args[i-1] == "-i":
			redacted[i] = "<private_key>"
		case strings.HasPrefix(arg, "UserKnownHostsFile="):
			redacted[i] = "UserKnownHostsFile=<known_hosts>"
		default:
			redacted[i] = redactSSHDiagnostic(arg, args)
		}
	}
	return strings.Join(redacted, " ")
}

func redactSSHDiagnostic(value string, args []string) string {
	out := value
	for i, arg := range args {
		if i > 0 && args[i-1] == "-i" {
			out = strings.ReplaceAll(out, arg, "<private_key>")
		}
		if strings.HasPrefix(arg, "UserKnownHostsFile=") {
			out = strings.ReplaceAll(out, strings.TrimPrefix(arg, "UserKnownHostsFile="), "<known_hosts>")
		}
	}
	out = quotedPublicKeyPattern.ReplaceAllString(out, "'--public-key' '<public_key>'")
	out = quotedSensitiveArgPattern.ReplaceAllString(out, "'--$1' '<path>'")
	out = publicKeyBlobPattern.ReplaceAllString(out, "<public_key>")
	out = absolutePathPattern.ReplaceAllString(out, "<path>")
	return out
}
