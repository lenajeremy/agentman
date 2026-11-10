package main

import "github.com/wailsapp/wails/v3/pkg/application"

// Appearance sets the app's look, as the Mac app's Appearance.apply
// (Views/SettingsView.swift): "light", "dark", or "system" to follow the
// computer. It changes every window at once, their pages (whose
// prefers-color-scheme follows), title bars, menus and alerts.
type Appearance struct{}

func (a *Appearance) Set(choice string) {
	application.InvokeSync(func() { setAppearance(choice) })
}
