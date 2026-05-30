package daemon

import "testing"

func TestPeersHandlerIncludesVersion(t *testing.T) {
	d, _, _ := newTestDaemon(t)
	out, ok := d.peersHandler().(map[string]any)
	if !ok {
		t.Fatalf("peersHandler returned %T, want map[string]any", d.peersHandler())
	}
	v, ok := out["version"]
	if !ok {
		t.Fatal("peers response missing \"version\" field")
	}
	if v != "dev" {
		t.Fatalf("version = %v, want \"dev\" (default)", v)
	}
}
