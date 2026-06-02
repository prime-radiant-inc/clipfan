package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/daemon"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

type localFleetResetFunc func(context.Context, daemon.OfflineLocalFleetResetOptions) (config.LocalFleetResetResult, string, error)

func RunLocalFleetReset(args []string, stdout io.Writer, stderr io.Writer) error {
	return runLocalFleetResetWithGate(args, stdout, stderr, releaseflags.ConfigV2WriteEnabled, daemon.OfflineLocalFleetReset)
}

func runLocalFleetReset(args []string, stdout io.Writer, stderr io.Writer, reset localFleetResetFunc) error {
	return runLocalFleetResetWithGate(args, stdout, stderr, releaseflags.ConfigV2WriteEnabled, reset)
}

func runLocalFleetResetWithGate(args []string, stdout io.Writer, stderr io.Writer, gateEnabled bool, reset localFleetResetFunc) error {
	fs := flag.NewFlagSet("local-fleet-reset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	confirm := fs.String("confirm", "", "type RESET LOCAL CLIPFAN FLEET to confirm destructive local fleet reset")
	waitTimeout := fs.Duration("wait-daemon-lock", 2*time.Second, "maximum time to wait for the local daemon lock")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *confirm != config.LocalFleetResetConfirmation {
		return config.ErrFleetResetConfirmationRequired
	}
	if !gateEnabled {
		return config.ErrConfigV2WritesDisabled
	}
	if reset == nil {
		return fmt.Errorf("missing local fleet reset runner")
	}

	configPath := config.Path()
	stateDir := config.StateDir()
	status, err := config.ReadLocalFleetResetStatus(configPath, stateDir)
	if err != nil {
		return err
	}
	result, backupPath, err := reset(context.Background(), daemon.OfflineLocalFleetResetOptions{
		ConfigPath:  configPath,
		StateDir:    stateDir,
		Request:     localFleetResetRequestFromStatus(status, *confirm),
		WaitTimeout: *waitTimeout,
		StopService: nil,
		Now:         nil,
	})
	if err != nil {
		return err
	}
	revision := "none"
	if result.ConfigRevision != nil {
		revision = fmt.Sprintf("%d", *result.ConfigRevision)
	}
	if result.BackupPath != "" {
		backupPath = result.BackupPath
	}
	_, _ = fmt.Fprintf(stdout, "local_fleet_reset_complete hostname=%s config_revision=%s backup=%s\n", result.HostID, revision, backupPath)
	return nil
}

func localFleetResetRequestFromStatus(status config.LocalFleetResetStatus, confirmation string) config.LocalFleetResetRequest {
	return config.LocalFleetResetRequest{
		Confirmation:           confirmation,
		ExpectedRevisionState:  status.RevisionState,
		ExpectedConfigRevision: status.ConfigRevision,
	}
}
