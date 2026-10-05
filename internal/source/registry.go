package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Registry fans discovery out across every adapter and routes per-session
// calls back to whichever adapter owns that session.
//
// Session IDs are prefixed with their kind ("claude:...", "codex:..."), so
// routing is a string split rather than a lookup table that could drift out of
// sync with what was discovered.
type Registry struct {
	mu      sync.RWMutex
	sources map[protocol.Kind]Source
	// last keeps the most recent successful snapshot per adapter. A transient
	// API/read error must not announce every session of that kind as ended and
	// tear down its live follows; the next successful empty snapshot is what
	// confirms they are genuinely gone.
	last map[protocol.Kind][]protocol.Session

	// folderIndex caches the scan of every agent's store behind folderIndexTTL.
	// See folders.go.
	folderMu      sync.Mutex
	folderIndex   *FolderIndex
	folderBuiltAt time.Time
	// pastByDir caches one directory's finished sessions; see pastListingTTL.
	pastByDir map[string]pastListing
}

// MaxPageMessages bounds a direct history request. The relay-facing daemon
// uses a smaller wire-oriented page size, while the terminal can reasonably
// print more at once. Keeping the source boundary finite prevents any caller
// from turning a user-controlled limit into a large allocation or API request.
const MaxPageMessages = 100

// ValidatePageLimit rejects invalid pagination before it reaches an adapter.
func ValidatePageLimit(limit int) error {
	if limit <= 0 || limit > MaxPageMessages {
		return fmt.Errorf("message limit must be between 1 and %d", MaxPageMessages)
	}
	return nil
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		sources: map[protocol.Kind]Source{},
		last:    map[protocol.Kind][]protocol.Session{},
	}
}

// Add registers an adapter.
func (r *Registry) Add(s Source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources[s.Kind()] = s
	delete(r.last, s.Kind())
}

// Kinds lists the registered adapter kinds, sorted for stable output.
func (r *Registry) Kinds() []protocol.Kind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]protocol.Kind, 0, len(r.sources))
	for kind := range r.sources {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}

// Discover queries every adapter and returns the combined session list.
//
// One adapter failing must not blank the whole list — a broken Codex install
// should not hide running Claude sessions — so errors are collected and
// returned alongside whatever did succeed.
func (r *Registry) Discover(ctx context.Context) ([]protocol.Session, error) {
	r.mu.RLock()
	sources := make([]Source, 0, len(r.sources))
	for _, s := range r.sources {
		sources = append(sources, s)
	}
	r.mu.RUnlock()

	// The adapters run side by side, and most of them list the tmux panes,
	// read the process table and capture panes. One sweep asks each of those
	// once; see tmux.WithSweep. The memory ends with the sweep.
	ctx, sweepDone := tmux.WithSweep(ctx)
	defer sweepDone()

	var (
		mu       sync.Mutex
		all      []protocol.Session
		failures []string
		wg       sync.WaitGroup
	)

	for _, s := range sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			sessions, err := s.Discover(ctx)
			if err != nil && sessions == nil {
				r.mu.RLock()
				sessions = append([]protocol.Session(nil), r.last[s.Kind()]...)
				r.mu.RUnlock()
			} else {
				// A non-nil slice alongside an error is an adapter-owned partial
				// snapshot. OpenCode uses this to merge the last known routes from
				// one failed local server with fresh data from the others.
				r.mu.Lock()
				r.last[s.Kind()] = append([]protocol.Session(nil), sessions...)
				r.mu.Unlock()
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", s.Kind(), err))
			}
			all = append(all, sessions...)
		}(s)
	}
	wg.Wait()

	SortSessions(all)

	if len(failures) > 0 {
		sort.Strings(failures)
		return all, fmt.Errorf("source: %s", strings.Join(failures, "; "))
	}
	return all, nil
}

// SortSessions puts the list in the order the app's agent screen relies on:
// sessions blocked on the user first (they are the ones going nowhere without
// attention), then busy ones, then everything else by recency.
func SortSessions(sessions []protocol.Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		if pi, pj := statePriority(sessions[i].State), statePriority(sessions[j].State); pi != pj {
			return pi < pj
		}
		return sessions[i].LastActivityAt > sessions[j].LastActivityAt
	})
}

func statePriority(s protocol.State) int {
	switch s {
	case protocol.StateWaitingInput:
		return 0
	case protocol.StateBusy:
		return 1
	case protocol.StateIdle:
		return 2
	default:
		return 3
	}
}

// Page routes a scrollback request to the owning adapter.
func (r *Registry) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	if err := ValidatePageLimit(limit); err != nil {
		return protocol.Page{}, err
	}
	s, err := r.forSession(sessionID)
	if err != nil {
		return protocol.Page{}, err
	}
	return s.Page(ctx, sessionID, before, limit)
}

// Follow routes a live-tail request to the owning adapter.
func (r *Registry) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	s, err := r.forSession(sessionID)
	if err != nil {
		return err
	}
	return s.Follow(ctx, sessionID, out)
}

// Inject routes a message to the owning adapter, reporting InjectNone when
// that adapter has no delivery channel.
func (r *Registry) Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return protocol.InjectNone, err
	}
	injector, ok := s.(Injector)
	if !ok {
		return protocol.InjectNone, fmt.Errorf("source: %s sessions cannot receive messages yet", s.Kind())
	}
	return injector.Inject(ctx, sessionID, text)
}

// Answerer is implemented by adapters that can resolve a pending question.
type Answerer interface {
	Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error
}

// QuestionInspector is implemented by terminal adapters that can cheaply
// re-read one live pane. It lets a Stop hook distinguish a real completion
// from an agent that stopped only to ask the user something, without waiting
// for an unrelated slow adapter in the next whole-registry discovery sweep.
type QuestionInspector interface {
	CurrentQuestion(ctx context.Context, sessionID string) (*protocol.Question, error)
}

// Interrupter is implemented by adapters that can stop an active turn.
type Interrupter interface {
	Interrupt(ctx context.Context, sessionID string) error
}

// LaunchCursor starts a new managed Cursor conversation through the adapter's
// structured streaming channel. Terminal-launched chats stay on tmux.
func (r *Registry) LaunchCursor(ctx context.Context, cwd, prompt string) (string, error) {
	r.mu.RLock()
	s := r.sources[protocol.KindCursorCLI]
	r.mu.RUnlock()
	launcher, ok := s.(interface {
		Launch(context.Context, string, string) (string, error)
	})
	if !ok {
		return "", fmt.Errorf("source: managed Cursor launch is unavailable")
	}
	return launcher.Launch(ctx, cwd, prompt)
}

// ResumeQueued starts durable managed-agent follow-ups after daemon startup.
func (r *Registry) ResumeQueued() {
	r.mu.RLock()
	s := r.sources[protocol.KindCursorCLI]
	r.mu.RUnlock()
	if resumable, ok := s.(interface{ ResumeQueued() }); ok {
		resumable.ResumeQueued()
	}
}

// EnableAsyncCursor makes daemon sends acknowledge once ACP accepts the turn.
// Standalone CLI sends remain attached until their child process finishes.
func (r *Registry) EnableAsyncCursor() {
	r.mu.RLock()
	s := r.sources[protocol.KindCursorCLI]
	r.mu.RUnlock()
	if async, ok := s.(interface{ EnableAsync() }); ok {
		async.EnableAsync()
	}
}

func (r *Registry) Close() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.sources {
		if closer, ok := s.(interface{ Close() }); ok {
			closer.Close()
		}
	}
}

// Answer routes a decision to the adapter owning the session.
func (r *Registry) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	s, err := r.forSession(sessionID)
	if err != nil {
		return err
	}
	answerer, ok := s.(Answerer)
	if !ok {
		return fmt.Errorf("source: %s questions cannot be answered remotely", s.Kind())
	}
	return answerer.Answer(ctx, sessionID, answer)
}

// CurrentQuestion performs a targeted live check when the owning adapter
// supports one. A nil question with a nil error means the pane is not blocked.
func (r *Registry) CurrentQuestion(
	ctx context.Context,
	sessionID string,
) (*protocol.Question, error) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return nil, err
	}
	inspector, ok := s.(QuestionInspector)
	if !ok {
		return nil, nil
	}
	return inspector.CurrentQuestion(ctx, sessionID)
}

// Interrupt routes cancellation to the adapter owning the session.
func (r *Registry) Interrupt(ctx context.Context, sessionID string) error {
	s, err := r.forSession(sessionID)
	if err != nil {
		return err
	}
	interrupter, ok := s.(Interrupter)
	if !ok {
		return fmt.Errorf("source: %s sessions cannot be interrupted remotely", s.Kind())
	}
	return interrupter.Interrupt(ctx, sessionID)
}

// ArtifactSource is implemented by adapters whose agent writes documents for
// the user apart from the conversation: Antigravity's implementation plan,
// task list and walkthrough, a screenshot it took, Kiro's specs.
//
// Names are the adapter's to mint and to confine. The daemon refuses a name
// from the phone that holds a separator or "..", but only the adapter knows
// where its artifacts live, so OpenArtifact must open nothing outside that
// place: no subdirectory, and no symlink (check with Lstat, not Stat).
type ArtifactSource interface {
	// Artifacts lists a session's artifacts, newest first.
	Artifacts(ctx context.Context, sessionID string) ([]protocol.Artifact, error)
	// OpenArtifact opens one for reading. The caller closes the file.
	OpenArtifact(ctx context.Context, sessionID, name string) (*os.File, os.FileInfo, error)
	// ReviewArtifact answers the agent's request to approve one, in whatever
	// form the CLI itself takes that answer — usually a message typed into
	// the session. comment is the user's request for changes, and is empty
	// when approving.
	ReviewArtifact(ctx context.Context, sessionID, name string, approve bool, comment string) error
}

// Artifacts lists a session's artifacts. A session whose adapter keeps none
// has an empty list rather than an error, because that is the true answer.
func (r *Registry) Artifacts(ctx context.Context, sessionID string) ([]protocol.Artifact, error) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return nil, err
	}
	artifacts, ok := s.(ArtifactSource)
	if !ok {
		return []protocol.Artifact{}, nil
	}
	return artifacts.Artifacts(ctx, sessionID)
}

// OpenArtifact routes an artifact read to the adapter owning the session.
func (r *Registry) OpenArtifact(ctx context.Context, sessionID, name string) (*os.File, os.FileInfo, error) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return nil, nil, err
	}
	artifacts, ok := s.(ArtifactSource)
	if !ok {
		return nil, nil, fmt.Errorf("source: %s sessions have no artifacts", s.Kind())
	}
	return artifacts.OpenArtifact(ctx, sessionID, name)
}

// ReviewArtifact routes a review to the adapter owning the session.
func (r *Registry) ReviewArtifact(
	ctx context.Context, sessionID, name string, approve bool, comment string,
) error {
	s, err := r.forSession(sessionID)
	if err != nil {
		return err
	}
	artifacts, ok := s.(ArtifactSource)
	if !ok {
		return fmt.Errorf("source: %s sessions have no artifacts to review", s.Kind())
	}
	return artifacts.ReviewArtifact(ctx, sessionID, name, approve, comment)
}

// AttachmentPlacer is implemented by adapters whose agent reads a user's
// images from a place of its own rather than from wherever they were saved.
// Antigravity, for one, keeps them in its conversation's .user_uploaded
// directory.
type AttachmentPlacer interface {
	// PlaceAttachment returns the path to give the agent for an image saved
	// at path: usually a copy made where the agent looks. On an error the
	// daemon gives the agent the original path instead, so a failure here
	// costs the agent's preferred form and never the image.
	PlaceAttachment(ctx context.Context, sessionID, path string) (string, error)
}

// PlaceAttachment maps one saved image to the path the session's agent should
// be given. An adapter with no preference gets the path unchanged.
func (r *Registry) PlaceAttachment(ctx context.Context, sessionID, path string) (string, error) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return path, err
	}
	placer, ok := s.(AttachmentPlacer)
	if !ok {
		return path, nil
	}
	return placer.PlaceAttachment(ctx, sessionID, path)
}

// AttachmentInjector is implemented by adapters that can hand images to the
// agent as parts of one structured message rather than as paths typed into a
// prompt: Cursor over ACP sends image blocks. The daemon prefers it to typing
// paths, and does not ask an adapter that delivers this way to place them.
type AttachmentInjector interface {
	// InjectWithAttachments delivers text and the images saved at paths
	// together. A session with no structured channel — a terminal chat of
	// the same agent — returns ErrAttachmentsAsPaths, and the daemon types
	// the paths into it instead.
	InjectWithAttachments(ctx context.Context, sessionID, text string, paths []string) (protocol.InjectMode, error)
}

// ErrAttachmentsAsPaths is what an AttachmentInjector returns for a session
// it cannot send structured images to, having delivered nothing.
var ErrAttachmentsAsPaths = errors.New("source: this session takes images as paths")

// InjectWithAttachments delivers a message with images through the owning
// adapter's structured channel. handled is false when there is none — the
// adapter does not implement AttachmentInjector, or declined this session
// with ErrAttachmentsAsPaths — and the caller types the paths instead.
func (r *Registry) InjectWithAttachments(
	ctx context.Context, sessionID, text string, paths []string,
) (mode protocol.InjectMode, handled bool, err error) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return protocol.InjectNone, false, err
	}
	injector, ok := s.(AttachmentInjector)
	if !ok {
		return protocol.InjectNone, false, nil
	}
	mode, err = injector.InjectWithAttachments(ctx, sessionID, text, paths)
	if errors.Is(err, ErrAttachmentsAsPaths) {
		return protocol.InjectNone, false, nil
	}
	return mode, true, err
}

// ResumeNamer is implemented by adapters that choose what a reopened session
// is called. Reopening starts the agent in a new tmux pane, and the phone is
// sent to the id discovery will publish for it; an adapter that keys a live
// session on its pane, or puts its own id in the pane's name, is the only
// place that knows what that id will be.
type ResumeNamer interface {
	// ResumedSession names the pane a resume of the native session opens,
	// and the session id discovery will give it. defaultPane is the name the
	// daemon would otherwise use; return it unchanged to keep it. An empty
	// sessionID keeps the daemon's own naming for both.
	//
	// A pane must start with tmux.Prefix and hold only letters, digits, "-"
	// and "_", or the daemon ignores the answer.
	ResumedSession(native, defaultPane string) (pane, sessionID string)
}

// ResumedSession asks the adapter owning sessionID how to name its resume. Both
// results are empty when the adapter leaves naming to the daemon.
func (r *Registry) ResumedSession(sessionID, native, defaultPane string) (pane, id string) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return "", ""
	}
	namer, ok := s.(ResumeNamer)
	if !ok {
		return "", ""
	}
	return namer.ResumedSession(native, defaultPane)
}

func (r *Registry) forSession(sessionID string) (Source, error) {
	kind, _, ok := strings.Cut(sessionID, ":")
	if !ok {
		return nil, fmt.Errorf("source: malformed session id %q", sessionID)
	}
	r.mu.RLock()
	s, exists := r.sources[protocol.Kind(kind)]
	r.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("source: no adapter for %q", kind)
	}
	return s, nil
}

// EachSource calls fn for every registered adapter.
//
// Used to hand shared state (such as the pending-message queue) to whichever
// adapters support it, without the registry needing to know which those are.
func (r *Registry) EachSource(fn func(Source)) {
	r.mu.RLock()
	sources := make([]Source, 0, len(r.sources))
	for _, s := range r.sources {
		sources = append(sources, s)
	}
	r.mu.RUnlock()
	for _, s := range sources {
		fn(s)
	}
}
