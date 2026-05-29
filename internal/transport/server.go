package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

type ReceiveFunc func(c clipboard.Content, origin string)

// PeersFunc returns a JSON-encodable snapshot of the daemon's peer state.
// Returning `any` keeps daemon types out of the transport package.
type PeersFunc func() any

// HistoryFunc returns a JSON-encodable snapshot of the clipboard history.
type HistoryFunc func(limit int) (any, error)

// RestoreFunc re-applies a history entry to the local clipboard.
type RestoreFunc func(id string) error

// PinFunc pins or unpins a history entry.
type PinFunc func(id string, pinned bool) error

// DeleteHistoryFunc removes a single entry, or all unpinned entries.
type DeleteHistoryFunc func(id string, allUnpinned bool) error

type Server struct {
	auth      *Auth
	listen    string
	onRecv    ReceiveFunc
	peersFn   PeersFunc
	historyFn HistoryFunc
	restoreFn RestoreFunc
	pinFn     PinFunc
	deleteFn  DeleteHistoryFunc
}

func NewServer(listen string, auth *Auth, onRecv ReceiveFunc, peersFn PeersFunc) *Server {
	return &Server{auth: auth, listen: listen, onRecv: onRecv, peersFn: peersFn}
}

// SetHistory wires the history endpoints. Called by the daemon after construction.
func (s *Server) SetHistory(h HistoryFunc, r RestoreFunc, p PinFunc, d DeleteHistoryFunc) {
	s.historyFn, s.restoreFn, s.pinFn, s.deleteFn = h, r, p, d
}

// Handler builds the HTTP routes. Exposed so it can be exercised in tests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/clip", s.postClip)
	mux.HandleFunc("GET /v1/peers", s.getPeers)
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /v1/history", s.getHistory)
	mux.HandleFunc("DELETE /v1/history", s.deleteHistory)
	mux.HandleFunc("POST /v1/restore", s.postRestore)
	mux.HandleFunc("POST /v1/history/pin", s.postPin)
	return mux
}

func (s *Server) Serve(ctx context.Context) error {
	srv := &http.Server{Addr: s.listen, Handler: s.Handler()}
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

func (s *Server) getPeers(w http.ResponseWriter, _ *http.Request) {
	if s.peersFn == nil {
		http.Error(w, "peers endpoint not wired", http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.peersFn())
}

func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	if s.readSigned(w, r) == nil {
		return // readSigned already wrote 401/400
	}
	if s.historyFn == nil {
		http.Error(w, "history disabled", http.StatusServiceUnavailable)
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	out, err := s.historyFn(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"entries": out})
}

func (s *Server) deleteHistory(w http.ResponseWriter, r *http.Request) {
	body := s.readSigned(w, r)
	if body == nil {
		return
	}
	var req struct {
		ID          string `json:"id"`
		AllUnpinned bool   `json:"all_unpinned"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.deleteFn(req.ID, req.AllUnpinned); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) postRestore(w http.ResponseWriter, r *http.Request) {
	body := s.readSigned(w, r)
	if body == nil {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.restoreFn(req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) postPin(w http.ResponseWriter, r *http.Request) {
	body := s.readSigned(w, r)
	if body == nil {
		return
	}
	var req struct {
		ID     string `json:"id"`
		Pinned bool   `json:"pinned"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.pinFn(req.ID, req.Pinned); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// readSigned reads the body and verifies the HMAC signature, mirroring postClip.
// Returns nil (after writing an error response) on failure. auth.Verify returns
// an error (nil == valid), not a bool.
func (s *Server) readSigned(w http.ResponseWriter, r *http.Request) []byte {
	sig := r.Header.Get("X-Clipfan-Sig")
	if sig == "" {
		http.Error(w, "missing X-Clipfan-Sig", http.StatusUnauthorized)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil
	}
	if err := s.auth.Verify(body, sig); err != nil {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return nil
	}
	return body
}
