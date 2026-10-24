package manager

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"path/filepath"
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

// An image sent with a message reaches the model, before the words, and is
// kept so the apps can show it again.
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
	if len(content) != 2 || content[0].OfImage == nil || content[1].OfText == nil || content[1].OfText.Text != "what is this?" {
		t.Fatalf("content = %+v", content)
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
	if len(content) != 1 || content[0].OfImage == nil {
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
