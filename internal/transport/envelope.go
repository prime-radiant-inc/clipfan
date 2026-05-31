package transport

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"time"
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
