package apphost

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestShowInFinderAndOpenAskTheSystemForAFileThatIsThere(t *testing.T) {
	var ran []string
	before := start
	start = func(name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}
	defer func() { start = before }()

	dir := t.TempDir()
	if runtime.GOOS == "darwin" {
		if err := Reveal(dir); err != nil {
			t.Fatal(err)
		}
		if err := OpenPath(dir); err != nil {
			t.Fatal(err)
		}
		if len(ran) != 2 || ran[0] != "open -R "+dir || ran[1] != "open "+dir {
			t.Fatalf("ran %q", ran)
		}
	}
	if err := OpenPath(filepath.Join(dir, "gone.txt")); err == nil {
		t.Fatal("opened a file that isn't there")
	}
	if err := Reveal("relative"); err == nil {
		t.Fatal("showed a relative path")
	}
}
