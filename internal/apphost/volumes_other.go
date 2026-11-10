//go:build !darwin

package apphost

func browsable(string) bool { return true }
