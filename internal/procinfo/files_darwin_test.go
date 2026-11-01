//go:build darwin

package procinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Files names what this process holds open, as lsof does: by descriptor,
// with links resolved, directories included.
func TestFilesAgreesWithLsof(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(link, "held.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	folder, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { folder.Close() })

	files, err := Files(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	mine := map[int]string{}
	for _, open := range files {
		mine[open.FD] = open.Path
	}
	if got := mine[int(file.Fd())]; got != filepath.Join(dir, "held.db") {
		t.Errorf("the file is named %q", got)
	}
	if got := mine[int(folder.Fd())]; got != dir {
		t.Errorf("the directory is named %q", got)
	}

	out, err := exec.Command("lsof", "-w", "-a", "-p", strconv.Itoa(os.Getpid()), "-Fpfn").Output()
	if err != nil && len(out) == 0 {
		t.Skipf("lsof: %v", err)
	}
	fd := ""
	var theirs []string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "f"):
			fd = strings.TrimRight(line[1:], "rwu ")
		case strings.HasPrefix(line, "n") && fd != "" && fd[0] >= '0' && fd[0] <= '9':
			if number, _ := strconv.Atoi(fd); filepath.IsAbs(line[1:]) && mine[number] != "" {
				theirs = append(theirs, fd)
				if mine[number] != line[1:] {
					t.Errorf("descriptor %d: %q, lsof says %q", number, mine[number], line[1:])
				}
			}
		}
	}
	for _, want := range []string{strconv.Itoa(int(file.Fd())), strconv.Itoa(int(folder.Fd()))} {
		if !slices.Contains(theirs, want) {
			t.Errorf("lsof and Files do not both list descriptor %s", want)
		}
	}
}
