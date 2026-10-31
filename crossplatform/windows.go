package main

import (
	"net/url"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Windows opens the app's other windows, as the Mac app's scenes are: an
// editor per folder, and Settings.
type Windows struct{}

// EditorOptions says what an editor window opens on.
type EditorOptions struct {
	// Folder is the project; SessionID an agent working in it.
	Folder    string `json:"folder"`
	SessionID string `json:"sessionId"`
	// File opens at once; Terminal shows the terminal; AgentHere shows the
	// folder's agent in the agent panel.
	File      string `json:"file"`
	Terminal  bool   `json:"terminal"`
	AgentHere bool   `json:"agentHere"`
}

// OpenEditor opens a folder in an editor window of its own.
func (w *Windows) OpenEditor(opts EditorOptions) {
	q := url.Values{"view": {"editor"}}
	for key, value := range map[string]string{"folder": opts.Folder, "session": opts.SessionID, "file": opts.File} {
		if value != "" {
			q.Set(key, value)
		}
	}
	if opts.Terminal {
		q.Set("terminal", "1")
	}
	if opts.AgentHere {
		q.Set("agent", "here")
	}
	application.Get().Window.NewWithOptions(window("Editor", "/?"+q.Encode(), 1200, 800))
}

// OpenSettings opens the Settings window (800×580, as the Mac's) on a page:
// general, notifications, permissions, personality, knowledge, keys or
// languages; "" for the last one shown.
func (w *Windows) OpenSettings(page string) {
	q := url.Values{"view": {"settings"}}
	if page != "" {
		q.Set("page", page)
	}
	opts := window("Settings", "/?"+q.Encode(), 800, 580)
	opts.MinWidth, opts.MinHeight = 800, 580
	application.Get().Window.NewWithOptions(opts)
}
