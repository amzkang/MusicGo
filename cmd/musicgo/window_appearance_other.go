//go:build !windows

package main

import "github.com/egoist/mygo"

// GTK and AppKit composite the transparent corners painted by the native UI.
const mainWindowTransparent = true

func configureWindowCorners(_ *mygo.Window) {}
