//go:build darwin

package clipboard

import (
	"bytes"
	"os/exec"
	"time"
)

type macBackend struct{}

func NewBackend() Backend { return &macBackend{} }

func (macBackend) Read() (Content, error) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return Content{}, err
	}
	return New(KindText, out, time.Now().UTC()), nil
}

func (macBackend) Write(c Content) error {
	if c.Kind != KindText {
		return nil
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = bytes.NewReader(c.Bytes)
	return cmd.Run()
}
