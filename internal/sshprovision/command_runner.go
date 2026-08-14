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
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

type CommandRunner interface {
	Run(context.Context, SSHCommand) (CommandOutput, error)
}

type ExecCommandRunner struct {
	MaxOutputBytes int
}

type SSHCommandError struct {
	cause           error
	redactedCommand string
	redactedStderr  string
}

func (e SSHCommandError) Error() string {
	message := ErrSSHCommandFailed.Error()
	if e.redactedCommand != "" {
		message += ": " + e.redactedCommand
	}
	if e.cause != nil {
		message += ": " + e.cause.Error()
	}
	if strings.TrimSpace(e.redactedStderr) != "" {
		message += ": " + e.redactedStderr
	}
	return message
}

func (e SSHCommandError) Unwrap() error {
	if e.cause == nil {
		return ErrSSHCommandFailed
	}
	return errors.Join(ErrSSHCommandFailed, e.cause)
}

func (e SSHCommandError) RedactedCommand() string {
	return e.redactedCommand
}

func (e SSHCommandError) RedactedStderr() string {
	return e.redactedStderr
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
	cmd.Env = sanitizedSSHEnv()
	capture := newOutputCapture(limit)
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	if command.Stdin != nil {
		cmd.Stdin = capture.Input(command.Stdin)
	}
	err := cmd.Run()
	output := capture.Finish()
	capture.Cleanup()
	if err != nil {
		return output, SSHCommandError{
			cause:           err,
			redactedCommand: redactSSHCommandArgs(command.Args),
			redactedStderr:  redactSSHDiagnostic(string(output.Stderr), command.Args),
		}
	}
	return output, nil
}

type limitedBuffer struct {
	limit     int
	truncated bool
	buf       bytes.Buffer
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	written := 0
	if w.limit > 0 && w.buf.Len() < w.limit {
		remaining := w.limit - w.buf.Len()
		if len(p) < remaining {
			remaining = len(p)
		}
		written, _ = w.buf.Write(p[:remaining])
	}
	if w.limit > 0 && written < len(p) {
		w.truncated = true
	}
	return len(p), nil
}

func (w *limitedBuffer) Bytes() []byte {
	return append([]byte(nil), w.buf.Bytes()...)
}

func (w *limitedBuffer) Truncated() bool {
	return w.truncated
}

var (
	absolutePathPattern       = regexp.MustCompile(`/[A-Za-z0-9._/@+-]+`)
	publicKeyBlobPattern      = regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`)
	quotedPublicKeyPattern    = regexp.MustCompile(`'--public-key' '[^']*'`)
	quotedSensitiveArgPattern = regexp.MustCompile(`'--(key-path|known-hosts|gateway-path)' '[^']*'`)
	jsonSecretLikePattern     = regexp.MustCompile(`(?i)"(shared_key|private_key|sync_key_path|token|hmac|nonce|password|credential|secret)"\s*:\s*"(?:\\.|[^"\\])*"`)
	secretLikePattern         = regexp.MustCompile(`(?i)\b(shared_key|private_key|sync_key_path|token|hmac|nonce|password|credential|secret)\b\s*[:=]\s*("[^"]*"|'[^']*'|[^,\s}]+)`)
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
	out = redactSecretLikeFields(out)
	out = publicKeyBlobPattern.ReplaceAllString(out, "<public_key>")
	out = absolutePathPattern.ReplaceAllString(out, "<path>")
	return out
}

func redactSecretLikeFields(value string) string {
	out := jsonSecretLikePattern.ReplaceAllString(value, `"$1":"<redacted>"`)
	return secretLikePattern.ReplaceAllString(out, "$1=<redacted>")
}
