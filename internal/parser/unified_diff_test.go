package parser

import (
	"fmt"
	"strings"
	"testing"
)

func TestUnifiedDiffShapes(t *testing.T) {
	cases := []struct {
		name, old, new, want string
	}{
		{"changed line", "alpha\nbeta\ngamma\n", "alpha\nBETA\ngamma\n",
			"--- a/w/f\n+++ b/w/f\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma"},
		{"new file", "", "hi\nthere\n",
			"--- /dev/null\n+++ b/w/f\n@@ -0,0 +1,2 @@\n+hi\n+there"},
		{"deleted file", "bye\n", "",
			"--- a/w/f\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-bye"},
		{"unchanged", "same\n", "same\n", ""},
	}
	for _, c := range cases {
		if got := UnifiedDiff("/w/f", c.old, c.new); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// Changes far apart become separate hunks with three lines of context, so a
// one-line edit to a long file is a few lines on the phone, not the file.
func TestUnifiedDiffSplitsDistantChanges(t *testing.T) {
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	old := strings.Join(lines, "\n") + "\n"
	lines[1], lines[25] = "LINE 2", "LINE 26"
	diff := UnifiedDiff("/w/f", old, strings.Join(lines, "\n")+"\n")
	if strings.Count(diff, "@@ -") != 2 {
		t.Fatalf("want two hunks:\n%s", diff)
	}
	if !strings.Contains(diff, "@@ -1,5 +1,5 @@\n line 1\n-line 2\n+LINE 2\n line 3") ||
		!strings.Contains(diff, "@@ -23,7 +23,7 @@\n line 23\n line 24\n line 25\n-line 26\n+LINE 26") {
		t.Fatalf("hunks:\n%s", diff)
	}
	if strings.Contains(diff, "line 12") {
		t.Fatalf("unchanged middle of the file leaked into the diff:\n%s", diff)
	}
}
