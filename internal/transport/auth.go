package transport

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
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

func writeCanonicalResponse(mac hashWriter, requestNonce string, body []byte) {
	mac.Write([]byte("response\n"))
	mac.Write([]byte(requestNonce))
	mac.Write([]byte("\n"))
	mac.Write(body)
}

type hashWriter interface {
	Write([]byte) (int, error)
}
