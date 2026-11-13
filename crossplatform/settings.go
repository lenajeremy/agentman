package main

import (
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Settings is what the Settings window asks the system for beyond Host:
// the home folder, which paths are shown under as ~, and the open panels
// its pages run, each as the Mac's NSOpenPanel is set up there.
type Settings struct{}

// Home is the user's home folder.
func (s *Settings) Home() string {
	home, _ := os.UserHomeDir()
	return home
}

// PickOptions sets up an open panel: a folder or folders, or a file (a
// program), its message and its button.
type PickOptions struct {
	Folders  bool   `json:"folders"`
	Multiple bool   `json:"multiple"`
	Message  string `json:"message"`
	Button   string `json:"button"`
	Start    string `json:"start"`
}

// Pick runs an open panel, app-modal as runModal() is; nothing when
// cancelled.
func (s *Settings) Pick(opts PickOptions) ([]string, error) {
	d := application.Get().Dialog.OpenFile().
		CanChooseDirectories(opts.Folders).CanChooseFiles(!opts.Folders).CanCreateDirectories(opts.Folders)
	if opts.Message != "" {
		d = d.SetMessage(opts.Message)
	}
	if opts.Button != "" {
		d = d.SetButtonText(opts.Button)
	}
	if opts.Start != "" {
		d = d.SetDirectory(opts.Start)
	}
	if opts.Multiple {
		return d.PromptForMultipleSelection()
	}
	path, err := d.PromptForSingleSelection()
	if err != nil || path == "" {
		return nil, err
	}
	return []string{path}, nil
}
