package manager

import "testing"

func TestCleanCommitMessage(t *testing.T) {
	for in, want := range map[string]string{
		"Add the diff panel.":                                "Add the diff panel",
		"```\nAdd the diff panel\n```":                       "Add the diff panel",
		"\"Fix the sidebar width\"":                          "Fix the sidebar width",
		"Add commit\n\n\n\nThe panel commits the files.\n\n": "Add commit\n\nThe panel commits the files.",
		"  \n": "",
	} {
		if got := cleanCommitMessage(in); got != want {
			t.Errorf("cleanCommitMessage(%q) = %q, want %q", in, got, want)
		}
	}
}
