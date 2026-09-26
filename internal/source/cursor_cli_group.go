package source

import (
	"context"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// CursorCLIGroup keeps interactive terminal chats and Agentman-owned ACP chats
// under the same public agent kind. Their identifiers never overlap.
type CursorCLIGroup struct {
	terminal *CursorCLISource
	acp      *CursorACPSource
}

func NewCursorCLIGroup(terminal *CursorCLISource, acp *CursorACPSource) *CursorCLIGroup {
	return &CursorCLIGroup{terminal: terminal, acp: acp}
}

func (s *CursorCLIGroup) Kind() protocol.Kind { return protocol.KindCursorCLI }

func (s *CursorCLIGroup) owned(id string) bool { return strings.HasPrefix(id, cursorACPPrefix) }

func (s *CursorCLIGroup) Discover(ctx context.Context) ([]protocol.Session, error) {
	legacy, legacyErr := s.terminal.Discover(ctx)
	managed, managedErr := s.acp.Discover(ctx)
	all := append(legacy, managed...)
	if legacyErr != nil {
		return all, legacyErr
	}
	return all, managedErr
}

func (s *CursorCLIGroup) Page(ctx context.Context, id, before string, limit int) (protocol.Page, error) {
	if s.owned(id) {
		return s.acp.Page(ctx, id, before, limit)
	}
	return s.terminal.Page(ctx, id, before, limit)
}

func (s *CursorCLIGroup) Follow(ctx context.Context, id string, out chan<- []protocol.Message) error {
	if s.owned(id) {
		return s.acp.Follow(ctx, id, out)
	}
	return s.terminal.Follow(ctx, id, out)
}

func (s *CursorCLIGroup) Inject(ctx context.Context, id, text string) (protocol.InjectMode, error) {
	if s.owned(id) {
		return s.acp.Inject(ctx, id, text)
	}
	return s.terminal.Inject(ctx, id, text)
}

func (s *CursorCLIGroup) Interrupt(ctx context.Context, id string) error {
	if s.owned(id) {
		return s.acp.Interrupt(ctx, id)
	}
	return s.terminal.Interrupt(ctx, id)
}

func (s *CursorCLIGroup) Answer(ctx context.Context, id string, answer protocol.QuestionAnswer) error {
	if s.owned(id) {
		return s.acp.Answer(ctx, id, answer)
	}
	return s.terminal.Answer(ctx, id, answer)
}

func (s *CursorCLIGroup) CurrentQuestion(ctx context.Context, id string) (*protocol.Question, error) {
	if s.owned(id) {
		return s.acp.CurrentQuestion(ctx, id)
	}
	return s.terminal.CurrentQuestion(ctx, id)
}

func (s *CursorCLIGroup) Launch(ctx context.Context, cwd, prompt string) (string, error) {
	return s.acp.Launch(ctx, cwd, prompt)
}

func (s *CursorCLIGroup) ResumeQueued() { s.acp.ResumeQueued() }
func (s *CursorCLIGroup) EnableAsync()  { s.acp.EnableAsync() }
func (s *CursorCLIGroup) Close()        { s.acp.Close() }
