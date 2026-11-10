package main

import (
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// AgentNotice is a notification as the Mac app's Notifier posts one: a
// title, perhaps a subtitle, a body, the link a click opens, and whether it
// plays the sound (Settings ▸ Notifications).
type AgentNotice struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Body     string `json:"body"`
	Link     string `json:"link"`
	Silent   bool   `json:"silent"`
}

// NotifyWith shows a notification with a subtitle and the sound as asked;
// clicking it sends the window its link, as Notify's do.
func (h *Host) NotifyWith(n AgentNotice) error {
	if h.notes == nil {
		return errNoNotifications
	}
	if ok, err := h.notes.CheckNotificationAuthorization(); err == nil && !ok {
		if ok, _ := h.notes.RequestNotificationAuthorization(); !ok {
			return nil
		}
	}
	options := notifications.NotificationOptions{
		ID: n.ID, Title: n.Title, Subtitle: n.Subtitle, Body: n.Body, Data: map[string]any{"link": n.Link},
	}
	if n.Silent {
		options.Sound = &notifications.NotificationSound{Silent: true}
	}
	return h.notes.SendNotification(options)
}
