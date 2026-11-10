package main

import (
	"net/url"
	"runtime"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
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

// OpenSettings opens the Settings window on a page: general, notifications,
// devices, permissions, personality, knowledge, keys or languages; "pair"
// for Devices pairing a new one; "" for the last one shown. There is one, as the Mac's Settings scene: opened again, it
// comes forward on the page asked for.
func (w *Windows) OpenSettings(page string) {
	settings.Lock()
	open := settings.window
	settings.Unlock()
	if open != nil {
		if page != "" {
			open.EmitEvent("agentman:settings-page", page)
		}
		open.Show()
		open.Focus()
		return
	}
	q := url.Values{"view": {"settings"}}
	if page != "" {
		q.Set("page", page)
	}
	keepSettings(application.Get().Window.NewWithOptions(settingsOptions("/?" + q.Encode())))
}

// The Settings window while it is open.
var settings struct {
	sync.Mutex
	window *application.WebviewWindow
}

// settingsOptions is the Mac's Settings window: 800×580 of page under a
// 32pt title bar that shows "Agentman Settings" over the page, a fixed size
// with the zoom button off. On Linux the title bar is the desktop's own,
// above the page.
func settingsOptions(url string) application.WebviewWindowOptions {
	width, height := 800, 612
	if runtime.GOOS != "darwin" {
		width, height = 800, 580
	}
	opts := window("Agentman Settings", url, width, height)
	opts.MinWidth, opts.MinHeight = 0, 0
	opts.DisableResize = true
	opts.MaximiseButtonState = application.ButtonDisabled
	opts.Mac.TitleBar = application.MacTitleBar{AppearsTransparent: true, FullSizeContent: true}
	// The page leaves its sidebar clear over the sidebar material
	// (settingsMaterial) while the window is active.
	opts.Mac.Backdrop = application.MacBackdropTransparent
	return opts
}

// keepSettings remembers the Settings window until it closes, and puts the
// sidebar material behind its page once there is a page.
func keepSettings(win *application.WebviewWindow) {
	settings.Lock()
	settings.window = win
	settings.Unlock()
	win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		application.InvokeSync(func() { settingsMaterial(win.NativeWindow()) })
	})
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		settings.Lock()
		if settings.window == win {
			settings.window = nil
		}
		settings.Unlock()
	})
}

// settingsStart says whether the first window is the Settings window
// (AGENTMAN_OPEN=view=settings…), as comparisons with the Mac's open it.
func settingsStart(url string) bool {
	return strings.Contains(url, "view=settings")
}
