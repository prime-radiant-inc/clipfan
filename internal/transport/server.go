package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

type ReceiveFunc func(c clipboard.Content, origin string)

type Server struct {
	auth   *Auth
	listen string
	onRecv ReceiveFunc
}

func NewServer(listen string, auth *Auth, onRecv ReceiveFunc) *Server {
	return &Server{auth: auth, listen: listen, onRecv: onRecv}
}

func (s *Server) Serve(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/clip", s.postClip)
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	srv := &http.Server{Addr: s.listen, Handler: mux}
	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithCancel(context.Background())
		cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) postClip(w http.ResponseWriter, r *http.Request) {
	sig := r.Header.Get("X-Clipfan-Sig")
	if sig == "" {
		http.Error(w, "missing X-Clipfan-Sig", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.auth.Verify(body, sig); err != nil {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	raw, err := env.Bytes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c := clipboard.New(clipboard.Kind(env.Kind), raw, env.TS)
	slog.Debug("clip received", "origin", env.Origin, "kind", env.Kind, "bytes", len(raw))
	s.onRecv(c, env.Origin)
	w.WriteHeader(http.StatusNoContent)
}
