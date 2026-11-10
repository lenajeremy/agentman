//go:build !darwin

package main

import (
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// watchScreenLock hears the screen lock and unlock: from logind's session
// (Lock and Unlock), and from the desktop's screen saver (ActiveChanged), on
// D-Bus. A desktop without either says nothing, and only the app in the
// background and sleep count as away.
func watchScreenLock(handler func(locked bool)) {
	go func() {
		if conn, err := dbus.ConnectSystemBus(); err == nil {
			_ = conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.login1.Session"))
			listen(conn, handler)
		}
	}()
	go func() {
		if conn, err := dbus.ConnectSessionBus(); err == nil {
			_ = conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.ScreenSaver"), dbus.WithMatchMember("ActiveChanged"))
			_ = conn.AddMatchSignal(dbus.WithMatchInterface("org.gnome.ScreenSaver"), dbus.WithMatchMember("ActiveChanged"))
			listen(conn, handler)
		}
	}()
}

func listen(conn *dbus.Conn, handler func(locked bool)) {
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	for s := range signals {
		switch s.Name {
		case "org.freedesktop.login1.Session.Lock":
			handler(true)
		case "org.freedesktop.login1.Session.Unlock":
			handler(false)
		case "org.freedesktop.ScreenSaver.ActiveChanged", "org.gnome.ScreenSaver.ActiveChanged":
			if len(s.Body) > 0 {
				if active, ok := s.Body[0].(bool); ok {
					handler(active)
				}
			}
		}
	}
}

// watchActive tells the shell when the app comes to the front and leaves
// it: whether any of its windows has the focus, looked at twice a second,
// so moving between its own windows isn't leaving.
func watchActive(app *application.App, set func(bool)) {
	go func() {
		for range time.Tick(500 * time.Millisecond) {
			focused := false
			for _, w := range app.Window.GetAll() {
				if w.IsFocused() {
					focused = true
					break
				}
			}
			set(focused)
		}
	}()
}
