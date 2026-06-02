package daemon

import (
	"context"
	"path/filepath"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

type OfflineListenerRepairOptions struct {
	ConfigPath  string
	StateDir    string
	Request     config.ListenerRepairRequest
	StopService func(context.Context) error
	WaitTimeout time.Duration
	Now         func() time.Time

	repair func(string, config.ListenerRepairRequest, string) (config.ListenerRepairStatus, error)
}

func OfflineListenerRepair(ctx context.Context, opts OfflineListenerRepairOptions) (config.ListenerRepairStatus, string, error) {
	if opts.StopService != nil {
		if err := opts.StopService(ctx); err != nil {
			return config.ListenerRepairStatus{}, "", err
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
		return config.ListenerRepairStatus{}, "", err
	}
	defer lock.release()

	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	backupPath := ListenerRepairBackupPath(configPath, now().UTC())
	repair := opts.repair
	if repair == nil {
		repair = config.RepairListenerWithBackup
	}
	status, err := repair(configPath, opts.Request, backupPath)
	return status, backupPath, err
}

func ListenerRepairBackupPath(configPath string, ts time.Time) string {
	stamp := ts.UTC().Format("20060102T150405Z")
	return filepath.Join(filepath.Dir(configPath), filepath.Base(configPath)+".listener-repair."+stamp+".bak")
}
