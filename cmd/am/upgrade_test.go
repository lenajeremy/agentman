package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The program read back from the job am service install writes, even with
// a character the plist escapes in its path.
func TestTheServicesProgramIsReadFromItsJob(t *testing.T) {
	binary := "/Users/me/Tools & Bits/am"
	plist := filepath.Join(t.TempDir(), "dev.agentman.serve.plist")
	content := renderServicePlist(servicePlist{Binary: binary, Log: "/tmp/serve.log", Home: "/Users/me", Path: "/usr/bin"})
	if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := serviceBinary(plist)
	if err != nil || got != binary {
		t.Fatalf("serviceBinary = %q, %v", got, err)
	}
}

// A copy of an older release is brought up to date; a build of unreleased
// work, or one that doesn't run, is left as it is; a copy already on the new
// release, or newer, keeps what it has.
func TestWhatHappensToTheServicesOwnCopy(t *testing.T) {
	for _, c := range []struct {
		copied string
		err    error
		want   copyAction
	}{
		{"0.30.0", nil, copyReplace},
		{"0.29.1", nil, copyReplace},
		{"0.31.0", nil, copyKeep},
		{"0.32.0", nil, copyKeep},
		{"0.30.0-dev+history-tab", nil, copyLeave},
		{"", errors.New("exit status 137"), copyLeave},
	} {
		if got := serviceCopyAction(c.copied, c.err, "0.31.0"); got != c.want {
			t.Errorf("serviceCopyAction(%q, %v) = %v, want %v", c.copied, c.err, got, c.want)
		}
	}
}

// The new am is a new file renamed over the old, never written into it: on
// Apple silicon an am rewritten in place is killed at launch.
func TestTheNewAmIsANewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "am")
	if err := os.WriteFile(path, []byte("old am"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(path, []byte("new am")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) || before.Sys().(*syscall.Stat_t).Ino == after.Sys().(*syscall.Stat_t).Ino {
		t.Error("the old file was written into")
	}
	if data, _ := os.ReadFile(path); string(data) != "new am" || after.Mode().Perm() != 0o755 {
		t.Errorf("new am = %q, mode %v", data, after.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".am.new-*")); len(leftovers) != 0 {
		t.Errorf("left %v behind", leftovers)
	}
}

func TestABinarysVersionIsWhatItSays(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "am")
	script := "#!/bin/sh\n[ \"$1 $2\" = \"version --json\" ] && echo '{\"version\":\"v0.31.0\"}'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := binaryVersion(context.Background(), fake); err != nil || got != "0.31.0" {
		t.Fatalf("binaryVersion = %q, %v", got, err)
	}
	if _, err := binaryVersion(context.Background(), filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing am reported a version")
	}
}
