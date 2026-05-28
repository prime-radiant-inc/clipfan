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
	WriteText(text []byte) error
	// WriteImage sets the OS clipboard to an image with a richer
	// representation than the text path. On macOS we write a single
	// NSPasteboardItem containing BOTH the PNG bytes (public.png) and
	// the file path as text (public.utf8-plain-text) — so Cmd-V into
	// Preview pastes the image while Cmd-V into a TUI app pastes the
	// path string. On Linux we punt to text-only because xclip has no
	// clean multi-target write.
	WriteImage(body []byte, path string) error
}
