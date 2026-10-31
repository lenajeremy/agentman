package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// Host is what the screens ask the system for, as the Mac app asks AppKit:
// file pickers, showing a file in the file manager, opening a file or link,
// notifications and the dock badge. Each is the Mac's on macOS and the
// freedesktop way on Linux.
type Host struct {
	notes *notifications.NotificationService
}

func newHost() *Host {
	h := &Host{}
	if notificationsWork() {
		h.notes = notifications.New()
	}
	return h
}

// notificationsWork says whether this process can post notifications: on
// macOS only from an app bundle (they need its identifier), not from the
// bare binary a development build is.
func notificationsWork() bool {
	if runtime.GOOS != "darwin" {
		return true
	}
	exe, err := os.Executable()
	return err == nil && strings.Contains(exe, ".app/Contents/MacOS/")
}

var errNoNotifications = errors.New("notifications need Agentman.app; a development build can't post them")

// PickFolder asks for a folder, as NSOpenPanel does; "" when cancelled.
func (h *Host) PickFolder(title, start string) (string, error) {
	d := application.Get().Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).SetTitle(title)
	if start != "" {
		d = d.SetDirectory(start)
	}
	return d.PromptForSingleSelection()
}

// PickFiles asks for files to attach; none when cancelled.
func (h *Host) PickFiles(title, start string) ([]string, error) {
	d := application.Get().Dialog.OpenFile().CanChooseFiles(true).CanChooseDirectories(false).SetTitle(title)
	if start != "" {
		d = d.SetDirectory(start)
	}
	return d.PromptForMultipleSelection()
}

// Reveal shows a file or folder in the file manager: selected in Finder, or
// through the freedesktop FileManager1 interface, else its folder opened.
func (h *Host) Reveal(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("not an absolute path")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	default:
		uri := "file://" + path
		if err := exec.Command("gdbus", "call", "--session", "--dest", "org.freedesktop.FileManager1",
			"--object-path", "/org/freedesktop/FileManager1", "--method", "org.freedesktop.FileManager1.ShowItems",
			"['"+uri+"']", "").Run(); err == nil {
			return nil
		}
		dir := path
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			dir = filepath.Dir(path)
		}
		return exec.Command("xdg-open", dir).Start()
	}
}

// OpenPath opens a file in the app the system has for it.
func (h *Host) OpenPath(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("not an absolute path")
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Start()
	}
	return exec.Command("xdg-open", path).Start()
}

// OpenURL opens a web link in the browser.
func (h *Host) OpenURL(url string) error {
	return application.Get().Browser.OpenURL(url)
}

// Notify shows a notification; clicking it sends the window the link it
// carries (an agentman:// route), as the Mac app's do.
func (h *Host) Notify(id, title, body, link string) error {
	if h.notes == nil {
		return errNoNotifications
	}
	if ok, err := h.notes.CheckNotificationAuthorization(); err == nil && !ok {
		if ok, _ := h.notes.RequestNotificationAuthorization(); !ok {
			return errors.New("notifications are off for Agentman")
		}
	}
	return h.notes.SendNotification(notifications.NotificationOptions{
		ID: id, Title: title, Body: body, Data: map[string]any{"link": link},
	})
}

// ClearNotification takes a notification away once what it was about is
// settled.
func (h *Host) ClearNotification(id string) {
	if h.notes != nil {
		_ = h.notes.RemoveDeliveredNotification(id)
	}
}

// SetBadge puts a count on the dock icon ("" clears it). macOS only.
func (h *Host) SetBadge(label string) error { return setBadge(label) }

// services are what main registers for the host: itself and the system
// services it uses.
func (h *Host) services() []application.Service {
	services := append([]application.Service{application.NewService(h)}, badgeServices()...)
	if h.notes == nil {
		return services
	}
	h.notes.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error != nil {
			return
		}
		if link, ok := result.Response.UserInfo["link"].(string); ok && link != "" {
			application.Get().Event.Emit("agentman:open", link)
		}
	})
	return append(services, application.NewService(h.notes))
}
