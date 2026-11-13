package apphost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAListingHidesDotFilesUntilAskedAndFollowsLinks(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "src"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("x"), 0o644))
	must(t, os.Symlink(filepath.Join(dir, "src"), filepath.Join(dir, "code")))

	listing, err := List(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]Entry{}
	for _, e := range listing.Entries {
		names[e.Name] = e
	}
	if _, ok := names[".env"]; ok || len(listing.Entries) != 3 {
		t.Fatalf("entries = %+v; want README.md, code and src, no .env", listing.Entries)
	}
	if !names["code"].Dir || !names["src"].Dir || names["README.md"].Dir || names["README.md"].Size != 2 {
		t.Fatalf("entries = %+v", listing.Entries)
	}
	if listing.Entries[0].Name != "code" || listing.Entries[1].Name != "README.md" {
		t.Fatalf("not by name, case aside: %+v", listing.Entries)
	}

	all, _ := List(dir, true)
	if len(all.Entries) != 4 {
		t.Fatalf("with hidden files: %d entries, want 4", len(all.Entries))
	}
	if _, err := List("relative/path", false); err == nil {
		t.Fatal("a relative path was listed")
	}
}

func TestATildeIsTheHomeFolder(t *testing.T) {
	t.Setenv("HOME", "/Users/someone")
	for in, want := range map[string]string{"~": "/Users/someone", "~/Code/": "/Users/someone/Code", " /tmp/a/../b ": "/tmp/b", "~x": "~x"} {
		if got := Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewFolderTakesAPlainNameOnce(t *testing.T) {
	dir := t.TempDir()
	path, err := MakeFolder(dir, "untitled folder")
	if err != nil || path != filepath.Join(dir, "untitled folder") {
		t.Fatalf("MakeFolder = %q, %v", path, err)
	}
	if _, err := MakeFolder(dir, "untitled folder"); err == nil {
		t.Fatal("made a folder over one already there")
	}
	for _, bad := range []string{"", "..", "a/b"} {
		if _, err := MakeFolder(dir, bad); err == nil {
			t.Errorf("made a folder named %q", bad)
		}
	}
}

func TestThePlacesEndWithTheComputer(t *testing.T) {
	places := ListPlaces()
	if len(places.Locations) == 0 || places.Locations[0].Path != "/" {
		t.Fatalf("locations = %+v; want the computer first", places.Locations)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
