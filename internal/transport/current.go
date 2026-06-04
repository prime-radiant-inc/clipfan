package transport

import (
	"errors"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

type CurrentPayload struct {
	HasCurrent bool           `json:"has_current"`
	NullReason string         `json:"null_reason,omitempty"`
	ID         string         `json:"id,omitempty"`
	Kind       clipboard.Kind `json:"kind,omitempty"`
	Body       []byte         `json:"body,omitempty"`
	TS         time.Time      `json:"ts,omitempty"`
	Origin     string         `json:"origin,omitempty"`
}

func CurrentPayloadFromContent(content clipboard.Content, origin string) CurrentPayload {
	return CurrentPayload{
		HasCurrent: true,
		ID:         content.ID,
		Kind:       content.Kind,
		Body:       append([]byte(nil), content.Bytes...),
		TS:         content.TS,
		Origin:     origin,
	}
}

func NoCurrentPayload(reason string) CurrentPayload {
	if reason == "" {
		reason = "no_visible_current"
	}
	return CurrentPayload{HasCurrent: false, NullReason: reason}
}

func (p CurrentPayload) Content() (clipboard.Content, bool, error) {
	if !p.HasCurrent {
		return clipboard.Content{}, false, nil
	}
	if p.ID == "" || p.TS.IsZero() {
		return clipboard.Content{}, false, errors.New("invalid current payload")
	}
	switch p.Kind {
	case clipboard.KindText, clipboard.KindImage:
	default:
		return clipboard.Content{}, false, errors.New("invalid current kind")
	}
	content := clipboard.New(p.Kind, append([]byte(nil), p.Body...), p.TS)
	content.ID = p.ID
	return content, true, nil
}
