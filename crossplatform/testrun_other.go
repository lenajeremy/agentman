//go:build !darwin

package main

import (
	"fmt"
	"os"
	"unsafe"
)

// On Linux the app is compared on a virtual display (scripts/teststack/
// linux.sh), which nobody sees, so a test run has nothing to hide.
func testHide(unsafe.Pointer) {}

func testSnapshot(unsafe.Pointer, string) {
	fmt.Fprintln(os.Stderr, "agentman: test snapshots are macOS only; use linux.sh")
	os.Exit(3)
}
