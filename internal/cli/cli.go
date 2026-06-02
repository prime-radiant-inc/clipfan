// Package cli implements clipfan's `copy` and `paste` subcommands. They
// share the daemon binary so a host that's running clipfan also gets the
// convenience CLI for free.
package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/store"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

const localURL = "http://127.0.0.1:7853"

// RunCopy implements `clipfan copy [--osc52 /dev/ttysXXX] [--image]`.
// Reads stdin and (a) POSTs it to the local clipfan daemon as a new
// clipboard event AND (b) optionally emits an OSC 52 sequence to the
// given tty so a terminal connected from a non-clipfan host (e.g. an
// iPhone SSH client) still gets the bytes via the standard OSC 52 path.
//
// Either path succeeding is treated as success. The CLI is designed
// for `tmux copy-pipe`, where errors would interrupt the user's flow.
func RunCopy(args []string) error {
	fs := flag.NewFlagSet("copy", flag.ContinueOnError)
	oscTTY := fs.String("osc52", "", "tty path to emit OSC 52 sequence to (e.g. tmux's #{client_tty})")
	forceImage := fs.Bool("image", false, "treat stdin as image bytes (default: auto-detect PNG)")
	noDaemon := fs.Bool("no-daemon", false, "skip the daemon push (OSC-52-only mode)")
	noOSC := fs.Bool("no-osc52", false, "skip the OSC 52 emit even if --osc52 is given")
	if err := fs.Parse(args); err != nil {
		return err
	}

	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(body) == 0 {
		return nil
	}

	kind := "text"
	if *forceImage || looksLikePNG(body) {
		kind = "image"
	}

	var daemonErr, oscErr error
	if !*noDaemon {
		daemonErr = pushToDaemon(kind, body)
	}
	if *oscTTY != "" && !*noOSC && kind == "text" {
		oscErr = emitOSC52(*oscTTY, body)
	}

	if daemonErr != nil && oscErr != nil {
		return fmt.Errorf("both paths failed: daemon=%v osc52=%v", daemonErr, oscErr)
	}
	if daemonErr != nil && !*noDaemon {
		fmt.Fprintln(os.Stderr, "clipfan copy: daemon push failed:", daemonErr)
	}
	if oscErr != nil {
		fmt.Fprintln(os.Stderr, "clipfan copy: osc52 emit failed:", oscErr)
	}
	return nil
}

// RunPaste implements `clipfan paste`. It reads the daemon's current
// state file (text or image-path) and writes the text representation
// to stdout. With --raw on an image state, writes the image bytes
// instead.
func RunPaste(args []string) error {
	fs := flag.NewFlagSet("paste", flag.ContinueOnError)
	raw := fs.Bool("raw", false, "if state is an image, write image bytes; otherwise write text")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *raw {
		state, err := store.LoadState()
		if err != nil {
			return err
		}
		if state.Kind == "image" && state.ImagePath != "" {
			f, err := os.Open(state.ImagePath)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(os.Stdout, f)
			return err
		}
	}
	text, err := store.LoadText()
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(text)
	return err
}

func pushToDaemon(kind string, body []byte) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	origin := cfg.Hostname
	if origin == "" {
		h, _ := os.Hostname()
		origin = shortName(h)
	}
	sealedBody, bodyNonce, err := auth.SealBody(body)
	if err != nil {
		return err
	}
	env := transport.Envelope{
		ID:        transport.NewClipID(),
		Origin:    origin,
		Recipient: origin,
		TS:        time.Now().UTC(),
		Kind:      kind,
		Body:      sealedBody,
		Nonce:     bodyNonce,
	}
	raw, _ := json.Marshal(env)

	req, _ := http.NewRequest("POST", localURL+"/v1/clip", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	headers, err := auth.SignedRequestHeaders(req.Method, req.URL.RequestURI(), raw, transport.SignedRequestOptions{})
	if err != nil {
		return err
	}
	for header, value := range headers {
		req.Header.Set(header, value)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func emitOSC52(ttyPath string, body []byte) error {
	if !strings.HasPrefix(ttyPath, "/dev/") {
		return errors.New("osc52 path must be a /dev/tty…")
	}
	f, err := os.OpenFile(ttyPath, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	encoded := base64.StdEncoding.EncodeToString(body)
	// OSC 52 ; c ; <base64> BEL — bytes flow back through SSH to whichever
	// terminal owns the tty. Apple Terminal drops it; Blink/Termius/iTerm/
	// Kitty/Ghostty consume it and write the local clipboard.
	_, err = fmt.Fprintf(f, "\x1b]52;c;%s\x07", encoded)
	return err
}

func looksLikePNG(b []byte) bool {
	return len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
}

func shortName(h string) string {
	h = strings.TrimSuffix(h, ".local")
	return strings.SplitN(h, ".", 2)[0]
}
