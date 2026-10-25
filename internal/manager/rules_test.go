package manager

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, text string
		want          bool
	}{
		{"npm run *", "npm run build", true},
		{"npm run *", "npm test", false},
		{"git push*", "git push origin main", true},
		{"*.md", "docs/guide/readme.md", true},
		{"docs/*", "docs/a/b.txt", true},
		{"make ?", "make a", true},
		{"make ?", "make ab", false},
		{"curl https://api.example.com/*", "curl https://api.example.com/v1/x", true},
		{"", "anything", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.text); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v", c.pattern, c.text, got)
		}
	}
}

func rulePolicy(kinds protocol.ApproveKinds, rules ...protocol.PermissionRule) policy {
	return policy{kinds: kinds, rules: rules}
}

func TestAnAskRuleWinsOverTheBuiltInPolicy(t *testing.T) {
	p := rulePolicy(allKinds, protocol.PermissionRule{Action: "ask", Kind: "command", Pattern: "npm test*"})
	d := decide(protocol.Session{Cwd: "/work/app"}, "command", "npm run build && npm test", "1", p)
	if d.auto || !strings.Contains(d.reason, "npm test*") {
		t.Fatalf("an ask rule on a part of the command didn't hold it: %+v", d)
	}
	d = decide(protocol.Session{Cwd: "/work/app"}, "command", "npm run build", "1", p)
	if !d.auto {
		t.Fatalf("a safe command no rule names waited: %+v", d)
	}
}

func TestAnAllowRuleWorksInItsFolderOnly(t *testing.T) {
	p := rulePolicy(allKinds, protocol.PermissionRule{Action: "allow", Kind: "command", Pattern: "git push*", Folder: "/work/app"})
	if d := decide(protocol.Session{Cwd: "/work/app/sub"}, "command", "git push origin main", "1", p); !d.auto {
		t.Errorf("git push in the rule's folder waited: %+v", d)
	}
	if d := decide(protocol.Session{Cwd: "/work/other"}, "command", "git push origin main", "1", p); d.auto {
		t.Error("git push was approved outside the rule's folder")
	}
}

func TestNoRuleApprovesRootOrCredentials(t *testing.T) {
	p := rulePolicy(allKinds,
		protocol.PermissionRule{Action: "allow", Kind: "any", Pattern: "sudo *"},
		protocol.PermissionRule{Action: "allow", Kind: "any", Pattern: "*id_ed25519*"},
	)
	if d := decide(protocol.Session{Cwd: "/work"}, "command", "sudo make install", "1", p); d.auto {
		t.Error("a rule approved a command run as root")
	}
	key := filepath.Join(homeDir(), ".ssh", "id_ed25519")
	if d := decide(protocol.Session{Cwd: homeDir()}, "read", key, "1", p); d.auto {
		t.Error("a rule approved reading a private key")
	}
	if d := decide(protocol.Session{Cwd: "/work"}, "command", "cat "+key, "1", p); d.auto {
		t.Error("a rule approved a command reading a private key")
	}
}

func TestSwitchingAKindOffMakesItWait(t *testing.T) {
	off := protocol.ApproveKinds{}
	session := protocol.Session{Cwd: "/work/app"}
	for _, c := range []struct{ kind, target string }{
		{"command", "ls -la"}, {"edit", "src/main.go"}, {"read", "README.md"}, {"continue", ""},
	} {
		if d := decide(session, c.kind, c.target, "1", rulePolicy(off)); d.auto || d.reason == "" {
			t.Errorf("%s waited for nothing with every kind off: %+v", c.kind, d)
		}
		if d := decide(session, c.kind, c.target, "1", rulePolicy(allKinds)); !d.auto {
			t.Errorf("%s waited with every kind on: %+v", c.kind, d)
		}
	}
}

func TestAPathRuleMatchesInsideTheFolder(t *testing.T) {
	p := rulePolicy(protocol.ApproveKinds{}, protocol.PermissionRule{Action: "allow", Kind: "edit", Pattern: "docs/*"})
	session := protocol.Session{Cwd: "/work/app"}
	if d := decide(session, "edit", "/work/app/docs/guide.md", "1", p); !d.auto {
		t.Errorf("an edit under docs/ waited: %+v", d)
	}
	if d := decide(session, "edit", "src/main.go", "1", p); d.auto {
		t.Error("an edit outside docs/ was approved with edits switched off")
	}
}

func TestRulesAreValidated(t *testing.T) {
	for _, bad := range [][]protocol.PermissionRule{
		{{Action: "maybe", Kind: "command", Pattern: "ls"}},
		{{Action: "allow", Kind: "everything", Pattern: "ls"}},
		{{Action: "allow", Kind: "command", Pattern: " "}},
		{{Action: "allow", Kind: "any", Pattern: "*"}},
		{{Action: "allow", Kind: "command", Pattern: "ls", Folder: "relative/path"}},
	} {
		if validateRules(bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := validateRules([]protocol.PermissionRule{{Action: "ask", Kind: "any", Pattern: "*"}, {Action: "allow", Kind: "command", Pattern: "make *", Folder: "~/work"}}); err != nil {
		t.Errorf("refused good rules: %v", err)
	}
}

func TestSettingsFromAnOlderAppKeepTheRules(t *testing.T) {
	h := newHarness(t, nil, false)
	settings := h.m.currentSettings()
	settings.Approve = &protocol.ApproveKinds{Reads: true}
	settings.Rules = []protocol.PermissionRule{{Action: "ask", Kind: "command", Pattern: "make deploy"}}
	if event := h.m.setSettings(&settings); event.Type == protocol.EvtError {
		t.Fatalf("set: %s", event.Error)
	}
	older := h.m.currentSettings()
	older.Approve, older.Rules = nil, nil
	older.Style = "Brief."
	if event := h.m.setSettings(&older); event.Type == protocol.EvtError {
		t.Fatalf("set from an older app: %s", event.Error)
	}
	now := h.m.currentSettings()
	if now.Style != "Brief." || len(now.Rules) != 1 || now.Approve == nil || now.Approve.Edits {
		t.Fatalf("an older app's settings lost the rules: %+v", now)
	}
	cleared := now
	cleared.Rules = []protocol.PermissionRule{}
	h.m.setSettings(&cleared)
	if len(h.m.currentSettings().Rules) != 0 {
		t.Fatal("an empty list didn't clear the rules")
	}
}

func TestCheckPermissionSaysWhatAgentmanWouldDo(t *testing.T) {
	settings := defaultSettings()
	if check := checkPermission(settings, "command", "go test ./...", "/work"); !check.Auto {
		t.Errorf("go test would wait: %+v", check)
	}
	if check := checkPermission(settings, "command", "rm -rf build", "/work"); check.Auto || check.Reason == "" {
		t.Errorf("rm would be approved: %+v", check)
	}
	settings.AutoApprove = false
	if check := checkPermission(settings, "command", "go test ./...", "/work"); check.Auto || !strings.Contains(check.Reason, "switched off") {
		t.Errorf("with approving off, go test would be approved: %+v", check)
	}
}
