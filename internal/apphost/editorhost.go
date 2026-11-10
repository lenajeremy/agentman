package apphost

import "encoding/json"

// editorMethods are the editor's calls (editorfs.go, editorsearch.go) by
// the names the desktop shell's EditorFiles service had. Watch and
// SetVimMenu were the shell's: the page watches over /app/watch, and there
// is no menu bar to tick Vim in.
func editorMethods() map[string]Method {
	var e EditorFiles
	none := func([]json.RawMessage) (any, error) { return nil, nil }
	return map[string]Method{
		"EditorFiles.List":     two(func(root, folder string) (any, error) { return e.List(root, folder), nil }),
		"EditorFiles.ListMany": two(func(root string, folders []string) (any, error) { return e.ListMany(root, folders), nil }),
		"EditorFiles.Read":     one(func(path string) (any, error) { return e.Read(path), nil }),
		"EditorFiles.Stat":     one(func(paths []string) (any, error) { return e.Stat(paths), nil }),
		"EditorFiles.Write":    two(func(path, text string) (any, error) { return e.Write(path, text) }),
		"EditorFiles.CreateFile": one(func(path string) (any, error) {
			return nil, e.CreateFile(path)
		}),
		"EditorFiles.CreateFolder": two(func(path string, intermediate bool) (any, error) {
			return nil, e.CreateFolder(path, intermediate)
		}),
		"EditorFiles.Move":      two(func(from, to string) (any, error) { return nil, e.Move(from, to) }),
		"EditorFiles.Copy":      two(func(from, to string) (any, error) { return nil, e.Copy(from, to) }),
		"EditorFiles.Trash":     one(func(path string) (any, error) { return e.Trash(path) }),
		"EditorFiles.InTrash":   one(func(path string) (any, error) { return e.InTrash(path), nil }),
		"EditorFiles.CopyFiles": one(func(paths []string) (any, error) { return e.CopyFiles(paths), nil }),
		"EditorFiles.Git":       two(func(root string, args []string) (any, error) { return e.Git(root, args), nil }),
		"EditorFiles.AllFiles":  one(func(root string) (any, error) { return e.AllFiles(root), nil }),
		"EditorFiles.Search": func(args []json.RawMessage) (any, error) {
			var root, query string
			var options SearchOptions
			var files []string
			var edited map[string]string
			var id int64
			for i, into := range []any{&root, &query, &options, &files, &edited, &id} {
				if err := arg(args, i, into); err != nil {
					return nil, err
				}
			}
			return e.Search(root, query, options, files, edited, id), nil
		},
		"EditorFiles.Watch":      none,
		"EditorFiles.SetVimMenu": none,
	}
}

func one[A any](f func(A) (any, error)) Method {
	return func(args []json.RawMessage) (any, error) {
		var a A
		if err := arg(args, 0, &a); err != nil {
			return nil, err
		}
		return f(a)
	}
}

func two[A, B any](f func(A, B) (any, error)) Method {
	return func(args []json.RawMessage) (any, error) {
		var a A
		var b B
		if err := arg(args, 0, &a); err != nil {
			return nil, err
		}
		if err := arg(args, 1, &b); err != nil {
			return nil, err
		}
		return f(a, b)
	}
}
