package parser

import "testing"

func TestDescribeToolSaysWhatAStepDid(t *testing.T) {
	cases := []struct{ name, summary, own, want string }{
		// The agent's own words, turned into what happened.
		{"Bash", "git commit -qam x", "Commit the rename lookup fix", "Committed the rename lookup fix"},
		{"Bash", "lsof -nP -iTCP:8099", "Check the relay is listening on 8099", "Checked the relay is listening on 8099"},
		{"Bash", "go build ./...", "Rebuild the daemon and start the sandbox", "Rebuilt the daemon and start the sandbox"},
		{"Bash", "rm -rf build", "Remove the old build", "Removed the old build"},
		{"Bash", "x", "Copy the app aside", "Copied the app aside"},
		{"Bash", "x", "Stop the sandbox", "Stopped the sandbox"},
		{"Bash", "x", "Run the Swift tests", "Ran the Swift tests"},
		{"Bash", "x", "Find where the daemon sends messages", "Found where the daemon sends messages"},
		{"Bash", "x", "Already checked", "Already checked"},
		{"Bash", "x", "The tests", "The tests"},
		// File tools.
		{"Edit", "/Users/me/agentman/internal/manager/tools.go", "", "Edited tools.go"},
		{"Read", "/tmp/shot.png", "", "Read shot.png"},
		{"Write", "/tmp/new.md", "", "Wrote new.md"},
		{"Grep", "toolPath", "", "Searched for “toolPath”"},
		{"WebFetch", "https://www.example.com/docs", "", "Fetched a page from example.com"},
		// Commands without a description: Codex, OpenCode and the rest.
		{"exec_command", `git commit -qm "Show sent images on the phone"`, "", "Committed “Show sent images on the phone”"},
		{"exec_command", "cd /repo && go test -race ./internal/...", "", "Ran the Go tests"},
		{"shell", "GOFLAGS=-mod=mod go vet ./...", "", "Checked the Go code"},
		{"exec_command", "sed -n 1,40p internal/daemon/daemon.go", "", "Read daemon.go"},
		{"exec_command", "rm -rf desktop/build", "", "Removed build"},
		{"exec_command", "npm test 2>&1 | tail", "", "Ran the tests"},
		{"exec_command", "xcodebuild -scheme App test", "", "Ran the app's tests"},
		{"exec_command", "curl -fsS http://localhost:8099/health", "", "Called localhost:8099"},
		{"exec_command", "frobnicate --all", "", "Ran frobnicate"},
	}
	for _, c := range cases {
		if got := DescribeTool(c.name, c.summary, c.own); got != c.want {
			t.Errorf("DescribeTool(%q, %q, %q) = %q, want %q", c.name, c.summary, c.own, got, c.want)
		}
	}
}
