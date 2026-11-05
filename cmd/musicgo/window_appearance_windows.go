package main

import (
	"runtime"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo"
)

// The normal Direct3D surface is opaque. Let Windows clip the real window
// instead of painting transparent UI corners that would appear black.
const mainWindowTransparent = false

func configureWindowCorners(win *mygo.Window) {
	setAttribute := syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	if setAttribute.Find() != nil {
		return
	}
	const dwmWindowCornerPreference = 33
	preference := uint32(2) // DWMWCP_ROUND; Windows squares maximized windows.
	_, _, _ = setAttribute.Call(win.NativeHandle(), dwmWindowCornerPreference,
		uintptr(unsafe.Pointer(&preference)), unsafe.Sizeof(preference))
	runtime.KeepAlive(&preference)
}
