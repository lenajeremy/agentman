package apphost

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// EditorFiles is the editor's way to the disk, as the Mac app's
// EditorStore and EditorDocument use FileManager: a folder's files, a file
// read and saved, files made, moved, copied and put in the Trash, git asked
// what changed, and the folder searched. Paths are absolute. (It was the
// desktop shell's EditorFiles service, crossplatform/editorfs.go; am serve
// answers it for the web app, under the same names.)
type EditorFiles struct{}

// EditorEntry is a file or folder in the tree.
type EditorEntry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
}

// editorMaxBytes is the most the editor opens as text (EditorDocument.maxBytes).
const editorMaxBytes = 8 << 20

// editorImageTypes are shown as pictures (EditorDocument.imageTypes).
var editorImageTypes = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "heic": true,
	"tiff": true, "tif": true, "bmp": true, "ico": true, "icns": true,
}

// List lists a folder of the tree: folders first, then by name as Finder
// sorts them, without .git and .DS_Store. folder is relative to root.
func (e *EditorFiles) List(root, folder string) []EditorEntry {
	return listFolder(root, folder)
}

// ListMany lists several folders at once, for the refresh every 2 s.
func (e *EditorFiles) ListMany(root string, folders []string) map[string][]EditorEntry {
	out := make(map[string][]EditorEntry, len(folders))
	for _, folder := range folders {
		out[folder] = listFolder(root, folder)
	}
	return out
}

func listFolder(root, folder string) []EditorEntry {
	dir := root
	if folder != "" {
		dir = filepath.Join(root, folder)
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return []EditorEntry{}
	}
	entries := make([]EditorEntry, 0, len(items))
	for _, item := range items {
		name := item.Name()
		if name == ".git" || name == ".DS_Store" {
			continue
		}
		path := name
		if folder != "" {
			path = folder + "/" + name
		}
		entries = append(entries, EditorEntry{Path: path, Name: name, Directory: item.IsDir()})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Directory != entries[j].Directory {
			return entries[i].Directory
		}
		return finderLess(entries[i].Name, entries[j].Name)
	})
	return entries
}

// finderLess orders names as Finder does (localizedStandardCompare): case
// apart, and runs of digits by their value, so "file 2" comes before
// "file 10".
func finderLess(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		ca, cb := ra[i], rb[j]
		if unicode.IsDigit(ca) && unicode.IsDigit(cb) {
			si := i
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			sj := j
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[si:i]), "0")
			nb := strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		la, lb := unicode.ToLower(ca), unicode.ToLower(cb)
		if la != lb {
			return la < lb
		}
		i++
		j++
	}
	if len(ra)-i != len(rb)-j {
		return len(ra)-i < len(rb)-j
	}
	return a < b
}

// EditorFile is a file as the editor opens it: text, a picture, or one it
// can't edit (binary, over 8 MB, not there).
type EditorFile struct {
	// Kind is text, image, binary, tooLarge or missing.
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Modified is when the file last changed, in nanoseconds, as a string
	// (JavaScript's numbers can't hold it); "" when it isn't there.
	Modified string `json:"modified"`
	Size     int64  `json:"size"`
}

func modifiedOf(info fs.FileInfo) string {
	return strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

// Read reads a file as EditorDocument.load does.
func (e *EditorFiles) Read(path string) EditorFile {
	info, err := os.Stat(path)
	if err != nil {
		return EditorFile{Kind: "missing"}
	}
	file := EditorFile{Modified: modifiedOf(info), Size: info.Size()}
	if editorImageTypes[strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))] {
		file.Kind = "image"
		return file
	}
	if info.IsDir() {
		file.Kind = "binary"
		return file
	}
	if info.Size() > editorMaxBytes {
		file.Kind = "tooLarge"
		return file
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return EditorFile{Kind: "missing"}
	}
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 || !utf8.Valid(data) {
		file.Kind = "binary"
		return file
	}
	file.Kind = "text"
	file.Text = string(data)
	return file
}

// EditorStat is whether a file is there, and when it last changed.
type EditorStat struct {
	Exists    bool   `json:"exists"`
	Directory bool   `json:"directory"`
	Modified  string `json:"modified"`
}

// Stat looks at several files at once, for the open files' check against
// the disk.
func (e *EditorFiles) Stat(paths []string) []EditorStat {
	out := make([]EditorStat, len(paths))
	for i, path := range paths {
		if info, err := os.Stat(path); err == nil {
			out[i] = EditorStat{Exists: true, Directory: info.IsDir(), Modified: modifiedOf(info)}
		}
	}
	return out
}

// Write saves text to a file as EditorDocument.save does: through a link to
// the file rather than over the link, keeping the file's permissions, and
// all at once (a new file put in place). Answers when it last changed.
func (e *EditorFiles) Write(path, text string) (string, error) {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	temp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".agentman-*")
	if err != nil {
		return "", plainError(err)
	}
	defer os.Remove(temp.Name())
	if _, err := temp.WriteString(text); err != nil {
		temp.Close()
		return "", plainError(err)
	}
	if err := temp.Close(); err != nil {
		return "", plainError(err)
	}
	if err := os.Chmod(temp.Name(), mode); err != nil {
		return "", plainError(err)
	}
	if err := os.Rename(temp.Name(), target); err != nil {
		return "", plainError(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", nil
	}
	return modifiedOf(info), nil
}

// CreateFile makes an empty file; it must not be there already.
func (e *EditorFiles) CreateFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return plainError(err)
	}
	return file.Close()
}

// CreateFolder makes a folder, and the ones on the way when asked.
func (e *EditorFiles) CreateFolder(path string, intermediate bool) error {
	if intermediate {
		return plainError(os.MkdirAll(path, 0o755))
	}
	return plainError(os.Mkdir(path, 0o755))
}

// Move moves or renames a file or folder; nothing may be at to. Taken out
// of the Linux Trash, its note there goes too.
func (e *EditorFiles) Move(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s is already there", filepath.Base(to))
	}
	if err := os.Rename(from, to); err != nil {
		return plainError(err)
	}
	forgetTrashed(from)
	return nil
}

// Copy copies a file or folder, as FileManager.copyItem does.
func (e *EditorFiles) Copy(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s is already there", filepath.Base(to))
	}
	return plainError(copyItem(from, to))
}

func copyItem(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		link, err := os.Readlink(from)
		if err != nil {
			return err
		}
		return os.Symlink(link, to)
	case info.IsDir():
		if err := os.Mkdir(to, info.Mode().Perm()); err != nil {
			return err
		}
		items, err := os.ReadDir(from)
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := copyItem(filepath.Join(from, item.Name()), filepath.Join(to, item.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		src, err := os.Open(from)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close()
			return err
		}
		return dst.Close()
	}
}

// Trash moves a file or folder to the Trash and answers where it went, so
// undo can bring it back ("" when the system doesn't say).
func (e *EditorFiles) Trash(path string) (string, error) {
	return trashItem(path)
}

// InTrash says whether a path is in the Trash.
func (e *EditorFiles) InTrash(path string) bool {
	trash := trashFolder()
	return trash != "" && strings.HasPrefix(filepath.Clean(path), trash+string(filepath.Separator))
}

// Home is the user's home folder, which paths are shortened to with ~.
func (e *EditorFiles) Home() string {
	home, _ := os.UserHomeDir()
	return home
}

// CopyFiles puts files on the clipboard as files, as Finder copies them
// (tree y, c and x). Answers false where that can't be done.
func (e *EditorFiles) CopyFiles(paths []string) bool {
	return copyFileURLs(paths)
}

// GitResult is what git printed, and whether it worked.
type GitResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

// Git runs git in a folder without taking its index lock, so a refresh
// never gets in the way of an agent committing at the same moment.
func (e *EditorFiles) Git(root string, args []string) GitResult {
	out, err := runGit(root, args...)
	return GitResult{OK: err == nil, Output: out}
}

func gitPath() string {
	if path, err := exec.LookPath("git"); err == nil {
		return path
	}
	return "/usr/bin/git"
}

func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command(gitPath(), append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}

// editorQuietFolders are heavy and rarely read: dimmed in the tree, and
// left out of a walk (EditorStore.quietFolders).
var editorQuietFolders = map[string]bool{
	"node_modules": true, ".build": true, "build": true, "dist": true, ".next": true, "DerivedData": true,
	"Pods": true, "target": true, "__pycache__": true, ".venv": true, "venv": true, ".expo": true,
}

// AllFiles lists every file in the folder for Go to File: git's list when
// it is a repository, so ignored files stay out, or a walk of the folder.
func (e *EditorFiles) AllFiles(root string) []string {
	if out, err := runGit(root, "ls-files", "-co", "--exclude-standard", "-z"); err == nil {
		files := []string{}
		for _, path := range strings.Split(out, "\x00") {
			if path != "" {
				files = append(files, path)
			}
		}
		return files
	}
	return walkFolder(root, 20000)
}

func walkFolder(root string, limit int) []string {
	files := []string{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || editorQuietFolders[name] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		files = append(files, filepath.ToSlash(rel))
		if len(files) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return files
}

// plainError keeps what went wrong without Go's "open /path:" prefix, as
// the Mac app's alerts say it.
func plainError(err error) error {
	if err == nil {
		return nil
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		switch {
		case errors.Is(err, fs.ErrExist):
			return fmt.Errorf("“%s” couldn’t be created because an item with the same name already exists.", filepath.Base(pathErr.Path))
		case errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("The file “%s” doesn’t exist.", filepath.Base(pathErr.Path))
		case errors.Is(err, fs.ErrPermission):
			return fmt.Errorf("You don’t have permission to save the file “%s”.", filepath.Base(pathErr.Path))
		}
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return fmt.Errorf("“%s” couldn’t be moved: %v", filepath.Base(linkErr.Old), linkErr.Err)
	}
	return err
}

// ServeEditorFile answers /__editor/file?path=<absolute path>: a picture
// from the disk, for the editor's image viewer and Markdown preview. Only
// pictures: nothing else on the disk is served.
func ServeEditorFile(w http.ResponseWriter, r *http.Request) {
	{
		path := r.URL.Query().Get("path")
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if !filepath.IsAbs(path) || (!editorImageTypes[ext] && ext != "svg" && ext != "avif") {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		kind := mime.TypeByExtension("." + ext)
		if ext == "svg" {
			kind = "image/svg+xml"
		}
		if kind == "" {
			kind = http.DetectContentType(data)
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	}
}
