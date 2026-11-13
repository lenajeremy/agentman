//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// Linux has no dock badge every desktop shows; the sidebar says what needs you.
func badgeServices() []application.Service { return nil }

func setBadge(string) error { return nil }
