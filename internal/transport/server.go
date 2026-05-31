package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

const (
	signatureSkew  = 2 * time.Minute
	nonceRetention = 2 * signatureSkew
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
	configFn  func(maxHistory int) error
	nonces    *nonceCache
	now       func() time.Time
	recipient string
}

func NewServer(listen string, auth *Auth, onRecv ReceiveFunc, peersFn PeersFunc) *Server {
	return &Server{
		auth:    auth,
		listen:  listen,
		onRecv:  onRecv,
		peersFn: peersFn,
		nonces:  newNonceCache(nonceRetention),
		now:     time.Now,
	}
}

// SetRecipientIdentity enables recipient validation for signed peer clip posts.
// Tests and local harnesses that do not configure it accept any recipient.
func (s *Server) SetRecipientIdentity(recipient string) {
	s.recipient = recipient
}

// SetHistory wires the history endpoints. Called by the daemon after construction.
func (s *Server) SetHistory(h HistoryFunc, r RestoreFunc, p PinFunc, d DeleteHistoryFunc) {
	s.historyFn, s.restoreFn, s.pinFn, s.deleteFn = h, r, p, d
}

// SetConfigFunc wires the config-write endpoint. Called by the daemon.
func (s *Server) SetConfigFunc(fn func(maxHistory int) error) { s.configFn = fn }

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
	mux.HandleFunc("POST /v1/config", s.postConfig)
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
	body := s.readSigned(w, r, 64<<20)
	if body == nil {
		return
	}
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.recipient != "" && !RecipientMatches(env.Recipient, s.recipient) {
		http.Error(w, "wrong recipient", http.StatusForbidden)
		return
	}
	raw, err := env.Bytes(s.auth)
	if err != nil {
		http.Error(w, "decrypt envelope body", http.StatusBadRequest)
		return
	}
	c := clipboard.New(clipboard.Kind(env.Kind), raw, env.TS)
	c.ID = env.ID
	c.Concealed = env.Concealed
	slog.Debug("clip received", "id", env.ID, "origin", env.Origin, "kind", env.Kind, "bytes", len(raw))
	s.onRecv(c, env.Origin)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getPeers(w http.ResponseWriter, r *http.Request) {
	if s.readSignedLocal(w, r) == nil {
		return
	}
	if s.peersFn == nil {
		http.Error(w, "peers endpoint not wired", http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.peersFn())
}

func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	if s.readSignedLocal(w, r) == nil {
		return
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
	body := s.readSignedLocal(w, r)
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
	body := s.readSignedLocal(w, r)
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
	body := s.readSignedLocal(w, r)
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

func (s *Server) postConfig(w http.ResponseWriter, r *http.Request) {
	body := s.readSignedLocal(w, r)
	if body == nil {
		return
	}
	if s.configFn == nil {
		http.Error(w, "config endpoint not wired", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		MaxHistory int `json:"max_history"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.configFn(req.MaxHistory); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) readSignedLocal(w http.ResponseWriter, r *http.Request) []byte {
	if !isLoopbackRemote(r.RemoteAddr) {
		http.Error(w, "loopback required", http.StatusForbidden)
		return nil
	}
	body := s.readSigned(w, r, 1<<20)
	if body == nil {
		return nil
	}
	return body
}

// readSigned reads the body and verifies the canonical request HMAC signature.
// Returns nil after writing an error response on failure.
func (s *Server) readSigned(w http.ResponseWriter, r *http.Request, maxBody int64) []byte {
	sig := r.Header.Get("X-Clipfan-Sig")
	if sig == "" {
		http.Error(w, "missing X-Clipfan-Sig", http.StatusUnauthorized)
		return nil
	}
	ts := r.Header.Get("X-Clipfan-Ts")
	if ts == "" {
		http.Error(w, "missing X-Clipfan-Ts", http.StatusUnauthorized)
		return nil
	}
	nonce := r.Header.Get("X-Clipfan-Nonce")
	if nonce == "" {
		http.Error(w, "missing X-Clipfan-Nonce", http.StatusUnauthorized)
		return nil
	}
	unixTs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		http.Error(w, "bad timestamp", http.StatusUnauthorized)
		return nil
	}
	now := s.now()
	requestTime := time.Unix(unixTs, 0)
	if requestTime.Before(now.Add(-signatureSkew)) || requestTime.After(now.Add(signatureSkew)) {
		http.Error(w, "stale timestamp", http.StatusUnauthorized)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil
	}
	if err := s.auth.VerifyRequest(r.Method, r.URL.RequestURI(), ts, nonce, body, sig); err != nil {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return nil
	}
	if !s.nonces.accept(nonce, now) {
		http.Error(w, "replayed nonce", http.StatusUnauthorized)
		return nil
	}
	return body
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
