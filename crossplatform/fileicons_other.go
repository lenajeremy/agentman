//go:build !darwin

package main

// drawFileIcon has no system icons to draw off macOS: the page draws its own.
func drawFileIcon(path string, size, scale float64) []byte { return nil }
