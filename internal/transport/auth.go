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

func (a *Auth) Sign(body []byte) string {
	mac := hmac.New(sha256.New, a.key)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *Auth) Verify(body []byte, sig string) error {
	expect, err := hex.DecodeString(sig)
	if err != nil {
		return err
	}
	got := hmac.New(sha256.New, a.key)
	got.Write(body)
	if !hmac.Equal(expect, got.Sum(nil)) {
		return errors.New("bad signature")
	}
	return nil
}
