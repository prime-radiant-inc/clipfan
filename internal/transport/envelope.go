package transport

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

type Envelope struct {
	ID        string    `json:"id"`
	Origin    string    `json:"origin"`
	Recipient string    `json:"recipient"`
	TS        time.Time `json:"ts"`
	Kind      string    `json:"kind"`
	Body      string    `json:"body"`
	Nonce     string    `json:"nonce"`
	Concealed bool      `json:"concealed"`
}

var (
	ErrWrongRecipient          = errors.New("wrong_recipient")
	ErrFutureEnvelopeTimestamp = errors.New("future_envelope_timestamp")
	ErrEnvelopeDecrypt         = errors.New("envelope_decrypt_failed")
)

// NewClipID returns a random 128-bit hex token identifying one logical clip.
// Assigned once at a clip's origin and preserved verbatim through every relay,
// so the mesh can dedup by identity rather than by mutable content bytes.
func NewClipID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// An empty ID is treated downstream as "no ID": the receiving node drops
		// the clip rather than propagating a bad identifier, so a CSPRNG failure
		// degrades safely to non-propagation instead of a corrupted clip ID.
		return ""
	}
	return hex.EncodeToString(b[:])
}

func (e *Envelope) Bytes(auth *Auth) ([]byte, error) {
	return auth.OpenBody(e.Nonce, e.Body)
}

func EncodeBody(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func BuildEnvelope(auth *Auth, content clipboard.Content, origin string, recipient string) (Envelope, error) {
	body, bodyNonce, err := auth.SealBody(content.Bytes)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		ID:        content.ID,
		Origin:    origin,
		Recipient: recipient,
		TS:        content.TS,
		Kind:      string(content.Kind),
		Body:      body,
		Nonce:     bodyNonce,
		Concealed: content.Concealed,
	}, nil
}

func OpenEnvelope(auth *Auth, env Envelope, recipientIdentity string, receivedAt time.Time) (clipboard.Content, string, error) {
	if recipientIdentity != "" && !RecipientMatches(env.Recipient, recipientIdentity) {
		return clipboard.Content{}, "", ErrWrongRecipient
	}
	if env.TS.After(receivedAt.Add(signatureSkew)) {
		return clipboard.Content{}, "", ErrFutureEnvelopeTimestamp
	}
	raw, err := env.Bytes(auth)
	if err != nil {
		return clipboard.Content{}, "", fmt.Errorf("%w: %v", ErrEnvelopeDecrypt, err)
	}
	c := clipboard.New(clipboard.Kind(env.Kind), raw, env.TS)
	c.ID = env.ID
	c.Concealed = env.Concealed
	return c, env.Origin, nil
}
