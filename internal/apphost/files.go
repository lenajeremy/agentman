package apphost

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// FolderInfo says whether a folder is there, and inside a git repository:
// what the New Agent page and the agent's More menu check, as the Mac app
// does with fileExists and its repository walk.
type FolderInfo struct {
	Exists     bool `json:"exists"`
	Repository bool `json:"repository"`
}

func Folder(path string) FolderInfo {
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

// Read reads a regular file, up to max bytes.
func Read(path string, max int64) (PickedFile, error) {
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
