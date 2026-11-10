// Agentman, the cross-platform app: every agent `am serve` watches, in one
// window, on macOS, Linux and Windows. The screens are a web page
// (frontend/), which talks to am serve itself over a loopback WebSocket.
// This program is the shell around it: the window, and what a page cannot
// do on its own, such as finding the daemon.
package main

import (
	"embed"
	"fmt"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	test := testMode()
	options := application.Options{
		Name:        "Agentman",
		Description: "Every coding agent on this computer, in one window",
		Services: append([]application.Service{
			application.NewService(&Daemon{}),
			application.NewService(&Windows{}),
			application.NewService(&Menus{}),
			application.NewService(&Files{}),
		}, newHost().services()...),
		Assets: application.AssetOptions{
			Handler: withSymbols(application.AssetFileServerFS(assets)),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	}

	width, height := 1280, 820
	// AGENTMAN_SIZE=1560x980 sizes the first window, to compare it with
	// the Mac app's at the same size.
	if size := os.Getenv("AGENTMAN_SIZE"); size != "" {
		_, _ = fmt.Sscanf(size, "%dx%d", &width, &height)
	}
	first := window("Agentman", startURL(), width, height)
	if test != nil {
		test.prepare(&options, &first)
	}
	app := application.New(options)
	win := app.Window.NewWithOptions(first)
	if test != nil {
		test.start(app, win)
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// window is how every window looks: no title bar, the page under the
// traffic lights, as the Mac app's windows are.
func window(title, url string, width, height int) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Title:     title,
		Width:     width,
		Height:    height,
		MinWidth:  640,
		MinHeight: 420,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 32,
			Backdrop:                application.MacBackdropNormal,
			TitleBar:                application.MacTitleBarHidden,
		},
		BackgroundColour: application.NewRGB(250, 250, 250),
		URL:              url,
	}
}

// startURL is the page the window opens on. AGENTMAN_OPEN picks another, as
// its query: "tab=terminal" opens the first agent's terminal. Tests and
// screenshots use it to reach a screen without clicking.
func startURL() string {
	if open := os.Getenv("AGENTMAN_OPEN"); open != "" {
		return "/?" + open
	}
	return "/"
}
