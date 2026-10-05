package source

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// CursorCLIGroup keeps interactive terminal chats and Agentman-owned ACP chats
// under the same public agent kind. Their identifiers never overlap.
//
// The registry holds this group, not the two sources, so every optional
// interface the daemon looks for has to be forwarded here explicitly. History
// and Closer were once implemented only on CursorCLISource, which the registry
// never sees: Cursor chats were missing from every folder and ending a pane
// from the phone always failed.
type CursorCLIGroup struct {
	terminal *CursorCLISource
	acp      *CursorACPSource
}

// The interfaces the daemon discovers by type assertion. Keeping the proof
// beside the group is what stops a forwarded method from quietly going
// missing again.
var (
	_ Closer            = (*CursorCLIGroup)(nil)
	_ History           = (*CursorCLIGroup)(nil)
	_ Injector          = (*CursorCLIGroup)(nil)
	_ Answerer          = (*CursorCLIGroup)(nil)
	_ QuestionInspector = (*CursorCLIGroup)(nil)
	_ Interrupter       = (*CursorCLIGroup)(nil)
	_ ResumeNamer       = (*CursorCLIGroup)(nil)
)

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

// TmuxName implements Closer. ACP chats run in a child process the source
// owns, never in a pane, so there is nothing for "end" to close.
func (s *CursorCLIGroup) TmuxName(id string) (string, bool) {
	if s.owned(id) {
		return "", false
	}
	return s.terminal.TmuxName(id)
}

// ResumedSession implements ResumeNamer. A resumed chat opens in a managed
// pane, and discovery publishes a chat a managed pane holds under the pane's
// id, so that is the id the phone must be sent to. The daemon used to return
// "cursor-cli:<chat id>", which no sweep ever produces: the phone moved to a
// session that never appeared and reported that it had not come back up.
func (s *CursorCLIGroup) ResumedSession(_, defaultPane string) (string, string) {
	return defaultPane, cursorCLIPaneIDPrefix + defaultPane
}

// Past implements History over both stores. Terminal chats and ACP chats are
// separate conversations with separate ids, so the merge is a plain union,
// ordered the way every other adapter orders its history.
func (s *CursorCLIGroup) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	terminal, terminalErr := s.terminal.Past(ctx, dir, limit)
	managed, managedErr := s.acp.Past(ctx, dir, limit)
	all := append(terminal, managed...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].LastActivityAt > all[j].LastActivityAt })
	if limit = limitOrDefault(limit); len(all) > limit {
		all = all[:limit]
	}
	return all, errors.Join(terminalErr, managedErr)
}

// Directories implements History, summing both stores per directory.
func (s *CursorCLIGroup) Directories(ctx context.Context) ([]protocol.Folder, error) {
	terminal, terminalErr := s.terminal.Directories(ctx)
	managed, managedErr := s.acp.Directories(ctx)
	merged := map[string]*protocol.Folder{}
	for _, folder := range append(terminal, managed...) {
		into := merged[folder.Path]
		if into == nil {
			into = &protocol.Folder{Path: folder.Path}
			merged[folder.Path] = into
		}
		into.Agents += folder.Agents
		into.LastActivityAt = max(into.LastActivityAt, folder.LastActivityAt)
	}
	folders := make([]protocol.Folder, 0, len(merged))
	for _, folder := range merged {
		folders = append(folders, *folder)
	}
	return folders, errors.Join(terminalErr, managedErr)
}

func (s *CursorCLIGroup) Launch(ctx context.Context, cwd, prompt string) (string, error) {
	return s.acp.Launch(ctx, cwd, prompt)
}

// SetPending forwards the hook-delivery queue to terminal chats. ACP chats
// queue their own follow-ups, durably, in their records.
func (s *CursorCLIGroup) SetPending(queue *PendingQueue) { s.terminal.SetPending(queue) }

func (s *CursorCLIGroup) ResumeQueued() { s.acp.ResumeQueued() }
func (s *CursorCLIGroup) EnableAsync()  { s.acp.EnableAsync() }
func (s *CursorCLIGroup) Close()        { s.acp.Close() }
