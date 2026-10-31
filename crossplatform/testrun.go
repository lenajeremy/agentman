package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// A test run (AGENTMAN_TEST_SNAPSHOT=<file.png>) compares this app with the
// Mac app without showing anything: the app never becomes the active app,
// its window is transparent and lets clicks through, and after
// AGENTMAN_TEST_DELAY seconds (default 10) it saves a picture of its page,
// drawn by WebKit itself, and quits. Whoever is using the computer sees
// nothing. scripts/teststack/compare.sh runs it.
type testRun struct {
	path  string
	delay time.Duration
}

func testMode() *testRun {
	path := os.Getenv("AGENTMAN_TEST_SNAPSHOT")
	if path == "" {
		return nil
	}
	delay := 10 * time.Second
	if s, err := strconv.ParseFloat(os.Getenv("AGENTMAN_TEST_DELAY"), 64); err == nil && s > 0 {
		delay = time.Duration(s * float64(time.Second))
	}
	return &testRun{path: path, delay: delay}
}

// prepare changes the app's and the window's options before they exist.
func (t *testRun) prepare(app *application.Options, win *application.WebviewWindowOptions) {
	app.Mac.ActivationPolicy = application.ActivationPolicyAccessory
	win.Hidden = true
}

// start hides the window once it exists, and takes the picture later.
func (t *testRun) start(app *application.App, win *application.WebviewWindow) {
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		application.InvokeSync(func() { testHide(win.NativeWindow()) })
		go func() {
			time.Sleep(t.delay)
			application.InvokeSync(func() { testSnapshot(win.NativeWindow(), t.path) })
			// A picture that never comes must not leave the app running.
			time.Sleep(20 * time.Second)
			fmt.Fprintln(os.Stderr, "agentman: test snapshot timed out")
			os.Exit(4)
		}()
	})
}
