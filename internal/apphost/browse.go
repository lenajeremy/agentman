package apphost

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// The open panel the web app draws in place of NSOpenPanel (web/src/picker)
// browses the computer through these: a folder's entries, the sidebar's
// places, and New Folder. A browser's own file picker can't stand in: it
// never tells the page where a file is, and the screens need paths (a
// folder for an agent to work in, a file to hand it).

// Entry is one item in a folder.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Dir is a folder, following a symbolic link; Package is a folder
	// macOS shows as one file (an .app), which the panel doesn't open.
	Dir     bool  `json:"dir"`
	Package bool  `json:"package,omitempty"`
	Hidden  bool  `json:"hidden,omitempty"`
	Size    int64 `json:"size"`
	// Modified is when, in milliseconds since 1970.
	Modified int64 `json:"modified"`
}

// Listing is a folder's entries, folders and files together by name.
type Listing struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
	// Truncated says more were there than maxEntries.
	Truncated bool `json:"truncated,omitempty"`
}

const maxEntries = 10000

// packages are the folders the Mac shows as files.
var packages = map[string]bool{
	".app": true, ".bundle": true, ".framework": true, ".plugin": true, ".kext": true, ".pkg": true,
	".xcodeproj": true, ".xcworkspace": true, ".playground": true, ".photoslibrary": true, ".rtfd": true,
	".pages": true, ".numbers": true, ".key": true,
}

// Expand turns ~ and ~/… into the home folder, as the panel's Go to Folder
// takes them, and cleans the path.
func Expand(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = filepath.Join(Home(), strings.TrimPrefix(path, "~"))
	}
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

// List lists a folder; hidden files (a leading dot) only when asked.
func List(path string, hidden bool) (Listing, error) {
	path = Expand(path)
	if !filepath.IsAbs(path) {
		return Listing{}, errors.New("not an absolute path")
	}
	items, err := os.ReadDir(path)
	if err != nil {
		return Listing{}, err
	}
	listing := Listing{Path: path, Entries: []Entry{}}
	for _, item := range items {
		name := item.Name()
		dot := strings.HasPrefix(name, ".")
		if dot && !hidden {
			continue
		}
		if len(listing.Entries) == maxEntries {
			listing.Truncated = true
			break
		}
		full := filepath.Join(path, name)
		info, err := os.Stat(full) // through a link, to what it points at
		if err != nil {
			info, err = item.Info()
			if err != nil {
				continue
			}
		}
		entry := Entry{Name: name, Path: full, Dir: info.IsDir(), Hidden: dot, Modified: info.ModTime().UnixMilli()}
		if entry.Dir {
			entry.Package = runtime.GOOS == "darwin" && packages[strings.ToLower(filepath.Ext(name))]
		} else {
			entry.Size = info.Size()
		}
		listing.Entries = append(listing.Entries, entry)
	}
	sort.Slice(listing.Entries, func(i, j int) bool {
		return strings.ToLower(listing.Entries[i].Name) < strings.ToLower(listing.Entries[j].Name)
	})
	return listing, nil
}

// Place is one of the panel sidebar's rows.
type Place struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Kind picks the icon: home, desktop, documents, downloads,
	// applications, computer or drive.
	Kind string `json:"kind"`
}

// Places are the sidebar's Favorites and Locations, as the Mac's open
// panel lists them: home and the folders in it that are there, then the
// computer's disk and any others mounted.
type Places struct {
	Home      string  `json:"home"`
	Favorites []Place `json:"favorites"`
	Locations []Place `json:"locations"`
}

func ListPlaces() Places {
	home := Home()
	places := Places{Home: home, Favorites: []Place{}, Locations: []Place{}}
	if runtime.GOOS == "darwin" {
		places.Favorites = append(places.Favorites, existing(Place{"Applications", "/Applications", "applications"})...)
	}
	places.Favorites = append(places.Favorites, existing(
		Place{"Desktop", filepath.Join(home, "Desktop"), "desktop"},
		Place{"Documents", filepath.Join(home, "Documents"), "documents"},
		Place{"Downloads", filepath.Join(home, "Downloads"), "downloads"},
	)...)
	if home != "" {
		places.Favorites = append(places.Favorites, Place{filepath.Base(home), home, "home"})
	}
	places.Locations = drives()
	return places
}

// drives are the disks: on a Mac, /Volumes holds a link to / under the
// startup disk's name ("Macintosh HD") and the others by theirs, less the
// ones Finder hides (Recovery); on Linux,
// the computer, and what is mounted in /media/<user>.
func drives() []Place {
	var found []Place
	if runtime.GOOS == "darwin" {
		items, _ := os.ReadDir("/Volumes")
		for _, item := range items {
			full := filepath.Join("/Volumes", item.Name())
			if target, err := os.Readlink(full); err == nil && target == "/" {
				found = append([]Place{{item.Name(), "/", "computer"}}, found...)
			} else if info, err := os.Stat(full); err == nil && info.IsDir() && browsable(full) {
				found = append(found, Place{item.Name(), full, "drive"})
			}
		}
	} else {
		user := filepath.Base(Home())
		items, _ := os.ReadDir(filepath.Join("/media", user))
		for _, item := range items {
			if item.IsDir() {
				found = append(found, Place{item.Name(), filepath.Join("/media", user, item.Name()), "drive"})
			}
		}
	}
	if len(found) == 0 || found[0].Path != "/" {
		found = append([]Place{{"Computer", "/", "computer"}}, found...)
	}
	return found
}

func existing(places ...Place) []Place {
	var kept []Place
	for _, p := range places {
		if info, err := os.Stat(p.Path); err == nil && info.IsDir() {
			kept = append(kept, p)
		}
	}
	return kept
}

// MakeFolder makes a folder named name in parent: the panel's New Folder.
func MakeFolder(parent, name string) (string, error) {
	parent = Expand(parent)
	name = strings.TrimSpace(name)
	if !filepath.IsAbs(parent) {
		return "", errors.New("not an absolute path")
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return "", errors.New("that name can't be used for a folder")
	}
	path := filepath.Join(parent, name)
	if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", errors.New("the name \"" + name + "\" is already taken")
		}
		return "", err
	}
	return path, nil
}
