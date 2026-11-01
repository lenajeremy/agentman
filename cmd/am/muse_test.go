package main

import "testing"

func TestMuseResumeTargetIsTheNamedSession(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--provider", "meta"}, ""},
		{[]string{"resume", "01a19447-2bc8-78e2-b685-8762e93e33fd"}, "01a19447-2bc8-78e2-b685-8762e93e33fd"},
		// A flag's value before the id is not the id.
		{[]string{"resume", "--model", "x", "01A19468-68BC-7991-A879-41772098D5DB"}, "01a19468-68bc-7991-a879-41772098d5db"},
		// Named by name, or not named: left to Muse.
		{[]string{"resume", "muted-pasiphae"}, ""},
		{[]string{"resume", "--last"}, ""},
		{[]string{"resume"}, ""},
		{[]string{"exec", "01a19447-2bc8-78e2-b685-8762e93e33fd"}, ""},
	}
	for _, c := range cases {
		if got := museResumeTarget(c.args); got != c.want {
			t.Errorf("%v: got %q, want %q", c.args, got, c.want)
		}
	}
}
