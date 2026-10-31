package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Menus draws the system's own menus and alerts for the screens: a menu
// popped up under a button or at a right-click, as SwiftUI's Menu and
// contextMenu are NSMenus, and an alert sheet, as confirmationDialog is.
// The page names each item by a key, and hears which was chosen as the
// "agentman:menu" event, sent to the window the menu was opened in.
type Menus struct {
	next atomic.Int64
}

// MenuItem is one line of a popped-up menu: an item to choose, a section
// heading, or a separator.
type MenuItem struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Header    bool   `json:"header"`
	Separator bool   `json:"separator"`
	Checked   bool   `json:"checked"`
	Disabled  bool   `json:"disabled"`
}

// MenuChoice is what the "agentman:menu" event carries: the menu, by the id
// Popup was given, and the key of the item chosen.
type MenuChoice struct {
	Menu string `json:"menu"`
	Key  string `json:"key"`
}

// Popup opens a menu at x, y in the calling window's page (CSS pixels, from
// its top left). On macOS it returns once the menu has closed.
func (m *Menus) Popup(ctx context.Context, menu string, items []MenuItem, x, y int) error {
	window, ok := ctx.Value(application.WindowKey).(application.Window)
	if !ok || window == nil {
		return errors.New("no window to open the menu in")
	}
	name := fmt.Sprintf("agentman-popup-%d", m.next.Add(1))
	popup := application.NewContextMenu(name)
	for _, item := range items {
		switch {
		case item.Separator:
			popup.AddSeparator()
		case item.Header:
			popup.AddSectionHeader(item.Label)
		default:
			var entry *application.MenuItem
			if item.Checked {
				entry = popup.AddCheckbox(item.Label, true)
			} else {
				entry = popup.Add(item.Label)
			}
			entry.SetEnabled(!item.Disabled)
			key := item.Key
			entry.OnClick(func(*application.Context) {
				window.EmitEvent("agentman:menu", MenuChoice{Menu: menu, Key: key})
			})
		}
	}
	popup.Update()
	window.OpenContextMenu(&application.ContextMenuData{Id: name, X: x, Y: y})
	return nil
}

// Alert asks a question in a sheet over the calling window, as the Mac
// app's confirmation dialogs do, and answers with the label of the button
// pressed: the last one is Cancel, which Escape presses too.
type Alert struct {
	Title   string   `json:"title"`
	Message string   `json:"message"`
	Buttons []string `json:"buttons"`
}

func (m *Menus) Confirm(ctx context.Context, alert Alert) string {
	if len(alert.Buttons) == 0 {
		alert.Buttons = []string{"OK"}
	}
	chosen := make(chan string, 1)
	dialog := application.Get().Dialog.Question().SetTitle(alert.Title).SetMessage(alert.Message)
	if window, ok := ctx.Value(application.WindowKey).(application.Window); ok && window != nil {
		dialog.AttachToWindow(window)
	}
	for i, label := range alert.Buttons {
		button := dialog.AddButton(label)
		button.OnClick(func() { chosen <- label })
		if i == 0 {
			dialog.SetDefaultButton(button)
		}
		if i == len(alert.Buttons)-1 && len(alert.Buttons) > 1 {
			dialog.SetCancelButton(button)
		}
	}
	dialog.Show()
	select {
	case label := <-chosen:
		return label
	case <-ctx.Done():
		return alert.Buttons[len(alert.Buttons)-1]
	}
}

// Files is what the screens read of the user's files, which a page can't:
// whether a folder is there and in a repository, and the bytes of a file
// they picked to attach.
type Files struct{}

// FolderInfo says whether a folder exists and is in a git repository: it
// or a folder above it, up to the home it is in, holds .git.
type FolderInfo struct {
	Exists     bool `json:"exists"`
	Repository bool `json:"repository"`
}

func (f *Files) Folder(path string) FolderInfo {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() || !filepath.IsAbs(path) {
		return FolderInfo{}
	}
	result := FolderInfo{Exists: true}
	for dir := filepath.Clean(path); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			result.Repository = true
			break
		}
	}
	return result
}

// PickedFile is a file read for an attachment: its bytes as base64, what
// they are, and its size.
type PickedFile struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
	// Data is the file's content, base64; left out for a file larger than
	// was asked for.
	Data string `json:"data"`
}

// Read reads a regular file the user chose, up to max bytes.
func (f *Files) Read(path string, max int64) (PickedFile, error) {
	if !filepath.IsAbs(path) {
		return PickedFile{}, errors.New("not an absolute path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return PickedFile{}, err
	}
	if !info.Mode().IsRegular() {
		return PickedFile{}, errors.New("not a file")
	}
	file := PickedFile{Path: path, Name: filepath.Base(path), Size: info.Size()}
	handle, err := os.Open(path)
	if err != nil {
		return file, err
	}
	defer handle.Close()
	head := make([]byte, 512)
	n, _ := handle.Read(head)
	file.Mime = http.DetectContentType(head[:n])
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		file.Mime = "image/svg+xml"
	}
	if info.Size() > max {
		return file, nil
	}
	if _, err := handle.Seek(0, io.SeekStart); err != nil {
		return file, err
	}
	data, err := io.ReadAll(io.LimitReader(handle, max))
	if err != nil {
		return file, err
	}
	file.Data = base64.StdEncoding.EncodeToString(data)
	return file, nil
}
