package transport

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	AuthVersionRequestHMAC = "clipfan-v1/request-hmac"
	hkdfSalt               = "clipfan-v1/hkdf-salt"
	sshHelloHMACLabel      = "clipfan-v1/ssh-hello-hmac"
	bodyAEADLabel          = "clipfan-v1/body-aead"

	HeaderAuthVersion = "X-Clipfan-Auth-Version"
	HeaderTimestamp   = "X-Clipfan-Ts"
	HeaderNonce       = "X-Clipfan-Nonce"
	HeaderSignature   = "X-Clipfan-Sig"
)

var (
	ErrAuthVersionMismatch = errors.New("auth_version_mismatch")
	ErrBadSignature        = errors.New("bad_signature")
)

type Auth struct{ key []byte }

func NewAuth(b64Key string) (*Auth, error) {
	k, err := base64.StdEncoding.DecodeString(b64Key)
	if err != nil {
		return nil, err
	}
	if len(k) < 16 {
		return nil, errors.New("shared key too short (need 16+ bytes after base64 decode)")
	}
	return &Auth{key: k}, nil
}

func DeriveKey(rawKey []byte, label string) ([]byte, error) {
	switch label {
	case AuthVersionRequestHMAC, sshHelloHMACLabel, bodyAEADLabel:
	default:
		return nil, fmt.Errorf("unknown clipfan key label: %s", label)
	}
	return hkdfSHA256(rawKey, []byte(hkdfSalt), []byte(label), 32), nil
}

func (a *Auth) RequestHMACKey() ([]byte, error) {
	if a == nil {
		return nil, errors.New("auth required")
	}
	return DeriveKey(a.key, AuthVersionRequestHMAC)
}

func (a *Auth) SignRequest(method, requestURI, timestamp, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, a.key)
	writeCanonicalRequest(mac, method, requestURI, timestamp, nonce, body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *Auth) VerifyRequest(method, requestURI, timestamp, nonce string, body []byte, sig string) error {
	expect, err := hex.DecodeString(sig)
	if err != nil {
		return err
	}
	got := hmac.New(sha256.New, a.key)
	writeCanonicalRequest(got, method, requestURI, timestamp, nonce, body)
	if !hmac.Equal(expect, got.Sum(nil)) {
		return errors.New("bad signature")
	}
	return nil
}

func (a *Auth) SignRequestWithAuthVersion(method, requestURI, timestamp, nonce string, body []byte, authVersion string) (string, error) {
	key, err := a.requestSigningKey(authVersion)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	writeCanonicalRequestWithAuthVersion(mac, method, requestURI, timestamp, nonce, body, authVersion)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (a *Auth) VerifyRequestWithAuthVersion(method, requestURI, timestamp, nonce string, body []byte, sig, authVersion string) error {
	expect, err := hex.DecodeString(sig)
	if err != nil {
		return err
	}
	key, err := a.requestSigningKey(authVersion)
	if err != nil {
		return err
	}
	got := hmac.New(sha256.New, key)
	writeCanonicalRequestWithAuthVersion(got, method, requestURI, timestamp, nonce, body, authVersion)
	if !hmac.Equal(expect, got.Sum(nil)) {
		return errors.New("bad signature")
	}
	return nil
}

func (a *Auth) VerifyRequestRequiredAuthVersion(method, requestURI, timestamp, nonce string, body []byte, sig, authVersion, requiredAuthVersion string) error {
	if authVersion != requiredAuthVersion {
		return ErrAuthVersionMismatch
	}
	if err := a.VerifyRequestWithAuthVersion(method, requestURI, timestamp, nonce, body, sig, authVersion); err != nil {
		return ErrBadSignature
	}
	return nil
}

func (a *Auth) SignResponse(requestNonce string, body []byte) string {
	mac := hmac.New(sha256.New, a.key)
	writeCanonicalResponse(mac, requestNonce, body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *Auth) VerifyResponse(requestNonce string, body []byte, sig string) error {
	expect, err := hex.DecodeString(sig)
	if err != nil {
		return err
	}
	got := hmac.New(sha256.New, a.key)
	writeCanonicalResponse(got, requestNonce, body)
	if !hmac.Equal(expect, got.Sum(nil)) {
		return errors.New("bad response signature")
	}
	return nil
}

func (a *Auth) SignResponseWithAuthVersion(requestNonce string, body []byte, authVersion string) (string, error) {
	key, err := a.requestSigningKey(authVersion)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	writeCanonicalResponseWithAuthVersion(mac, requestNonce, body, authVersion)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (a *Auth) VerifyResponseWithAuthVersion(requestNonce string, body []byte, sig, authVersion string) error {
	expect, err := hex.DecodeString(sig)
	if err != nil {
		return err
	}
	key, err := a.requestSigningKey(authVersion)
	if err != nil {
		return err
	}
	got := hmac.New(sha256.New, key)
	writeCanonicalResponseWithAuthVersion(got, requestNonce, body, authVersion)
	if !hmac.Equal(expect, got.Sum(nil)) {
		return errors.New("bad response signature")
	}
	return nil
}

type SignedRequestOptions struct {
	Timestamp   time.Time
	Nonce       string
	AuthVersion string
}

func (a *Auth) SignedRequestHeaders(method, requestURI string, body []byte, opts SignedRequestOptions) (map[string]string, error) {
	t := opts.Timestamp
	if t.IsZero() {
		t = time.Now()
	}
	nonce := opts.Nonce
	if nonce == "" {
		nonce = NewClipID()
		if nonce == "" {
			return nil, errors.New("generate request nonce")
		}
	}
	ts := fmt.Sprintf("%d", t.Unix())
	sig, err := a.SignRequestWithAuthVersion(method, requestURI, ts, nonce, body, opts.AuthVersion)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{
		HeaderTimestamp: ts,
		HeaderNonce:     nonce,
		HeaderSignature: sig,
	}
	if opts.AuthVersion != "" {
		headers[HeaderAuthVersion] = opts.AuthVersion
	}
	return headers, nil
}

func (a *Auth) requestSigningKey(authVersion string) ([]byte, error) {
	if a == nil {
		return nil, errors.New("auth required")
	}
	switch authVersion {
	case "":
		return a.key, nil
	case AuthVersionRequestHMAC:
		return a.RequestHMACKey()
	default:
		return nil, fmt.Errorf("unsupported auth version: %s", authVersion)
	}
}

func writeCanonicalRequest(mac hashWriter, method, requestURI, timestamp, nonce string, body []byte) {
	mac.Write([]byte(method))
	mac.Write([]byte("\n"))
	mac.Write([]byte(requestURI))
	mac.Write([]byte("\n"))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write([]byte(nonce))
	mac.Write([]byte("\n"))
	mac.Write(body)
}

func writeCanonicalRequestWithAuthVersion(mac hashWriter, method, requestURI, timestamp, nonce string, body []byte, authVersion string) {
	if authVersion == "" {
		writeCanonicalRequest(mac, method, requestURI, timestamp, nonce, body)
		return
	}
	mac.Write([]byte(method))
	mac.Write([]byte("\n"))
	mac.Write([]byte(requestURI))
	mac.Write([]byte("\n"))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write([]byte(nonce))
	mac.Write([]byte("\n"))
	mac.Write([]byte("auth_version="))
	mac.Write([]byte(authVersion))
	mac.Write([]byte("\n"))
	mac.Write(body)
}

func writeCanonicalResponse(mac hashWriter, requestNonce string, body []byte) {
	mac.Write([]byte("response\n"))
	mac.Write([]byte(requestNonce))
	mac.Write([]byte("\n"))
	mac.Write(body)
}

func writeCanonicalResponseWithAuthVersion(mac hashWriter, requestNonce string, body []byte, authVersion string) {
	if authVersion == "" {
		writeCanonicalResponse(mac, requestNonce, body)
		return
	}
	mac.Write([]byte("response\n"))
	mac.Write([]byte(requestNonce))
	mac.Write([]byte("\n"))
	mac.Write([]byte("auth_version="))
	mac.Write([]byte(authVersion))
	mac.Write([]byte("\n"))
	mac.Write(body)
}

func hkdfSHA256(ikm, salt, info []byte, length int) []byte {
	extract := hmac.New(sha256.New, salt)
	extract.Write(ikm)
	prk := extract.Sum(nil)

	var okm []byte
	var prev []byte
	for counter := byte(1); len(okm) < length; counter++ {
		expand := hmac.New(sha256.New, prk)
		expand.Write(prev)
		expand.Write(info)
		expand.Write([]byte{counter})
		prev = expand.Sum(nil)
		okm = append(okm, prev...)
	}
	return okm[:length]
}

type hashWriter interface {
	Write([]byte) (int, error)
}
