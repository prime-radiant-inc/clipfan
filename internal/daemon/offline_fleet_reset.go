package daemon

import (
	"context"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

type OfflineLocalFleetResetOptions struct {
	ConfigPath  string
	StateDir    string
	Request     config.LocalFleetResetRequest
	StopService func(context.Context) error
	WaitTimeout time.Duration
	Now         func() time.Time

	reset func(string, string, config.LocalFleetResetRequest, string) (config.LocalFleetResetResult, error)
}

func OfflineLocalFleetReset(ctx context.Context, opts OfflineLocalFleetResetOptions) (config.LocalFleetResetResult, string, error) {
	if opts.StopService != nil {
		if err := opts.StopService(ctx); err != nil {
			return config.LocalFleetResetResult{}, "", err
		}
	}
	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = config.Path()
	}
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = config.StateDir()
	}
	waitCtx := ctx
	cancel := func() {}
	if opts.WaitTimeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, opts.WaitTimeout)
	}
	defer cancel()
	lock, err := waitForDaemonLock(waitCtx, stateDir)
	if err != nil {
		return config.LocalFleetResetResult{}, "", err
	}
	defer lock.release()

	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	backupPath := config.LocalFleetResetBackupPath(configPath, now().UTC())
	reset := opts.reset
	if reset == nil {
		reset = config.ResetLocalFleetWithBackup
	}
	result, err := reset(configPath, stateDir, opts.Request, backupPath)
	return result, backupPath, err
}
