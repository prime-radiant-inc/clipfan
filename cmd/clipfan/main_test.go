package main

import (
	"bytes"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/version"
)

func TestRunVersionCommandPrintsBuildVersion(t *testing.T) {
	oldVersion := version.Version
	version.Version = "test-version"
	defer func() { version.Version = oldVersion }()

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"version"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if got := stdout.String(); got != "test-version\n" {
		t.Fatalf("stdout = %q, want test-version newline", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
