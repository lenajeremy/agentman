package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Shell is the app around the main window, as the Mac app's AgentmanApp
// and AppDelegate are: the menu bar and its shortcuts, agentman:// links
// and a second launch handing its link to the first, asking before
// quitting with unsaved files, restarting the daemon, and telling the page
// when the user is away (the app in the background, the screen locked, the
// computer asleep).
//
// What the page hears, on every window:
//
//	agentman:open        a link to follow (agentman://…)
//	agentman:command     a menu item for the page: "new-agent",
//	                     "toggle-sidebar", "cycle-appearance", "palette",
//	                     "agentman", "away"
//	agentman:active      true or false: the app came to the front or left it
//	agentman:away        "lock", "unlock" or "sleep"
//	agentman:fullscreen  true or false, for the main window
//	agentman:quit-check  a token; answered with Shell.QuitAnswer
//	agentman:quit-save   a token; answered with Shell.SaveAnswer
type Shell struct {
	app  *application.App
	main *application.WebviewWindow

	// allowQuit lets the next quit through once the page has had its say.
	allowQuit atomic.Bool
	checking  atomic.Bool

	mu      sync.Mutex
	token   string
	answers map[string]quitAnswer

	active atomic.Bool
}

// UnsavedFile is a file an editor window holds changes to.
type UnsavedFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type quitAnswer struct {
	files []UnsavedFile
	saved bool
}

// extraMenus are menus other parts of the app add to the menu bar, after Go
// (the editor's, §12), each by a line in an init function of its own file.
var extraMenus []func(menu *application.Menu, shell *Shell)

func newShell() *Shell {
	return &Shell{answers: map[string]quitAnswer{}}
}

// prepare sets what the shell needs before the app exists: a second launch
// hands its link to the first, quitting asks the page, and the main window
// is the Mac's size.
func (s *Shell) prepare(options *application.Options, first *application.WebviewWindowOptions, test bool) {
	options.ShouldQuit = s.shouldQuit
	holdKeysRepeat()
	// One window, which links open in: a second launch (agentman://… from
	// the system, or the program run again) gives its link to the first and
	// goes. A test run, or one on a test daemon, stays apart from the
	// user's own copy of the app.
	if !test {
		id := "dev.agentman.crossplatform"
		if home := os.Getenv("AGENTMAN_HOME"); home != "" {
			sum := sha256.Sum256([]byte(home))
			// Each part of the id must start with a letter (D-Bus names on Linux).
			id += ".test.h" + hex.EncodeToString(sum[:6])
		}
		options.SingleInstance = &application.SingleInstanceOptions{
			UniqueID: id,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				s.raise()
				for _, arg := range data.Args {
					if isAppLink(arg) {
						s.emit("agentman:open", arg)
					}
				}
			},
		}
	}
	first.MinWidth, first.MinHeight = 1040, 640
	if os.Getenv("AGENTMAN_SIZE") == "" {
		first.Width, first.Height = 1560, 980
	}
}

// start wires the shell to the app once it exists.
func (s *Shell) start(app *application.App, win *application.WebviewWindow, test bool) {
	s.app, s.main = app, win
	app.Menu.SetApplicationMenu(s.menu())

	// A link the system opened the app with, or sent it later.
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		if link := e.Context().URL(); isAppLink(link) {
			s.emitLater("agentman:open", link)
		}
	})
	for _, arg := range os.Args[1:] {
		if isAppLink(arg) {
			s.emitLater("agentman:open", arg)
		}
	}

	win.OnWindowEvent(events.Common.WindowFullscreen, func(*application.WindowEvent) { s.emit("agentman:fullscreen", true) })
	win.OnWindowEvent(events.Common.WindowUnFullscreen, func(*application.WindowEvent) { s.emit("agentman:fullscreen", false) })

	// Away: the app in the background, the screen locked, asleep. A test
	// run is never in front and says nothing.
	if test {
		return
	}
	app.Event.OnApplicationEvent(events.Common.SystemWillSleep, func(*application.ApplicationEvent) { s.emit("agentman:away", "sleep") })
	watchActive(app, s.setActive)
	watchScreenLock(func(locked bool) {
		if locked {
			s.emit("agentman:away", "lock")
		} else {
			s.emit("agentman:away", "unlock")
		}
	})
}

func (s *Shell) setActive(active bool) {
	if s.active.Swap(active) == active {
		return
	}
	s.emit("agentman:active", active)
}

// Active says whether the app is in front, for a page that has just loaded.
func (s *Shell) Active() bool { return s.active.Load() }

// Home is the user's home folder: agents start inside it, and worktrees
// live in ~/Agentman/Worktrees.
func (s *Shell) Home() string {
	home, _ := os.UserHomeDir()
	return home
}

func (s *Shell) emit(name string, data any) {
	if s.app != nil {
		s.app.Event.Emit(name, data)
	}
}

// emitLater sends an event once the page has had time to load and listen,
// for links the app was launched with.
func (s *Shell) emitLater(name string, data any) {
	go func() {
		time.Sleep(1500 * time.Millisecond)
		s.emit(name, data)
	}()
}

// raise brings the main window forward, for a link from outside.
func (s *Shell) raise() {
	if s.main == nil {
		return
	}
	application.InvokeAsync(func() {
		s.main.Show()
		s.main.Focus()
	})
}

func isAppLink(link string) bool {
	return strings.HasPrefix(link, "agentman://")
}

// menu is the menu bar, as AgentmanApp.swift's commands make it.
func (s *Shell) menu() *application.Menu {
	menu := application.NewMenu()
	command := func(name string) func(*application.Context) {
		return func(*application.Context) {
			if name == "palette" || name == "agentman" || name == "away" {
				s.raise()
			}
			s.emit("agentman:command", name)
		}
	}
	if runtime.GOOS == "darwin" {
		app := menu.AddSubmenu("Agentman")
		app.AddRole(application.About)
		app.AddSeparator()
		app.Add("Settings…").SetAccelerator("CmdOrCtrl+,").OnClick(func(*application.Context) {
			(&Windows{}).OpenSettings("")
		})
		// The daemon outlives the app, by design: quitting the app leaves
		// it running, so a stuck one is restarted here.
		app.Add("Restart Daemon…").OnClick(func(*application.Context) { go s.confirmRestart() })
		app.AddSeparator()
		app.AddRole(application.ServicesMenu)
		app.AddSeparator()
		app.AddRole(application.Hide)
		app.AddRole(application.HideOthers)
		app.AddRole(application.UnHide)
		app.AddSeparator()
		app.AddRole(application.Quit)
	}
	file := menu.AddSubmenu("File")
	file.Add("New Agent").SetAccelerator("CmdOrCtrl+N").OnClick(command("new-agent"))
	file.AddSeparator()
	file.AddRole(application.CloseWindow)
	if runtime.GOOS != "darwin" {
		file.Add("Settings…").SetAccelerator("CmdOrCtrl+,").OnClick(func(*application.Context) { (&Windows{}).OpenSettings("") })
		file.Add("Restart Daemon…").OnClick(func(*application.Context) { go s.confirmRestart() })
		file.AddSeparator()
		file.AddRole(application.Quit)
	}
	menu.AddRole(application.EditMenu)
	view := menu.AddSubmenu("View")
	// ⌃⌘S on the Mac; Ctrl+Alt+S elsewhere, where there is no ⌘ and Super
	// belongs to the desktop.
	sidebarKey := "Ctrl+Cmd+S"
	if runtime.GOOS != "darwin" {
		sidebarKey = "Ctrl+Alt+S"
	}
	view.Add("Toggle Sidebar").SetAccelerator(sidebarKey).OnClick(command("toggle-sidebar"))
	// Light, Dark, Match the Mac, in turn.
	view.Add("Cycle Appearance").SetAccelerator("Alt+Shift+CmdOrCtrl+A").OnClick(command("cycle-appearance"))
	view.AddSeparator()
	view.AddRole(application.ToggleFullscreen)
	gomenu := menu.AddSubmenu("Go")
	gomenu.Add("Command Palette…").SetAccelerator("CmdOrCtrl+K").OnClick(command("palette"))
	gomenu.Add("Agentman").SetAccelerator("Alt+CmdOrCtrl+0").OnClick(command("agentman"))
	gomenu.Add("What Happened While I Was Away").OnClick(command("away"))
	for _, add := range extraMenus {
		add(menu, s)
	}
	menu.AddRole(application.WindowMenu)
	menu.AddRole(application.HelpMenu)
	return menu
}

// shouldQuit asks the windows, the first time, whether any holds unsaved
// files, and quits once they have answered: at once when none does, else
// after the Mac's alert (AgentmanApp.swift's applicationShouldTerminate).
func (s *Shell) shouldQuit() bool {
	if s.allowQuit.Load() || s.app == nil {
		return true
	}
	if s.checking.Swap(true) {
		return false
	}
	go s.checkQuit()
	return false
}

func (s *Shell) newToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = fmt.Sprintf("q%d", time.Now().UnixNano())
	s.answers = map[string]quitAnswer{}
	return s.token
}

// gather waits for every window to answer the token, or a second.
func (s *Shell) gather(token string) []quitAnswer {
	windows := len(s.app.Window.GetAll())
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n := len(s.answers)
		s.mu.Unlock()
		if n >= windows {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []quitAnswer
	for _, a := range s.answers {
		list = append(list, a)
	}
	return list
}

// QuitAnswer is a window's answer to agentman:quit-check: its unsaved files.
func (s *Shell) QuitAnswer(ctx context.Context, token string, files []UnsavedFile) {
	s.answer(ctx, token, quitAnswer{files: files})
}

// SaveAnswer is a window's answer to agentman:quit-save: whether every file
// it held was saved.
func (s *Shell) SaveAnswer(ctx context.Context, token string, saved bool) {
	s.answer(ctx, token, quitAnswer{saved: saved})
}

func (s *Shell) answer(ctx context.Context, token string, a quitAnswer) {
	name := "window"
	if w, ok := ctx.Value(application.WindowKey).(application.Window); ok && w != nil {
		name = fmt.Sprint(w.ID())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == s.token {
		s.answers[name] = a
	}
}

func (s *Shell) checkQuit() {
	defer s.checking.Store(false)
	token := s.newToken()
	s.emit("agentman:quit-check", token)
	var unsaved []UnsavedFile
	for _, a := range s.gather(token) {
		unsaved = append(unsaved, a.files...)
	}
	if len(unsaved) == 0 {
		s.quitNow()
		return
	}
	title := "Save the changes to " + unsaved[0].Name + " before quitting?"
	message := "Your changes are lost if you don't save them."
	save := "Save"
	if len(unsaved) > 1 {
		title = fmt.Sprintf("Save the changes to %d files before quitting?", len(unsaved))
		paths := make([]string, len(unsaved))
		for i, f := range unsaved {
			paths[i] = f.Path
		}
		message = strings.Join(paths, "\n")
		save = "Save All"
	}
	switch runAlert(title, message, []string{save, "Cancel", "Don't Save"}) {
	case 0:
		token := s.newToken()
		s.emit("agentman:quit-save", token)
		for _, a := range s.gather(token) {
			if !a.saved {
				return
			}
		}
		s.quitNow()
	case 2:
		s.quitNow()
	}
}

func (s *Shell) quitNow() {
	s.allowQuit.Store(true)
	s.app.Quit()
}

// The login service am serve runs as, which Restart Daemon kicks.
const daemonService = "dev.agentman.serve"

// confirmRestart asks, restarts am serve, and says why when it couldn't
// (AgentsStore.swift's DaemonRestart).
func (s *Shell) confirmRestart() {
	if runAlert("Restart the daemon?",
		"Agentman's daemon starts again in a few seconds. Your agents keep running; a message still being sent may need sending again.",
		[]string{"Restart", "Cancel"}) != 0 {
		return
	}
	if problem := restartDaemon(); problem != "" {
		runAlert("The daemon couldn't be restarted", problem, []string{"OK"})
	}
}

// restartDaemon kicks the service as `am service restart` does; answers why
// it couldn't. Against a test daemon (AGENTMAN_HOME) it kicks a service
// that doesn't exist, so a test never restarts the user's own.
func restartDaemon() string {
	notService := "It isn't running as a service on this Mac. Run `am service restart` in a terminal, or stop am serve where it runs and start it again."
	if runtime.GOOS != "darwin" {
		return "It isn't running as a service on this computer. Run `am service restart` in a terminal, or stop am serve where it runs and start it again."
	}
	label := daemonService
	if os.Getenv("AGENTMAN_HOME") != "" {
		label += ".test-not-a-service"
	}
	cmd := exec.Command("/bin/launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return notService
		}
		return err.Error()
	}
	return ""
}
