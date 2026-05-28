package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/prime-radiant-inc/clipfan/internal/cli"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/daemon"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: clipfan [daemon|copy|paste] [flags]")
	fmt.Fprintln(os.Stderr, "  (no subcommand) — run the daemon (back-compat)")
	fmt.Fprintln(os.Stderr, "  daemon          — explicitly run the daemon")
	fmt.Fprintln(os.Stderr, "  copy [--osc52 /dev/tty] [--image] [--no-daemon] [--no-osc52]")
	fmt.Fprintln(os.Stderr, "                  — read stdin, push to local daemon and/or emit OSC 52")
	fmt.Fprintln(os.Stderr, "  paste [--raw]   — write current clipfan state to stdout")
}

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "copy":
			if err := cli.RunCopy(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "clipfan copy:", err)
				os.Exit(1)
			}
			return
		case "paste":
			if err := cli.RunPaste(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "clipfan paste:", err)
				os.Exit(1)
			}
			return
		case "help", "-h", "--help":
			usage()
			return
		case "daemon":
			os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
			// fall through to daemon mode
		}
	}
	runDaemon()
}

func runDaemon() {
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	lvl := slog.LevelInfo
	if *debug {
		lvl = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))

	cfg, err := config.Load()
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
