package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEmptyArtifactListIsSentAsAnEmptyArray guards the difference between
// "this session has no artifacts" and "this daemon said nothing about them".
// With omitempty an empty list vanished from the frame, and the two answers
// became one.
func TestEmptyArtifactListIsSentAsAnEmptyArray(t *testing.T) {
	encoded, err := json.Marshal(Event{Type: EvtArtifacts, SessionID: "antigravity:c1", Artifacts: []Artifact{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"artifacts":[]`) {
		t.Fatalf("an empty artifact list was not sent as []: %s", encoded)
	}

	// Every other event leaves the field out entirely, as before.
	encoded, err = json.Marshal(Event{Type: EvtSessionGone, SessionID: "antigravity:c1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "artifacts") {
		t.Fatalf("an event with no artifact list carried one: %s", encoded)
	}
}

func TestArtifactWireNames(t *testing.T) {
	encoded, err := json.Marshal(Artifact{
		Name: "implementation_plan.md", Kind: "plan", Title: "Plan", Summary: "Steps",
		UpdatedAt: 1, Size: 2, MIME: "text/markdown", Review: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"implementation_plan.md","kind":"plan","title":"Plan","summary":"Steps",` +
		`"updatedAt":1,"size":2,"mime":"text/markdown","review":true}`
	if string(encoded) != want {
		t.Fatalf("artifact encoded as\n%s\nwant\n%s", encoded, want)
	}

	var req Request
	if err := json.Unmarshal([]byte(`{"type":"review_artifact","sessionId":"antigravity:c1",`+
		`"path":"implementation_plan.md","approve":true,"clientId":"c"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Type != ReqReviewArtifact || req.Path != "implementation_plan.md" || !req.Approve {
		t.Fatalf("review request decoded as %+v", req)
	}
}

func TestSessionStatusFieldsAreOptionalOnTheWire(t *testing.T) {
	encoded, err := json.Marshal(Session{ID: "kiro:k1", Kind: KindKiro})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mode", "contextPercent", "artifacts", "artifactsToReview"} {
		if strings.Contains(string(encoded), `"`+name+`"`) {
			t.Errorf("a session without %s still sent it: %s", name, encoded)
		}
	}
	encoded, err = json.Marshal(Session{
		ID: "kiro:k1", Kind: KindKiro, Mode: "plan", ContextPercent: 42, Artifacts: 3, ArtifactsToReview: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"mode":"plan"`, `"contextPercent":42`, `"artifacts":3`, `"artifactsToReview":1`} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("%s missing from %s", field, encoded)
		}
	}
}

// A refusal with a reason travels as the option chosen plus the note, in the
// fields a terminal answer and a custom answer already use.
func TestAnOptionWithTextRoundTrips(t *testing.T) {
	encoded, err := json.Marshal(QuestionOption{Key: "3", Label: "No, and tell Claude what to do", WithText: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"withText":true`) {
		t.Fatalf("option encoded as %s", encoded)
	}
	var option QuestionOption
	if err := json.Unmarshal(encoded, &option); err != nil || !option.WithText {
		t.Fatalf("option decoded as %+v, %v", option, err)
	}
	plain, _ := json.Marshal(QuestionOption{Key: "1", Label: "Yes"})
	if strings.Contains(string(plain), "withText") {
		t.Fatalf("an ordinary option carried the field: %s", plain)
	}

	var req Request
	if err := json.Unmarshal([]byte(`{"type":"answer_question","sessionId":"claude:s1",`+
		`"questionId":"q","optionKey":"3","answerText":"use the staging database"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.OptionKey != "3" || req.AnswerText != "use the staging database" {
		t.Fatalf("answer decoded as %+v", req)
	}
}

func TestSwitchingWireNames(t *testing.T) {
	encoded, err := json.Marshal(Session{
		ID: "kiro:k1", Kind: KindKiro, Modes: []string{"kiro_default", "kiro_planner"},
		Models: []string{"claude-sonnet-4.5"}, ModelScope: ModelScopeSession,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		`"modes":["kiro_default","kiro_planner"]`, `"models":["claude-sonnet-4.5"]`, `"modelScope":"session"`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("%s missing from %s", field, encoded)
		}
	}
	plain, _ := json.Marshal(Session{ID: "kiro:k1"})
	for _, name := range []string{"modes", "models", "modelScope"} {
		if strings.Contains(string(plain), `"`+name+`"`) {
			t.Errorf("a session that offers no switching still sent %s: %s", name, plain)
		}
	}
	if ReqSetMode != "set_mode" || ReqSetModel != "set_model" ||
		ModelScopeSession != "session" || ModelScopeDefault != "default" {
		t.Fatal("a wire name changed")
	}
}

// The app reads these names, and sends Commit back, so they are pinned here
// the way mobile/lib/protocol.test.mts pins its side.
func TestCommitHistoryWireNames(t *testing.T) {
	encoded, err := json.Marshal(WorkspaceResult{
		Kind: "commits", SessionID: "claude:s", Branch: "main", Truncated: true,
		Commits: []WorkspaceCommit{{
			Hash: strings.Repeat("a", 40), Subject: "Fix", Author: "Ada", AuthoredAt: 1,
			Merge: true, Files: 2, Added: 3, Removed: 4,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"kind":"commits","sessionId":"claude:s","truncated":true,"commits":[{"hash":"` +
		strings.Repeat("a", 40) + `","subject":"Fix","author":"Ada","authoredAt":1,"merge":true,` +
		`"files":2,"added":3,"removed":4}],"branch":"main"}`
	if string(encoded) != want {
		t.Fatalf("commits encoded as\n%s\nwant\n%s", encoded, want)
	}

	var req Request
	if err := json.Unmarshal([]byte(`{"type":"commit_diff","sessionId":"claude:s",`+
		`"commit":"`+strings.Repeat("b", 40)+`","path":"a.go"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Type != ReqCommitDiff || req.Commit != strings.Repeat("b", 40) || req.Path != "a.go" {
		t.Fatalf("commit diff request decoded as %+v", req)
	}
}

func TestBranchWireNames(t *testing.T) {
	encoded, err := json.Marshal(WorkspaceBranch{Name: "origin/main", Ref: "refs/remotes/origin/main", Remote: true, Current: true, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"origin/main","ref":"refs/remotes/origin/main","remote":true,"current":true,"updatedAt":1}`
	if string(encoded) != want {
		t.Fatalf("branch encoded as\n%s\nwant\n%s", encoded, want)
	}
	var req Request
	if err := json.Unmarshal([]byte(`{"type":"list_commits","sessionId":"claude:s","ref":"refs/heads/feature"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Type != ReqListCommits || req.Ref != "refs/heads/feature" {
		t.Fatalf("list_commits decoded as %+v", req)
	}
}
