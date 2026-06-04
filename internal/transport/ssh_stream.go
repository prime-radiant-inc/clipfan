package transport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

const (
	SSHStreamProtocolV1        = 1
	SSHStreamPurposeSyncStream = "sync-stream"

	SSHStreamFrameHello = "hello"
	SSHStreamFrameState = "state"
	SSHStreamFrameAck   = "ack"
	SSHStreamFrameError = "error"

	MaxSSHStreamFrameBytes   = 90 << 20
	MaxSSHStreamPayloadBytes = 64 << 20

	SSHStreamHelloNonceBucketCap  = 256
	SSHStreamHelloNonceProcessCap = 8192
)

var (
	ErrSSHStreamFrameTooLarge   = errors.New("ssh_stream_frame_too_large")
	ErrSSHStreamPayloadTooLarge = errors.New("ssh_stream_payload_too_large")
	ErrSSHStreamUnexpectedEOF   = errors.New("ssh_stream_unexpected_eof")
	ErrSSHStreamUnexpectedFrame = errors.New("ssh_stream_unexpected_frame")
	ErrSSHStreamInvalidHello    = errors.New("ssh_stream_invalid_hello")
	ErrSSHStreamSenderMismatch  = errors.New("ssh_stream_sender_mismatch")
)

type SSHStreamHello struct {
	Protocols []int  `json:"protocols"`
	Purpose   string `json:"purpose"`
	HostID    string `json:"host_id"`
	PeerID    string `json:"peer_id"`
	TS        int64  `json:"ts"`
	Nonce     string `json:"nonce"`
	Sig       string `json:"sig"`
}

type SSHStreamFrame struct {
	Type        string    `json:"type"`
	Protocols   []int     `json:"protocols,omitempty"`
	Purpose     string    `json:"purpose,omitempty"`
	HostID      string    `json:"host_id,omitempty"`
	PeerID      string    `json:"peer_id,omitempty"`
	TS          int64     `json:"ts,omitempty"`
	Nonce       string    `json:"nonce,omitempty"`
	Sig         string    `json:"sig,omitempty"`
	Seq         uint64    `json:"seq,omitempty"`
	Sender      string    `json:"sender,omitempty"`
	Clip        *Envelope `json:"clip,omitempty"`
	NullReason  string    `json:"null_reason,omitempty"`
	ID          string    `json:"id,omitempty"`
	Status      string    `json:"status,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Code        string    `json:"code,omitempty"`
	clipPresent bool
}

func (f SSHStreamFrame) MarshalJSON() ([]byte, error) {
	fields := map[string]any{"type": f.Type}
	if len(f.Protocols) > 0 {
		fields["protocols"] = f.Protocols
	}
	if f.Purpose != "" {
		fields["purpose"] = f.Purpose
	}
	if f.HostID != "" {
		fields["host_id"] = f.HostID
	}
	if f.PeerID != "" {
		fields["peer_id"] = f.PeerID
	}
	if f.TS != 0 {
		fields["ts"] = f.TS
	}
	if f.Nonce != "" {
		fields["nonce"] = f.Nonce
	}
	if f.Sig != "" {
		fields["sig"] = f.Sig
	}
	if f.Seq != 0 {
		fields["seq"] = f.Seq
	}
	if f.Sender != "" {
		fields["sender"] = f.Sender
	}
	if f.Clip != nil {
		fields["clip"] = f.Clip
	} else if f.Type == SSHStreamFrameState {
		fields["clip"] = nil
	}
	if f.NullReason != "" {
		fields["null_reason"] = f.NullReason
	}
	if f.ID != "" {
		fields["id"] = f.ID
	}
	if f.Status != "" {
		fields["status"] = f.Status
	}
	if f.Reason != "" {
		fields["reason"] = f.Reason
	}
	if f.Code != "" {
		fields["code"] = f.Code
	}
	return json.Marshal(fields)
}

type SSHSyncStream struct {
	auth            *Auth
	localID         string
	remoteID        string
	purpose         string
	nonces          *SSHStreamHelloNonceCache
	nextSeq         uint64
	maxPayloadBytes int
	reader          *bufio.Reader
	readCloser      io.Closer
	readDeadliner   interface{ SetReadDeadline(time.Time) error }
	writer          io.Writer
}

type SSHStreamStateResult struct {
	Seq        uint64
	Sender     string
	Content    clipboard.Content
	Origin     string
	NullReason string
}

type SSHStreamAckResult struct {
	Seq    uint64
	ID     string
	Status string
	Reason string
}

type SSHStreamEvent struct {
	Type      string
	State     SSHStreamStateResult
	Ack       SSHStreamAckResult
	ErrorCode string
}

type SSHStreamHelloNonceCache struct {
	mu         sync.Mutex
	seen       map[string]sshStreamHelloNonceEntry
	bucketCap  int
	processCap int
}

var defaultSSHStreamHelloNonceCache = NewSSHStreamHelloNonceCache()

func NewSSHStreamHelloNonceCache() *SSHStreamHelloNonceCache {
	return newSSHStreamHelloNonceCacheWithCaps(SSHStreamHelloNonceBucketCap, SSHStreamHelloNonceProcessCap)
}

func newSSHStreamHelloNonceCacheWithCaps(bucketCap int, processCap int) *SSHStreamHelloNonceCache {
	if bucketCap <= 0 {
		bucketCap = SSHStreamHelloNonceBucketCap
	}
	if processCap <= 0 {
		processCap = SSHStreamHelloNonceProcessCap
	}
	return &SSHStreamHelloNonceCache{
		seen:       map[string]sshStreamHelloNonceEntry{},
		bucketCap:  bucketCap,
		processCap: processCap,
	}
}

type sshStreamHelloNonceEntry struct {
	bucket string
	seenAt time.Time
}

func NewSSHSyncStream(auth *Auth, localID string, remoteID string, input io.Reader, output io.Writer) *SSHSyncStream {
	stream := &SSHSyncStream{
		auth:            auth,
		localID:         localID,
		remoteID:        remoteID,
		purpose:         SSHStreamPurposeSyncStream,
		nonces:          defaultSSHStreamHelloNonceCache,
		nextSeq:         1,
		maxPayloadBytes: MaxSSHStreamPayloadBytes,
		reader:          bufio.NewReader(input),
		writer:          output,
	}
	if closer, ok := input.(io.Closer); ok {
		stream.readCloser = closer
	}
	if deadliner, ok := input.(interface{ SetReadDeadline(time.Time) error }); ok {
		stream.readDeadliner = deadliner
	}
	return stream
}

func (s *SSHSyncStream) SetHelloNonceCache(cache *SSHStreamHelloNonceCache) {
	if cache == nil {
		s.nonces = defaultSSHStreamHelloNonceCache
		return
	}
	s.nonces = cache
}

func NewSSHStreamHello(auth *Auth, purpose string, hostID string, peerID string, now time.Time, nonce string) (SSHStreamHello, error) {
	if nonce == "" {
		generated, err := randomSSHStreamNonce()
		if err != nil {
			return SSHStreamHello{}, err
		}
		nonce = generated
	}
	hello := SSHStreamHello{
		Protocols: []int{SSHStreamProtocolV1},
		Purpose:   purpose,
		HostID:    hostID,
		PeerID:    peerID,
		TS:        now.UTC().Unix(),
		Nonce:     nonce,
	}
	sig, err := signSSHStreamHello(auth, hello)
	if err != nil {
		return SSHStreamHello{}, err
	}
	hello.Sig = sig
	return hello, nil
}

func (s *SSHSyncStream) WriteHello(ctx context.Context, hello SSHStreamHello) error {
	if err := verifySSHStreamHelloIdentity(hello, s.purpose, s.localID, s.remoteID); err != nil {
		return err
	}
	if err := verifySSHStreamHelloSignature(s.auth, hello); err != nil {
		return err
	}
	return s.writeFrame(ctx, helloFrame(hello))
}

func (s *SSHSyncStream) ReadHello(ctx context.Context, receivedAt time.Time) (SSHStreamHello, error) {
	frame, err := s.readFrame(ctx)
	if err != nil {
		return SSHStreamHello{}, err
	}
	if frame.Type != SSHStreamFrameHello {
		return SSHStreamHello{}, fmt.Errorf("%w: %s", ErrSSHStreamUnexpectedFrame, frame.Type)
	}
	hello := frameHello(frame)
	if err := s.verifyHello(hello, receivedAt); err != nil {
		return SSHStreamHello{}, err
	}
	return hello, nil
}

func VerifySSHStreamHello(auth *Auth, hello SSHStreamHello, receivedAt time.Time) error {
	return verifySSHStreamHello(auth, hello, receivedAt, SSHStreamPurposeSyncStream, "", "", nil)
}

func (s *SSHSyncStream) verifyHello(hello SSHStreamHello, receivedAt time.Time) error {
	return verifySSHStreamHello(s.auth, hello, receivedAt, s.purpose, s.remoteID, s.localID, s.nonces)
}

func verifySSHStreamHello(auth *Auth, hello SSHStreamHello, receivedAt time.Time, expectedPurpose string, expectedHostID string, expectedPeerID string, nonceCache *SSHStreamHelloNonceCache) error {
	if err := validateSSHStreamHelloShape(hello); err != nil {
		return err
	}
	if err := verifySSHStreamHelloIdentity(hello, expectedPurpose, expectedHostID, expectedPeerID); err != nil {
		return err
	}
	if receivedAt.IsZero() {
		return fmt.Errorf("%w: missing received time", ErrSSHStreamInvalidHello)
	}
	ts := time.Unix(hello.TS, 0)
	if ts.Before(receivedAt.Add(-signatureSkew)) || ts.After(receivedAt.Add(signatureSkew)) {
		return fmt.Errorf("%w: timestamp", ErrSSHStreamInvalidHello)
	}
	if err := verifySSHStreamHelloSignature(auth, hello); err != nil {
		return err
	}
	if nonceCache != nil && !nonceCache.remember(hello.HostID, hello.Purpose, hello.Nonce, receivedAt) {
		return fmt.Errorf("%w: replayed nonce", ErrSSHStreamInvalidHello)
	}
	return nil
}

func (s *SSHSyncStream) WriteState(ctx context.Context, seq uint64, content clipboard.Content, origin string) error {
	if seq == 0 {
		return fmt.Errorf("%w: zero seq", ErrSSHStreamUnexpectedFrame)
	}
	if len(content.Bytes) > s.maxPayloadBytes {
		return ErrSSHStreamPayloadTooLarge
	}
	env, err := BuildEnvelope(s.auth, content, origin, s.remoteID)
	if err != nil {
		return err
	}
	return s.writeFrame(ctx, SSHStreamFrame{Type: SSHStreamFrameState, Seq: seq, Sender: s.localID, Clip: &env})
}

func (s *SSHSyncStream) WriteNullState(ctx context.Context, seq uint64, nullReason string) error {
	if seq == 0 || !validSSHStreamNullReason(nullReason) {
		return fmt.Errorf("%w: null state", ErrSSHStreamUnexpectedFrame)
	}
	return s.writeFrame(ctx, SSHStreamFrame{Type: SSHStreamFrameState, Seq: seq, Sender: s.localID, NullReason: nullReason})
}

func (s *SSHSyncStream) ReadState(ctx context.Context, receivedAt time.Time) (clipboard.Content, string, error) {
	result, err := s.ReadStateFrame(ctx, receivedAt)
	if err != nil {
		return clipboard.Content{}, "", err
	}
	if result.NullReason != "" {
		return clipboard.Content{}, result.Sender, nil
	}
	return result.Content, result.Origin, nil
}

func (s *SSHSyncStream) ReadStateFrame(ctx context.Context, receivedAt time.Time) (SSHStreamStateResult, error) {
	frame, err := s.readFrame(ctx)
	if err != nil {
		return SSHStreamStateResult{}, err
	}
	return s.stateResultFromFrame(frame, receivedAt)
}

func (s *SSHSyncStream) ReadNext(ctx context.Context, receivedAt time.Time) (SSHStreamEvent, error) {
	frame, err := s.readFrame(ctx)
	if err != nil {
		return SSHStreamEvent{}, err
	}
	switch frame.Type {
	case SSHStreamFrameState:
		state, err := s.stateResultFromFrame(frame, receivedAt)
		if err != nil {
			return SSHStreamEvent{}, err
		}
		return SSHStreamEvent{Type: SSHStreamFrameState, State: state}, nil
	case SSHStreamFrameAck:
		ack, err := ackResultFromFrame(frame)
		if err != nil {
			return SSHStreamEvent{}, err
		}
		return SSHStreamEvent{Type: SSHStreamFrameAck, Ack: ack}, nil
	case SSHStreamFrameError:
		if frame.Code == "" {
			return SSHStreamEvent{}, fmt.Errorf("%w: missing error code", ErrSSHStreamUnexpectedFrame)
		}
		return SSHStreamEvent{Type: SSHStreamFrameError, ErrorCode: frame.Code}, nil
	default:
		return SSHStreamEvent{}, fmt.Errorf("%w: %s", ErrSSHStreamUnexpectedFrame, frame.Type)
	}
}

func (s *SSHSyncStream) stateResultFromFrame(frame SSHStreamFrame, receivedAt time.Time) (SSHStreamStateResult, error) {
	if frame.Type != SSHStreamFrameState || frame.Seq == 0 {
		return SSHStreamStateResult{}, fmt.Errorf("%w: %s", ErrSSHStreamUnexpectedFrame, frame.Type)
	}
	if frame.Seq != s.nextSeq {
		return SSHStreamStateResult{}, fmt.Errorf("%w: seq %d", ErrSSHStreamUnexpectedFrame, frame.Seq)
	}
	if frame.Sender != s.remoteID {
		return SSHStreamStateResult{}, ErrSSHStreamSenderMismatch
	}
	if !frame.clipPresent {
		return SSHStreamStateResult{}, fmt.Errorf("%w: missing clip", ErrSSHStreamUnexpectedFrame)
	}
	if frame.Clip == nil {
		if !validSSHStreamNullReason(frame.NullReason) {
			return SSHStreamStateResult{}, fmt.Errorf("%w: null state", ErrSSHStreamUnexpectedFrame)
		}
		s.nextSeq++
		return SSHStreamStateResult{Seq: frame.Seq, Sender: frame.Sender, NullReason: frame.NullReason}, nil
	}
	content, origin, err := OpenEnvelope(s.auth, *frame.Clip, s.localID, receivedAt)
	if err != nil {
		return SSHStreamStateResult{}, err
	}
	if len(content.Bytes) > s.maxPayloadBytes {
		return SSHStreamStateResult{}, ErrSSHStreamPayloadTooLarge
	}
	s.nextSeq++
	return SSHStreamStateResult{Seq: frame.Seq, Sender: frame.Sender, Content: content, Origin: origin}, nil
}

func (s *SSHSyncStream) WriteAck(ctx context.Context, seq uint64, clipID string, status string, reason string) error {
	if err := validateSSHStreamAck(seq, clipID, status); err != nil {
		return err
	}
	return s.writeFrame(ctx, SSHStreamFrame{Type: SSHStreamFrameAck, Seq: seq, ID: clipID, Status: status, Reason: reason})
}

func (s *SSHSyncStream) WriteError(ctx context.Context, code string) error {
	return s.writeFrame(ctx, SSHStreamFrame{Type: SSHStreamFrameError, Code: code})
}

func (s *SSHSyncStream) writeFrame(ctx context.Context, frame SSHStreamFrame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(data) > MaxSSHStreamFrameBytes {
		return ErrSSHStreamFrameTooLarge
	}
	data = append(data, '\n')
	_, err = s.writer.Write(data)
	return err
}

func (s *SSHSyncStream) readFrame(ctx context.Context) (SSHStreamFrame, error) {
	line, err := s.readFrameLine(ctx, MaxSSHStreamFrameBytes)
	if err != nil {
		return SSHStreamFrame{}, err
	}
	frame, err := decodeSSHStreamFrame(line)
	if err != nil {
		return SSHStreamFrame{}, err
	}
	switch frame.Type {
	case SSHStreamFrameHello, SSHStreamFrameState, SSHStreamFrameAck, SSHStreamFrameError:
		return frame, nil
	default:
		return SSHStreamFrame{}, fmt.Errorf("%w: %s", ErrSSHStreamUnexpectedFrame, frame.Type)
	}
}

func (s *SSHSyncStream) readFrameLine(ctx context.Context, maxPayloadBytes int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	disarm := s.armReadCancellation(ctx)
	defer disarm()
	var line []byte
	for {
		chunk, err := s.reader.ReadSlice('\n')
		if len(chunk) > 0 {
			line = append(line, chunk...)
			if sshStreamPayloadLen(line) > maxPayloadBytes {
				return nil, ErrSSHStreamFrameTooLarge
			}
		}
		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			continue
		case errors.Is(err, io.EOF):
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, ErrSSHStreamUnexpectedEOF
		default:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, err
		}
	}
}

func (s *SSHSyncStream) armReadCancellation(ctx context.Context) func() {
	if ctx.Done() == nil || (s.readCloser == nil && s.readDeadliner == nil) {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if s.readDeadliner != nil {
				_ = s.readDeadliner.SetReadDeadline(time.Now())
				return
			}
			_ = s.readCloser.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

func sshStreamPayloadLen(line []byte) int {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		return len(line) - 1
	}
	return len(line)
}

func helloFrame(hello SSHStreamHello) SSHStreamFrame {
	return SSHStreamFrame{
		Type:      SSHStreamFrameHello,
		Protocols: append([]int(nil), hello.Protocols...),
		Purpose:   hello.Purpose,
		HostID:    hello.HostID,
		PeerID:    hello.PeerID,
		TS:        hello.TS,
		Nonce:     hello.Nonce,
		Sig:       hello.Sig,
	}
}

func frameHello(frame SSHStreamFrame) SSHStreamHello {
	return SSHStreamHello{
		Protocols: append([]int(nil), frame.Protocols...),
		Purpose:   frame.Purpose,
		HostID:    frame.HostID,
		PeerID:    frame.PeerID,
		TS:        frame.TS,
		Nonce:     frame.Nonce,
		Sig:       frame.Sig,
	}
}

func validateSSHStreamHelloShape(hello SSHStreamHello) error {
	if hello.Purpose == "" || hello.HostID == "" || hello.PeerID == "" || hello.TS == 0 || hello.Nonce == "" || hello.Sig == "" {
		return fmt.Errorf("%w: missing field", ErrSSHStreamInvalidHello)
	}
	if !validSSHStreamProtocols(hello.Protocols) {
		return fmt.Errorf("%w: protocols", ErrSSHStreamInvalidHello)
	}
	return nil
}

func verifySSHStreamHelloIdentity(hello SSHStreamHello, expectedPurpose string, expectedHostID string, expectedPeerID string) error {
	if expectedPurpose != "" && hello.Purpose != expectedPurpose {
		return fmt.Errorf("%w: purpose", ErrSSHStreamInvalidHello)
	}
	if expectedHostID != "" && hello.HostID != expectedHostID {
		return fmt.Errorf("%w: host id", ErrSSHStreamInvalidHello)
	}
	if expectedPeerID != "" && hello.PeerID != expectedPeerID {
		return fmt.Errorf("%w: peer id", ErrSSHStreamInvalidHello)
	}
	return nil
}

func validSSHStreamProtocols(protocols []int) bool {
	if len(protocols) == 0 {
		return false
	}
	previous := 0
	hasV1 := false
	for _, protocol := range protocols {
		if protocol <= 0 || protocol <= previous {
			return false
		}
		if protocol == SSHStreamProtocolV1 {
			hasV1 = true
		}
		previous = protocol
	}
	return hasV1
}

func signSSHStreamHello(auth *Auth, hello SSHStreamHello) (string, error) {
	key, err := sshHelloHMACKey(auth)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(sshStreamHelloCanonical(hello))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func verifySSHStreamHelloSignature(auth *Auth, hello SSHStreamHello) error {
	if err := validateSSHStreamHelloShape(hello); err != nil {
		return err
	}
	got, err := hex.DecodeString(hello.Sig)
	if err != nil {
		return fmt.Errorf("%w: signature", ErrSSHStreamInvalidHello)
	}
	expected, err := signSSHStreamHello(auth, SSHStreamHello{
		Protocols: hello.Protocols,
		Purpose:   hello.Purpose,
		HostID:    hello.HostID,
		PeerID:    hello.PeerID,
		TS:        hello.TS,
		Nonce:     hello.Nonce,
	})
	if err != nil {
		return err
	}
	expectedBytes, err := hex.DecodeString(expected)
	if err != nil {
		return err
	}
	if !hmac.Equal(got, expectedBytes) {
		return fmt.Errorf("%w: signature", ErrSSHStreamInvalidHello)
	}
	return nil
}

func sshStreamHelloCanonical(hello SSHStreamHello) []byte {
	var b strings.Builder
	b.WriteString("clipfan-ssh-hello-v1\n")
	b.WriteString("host_id=")
	b.WriteString(hello.HostID)
	b.WriteString("\npeer_id=")
	b.WriteString(hello.PeerID)
	b.WriteString("\npurpose=")
	b.WriteString(hello.Purpose)
	b.WriteString("\nprotocols=")
	b.WriteString(sshStreamProtocolList(hello.Protocols))
	b.WriteString("\nts=")
	b.WriteString(strconv.FormatInt(hello.TS, 10))
	b.WriteString("\nnonce=")
	b.WriteString(hello.Nonce)
	b.WriteString("\n\n")
	return []byte(b.String())
}

func sshStreamProtocolList(protocols []int) string {
	values := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		values = append(values, strconv.Itoa(protocol))
	}
	return strings.Join(values, ",")
}

func sshHelloHMACKey(auth *Auth) ([]byte, error) {
	if auth == nil {
		return nil, errors.New("auth required")
	}
	return DeriveKey(auth.key, sshHelloHMACLabel)
}

func randomSSHStreamNonce() (string, error) {
	var nonce [16]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce[:]), nil
}

func validSSHStreamNullReason(reason string) bool {
	switch reason {
	case "no_visible_current", "concealed_clear", "user_cleared_current":
		return true
	default:
		return false
	}
}

func ackResultFromFrame(frame SSHStreamFrame) (SSHStreamAckResult, error) {
	if frame.Type != SSHStreamFrameAck {
		return SSHStreamAckResult{}, fmt.Errorf("%w: %s", ErrSSHStreamUnexpectedFrame, frame.Type)
	}
	if err := validateSSHStreamAck(frame.Seq, frame.ID, frame.Status); err != nil {
		return SSHStreamAckResult{}, err
	}
	return SSHStreamAckResult{Seq: frame.Seq, ID: frame.ID, Status: frame.Status, Reason: frame.Reason}, nil
}

func validateSSHStreamAck(seq uint64, clipID string, status string) error {
	if seq == 0 || !validSSHStreamAckStatus(status) {
		return fmt.Errorf("%w: ack", ErrSSHStreamUnexpectedFrame)
	}
	if status == "no_state" {
		if clipID != "" {
			return fmt.Errorf("%w: no_state ack id", ErrSSHStreamUnexpectedFrame)
		}
		return nil
	}
	if clipID == "" {
		return fmt.Errorf("%w: missing ack id", ErrSSHStreamUnexpectedFrame)
	}
	return nil
}

func validSSHStreamAckStatus(status string) bool {
	switch status {
	case "no_state", "applied", "ignored_seen", "ignored_echo", "ignored_older", "ignored_concealed", "rejected":
		return true
	default:
		return false
	}
}

func (c *SSHStreamHelloNonceCache) remember(hostID string, purpose string, nonce string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string]sshStreamHelloNonceEntry{}
	}
	if c.bucketCap <= 0 {
		c.bucketCap = SSHStreamHelloNonceBucketCap
	}
	if c.processCap <= 0 {
		c.processCap = SSHStreamHelloNonceProcessCap
	}
	expiresBefore := now.Add(-nonceRetention)
	for key, entry := range c.seen {
		if entry.seenAt.Before(expiresBefore) {
			delete(c.seen, key)
		}
	}
	bucket := sshStreamHelloNonceBucket(hostID, purpose)
	key := sshStreamHelloNonceKey(bucket, nonce)
	if _, ok := c.seen[key]; ok {
		return false
	}
	for c.bucketSize(bucket) >= c.bucketCap {
		c.evictOldestInBucket(bucket)
	}
	for len(c.seen) >= c.processCap {
		c.evictOldest()
	}
	c.seen[key] = sshStreamHelloNonceEntry{bucket: bucket, seenAt: now}
	return true
}

func (c *SSHStreamHelloNonceCache) bucketSize(bucket string) int {
	count := 0
	for _, entry := range c.seen {
		if entry.bucket == bucket {
			count++
		}
	}
	return count
}

func (c *SSHStreamHelloNonceCache) evictOldestInBucket(bucket string) {
	oldestKey := ""
	var oldest time.Time
	for key, entry := range c.seen {
		if entry.bucket != bucket {
			continue
		}
		if oldestKey == "" || entry.seenAt.Before(oldest) {
			oldestKey = key
			oldest = entry.seenAt
		}
	}
	if oldestKey != "" {
		delete(c.seen, oldestKey)
	}
}

func (c *SSHStreamHelloNonceCache) evictOldest() {
	oldestKey := ""
	var oldest time.Time
	for key, entry := range c.seen {
		if oldestKey == "" || entry.seenAt.Before(oldest) {
			oldestKey = key
			oldest = entry.seenAt
		}
	}
	if oldestKey != "" {
		delete(c.seen, oldestKey)
	}
}

func sshStreamHelloNonceBucket(hostID string, purpose string) string {
	return hostID + "\x00" + purpose
}

func sshStreamHelloNonceKey(bucket string, nonce string) string {
	return bucket + "\x00" + nonce
}

func decodeSSHStreamFrame(line []byte) (SSHStreamFrame, error) {
	if err := validateSSHStreamFrameObject(line); err != nil {
		return SSHStreamFrame{}, err
	}
	var frame SSHStreamFrame
	if err := json.Unmarshal(line, &frame); err != nil {
		return SSHStreamFrame{}, err
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(line, &rawFields); err != nil {
		return SSHStreamFrame{}, err
	}
	_, frame.clipPresent = rawFields["clip"]
	return frame, nil
}

func validateSSHStreamFrameObject(line []byte) error {
	dec := json.NewDecoder(bytes.NewReader(line))
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("%w: root object", ErrSSHStreamUnexpectedFrame)
	}
	seen := map[string]struct{}{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("%w: object key", ErrSSHStreamUnexpectedFrame)
		}
		if _, ok := sshStreamFrameFields[key]; !ok {
			return fmt.Errorf("%w: unknown field %s", ErrSSHStreamUnexpectedFrame, key)
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: duplicate field %s", ErrSSHStreamUnexpectedFrame, key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
	}
	token, err = dec.Token()
	if err != nil {
		return err
	}
	delim, ok = token.(json.Delim)
	if !ok || delim != '}' {
		return fmt.Errorf("%w: root object", ErrSSHStreamUnexpectedFrame)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing data", ErrSSHStreamUnexpectedFrame)
		}
		return err
	}
	return nil
}

var sshStreamFrameFields = map[string]struct{}{
	"type":        {},
	"protocols":   {},
	"purpose":     {},
	"host_id":     {},
	"peer_id":     {},
	"ts":          {},
	"nonce":       {},
	"sig":         {},
	"seq":         {},
	"sender":      {},
	"clip":        {},
	"null_reason": {},
	"id":          {},
	"status":      {},
	"reason":      {},
	"code":        {},
}
