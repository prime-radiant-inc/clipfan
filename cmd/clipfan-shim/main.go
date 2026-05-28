// Command clipfan-shim impersonates xclip and wl-paste for the subset of
// invocations that Claude Code (and similar TUI tools) issue when reading
// the clipboard for image paste. It reads from clipfan's local state
// directory instead of from an X server / Wayland compositor, so Ctrl-V
// image paste works on headless Linux remotes.
//
// Install by symlinking this binary as `xclip` and/or `wl-paste` on
// PATH ahead of the real binaries (e.g. in ~/.local/bin).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/cli"
	"github.com/prime-radiant-inc/clipfan/internal/store"
)

func main() {
	name := filepath.Base(os.Args[0])
	args := os.Args[1:]
	switch name {
	case "xclip":
		os.Exit(xclipMode(args))
	case "wl-paste":
		os.Exit(wlPasteMode(args))
	default:
		fmt.Fprintf(os.Stderr, "clipfan-shim: invoked as %q; expected xclip or wl-paste (symlink the binary)\n", name)
		os.Exit(2)
	}
}

func xclipMode(args []string) int {
	target := "STRING"
	op := "out"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-selection":
			i++ // ignored — we only model the clipboard selection
		case "-t":
			i++
			if i < len(args) {
				target = args[i]
			}
		case "-o", "--out":
			op = "out"
		case "-i", "--in":
			op = "in"
		case "-d", "-display":
			i++ // ignored
		case "-h", "-help", "--help":
			fmt.Println("clipfan-shim xclip mode: -selection <sel> -t <target> -o")
			return 0
		}
	}
	if op == "in" {
		// xclip -i writes to the OS clipboard; we re-route to clipfan's
		// daemon so callers like `tmux ... copy-pipe 'xclip ... -i'` keep
		// working as a "make this text the fleet clipboard" path.
		if err := cli.RunCopy(nil); err != nil {
			fmt.Fprintf(os.Stderr, "clipfan-shim: copy failed: %v\n", err)
			return 1
		}
		return 0
	}
	return serveTarget(target)
}

func wlPasteMode(args []string) int {
	target := "text/plain"
	listOnly := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--list-types", "-l":
			listOnly = true
		case "--type", "-t":
			i++
			if i < len(args) {
				target = args[i]
			}
		case "--no-newline", "-n":
			// no-op
		case "--primary", "-p":
			// no-op — primary selection not modeled
		case "-h", "--help":
			fmt.Println("clipfan-shim wl-paste mode: --type <mime> | --list-types")
			return 0
		}
	}
	if listOnly {
		return serveTarget("TARGETS")
	}
	return serveTarget(target)
}

func serveTarget(target string) int {
	state, err := store.LoadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipfan-shim: %v\n", err)
		return 1
	}

	if strings.EqualFold(target, "TARGETS") {
		if state.Kind == "image" && state.ImagePath != "" {
			if _, err := os.Stat(state.ImagePath); err == nil {
				fmt.Println("image/png")
				fmt.Println("image/jpeg")
				fmt.Println("image/bmp")
			}
		}
		fmt.Println("UTF8_STRING")
		fmt.Println("text/plain")
		fmt.Println("text/plain;charset=utf-8")
		fmt.Println("TARGETS")
		return 0
	}

	if strings.HasPrefix(target, "image/") {
		if state.Kind != "image" || state.ImagePath == "" {
			return 1
		}
		f, err := os.Open(state.ImagePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "clipfan-shim: open %s: %v\n", state.ImagePath, err)
			return 1
		}
		defer f.Close()
		_, err = io.Copy(os.Stdout, f)
		if err != nil {
			return 1
		}
		return 0
	}

	// text
	text, err := store.LoadText()
	if err != nil {
		return 1
	}
	_, _ = os.Stdout.Write(text)
	return 0
}
