//go:build !darwin && !linux

package main

// setAppearance leaves the look to the system where the app has no say.
func setAppearance(string) {}
