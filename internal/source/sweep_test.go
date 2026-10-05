package source

import (
	"context"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// sweepWitness records whether each call it gets belongs to a sweep.
type sweepWitness struct {
	kind protocol.Kind
	mu   sync.Mutex
	saw  map[string]bool
}

func (w *sweepWitness) note(call string, ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.saw[call] = tmux.InSweep(ctx)
}

func (w *sweepWitness) Kind() protocol.Kind { return w.kind }
func (w *sweepWitness) Discover(ctx context.Context) ([]protocol.Session, error) {
	w.note("discover", ctx)
	return nil, nil
}
func (w *sweepWitness) Page(ctx context.Context, _, _ string, _ int) (protocol.Page, error) {
	w.note("page", ctx)
	return protocol.Page{}, nil
}
func (w *sweepWitness) Follow(context.Context, string, chan<- []protocol.Message) error { return nil }
func (w *sweepWitness) Inject(ctx context.Context, _, _ string) (protocol.InjectMode, error) {
	w.note("inject", ctx)
	return protocol.InjectTmux, nil
}

// Discovery shares one reading of tmux and ps across its adapters. Sending,
// answering and paging read the panes as they are now, so they never carry
// the sweep, and neither does anything after the sweep ends.
func TestOnlyDiscoveryRunsInASweep(t *testing.T) {
	witnesses := []*sweepWitness{
		{kind: protocol.KindClaude, saw: map[string]bool{}},
		{kind: protocol.KindCodex, saw: map[string]bool{}},
	}
	registry := NewRegistry()
	for _, witness := range witnesses {
		registry.Add(witness)
	}
	ctx := context.Background()
	if _, err := registry.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Inject(ctx, "claude:s1", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Page(ctx, "claude:s1", "", 10); err != nil {
		t.Fatal(err)
	}
	for _, witness := range witnesses {
		if !witness.saw["discover"] {
			t.Errorf("%s discovered outside a sweep", witness.kind)
		}
	}
	if witnesses[0].saw["inject"] || witnesses[0].saw["page"] {
		t.Errorf("an action ran inside a sweep: %v", witnesses[0].saw)
	}
}
