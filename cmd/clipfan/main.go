package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/prime-radiant-inc/clipfan/internal/cli"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/daemon"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
	"github.com/prime-radiant-inc/clipfan/internal/version"
)

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: clipfan [daemon|copy|paste|ssh-gateway|version] [flags]")
	fmt.Fprintln(w, "  (no subcommand) — run the daemon (back-compat)")
	fmt.Fprintln(w, "  daemon          — explicitly run the daemon")
	fmt.Fprintln(w, "  copy [--osc52 /dev/tty] [--image] [--no-daemon] [--no-osc52]")
	fmt.Fprintln(w, "                  — read stdin, push to local daemon and/or emit OSC 52")
	fmt.Fprintln(w, "  paste [--raw]   — write current clipfan state to stdout")
	fmt.Fprintln(w, "  storage-preflight — check local runtime storage and print offline repair guidance")
	fmt.Fprintln(w, "  local-fleet-reset --confirm \"RESET LOCAL CLIPFAN FLEET\"")
	fmt.Fprintln(w, "                  — destructively reset local fleet credentials when recovery requires it")
	fmt.Fprintln(w, "  ssh-gateway --authorized-peer <peer> --authorized-key-id <key>")
	fmt.Fprintln(w, "                  — command-locked SSH gateway entrypoint")
	fmt.Fprintln(w, "  version         — print the build version")
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) >= 1 {
		switch args[0] {
		case "copy":
			if err := cli.RunCopy(args[1:]); err != nil {
				fmt.Fprintln(stderr, "clipfan copy:", err)
				return 1
			}
			return 0
		case "paste":
			if err := cli.RunPaste(args[1:]); err != nil {
				fmt.Fprintln(stderr, "clipfan paste:", err)
				return 1
			}
			return 0
		case "storage-preflight":
			if err := cli.RunStoragePreflight(args[1:], stdout, stderr); err != nil {
				fmt.Fprintln(stderr, "clipfan storage-preflight:", err)
				return 1
			}
			return 0
		case "local-fleet-reset":
			if err := cli.RunLocalFleetReset(args[1:], stdout, stderr); err != nil {
				fmt.Fprintln(stderr, "clipfan local-fleet-reset:", err)
				return 1
			}
			return 0
		case "ssh-gateway":
			if err := cli.RunSSHGateway(args[1:], stdout, stderr); err != nil {
				fmt.Fprintln(stderr, "clipfan ssh-gateway:", err)
				return 1
			}
			return 0
		case "ssh-install-authorized-key":
			if err := cli.RunSSHInstallAuthorizedKey(args[1:], stdout, stderr); err != nil {
				fmt.Fprintln(stderr, "clipfan ssh-install-authorized-key:", err)
				return 1
			}
			return 0
		case "ssh-ensure-sync-key":
			if err := cli.RunSSHEnsureSyncKey(args[1:], stdout, stderr); err != nil {
				fmt.Fprintln(stderr, "clipfan ssh-ensure-sync-key:", err)
				return 1
			}
			return 0
		case "ssh-install-known-host":
			if err := cli.RunSSHInstallKnownHost(args[1:], stdout, stderr); err != nil {
				fmt.Fprintln(stderr, "clipfan ssh-install-known-host:", err)
				return 1
			}
			return 0
		case "ssh-run-probe":
			if err := cli.RunSSHRunProbe(args[1:], stdout, stderr); err != nil {
				fmt.Fprintln(stderr, "clipfan ssh-run-probe:", err)
				return 1
			}
			return 0
		case "help", "-h", "--help":
			usage(stderr)
			return 0
		case "version":
			if len(args) >= 2 && args[1] == "--json" {
				if err := json.NewEncoder(stdout).Encode(versionJSONPayload()); err != nil {
					fmt.Fprintln(stderr, "clipfan version:", err)
					return 1
				}
				return 0
			}
			fmt.Fprintln(stdout, version.Version)
			return 0
		case "daemon":
			os.Args = append([]string{os.Args[0]}, args[1:]...)
			// fall through to daemon mode
		}
	}
	runDaemon()
	return 0
}

func versionJSONPayload() map[string]any {
	return map[string]any{
		"version": version.Version,
		"capabilities": map[string]any{
			"config_v2":              true,
			"config_v2_local_auth":   transport.AuthVersionRequestHMAC,
			"peer_http_runtime_gate": true,
		},
		"release_gates": map[string]any{
			"config_v2_write_enabled":    releaseflags.ConfigV2WriteEnabled,
			"peer_http_runtime_disabled": releaseflags.PeerHTTPRuntimeDisabled,
		},
	}
}

func runDaemon() {
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	lvl := slog.LevelInfo
	if *debug {
		lvl = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))

	cfg, err := config.LoadForDaemonStart()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	slog.Info("clipfan starting",
		"listen", cfg.Listen,
		"discovery", cfg.Discovery,
		"config", config.Path(),
	)

	d, err := daemon.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down")
		cancel()
	}()

	if err := d.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
}
