package manager

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// A harness whose Agentman suggests one note, with a notes store to save it in.
func suggesting(t *testing.T) (*harness, *knowledge.Store, protocol.ManagerEntry) {
	t.Helper()
	model := &scriptedModel{replies: []string{
		toolReply("t1", "suggest_note", `{"title":"Commits","body":"No Co-Authored-By lines in commits.","why":"You said so twice."}`),
		textReply("I've suggested a note."),
	}}
	h := newHarness(t, model, true)
	store := knowledge.Open(filepath.Join(t.TempDir(), "knowledge"))
	h.m.knowledge = store
	h.m.send(context.Background(), "please don't add attribution to commits")
	h.waitIdle(t)
	for _, entry := range h.entries() {
		if entry.Card != nil && entry.Card.Kind == protocol.CardNote {
			return h, store, entry
		}
	}
	t.Fatalf("no note card: %+v", h.entries())
	return nil, nil, protocol.ManagerEntry{}
}

// notes is every note kept, read in full.
func notes(t *testing.T, store *knowledge.Store) []knowledge.Note {
	t.Helper()
	all, _ := store.List(knowledge.Shared)
	private, _ := store.List(knowledge.Private)
	var full []knowledge.Note
	for _, note := range append(all, private...) {
		read, err := store.Read(note.Tier, note.Name)
		if err != nil {
			t.Fatal(err)
		}
		full = append(full, read)
	}
	return full
}

// A suggestion is only a card: nothing is saved until the user says so.
func TestASuggestedNoteIsNotSavedUntilApproved(t *testing.T) {
	h, store, entry := suggesting(t)
	if entry.Card.State != protocol.CardOpen || entry.Card.Note.Title != "Commits" || entry.Card.Reason != "You said so twice." {
		t.Fatalf("card = %+v", entry.Card)
	}
	if got := notes(t, store); len(got) != 0 {
		t.Fatalf("saved before approval: %+v", got)
	}

	reply := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerCardReply, CardID: entry.Card.ID})
	if reply.Type != protocol.EvtManagerEntry || reply.Entry.Card.State != protocol.CardAnswered {
		t.Fatalf("reply = %+v", reply)
	}
	got := notes(t, store)
	if len(got) != 1 || got[0].Title != "Commits" || !strings.Contains(got[0].Body, "Co-Authored-By") || got[0].Tier != knowledge.Shared {
		t.Fatalf("saved = %+v", got)
	}
}

func TestAnEditedNoteIsSavedAsEdited(t *testing.T) {
	h, store, entry := suggesting(t)
	edited := &protocol.Note{Title: "Commit messages", Body: "Never add attribution lines.", Tier: knowledge.Private}
	reply := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerCardReply, CardID: entry.Card.ID, Note: edited})
	if reply.Type != protocol.EvtManagerEntry || !strings.Contains(reply.Entry.Card.Outcome, "your changes") {
		t.Fatalf("reply = %+v", reply)
	}
	got := notes(t, store)
	if len(got) != 1 || got[0].Title != "Commit messages" || strings.TrimSpace(got[0].Body) != "Never add attribution lines." || got[0].Tier != knowledge.Private {
		t.Fatalf("saved = %+v", got)
	}
}

func TestADismissedNoteIsNotSaved(t *testing.T) {
	h, store, entry := suggesting(t)
	reply := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerCardDismiss, CardID: entry.Card.ID})
	if reply.Type != protocol.EvtManagerEntry || reply.Entry.Card.State != protocol.CardDismissed {
		t.Fatalf("reply = %+v", reply)
	}
	if got := notes(t, store); len(got) != 0 {
		t.Fatalf("saved = %+v", got)
	}
	if told := h.m.takeNotes(); !strings.Contains(told, "don't suggest it again") {
		t.Fatalf("Agentman was not told: %q", told)
	}
}

// A note is about no agent, so agents coming and going leave it waiting.
func TestASuggestedNoteOutlastsTheAgents(t *testing.T) {
	h, _, entry := suggesting(t)
	h.m.reconcile(context.Background(), nil)
	for _, open := range h.m.conversation.openCards() {
		if open.Card.ID == entry.Card.ID {
			return
		}
	}
	t.Fatal("the note card was settled when the agents went")
}

func TestTheSameNoteIsNotSuggestedTwice(t *testing.T) {
	h, _, _ := suggesting(t)
	again := runSuggestNote(context.Background(), h.m, map[string]any{"title": "commits", "body": "x"})
	if !strings.Contains(again.text, "already waiting") {
		t.Fatalf("again = %+v", again)
	}
}

// Agentman deletes a note when the user asks it to.
func TestAgentmanDeletesANoteWhenAsked(t *testing.T) {
	model := &scriptedModel{replies: []string{
		toolReply("t1", "delete_note", `{"name":"commits"}`),
		textReply("Deleted."),
	}}
	h := newHarness(t, model, true)
	store := knowledge.Open(filepath.Join(t.TempDir(), "knowledge"))
	h.m.knowledge = store
	if _, err := store.Write(knowledge.Note{Tier: knowledge.Private, Title: "Commits", Body: "No attribution."}); err != nil {
		t.Fatal(err)
	}
	h.m.send(context.Background(), "delete my commits note")
	h.waitIdle(t)
	if got := notes(t, store); len(got) != 0 {
		t.Fatalf("still kept: %+v", got)
	}
	var shown bool
	for _, entry := range h.entries() {
		shown = shown || (entry.Role == "activity" && entry.Text == "Deleted your note “Commits”")
	}
	if !shown {
		t.Fatalf("entries = %+v", h.entries())
	}
	if gone := runDeleteNote(context.Background(), h.m, map[string]any{"name": "commits"}); !gone.isError {
		t.Fatalf("deleting it again = %+v", gone)
	}
}
