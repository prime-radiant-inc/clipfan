package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/daemon"
)

func main() {
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
