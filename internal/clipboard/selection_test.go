package clipboard

import "testing"

func TestChooseBackend(t *testing.T) {
	all := func(string) bool { return true }
	none := func(string) bool { return false }
	only := func(name string) func(string) bool {
		return func(b string) bool { return b == name }
	}

	cases := []struct {
		name    string
		wayland string
		x11     string
		have    func(string) bool
		want    backendKind
	}{
		{"headless: no display at all", "", "", all, backendHeadless},
		{"headless: xclip present but no DISPLAY", "", "", only("xclip"), backendHeadless},
		{"xclip: DISPLAY set and xclip present", "", ":0", only("xclip"), backendXclip},
		{"headless: DISPLAY set but xclip missing", "", ":0", none, backendHeadless},
		{"wayland: WAYLAND_DISPLAY set and wl-paste present", "wayland-0", "", only("wl-paste"), backendWayland},
		{"xclip fallback: wayland set but wl-paste missing, DISPLAY+xclip present", "wayland-0", ":0", only("xclip"), backendXclip},
		{"headless: wayland set but wl-paste missing and no DISPLAY", "wayland-0", "", none, backendHeadless},
		{"wayland wins when both displays + both tools present", "wayland-0", ":0", all, backendWayland},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := chooseBackend(c.wayland, c.x11, c.have); got != c.want {
				t.Fatalf("chooseBackend(%q,%q) = %q, want %q", c.wayland, c.x11, got, c.want)
			}
		})
	}
}
