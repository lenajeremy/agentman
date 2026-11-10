// Package apphost is what the app's screens ask the computer for that a web
// page can't do itself: whether a folder exists, a picked file's contents,
// the home folder, the user's locale, the open panel's folders, and the
// editor's disk (its files, git, search, the Trash and a folder watched). am serve
// answers it for the web app at /app/host/<name> (cmd/am/webapp.go), under
// the names the desktop shell's services had ("Files.Folder"), so the
// screens call it the same way in either.
//
// It runs as the user, on the computer am serve runs on: the web app is for
// that same computer, and only a signed-in app reaches it.
package apphost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// A Method takes the call's arguments, as JSON, and answers.
type Method func(args []json.RawMessage) (any, error)

// ErrNoMethod means the name isn't one of Methods.
var ErrNoMethod = errors.New("no such host method")

// Methods are what the screens may call, by name.
func Methods() map[string]Method {
	home := func([]json.RawMessage) (any, error) { return Home(), nil }
	methods := map[string]Method{
		"Files.Folder": func(args []json.RawMessage) (any, error) {
			var path string
			if err := arg(args, 0, &path); err != nil {
				return nil, err
			}
			return Folder(path), nil
		},
		"Files.Read": func(args []json.RawMessage) (any, error) {
			var path string
			var max int64
			if err := arg(args, 0, &path); err != nil {
				return nil, err
			}
			if err := arg(args, 1, &max); err != nil {
				return nil, err
			}
			return Read(path, max)
		},
		"Files.List": func(args []json.RawMessage) (any, error) {
			var path string
			var hidden bool
			if err := arg(args, 0, &path); err != nil {
				return nil, err
			}
			if len(args) > 1 {
				if err := arg(args, 1, &hidden); err != nil {
					return nil, err
				}
			}
			return List(path, hidden)
		},
		"Files.Places": func([]json.RawMessage) (any, error) { return ListPlaces(), nil },
		"Files.MakeFolder": func(args []json.RawMessage) (any, error) {
			var parent, name string
			if err := arg(args, 0, &parent); err != nil {
				return nil, err
			}
			if err := arg(args, 1, &name); err != nil {
				return nil, err
			}
			return MakeFolder(parent, name)
		},
		"Host.Locale":      func([]json.RawMessage) (any, error) { return Locale(), nil },
		"Host.Reveal":      one(func(path string) (any, error) { return nil, Reveal(path) }),
		"Host.OpenPath":    one(func(path string) (any, error) { return nil, OpenPath(path) }),
		"Shell.Home":       home,
		"Settings.Home":    home,
		"EditorFiles.Home": home,
	}
	for name, method := range editorMethods() {
		methods[name] = method
	}
	return methods
}

// Home is the user's home folder: agents start inside it, worktrees live in
// ~/Agentman/Worktrees, and paths are shown under it as ~.
func Home() string {
	home, _ := os.UserHomeDir()
	return home
}

func arg(args []json.RawMessage, i int, into any) error {
	if i >= len(args) {
		return fmt.Errorf("argument %d is missing", i+1)
	}
	if err := json.Unmarshal(args[i], into); err != nil {
		return fmt.Errorf("argument %d: %w", i+1, err)
	}
	return nil
}
