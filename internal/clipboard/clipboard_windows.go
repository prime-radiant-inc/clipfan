//go:build windows

package clipboard

import (
	"bytes"
	"errors"
	"os/exec"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsBackend reads/writes the Windows clipboard. Text uses the built-in
// clip.exe / Get-Clipboard; images use the registered "PNG" clipboard format
// (raw PNG bytes — the same representation the macOS/Linux backends exchange).
type windowsBackend struct{}

func NewBackend() Backend { return &windowsBackend{} }

// Lazy-loaded user32/kernel32 entry points for clipboard + global memory.
// x/sys/windows does not export the clipboard or Global* functions.
var (
	lazyUser32   = windows.NewLazySystemDLL("user32.dll")
	lazyKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard              = lazyUser32.NewProc("OpenClipboard")
	procCloseClipboard             = lazyUser32.NewProc("CloseClipboard")
	procEmptyClipboard             = lazyUser32.NewProc("EmptyClipboard")
	procIsClipboardFormatAvailable = lazyUser32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardData           = lazyUser32.NewProc("GetClipboardData")
	procSetClipboardData           = lazyUser32.NewProc("SetClipboardData")
	procRegisterClipboardFormat    = lazyUser32.NewProc("RegisterClipboardFormatW")

	procGlobalAlloc  = lazyKernel32.NewProc("GlobalAlloc")
	procGlobalLock   = lazyKernel32.NewProc("GlobalLock")
	procGlobalUnlock = lazyKernel32.NewProc("GlobalUnlock")
	procGlobalFree   = lazyKernel32.NewProc("GlobalFree")
	procGlobalSize   = lazyKernel32.NewProc("GlobalSize")
)

const gmemMoveable = 0x0002

// pngClipboardFormat returns the registered "PNG" format id (cached), or 0.
var (
	pngFormatOnce sync.Once
	pngFormat     uint32
)

func pngClipboardFormat() uint32 {
	pngFormatOnce.Do(func() {
		pngFormat = registerFormat("PNG")
	})
	return pngFormat
}

func registerFormat(name string) uint32 {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := procRegisterClipboardFormat.Call(uintptr(unsafe.Pointer(n)))
	return uint32(r)
}

// openClipboard opens the clipboard with no owner window; returns the BOOL result.
func openClipboard() uintptr {
	r, _, _ := procOpenClipboard.Call(0)
	return r
}

// readImagePNG returns the raw PNG bytes held on the clipboard under the
// registered "PNG" format, if any.
func readImagePNG() ([]byte, bool) {
	fmt := pngClipboardFormat()
	if fmt == 0 {
		return nil, false
	}
	if openClipboard() == 0 {
		return nil, false
	}
	defer procCloseClipboard.Call()
	if avail, _, _ := procIsClipboardFormatAvailable.Call(uintptr(fmt)); avail == 0 {
		return nil, false
	}
	h, _, _ := procGetClipboardData.Call(uintptr(fmt))
	if h == 0 {
		return nil, false
	}
	sz, _, _ := procGlobalSize.Call(h)
	if sz == 0 {
		return nil, false
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(h)
	buf := make([]byte, int(sz))
	copy(buf, unsafe.Slice((*byte)(unsafe.Add(unsafe.Pointer(nil), ptr)), int(sz)))
	return buf, true
}

// writeImagePNG writes raw PNG bytes to the clipboard's "PNG" format, owning
// the whole clipboard (EmptyClipboard + SetClipboardData).
func writeImagePNG(png []byte) error {
	fmt := pngClipboardFormat()
	if fmt == 0 {
		return errors.New("clipfan: could not register PNG clipboard format")
	}
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(png)))
	if h == 0 {
		return errors.New("clipfan: GlobalAlloc failed")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		procGlobalFree.Call(h)
		return errors.New("clipfan: GlobalLock failed")
	}
	copy(unsafe.Slice((*byte)(unsafe.Add(unsafe.Pointer(nil), ptr)), len(png)), png)
	procGlobalUnlock.Call(h)
	if openClipboard() == 0 {
		procGlobalFree.Call(h)
		return errors.New("clipfan: OpenClipboard failed")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if hr, _, _ := procSetClipboardData.Call(uintptr(fmt), h); hr == 0 {
		procGlobalFree.Call(h)
		return errors.New("clipfan: SetClipboardData failed")
	}
	// On success the clipboard (now the system) owns the memory; do not free.
	return nil
}

// clipboardViewerIgnorePresent reports whether the current clipboard carries
// the "Clipboard Viewer Ignore" marker some password managers set to ask for
// exclusion from history. Best-effort concealment signal.
func clipboardViewerIgnorePresent() bool {
	fmt := registerFormat("Clipboard Viewer Ignore")
	if fmt == 0 {
		return false
	}
	if openClipboard() == 0 {
		return false
	}
	defer procCloseClipboard.Call()
	avail, _, _ := procIsClipboardFormatAvailable.Call(uintptr(fmt))
	return avail != 0
}

// Read returns the current clipboard contents: a PNG image if present, else
// text via PowerShell Get-Clipboard.
func (windowsBackend) Read() (Content, error) {
	concealed := clipboardViewerIgnorePresent()
	if png, ok := readImagePNG(); ok && len(png) > 0 {
		c := New(KindImage, png, time.Now().UTC())
		c.Concealed = concealed
		return c, nil
	}
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "Get-Clipboard -Raw")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return Content{}, nil
	}
	c := New(KindText, out, time.Now().UTC())
	c.Concealed = concealed
	return c, nil
}

// WriteText pipes text into clip.exe (the built-in Windows clipboard setter).
func (windowsBackend) WriteText(text []byte) error {
	cmd := exec.Command("clip")
	cmd.Stdin = bytes.NewReader(text)
	return cmd.Run()
}

// WriteImage writes the PNG bytes to the clipboard's "PNG" format. If that
// fails it falls back to writing the file path as text (matching the Linux
// backend's behaviour).
func (windowsBackend) WriteImage(body []byte, path string) error {
	if len(body) == 0 {
		return nil
	}
	if err := writeImagePNG(body); err == nil {
		return nil
	}
	if path == "" {
		return nil
	}
	return (windowsBackend{}).WriteText([]byte(path))
}
