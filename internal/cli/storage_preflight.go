package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/storagecheck"
)

type runtimeStorageCheck func(configRoot, stateRoot string) ([]storagecheck.Result, error)

func RunStoragePreflight(args []string, stdout io.Writer, stderr io.Writer) error {
	return runStoragePreflight(args, stdout, stderr, storagecheck.CheckRuntimeRoots)
}

func runStoragePreflight(args []string, stdout io.Writer, stderr io.Writer, check runtimeStorageCheck) error {
	fs := flag.NewFlagSet("storage-preflight", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	configRoot := filepath.Dir(config.Path())
	stateRoot := config.StateDir()
	results, err := check(configRoot, stateRoot)
	if prompt, ok := storagecheck.RepairPromptForResults(results, err); ok {
		_, _ = fmt.Fprint(stderr, prompt.Text())
		return err
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "clipfan runtime storage is local and supported")
	return nil
}
