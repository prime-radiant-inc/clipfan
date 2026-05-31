package transport

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

func (a *Auth) SealBody(plain []byte) (body string, nonce string, err error) {
	gcm, err := a.bodyCipher()
	if err != nil {
		return "", "", err
	}
	nonceBytes := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonceBytes); err != nil {
		return "", "", err
	}
	ciphertext := gcm.Seal(nil, nonceBytes, plain, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), base64.StdEncoding.EncodeToString(nonceBytes), nil
}

func (a *Auth) OpenBody(nonce, body string) ([]byte, error) {
	gcm, err := a.bodyCipher()
	if err != nil {
		return nil, err
	}
	nonceBytes, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		return nil, err
	}
	if len(nonceBytes) != gcm.NonceSize() {
		return nil, errors.New("invalid body nonce")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonceBytes, ciphertext, nil)
}

func (a *Auth) bodyCipher() (cipher.AEAD, error) {
	if a == nil {
		return nil, errors.New("auth required")
	}
	key := sha256.Sum256(a.key)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
