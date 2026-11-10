//go:build !darwin

package main

import "unsafe"

// settingsMaterial has no sidebar material to show on Linux: the page paints
// its sidebar itself.
func settingsMaterial(unsafe.Pointer) {}
