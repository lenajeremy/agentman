// Agentman, the cross-platform app: every agent `am serve` watches, in one
// window, on macOS, Linux and Windows. The screens are a web page
// (frontend/), which talks to am serve itself over a loopback WebSocket.
// This program is the shell around it: the window, and what a page cannot
// do on its own, such as finding the daemon.
package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := application.New(application.Options{
		Name:        "Agentman",
		Description: "Every coding agent on this computer, in one window",
		Services: []application.Service{
			application.NewService(&Daemon{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Agentman",
		Width:     1280,
		Height:    820,
		MinWidth:  760,
		MinHeight: 480,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 40,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(17, 17, 19),
		URL:              startURL(),
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
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
