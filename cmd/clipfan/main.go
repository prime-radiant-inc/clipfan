package main

import (
	"context"
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
	"github.com/prime-radiant-inc/clipfan/internal/version"
)

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: clipfan [daemon|copy|paste|version] [flags]")
	fmt.Fprintln(w, "  (no subcommand) — run the daemon (back-compat)")
	fmt.Fprintln(w, "  daemon          — explicitly run the daemon")
	fmt.Fprintln(w, "  copy [--osc52 /dev/tty] [--image] [--no-daemon] [--no-osc52]")
	fmt.Fprintln(w, "                  — read stdin, push to local daemon and/or emit OSC 52")
	fmt.Fprintln(w, "  paste [--raw]   — write current clipfan state to stdout")
	fmt.Fprintln(w, "  storage-preflight — check local runtime storage and print offline repair guidance")
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
		case "help", "-h", "--help":
			usage(stderr)
			return 0
		case "version":
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
