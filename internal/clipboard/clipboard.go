package clipboard

import (
	"crypto/sha256"
	"time"
)

type Kind string

const (
	KindText  Kind = "text"
	KindImage Kind = "image"
)

type Content struct {
	Kind  Kind
	Bytes []byte
	Hash  [32]byte
	TS    time.Time
}

func New(kind Kind, body []byte, ts time.Time) Content {
	return Content{
		Kind:  kind,
		Bytes: body,
		Hash:  sha256.Sum256(body),
		TS:    ts,
	}
}

type Backend interface {
	Read() (Content, error)
	Write(Content) error
}
