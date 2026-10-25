package source

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Shaped like OpenCode 1.18's GET /config/providers on this Mac (2026-10-20):
// the connected providers only, each with its models by id.
func ocProviders() map[string]any {
	return map[string]any{
		"providers": []any{
			map[string]any{"id": "openai", "models": map[string]any{
				"gpt-6-sol":        map[string]any{"id": "gpt-6-sol", "status": "active"},
				"gpt-6-sol-fast":   map[string]any{"id": "gpt-6-sol-fast"},
				"shared-model":     map[string]any{"id": "shared-model"},
				"gpt-4-deprecated": map[string]any{"id": "gpt-4-deprecated", "status": "deprecated"},
			}},
			map[string]any{"id": "opencode", "models": map[string]any{
				"big-pickle":   map[string]any{"id": "big-pickle"},
				"shared-model": map[string]any{"id": "shared-model"},
			}},
		},
		"default": map[string]any{"openai": "gpt-6-sol", "opencode": "big-pickle"},
	}
}

func TestOpenCodeOffersItsConnectedModels(t *testing.T) {
	fake := &ocFake{providers: ocProviders()}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	found, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session := found[0]
	want := []string{"gpt-6-sol", "gpt-6-sol-fast", "openai/shared-model", "big-pickle", "opencode/shared-model"}
	if !slices.Equal(session.Models, want) || session.ModelScope != protocol.ModelScopeSession {
		t.Fatalf("models = %q, scope = %q", session.Models, session.ModelScope)
	}
}

func TestOpenCodeSendsTheChosenModelWithEachMessage(t *testing.T) {
	fake := &ocFake{providers: ocProviders()}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.SetModel(ctx, "opencode:ses_1", "not-a-model"); err == nil {
		t.Fatal("SetModel accepted a model OpenCode doesn't have")
	}
	if err := src.SetModel(ctx, "opencode:ses_1", "opencode/shared-model"); err != nil {
		t.Fatal(err)
	}
	// The phone sees the switch as soon as the next sweep reports it.
	found, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if found[0].Model != "opencode/shared-model" {
		t.Fatalf("model after the switch = %q", found[0].Model)
	}
	if _, err := src.Inject(ctx, "opencode:ses_1", "go on"); err != nil {
		t.Fatal(err)
	}
	model, _ := fake.lastRequest().body["model"].(map[string]any)
	if model["providerID"] != "opencode" || model["modelID"] != "shared-model" {
		t.Fatalf("prompt model = %#v", fake.lastRequest().body["model"])
	}
}

func TestOpenCodeChoiceGivesWayToASwitchMadeElsewhere(t *testing.T) {
	src := &OpenCodeSource{}
	session := &openCodeSession{baseURL: "http://opencode.test", meta: protocol.Session{Model: "gpt-6-sol"}}
	// A list already fetched, so nothing here goes to a server.
	src.catalogs = map[string]*openCodeCatalog{"http://opencode.test": {fetched: time.Now()}}
	src.offerModels(context.Background(), "opencode:s", session) // records what OpenCode reports
	src.choices = map[string]openCodeChoice{"opencode:s": {
		name: "big-pickle", ref: openCodeModelRef{"opencode", "big-pickle"}, reported: "gpt-6-sol",
	}}

	// Still reporting the old model: the choice stands.
	session.meta.Model = "gpt-6-sol"
	src.offerModels(context.Background(), "opencode:s", session)
	if session.meta.Model != "big-pickle" {
		t.Fatalf("model = %q, want the choice", session.meta.Model)
	}
	// Switched in OpenCode's own TUI to a third model: that wins.
	session.meta.Model = "gpt-6-sol-fast"
	src.offerModels(context.Background(), "opencode:s", session)
	if session.meta.Model != "gpt-6-sol-fast" {
		t.Fatalf("model = %q, want the one chosen elsewhere", session.meta.Model)
	}
	if _, ok := src.chosenModel("opencode:s"); ok {
		t.Fatal("the phone's choice was kept after a switch elsewhere")
	}
}
