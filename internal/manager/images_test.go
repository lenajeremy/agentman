package manager

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func pngImage(t *testing.T) protocol.InlineImage {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	// The app's label doesn't matter: what the bytes are does.
	return protocol.InlineImage{MIME: "image/jpeg", Data: base64.StdEncoding.EncodeToString(buf.Bytes())}
}

// An image sent with a message reaches the model, before the words and
// named, and is kept so the apps can show it again.
func TestImagesReachTheModelAndAreKept(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply("A blank 4 by 3 image.")}}
	h := newHarness(t, model, true)
	reply := h.m.send(context.Background(), "what is this?", pngImage(t))
	if reply.Type != protocol.EvtManagerEntry || len(reply.Entry.Images) != 1 {
		t.Fatalf("reply = %+v", reply)
	}
	h.waitIdle(t)

	messages := model.request(0).Messages
	content := messages[len(messages)-1].Content
	if len(content) != 3 || content[0].OfImage == nil || content[2].OfText == nil || content[2].OfText.Text != "what is this?" {
		t.Fatalf("content = %+v", content)
	}
	if label := content[1].OfText; label == nil || !strings.Contains(label.Text, reply.Entry.Images[0]) {
		t.Fatalf("the image wasn't named for the model: %+v", content[1])
	}
	if media := content[0].OfImage.Source.OfBase64.MediaType; media != "image/png" {
		t.Fatalf("media type = %s, want what the bytes are", media)
	}

	name := reply.Entry.Images[0]
	if _, err := os.Stat(filepath.Join(h.dir, "images", name)); err != nil {
		t.Fatalf("not kept: %v", err)
	}
	shown := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerImage, Path: name})
	if shown.Type != protocol.EvtWorkspace || shown.Workspace.MIME != "image/png" || shown.Workspace.Image == "" {
		t.Fatalf("shown = %+v", shown)
	}
	// Phones up to 0.7.0 drop a picture with no session, which showed as a
	// blank box.
	if shown.Workspace.SessionID == "" {
		t.Error("the picture names no owner, so phones up to 0.7.0 drop it")
	}

	// A restart keeps the entry's images, and the model's history keeps the
	// image itself.
	again := h.newManager(t)
	entries, _ := again.conversation.page("", 10)
	if len(entries) < 1 || len(entries[0].Images) != 1 {
		t.Fatalf("after a restart: %+v", entries)
	}
	if history := again.conversation.messages(); history[0].Content[0].OfImage == nil {
		t.Fatal("the image left the model's history")
	}
}

// An image alone is a message.
func TestAnImageAloneIsAMessage(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply("That's blank.")}}
	h := newHarness(t, model, true)
	if reply := h.m.send(context.Background(), "", pngImage(t)); reply.Type == protocol.EvtError {
		t.Fatal(reply.Error)
	}
	h.waitIdle(t)
	content := model.request(0).Messages[0].Content
	if len(content) != 2 || content[0].OfImage == nil {
		t.Fatalf("content = %+v", content)
	}
}

func TestImagesThatArentAreRefused(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	notImage := protocol.InlineImage{MIME: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\nrm -rf /\n"))}
	if reply := h.m.send(context.Background(), "look", notImage); reply.Type != protocol.EvtError {
		t.Fatal("a script labelled as a PNG was sent")
	}
	five := []protocol.InlineImage{pngImage(t), pngImage(t), pngImage(t), pngImage(t), pngImage(t)}
	if reply := h.m.send(context.Background(), "look", five...); reply.Type != protocol.EvtError {
		t.Fatal("five images were sent")
	}
	if reply := h.m.send(context.Background(), "look", protocol.InlineImage{Data: "not base64!"}); reply.Type != protocol.EvtError {
		t.Fatal("broken base64 was sent")
	}
	// A refused message leaves Agentman ready for the next.
	h.m.mu.Lock()
	running := h.m.stopTurn != nil
	h.m.mu.Unlock()
	if running {
		t.Fatal("a refused message left a turn open")
	}
	for _, name := range []string{"../settings.json", "m-1-abcdef-0.png/../../x", "keys.json", ""} {
		if h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerImage, Path: name}).Type != protocol.EvtError {
			t.Errorf("read %q", name)
		}
	}
}

// An image the user sends Agentman for an agent goes on to the agent with
// the message, and only the user's own images can.
func TestAgentmanForwardsTheUsersImages(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply("Which agent should see it?")}}
	h := newHarness(t, model, true)
	sessions := h.agents.Sessions()
	sessions[0].Inject = protocol.InjectTmux
	h.agents.setSessions(sessions)
	h.agents.answer = delivered
	shot := pngImage(t)
	reply := h.m.send(context.Background(), "this is how the page looks", shot)
	h.waitIdle(t)
	name := reply.Entry.Images[0]

	model.mu.Lock()
	model.replies = append(model.replies,
		toolReply("t1", "send_to_agent", `{"agent":"site","text":"the header overlaps the nav here","images":["`+name+`"]}`),
		textReply("Sent."))
	model.mu.Unlock()
	h.m.send(context.Background(), "send it to site")
	h.waitIdle(t)
	sent := h.agents.sent(protocol.ReqSendMessage)
	if len(sent) != 1 || len(sent[0].Images) != 1 || sent[0].Images[0].Data != shot.Data || sent[0].Images[0].MIME != "image/png" {
		t.Fatalf("sent = %+v", sent)
	}
	var shown bool
	for _, entry := range h.entries() {
		shown = shown || (entry.Role == "activity" && entry.Text == "Sent site an image: “the header overlaps the nav here”")
	}
	if !shown {
		t.Fatalf("what was sent wasn't shown: %+v", h.entries())
	}

	for _, other := range []string{"../settings.json", "/etc/passwd", "m-1-abcdef-0.png", "keys.json"} {
		input := `{"agent":"site","text":"look","images":["` + other + `"]}`
		if result := h.m.runTool(context.Background(), "send_to_agent", []byte(input)); !result.isError {
			t.Errorf("%q was sent to the agent", other)
		}
	}
	if n := len(h.agents.sent(protocol.ReqSendMessage)); n != 1 {
		t.Errorf("%d messages reached the agent", n)
	}
}
