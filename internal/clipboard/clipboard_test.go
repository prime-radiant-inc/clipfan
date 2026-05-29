package clipboard

import (
	"testing"
	"time"
)

func TestNewDefaultsNotConcealed(t *testing.T) {
	c := New(KindText, []byte("x"), time.Unix(1, 0))
	if c.Concealed {
		t.Fatal("New content should default to not concealed")
	}
}
