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

// VersionFunc returns a JSON-encodable version snapshot for signed peer probes.
type VersionFunc func() any

// HistoryFunc returns a JSON-encodable snapshot of the clipboard history.
type HistoryFunc func(limit int) (any, error)

// RestoreFunc re-applies a history entry to the local clipboard.
type RestoreFunc func(id string) error

// PinFunc pins or unpins a history entry.
type PinFunc func(id string, pinned bool) error

// DeleteHistoryFunc removes a single entry, or all unpinned entries.
type DeleteHistoryFunc func(id string, allUnpinned bool) error

type ListenerRepairReadFunc func() (any, *HandlerError)
type ListenerRepairPatchFunc func(body []byte) (any, *HandlerError)

type HandlerError struct {
	Status int
	Code   string
}

func (e *HandlerError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func (e *HandlerError) httpStatus() int {
	if e == nil || e.Status == 0 {
		return http.StatusInternalServerError
	}
	return e.Status
}

type Server struct {
	auth                  *Auth
	listen                string
	onRecv                ReceiveFunc
	peersFn               PeersFunc
	versionFn             VersionFunc
	historyFn             HistoryFunc
	restoreFn             RestoreFunc
	pinFn                 PinFunc
	deleteFn              DeleteHistoryFunc
	configFn              func(maxHistory int) error
	listenerRepairReadFn  ListenerRepairReadFunc
	listenerRepairPatchFn ListenerRepairPatchFunc
	localRequiredAuthVer  string
	safeMode              bool
	safeInfo              SafeModeInfo
	nonces                *nonceCache
	now                   func() time.Time
	recipient             string
}

type SafeModeInfo struct {
	Origin                string
	Hostname              string
	ConfiguredListen      string
	EffectiveRepairListen string
	ParseError            string
	PeerSyncStarted       bool
	ConfigVersion         *int
	ConfigRevision        *uint64
	Port                  int
	StaticPeers           []string
}

type signedPayload struct {
	body        []byte
	nonce       string
	authVersion string
	receivedAt  time.Time
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

// SetVersionFunc wires the signed network version endpoint. Called by the daemon.
func (s *Server) SetVersionFunc(fn VersionFunc) { s.versionFn = fn }

func (s *Server) SetListenerRepair(readFn ListenerRepairReadFunc, patchFn ListenerRepairPatchFunc) {
	s.listenerRepairReadFn = readFn
	s.listenerRepairPatchFn = patchFn
}

func (s *Server) SetRequiredLocalAuthVersion(authVersion string) {
	s.localRequiredAuthVer = authVersion
}

func (s *Server) SetSafeMode(enabled bool) { s.safeMode = enabled }

func (s *Server) SetSafeModeInfo(info SafeModeInfo) {
	info.StaticPeers = append([]string(nil), info.StaticPeers...)
	s.safeInfo = info
}

// Handler builds the HTTP routes. Exposed so it can be exercised in tests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.safeMode {
		mux.HandleFunc("/", s.safeModeRoute)
		return mux
	}
	mux.HandleFunc("POST /v1/clip", s.postClip)
	mux.HandleFunc("GET /v1/peers", s.getPeers)
	mux.HandleFunc("GET /v1/version", s.getVersion)
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

func (s *Server) safeModeRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/health":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	case r.Method == http.MethodGet && r.URL.Path == "/v1/version":
		s.getSafeModeVersion(w, r)
	case r.Method == http.MethodGet && (r.URL.Path == "/v1/status" || r.URL.Path == "/v1/peers" || r.URL.Path == "/v1/ssh/logs"):
		s.getSafeModeReadOnly(w, r)
	case (r.Method == http.MethodGet || r.Method == http.MethodPatch) && r.URL.Path == "/v1/config/listener":
		s.handleSafeModeListenerRepair(w, r)
	default:
		writeSafeModeError(w, http.StatusConflict, "public_listen_requires_confirmation")
	}
}

func (s *Server) getSafeModeVersion(w http.ResponseWriter, r *http.Request) {
	signed := s.readSignedLocalRequiredAuthVersion(w, r, AuthVersionRequestHMAC)
	if signed == nil {
		return
	}
	if s.versionFn == nil {
		s.writeSignedError(w, signed, http.StatusServiceUnavailable, "safe_mode_status_unavailable_before_schema")
		return
	}
	s.writeSignedJSON(w, signed, s.safeModeVersionPayload())
}

func (s *Server) getSafeModeReadOnly(w http.ResponseWriter, r *http.Request) {
	signed := s.readSignedLocalRequiredAuthVersion(w, r, AuthVersionRequestHMAC)
	if signed == nil {
		return
	}
	switch r.URL.Path {
	case "/v1/status":
		s.writeSignedJSON(w, signed, s.safeModeStatusPayload())
	case "/v1/peers":
		s.writeSignedJSON(w, signed, s.safeModePeersPayload())
	case "/v1/ssh/logs":
		s.getSafeModeLogs(w, r, signed)
	default:
		s.writeSignedError(w, signed, http.StatusNotFound, "not_found")
	}
}

func (s *Server) safeModeVersionPayload() map[string]any {
	payload := map[string]any{}
	switch v := s.versionFn().(type) {
	case map[string]string:
		for key, value := range v {
			payload[key] = value
		}
	case map[string]any:
		for key, value := range v {
			payload[key] = value
		}
	default:
		payload["version"] = v
	}
	payload["safe_mode"] = true
	payload["config_version"] = s.safeInfo.ConfigVersion
	payload["config_revision"] = s.safeInfo.ConfigRevision
	return payload
}

func (s *Server) safeModeStatusPayload() map[string]any {
	return map[string]any{
		"status":                  "safe_mode_signed_repair",
		"hostname":                s.safeInfo.Hostname,
		"configured_listen":       s.safeInfo.ConfiguredListen,
		"effective_repair_listen": s.safeInfo.EffectiveRepairListen,
		"parse_error":             s.safeInfo.ParseError,
		"safe_mode":               true,
		"safe_mode_schema":        "safe_mode_v1",
		"peer_sync_started":       s.safeInfo.PeerSyncStarted,
		"config_version":          s.safeInfo.ConfigVersion,
		"config_revision":         s.safeInfo.ConfigRevision,
		"legacy_peer_suggestions": s.safeModeLegacyPeerSuggestions(),
		"log_ids":                 s.safeModeLogIDs(),
	}
}

func (s *Server) safeModePeersPayload() map[string]any {
	payload := s.safeModeStatusPayload()
	payload["origin"] = s.safeInfo.Origin
	payload["version"] = s.safeModeVersionString()
	payload["peers"] = s.safeModeLegacyPeerRows()
	return payload
}

func (s *Server) safeModeVersionString() string {
	if s.versionFn == nil {
		return ""
	}
	switch v := s.versionFn().(type) {
	case map[string]string:
		return v["version"]
	case map[string]any:
		if version, ok := v["version"].(string); ok {
			return version
		}
	case string:
		return v
	}
	return ""
}

func (s *Server) safeModeLegacyPeerSuggestions() []map[string]string {
	suggestions := make([]map[string]string, 0, len(s.safeInfo.StaticPeers))
	for _, peer := range s.safeInfo.StaticPeers {
		if peer == "" {
			continue
		}
		suggestions = append(suggestions, map[string]string{
			"hostname": peer,
			"source":   "static_peers",
			"status":   "legacy_http",
		})
	}
	return suggestions
}

func (s *Server) safeModeLegacyPeerRows() []map[string]any {
	rows := make([]map[string]any, 0, len(s.safeInfo.StaticPeers))
	for _, peer := range s.safeInfo.StaticPeers {
		if peer == "" {
			continue
		}
		rows = append(rows, map[string]any{
			"hostname":     peer,
			"port":         s.safeInfo.Port,
			"last_push_ok": false,
			"source":       "static_peers",
			"status":       "legacy_http",
		})
	}
	return rows
}

func (s *Server) safeModeLogIDs() []string {
	ids := []string{"safe-mode-listener"}
	for i, peer := range s.safeInfo.StaticPeers {
		if peer == "" {
			continue
		}
		ids = append(ids, "legacy-static-peer-"+strconv.Itoa(i))
	}
	return ids
}

func (s *Server) getSafeModeLogs(w http.ResponseWriter, r *http.Request, signed *signedPayload) {
	peerID := r.URL.Query().Get("peer")
	if peerID != "" && peerID != "local" {
		body := map[string]any{
			"type":      "error",
			"code":      "ssh_peer_logs_unavailable_before_schema",
			"peer_id":   peerID,
			"safe_mode": true,
			"entries":   []any{},
		}
		s.writeSignedJSONStatus(w, signed, http.StatusServiceUnavailable, body)
		return
	}
	s.writeSignedJSON(w, signed, map[string]any{
		"peer_id":   "local",
		"safe_mode": true,
		"entries":   s.safeModeLogEntries(),
		"truncated": false,
	})
}

func (s *Server) safeModeLogEntries() []map[string]any {
	code := s.safeInfo.ParseError
	if code == "" {
		code = "public_listen_requires_confirmation"
	}
	entries := []map[string]any{{
		"ts":      s.now().UTC().Format(time.RFC3339),
		"source":  "listener_repair",
		"durable": false,
		"log_id":  "safe-mode-listener",
		"phase":   "listener_safe_mode",
		"code":    code,
		"message": "Configured listener requires local repair before peer sync can start.",
	}}
	for i, peer := range s.safeInfo.StaticPeers {
		if peer == "" {
			continue
		}
		entries = append(entries, map[string]any{
			"ts":      s.now().UTC().Format(time.RFC3339),
			"source":  "remediation",
			"durable": false,
			"log_id":  "legacy-static-peer-" + strconv.Itoa(i),
			"phase":   "legacy_static_peer",
			"code":    "ssh_setup_required",
			"message": "Static peer requires SSH setup before sync.",
		})
	}
	return entries
}

func (s *Server) safeModeSignedUnavailable(w http.ResponseWriter, r *http.Request, code string) {
	signed := s.readSignedLocalRequiredAuthVersion(w, r, AuthVersionRequestHMAC)
	if signed == nil {
		return
	}
	s.writeSignedError(w, signed, http.StatusServiceUnavailable, code)
}

func (s *Server) handleSafeModeListenerRepair(w http.ResponseWriter, r *http.Request) {
	signed := s.readSignedLocalRequiredAuthVersion(w, r, AuthVersionRequestHMAC)
	if signed == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		if s.listenerRepairReadFn == nil {
			s.writeSignedError(w, signed, http.StatusServiceUnavailable, "listener_repair_unavailable")
			return
		}
		payload, handlerErr := s.listenerRepairReadFn()
		if handlerErr != nil {
			s.writeSignedError(w, signed, handlerErr.httpStatus(), handlerErr.Code)
			return
		}
		s.writeSignedJSON(w, signed, payload)
	case http.MethodPatch:
		if s.listenerRepairPatchFn == nil {
			s.writeSignedError(w, signed, http.StatusServiceUnavailable, "listener_repair_unavailable")
			return
		}
		payload, handlerErr := s.listenerRepairPatchFn(signed.body)
		if handlerErr != nil {
			s.writeSignedError(w, signed, handlerErr.httpStatus(), handlerErr.Code)
			return
		}
		s.writeSignedJSON(w, signed, payload)
	default:
		s.writeSignedError(w, signed, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func writeSafeModeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"type":"error","code":"` + code + `"}` + "\n"))
}

func (s *Server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	return s.ServeListener(ctx, ln)
}

func (s *Server) ServeListener(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler()}
	errCh := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
			if serveErr := <-errCh; serveErr != nil {
				return serveErr
			}
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func (s *Server) postClip(w http.ResponseWriter, r *http.Request) {
	signed := s.readSigned(w, r, 64<<20)
	if signed == nil {
		return
	}
	var env Envelope
	if err := json.Unmarshal(signed.body, &env); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.recipient != "" && !RecipientMatches(env.Recipient, s.recipient) {
		http.Error(w, "wrong recipient", http.StatusForbidden)
		return
	}
	if env.TS.After(signed.receivedAt.Add(signatureSkew)) {
		http.Error(w, "future envelope timestamp", http.StatusBadRequest)
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
	signed := s.readSignedLocal(w, r)
	if signed == nil {
		return
	}
	if s.peersFn == nil {
		http.Error(w, "peers endpoint not wired", http.StatusNotImplemented)
		return
	}
	s.writeSignedJSON(w, signed, s.peersFn())
}

func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	signed := s.readSigned(w, r, 1<<20)
	if signed == nil {
		return
	}
	if s.versionFn == nil {
		http.Error(w, "version endpoint not wired", http.StatusNotImplemented)
		return
	}
	s.writeSignedJSON(w, signed, s.versionFn())
}

func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	signed := s.readSignedLocal(w, r)
	if signed == nil {
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
	s.writeSignedJSON(w, signed, map[string]any{"entries": out})
}

func (s *Server) deleteHistory(w http.ResponseWriter, r *http.Request) {
	signed := s.readSignedLocal(w, r)
	if signed == nil {
		return
	}
	var req struct {
		ID          string `json:"id"`
		AllUnpinned bool   `json:"all_unpinned"`
	}
	if err := json.Unmarshal(signed.body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.deleteFn(req.ID, req.AllUnpinned); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeSignedBody(w, signed, http.StatusOK, nil)
}

func (s *Server) postRestore(w http.ResponseWriter, r *http.Request) {
	signed := s.readSignedLocal(w, r)
	if signed == nil {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(signed.body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.restoreFn(req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeSignedBody(w, signed, http.StatusOK, nil)
}

func (s *Server) postPin(w http.ResponseWriter, r *http.Request) {
	signed := s.readSignedLocal(w, r)
	if signed == nil {
		return
	}
	var req struct {
		ID     string `json:"id"`
		Pinned bool   `json:"pinned"`
	}
	if err := json.Unmarshal(signed.body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.pinFn(req.ID, req.Pinned); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeSignedBody(w, signed, http.StatusOK, nil)
}

func (s *Server) postConfig(w http.ResponseWriter, r *http.Request) {
	signed := s.readSignedLocal(w, r)
	if signed == nil {
		return
	}
	if s.configFn == nil {
		http.Error(w, "config endpoint not wired", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		MaxHistory int `json:"max_history"`
	}
	if err := json.Unmarshal(signed.body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.configFn(req.MaxHistory); err != nil {
		s.writeSignedBody(w, signed, http.StatusBadRequest, []byte(err.Error()+"\n"))
		return
	}
	s.writeSignedBody(w, signed, http.StatusOK, nil)
}

func (s *Server) writeSignedJSON(w http.ResponseWriter, signed *signedPayload, v any) {
	s.writeSignedJSONStatus(w, signed, http.StatusOK, v)
}

func (s *Server) writeSignedJSONStatus(w http.ResponseWriter, signed *signedPayload, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	s.writeSignedBody(w, signed, status, body)
}

func (s *Server) writeSignedBody(w http.ResponseWriter, signed *signedPayload, status int, body []byte) {
	if signed.authVersion != "" {
		w.Header().Set(HeaderAuthVersion, signed.authVersion)
	}
	sig, err := s.auth.SignResponseWithAuthVersion(signed.nonce, body, signed.authVersion)
	if err != nil {
		http.Error(w, "unsupported auth version", http.StatusUnauthorized)
		return
	}
	w.Header().Set("X-Clipfan-Response-Sig", sig)
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

func (s *Server) writeSignedError(w http.ResponseWriter, signed *signedPayload, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	body, err := json.Marshal(map[string]string{"type": "error", "code": code})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeSignedBody(w, signed, status, append(body, '\n'))
}

func (s *Server) readSignedLocal(w http.ResponseWriter, r *http.Request) *signedPayload {
	return s.readSignedLocalWithRequiredAuthVersion(w, r, s.localRequiredAuthVer)
}

func (s *Server) readSignedLocalRequiredAuthVersion(w http.ResponseWriter, r *http.Request, requiredAuthVersion string) *signedPayload {
	return s.readSignedLocalWithRequiredAuthVersion(w, r, requiredAuthVersion)
}

func (s *Server) readSignedLocalWithRequiredAuthVersion(w http.ResponseWriter, r *http.Request, requiredAuthVersion string) *signedPayload {
	if !isLoopbackRemote(r.RemoteAddr) {
		http.Error(w, "loopback required", http.StatusForbidden)
		return nil
	}
	return s.readSignedWithRequiredAuthVersion(w, r, 1<<20, requiredAuthVersion)
}

// readSigned reads the body and verifies the canonical request HMAC signature.
// Returns nil after writing an error response on failure.
func (s *Server) readSigned(w http.ResponseWriter, r *http.Request, maxBody int64) *signedPayload {
	return s.readSignedWithRequiredAuthVersion(w, r, maxBody, "")
}

func (s *Server) readSignedWithRequiredAuthVersion(w http.ResponseWriter, r *http.Request, maxBody int64, requiredAuthVersion string) *signedPayload {
	sig := r.Header.Get(HeaderSignature)
	if sig == "" {
		http.Error(w, "missing X-Clipfan-Sig", http.StatusUnauthorized)
		return nil
	}
	ts := r.Header.Get(HeaderTimestamp)
	if ts == "" {
		http.Error(w, "missing X-Clipfan-Ts", http.StatusUnauthorized)
		return nil
	}
	nonce := r.Header.Get(HeaderNonce)
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
	authVersion := r.Header.Get(HeaderAuthVersion)
	var verifyErr error
	if requiredAuthVersion == "" {
		verifyErr = s.auth.VerifyRequestWithAuthVersion(r.Method, r.URL.RequestURI(), ts, nonce, body, sig, authVersion)
	} else {
		verifyErr = s.auth.VerifyRequestRequiredAuthVersion(r.Method, r.URL.RequestURI(), ts, nonce, body, sig, authVersion, requiredAuthVersion)
	}
	if verifyErr != nil {
		switch {
		case errors.Is(verifyErr, ErrAuthVersionMismatch):
			http.Error(w, ErrAuthVersionMismatch.Error(), http.StatusUnauthorized)
		case errors.Is(verifyErr, ErrBadSignature):
			http.Error(w, ErrBadSignature.Error(), http.StatusUnauthorized)
		default:
			http.Error(w, "bad signature", http.StatusUnauthorized)
		}
		return nil
	}
	if !s.nonces.accept(nonce, now) {
		http.Error(w, "replayed nonce", http.StatusUnauthorized)
		return nil
	}
	return &signedPayload{body: body, nonce: nonce, authVersion: authVersion, receivedAt: now}
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
