package clipboard

// backendKind names the clipboard backend chosen for the current environment.
type backendKind string

const (
	backendWayland  backendKind = "wayland"
	backendXclip    backendKind = "xclip"
	backendHeadless backendKind = "headless"
)

// chooseBackend decides which clipboard backend to use from the display
// environment and which helper binaries are available. A graphical backend is
// chosen only when its display server is actually present: Wayland requires
// WAYLAND_DISPLAY, X11/xclip requires DISPLAY. On a headless host (neither set)
// it returns backendHeadless so the daemon never shells out to a clipboard tool
// that has no display to talk to — the cause of repeated "can't open display"
// write failures. have reports whether a named binary is on PATH.
func chooseBackend(waylandDisplay, x11Display string, have func(string) bool) backendKind {
	if waylandDisplay != "" && have("wl-paste") {
		return backendWayland
	}
	if x11Display != "" && have("xclip") {
		return backendXclip
	}
	return backendHeadless
}
