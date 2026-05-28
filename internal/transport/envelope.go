package transport

import (
	"encoding/base64"
	"time"
)

type Envelope struct {
	Origin string    `json:"origin"`
	TS     time.Time `json:"ts"`
	Kind   string    `json:"kind"`
	SHA256 string    `json:"sha256"`
	Body   string    `json:"body"`
}

func (e *Envelope) Bytes() ([]byte, error) {
	return base64.StdEncoding.DecodeString(e.Body)
}

func EncodeBody(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
