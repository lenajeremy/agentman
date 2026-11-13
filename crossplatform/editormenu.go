package main

import (
	"os"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The editor's menu commands, as the Mac app's EditorCommands: the Save
// group and the folder's commands in File, and the Terminal and Editor
// menus. Each item sends its command to the window in front as the
// "agentman:editor-command" event; an editor window runs it
// (frontend/src/editor/commands.ts), any other ignores it. The window's
// own keys run the same commands, so the shortcuts work where there is no
// menu bar (Linux) as well.

type editorItem struct {
	label, key, command string
}

var editorFileItems = []editorItem{
	{"Open Folder in Editor…", "CmdOrCtrl+O", "openFolder"},
	{"Go to File…", "CmdOrCtrl+P", "goToFile"},
	{"Search in Folder…", "CmdOrCtrl+Shift+F", "searchInFolder"},
	{"Toggle Agent Panel", "CmdOrCtrl+L", "toggleAgentPanel"},
}

var editorSaveItems = []editorItem{
	{"Save", "CmdOrCtrl+S", "save"},
	{"Save All", "CmdOrCtrl+Option+S", "saveAll"},
	{"Revert to Saved", "", "revert"},
}

// Titles here don't follow the editor's state, so they never depend on it:
// one toggle, and commands that do nothing when there is nothing to act on.
var editorTerminalItems = []editorItem{
	{"Toggle Terminal", "Ctrl+`", "toggleTerminal"},
	{"New Terminal", "Ctrl+Shift+`", "newTerminal"},
	{"Clear Terminal", "", "clearTerminal"},
	{"-", "", ""},
	{"Stop and Close Terminal", "", "closeTerminal"},
}

var editorEditorItems = []editorItem{
	{"Toggle Markdown Preview", "CmdOrCtrl+Shift+V", "togglePreview"},
	{"-", "", ""},
	{"Split Editor Right", "CmdOrCtrl+\\", "split"},
	{"Close Split", "", "closeSplit"},
	{"-", "", ""},
	{"Go to Definition", "F12", "goToDefinition"},
	{"Find References", "Shift+F12", "findReferences"},
	{"Rename Symbol…", "F2", "renameSymbol"},
	{"Format Document", "Option+Shift+F", "formatDocument"},
	{"Go to Symbol…", "CmdOrCtrl+Shift+O", "goToSymbol"},
	{"Next Problem", "F8", "nextProblem"},
	{"-", "", ""},
	{"Comment Lines", "", "commentLines"},
	{"Indent Lines", "", "indentLines"},
	{"Outdent Lines", "", "outdentLines"},
	{"-", "", ""},
	{"Vim Mode", "", "toggleVim"},
	{"-", "", ""},
	{"Bigger Text", "CmdOrCtrl+plus", "biggerText"},
	{"Smaller Text", "CmdOrCtrl+-", "smallerText"},
	{"Actual Size", "CmdOrCtrl+0", "actualSize"},
}

var vimItem struct {
	sync.Mutex
	item *application.MenuItem
}

// sendEditorCommand tells the window in front to run a command. Open
// Folder in Editor works from any window, so the shell does it itself.
func sendEditorCommand(command string) {
	if command == "openFolder" {
		go openFolderInEditor()
		return
	}
	if window := application.Get().Window.Current(); window != nil {
		window.EmitEvent("agentman:editor-command", command)
	}
}

func addEditorItems(menu *application.Menu, items []editorItem) {
	for _, item := range items {
		if item.label == "-" {
			menu.AddSeparator()
			continue
		}
		var entry *application.MenuItem
		if item.command == "toggleVim" {
			entry = menu.AddCheckbox(item.label, false)
			vimItem.Lock()
			vimItem.item = entry
			vimItem.Unlock()
		} else {
			entry = menu.Add(item.label)
		}
		if item.key != "" {
			entry.SetAccelerator(item.key)
		}
		command := item.command
		entry.OnClick(func(*application.Context) { sendEditorCommand(command) })
	}
}

// withEditorMenus is an application menu with the editor's items: the
// folder's commands and the Save group in its File menu (made when it has
// none), and the Terminal and Editor menus before Window, where SwiftUI
// puts a scene's CommandMenus.
func withEditorMenus(menu *application.Menu) *application.Menu {
	items := []*application.MenuItem{}
	for i := 0; menu.ItemAt(i) != nil; i++ {
		items = append(items, menu.ItemAt(i))
	}
	file := application.NewMenu()
	addEditorItems(file, editorFileItems)
	file.AddSeparator()
	fileAt := -1
	for i, item := range items {
		if fileAt < 0 && item.Label() == "File" && item.GetSubmenu() != nil {
			fileAt = i
			file.Append(item.GetSubmenu())
		}
	}
	if fileAt < 0 {
		file.AddRole(application.CloseWindow)
	}
	file.AddSeparator()
	addEditorItems(file, editorSaveItems)
	fileItem := application.NewSubmenu("File", file)
	switch {
	case fileAt >= 0:
		items[fileAt] = fileItem
	case len(items) > 0:
		items = append([]*application.MenuItem{items[0], fileItem}, items[1:]...)
	default:
		items = append(items, fileItem)
	}

	terminal := application.NewMenu()
	addEditorItems(terminal, editorTerminalItems)
	editor := application.NewMenu()
	addEditorItems(editor, editorEditorItems)
	added := []*application.MenuItem{application.NewSubmenu("Terminal", terminal), application.NewSubmenu("Editor", editor)}
	out := []*application.MenuItem{}
	placed := false
	for _, item := range items {
		if !placed && item.Label() == "Window" {
			out = append(out, added...)
			placed = true
		}
		out = append(out, item)
	}
	if !placed {
		out = append(out, added...)
	}
	return application.NewMenuFromItems(out[0], out[1:]...)
}

// setEditorMenus gives the app the editor's menus, on the menu bar the app
// has, or the system's usual one. Called once, from main.
func setEditorMenus(app *application.App) {
	menu := app.Menu.GetApplicationMenu()
	if menu == nil {
		menu = application.DefaultApplicationMenu()
	}
	app.Menu.SetApplicationMenu(withEditorMenus(menu))
}

// openFolderInEditor asks for a folder, as EditorOpener.chooseFolder does,
// and opens it in an editor window.
func openFolderInEditor() {
	home, _ := os.UserHomeDir()
	folder, err := application.Get().Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).
		SetMessage("Choose a folder to open in the editor").SetButtonText("Open").SetDirectory(home).
		PromptForSingleSelection()
	if err == nil && folder != "" {
		application.InvokeSync(func() { (&Windows{}).OpenEditor(EditorOptions{Folder: folder}) })
	}
}

// SetVimMenu ticks Editor ▸ Vim Mode as the setting is, which the editor
// windows keep.
func (e *EditorFiles) SetVimMenu(on bool) {
	vimItem.Lock()
	defer vimItem.Unlock()
	if vimItem.item != nil && vimItem.item.Checked() != on {
		vimItem.item.SetChecked(on)
	}
}
