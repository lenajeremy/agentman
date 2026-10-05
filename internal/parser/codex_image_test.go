package parser

import (
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Codex 0.160 records a viewed image as an ImageView item whose path is a
// file:// URL. The row must carry the plain path, or the phone has nothing it
// can open, and "[image]" so it reads like every other agent's image read.
func TestCodexImageViewNamesThePictureAsAPath(t *testing.T) {
	cases := map[string]string{
		`file:///private/tmp/remediation-landing.png`:                           "/private/tmp/remediation-landing.png",
		`file:///Users/mac/Desktop/Screenshot%202026-10-05%20at%2010.00.00.png`: "/Users/mac/Desktop/Screenshot 2026-10-05 at 10.00.00.png",
		`file://localhost/tmp/a.png`:                                            "/tmp/a.png",
		`/tmp/already-a-path.png`:                                               "/tmp/already-a-path.png",
	}
	for raw, want := range cases {
		line := `{"timestamp":"2026-08-09T06:43:31.841Z","type":"event_msg","payload":{"type":"item_completed",` +
			`"item":{"type":"ImageView","id":"exec-4f4c1f4c","path":"` + raw + `"}}}`
		got := NewCodexParser("codex:test").Parse(line, 0)
		if len(got) != 1 || got[0].Tool == nil {
			t.Fatalf("%s: rows = %+v", raw, got)
		}
		row := got[0]
		if row.Role != protocol.RoleTool || row.Tool.Name != "View image" || row.Tool.Status != protocol.ToolOK {
			t.Errorf("%s: row = %+v %+v", raw, row, *row.Tool)
		}
		if row.Tool.Summary != want {
			t.Errorf("%s: summary = %q, want %q", raw, row.Tool.Summary, want)
		}
		if row.Text != "[image]" {
			t.Errorf("%s: text = %q, want [image]", raw, row.Text)
		}
	}
}

// A file:// URL on another host is not a file on this Mac.
func TestCodexImageViewLeavesARemoteURLAlone(t *testing.T) {
	if got := codexFilePath("file://server/share/a.png"); got != "file://server/share/a.png" {
		t.Errorf("got %q", got)
	}
}
