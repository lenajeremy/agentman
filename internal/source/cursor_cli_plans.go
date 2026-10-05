package source

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// A plan Cursor writes is a document of its own, kept apart from the chat:
// ~/.cursor/plans/<name>-<chat id prefix>.plan.md, starting with a comment
// that names the whole chat id ("<!-- 305d21db-… -->"). Plan mode and ACP's
// create_plan both write there.
//
// The phone lists a chat's plans as artifacts so they can be read as
// documents rather than in a question's detail box. Approving one stays with
// the question Cursor shows ("Ready to build?"): that is what the CLI waits
// on, and it is what rings the phone. The daemon never asks for a review
// while a question is pending, so plans are listed without a review request.

const (
	cursorPlanSuffix = ".plan.md"
	// cursorPlanHead is how much of a plan is read to name it.
	cursorPlanHead = 8 << 10
	// cursorMaxPlans bounds one directory listing.
	cursorMaxPlans = 2000
)

var cursorPlanOwner = regexp.MustCompile(`^<!--\s*([A-Za-z0-9_-]{1,128})\s*-->$`)

type cursorPlan struct {
	name    string
	chat    string
	title   string
	summary string
	size    int64
	mod     time.Time
}

type cursorPlanHeadEntry struct {
	size int64
	mod  time.Time
	plan cursorPlan
}

func (s *CursorCLISource) plansDir() string {
	return filepath.Join(s.home, ".cursor", "plans")
}

// cursorPlans lists every plan, reading each file's head once per change.
func (s *CursorCLISource) cursorPlans() []cursorPlan {
	entries, err := os.ReadDir(s.plansDir())
	if err != nil {
		return nil
	}
	plans := make([]cursorPlan, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if len(plans) == cursorMaxPlans {
			break
		}
		name := entry.Name()
		// Regular files only: a symlink or directory named like a plan is
		// not one Cursor wrote.
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, cursorPlanSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		seen[name] = true
		s.turnMu.Lock()
		cached, ok := s.planHeads[name]
		s.turnMu.Unlock()
		if !ok || cached.size != info.Size() || !cached.mod.Equal(info.ModTime()) {
			plan, ok := readCursorPlanHead(filepath.Join(s.plansDir(), name), name, info)
			if !ok {
				continue
			}
			cached = cursorPlanHeadEntry{size: info.Size(), mod: info.ModTime(), plan: plan}
			s.turnMu.Lock()
			if s.planHeads == nil {
				s.planHeads = map[string]cursorPlanHeadEntry{}
			}
			s.planHeads[name] = cached
			s.turnMu.Unlock()
		}
		plans = append(plans, cached.plan)
	}
	s.turnMu.Lock()
	for name := range s.planHeads {
		if !seen[name] {
			delete(s.planHeads, name)
		}
	}
	s.turnMu.Unlock()
	sort.Slice(plans, func(i, j int) bool { return plans[i].mod.After(plans[j].mod) })
	return plans
}

func readCursorPlanHead(path, name string, info fs.FileInfo) (cursorPlan, bool) {
	file, err := os.Open(path)
	if err != nil {
		return cursorPlan{}, false
	}
	defer file.Close()
	head, err := io.ReadAll(io.LimitReader(file, cursorPlanHead))
	if err != nil {
		return cursorPlan{}, false
	}
	lines := strings.Split(string(head), "\n")
	match := cursorPlanOwner.FindStringSubmatch(strings.TrimSpace(lines[0]))
	if match == nil {
		return cursorPlan{}, false
	}
	plan := cursorPlan{name: name, chat: match[1], size: info.Size(), mod: info.ModTime(),
		title: strings.TrimSuffix(name, cursorPlanSuffix)}
	inBody := false
	for _, line := range lines[1:] {
		text := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(text, "# ") && !inBody:
			plan.title = clipRunes(strings.TrimSpace(strings.TrimPrefix(text, "# ")), 200)
			inBody = true
		case text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, "<!--"):
		default:
			plan.summary = clipRunes(text, 400)
			return plan, true
		}
	}
	return plan, true
}

// planChat is the chat whose plans a session shows.
func (s *CursorCLIGroup) planChat(sessionID string) string {
	var chat string
	switch {
	case strings.HasPrefix(sessionID, cursorACPPrefix):
		chat = strings.TrimPrefix(sessionID, cursorACPPrefix)
	case strings.HasPrefix(sessionID, cursorCLIChatPrefix):
		chat = strings.TrimPrefix(sessionID, cursorCLIChatPrefix)
	default:
		s.terminal.mu.RLock()
		session, ok := s.terminal.sessions[sessionID]
		s.terminal.mu.RUnlock()
		// A pane with no chat yet is named after the pane, and has no plans.
		if ok && session.store != "" {
			chat = session.meta.NativeID
		}
	}
	if !cursorCLIValidChatID(chat) {
		return ""
	}
	return chat
}

// Artifacts implements ArtifactSource: the chat's plans, newest first.
func (s *CursorCLIGroup) Artifacts(_ context.Context, sessionID string) ([]protocol.Artifact, error) {
	chat := s.planChat(sessionID)
	artifacts := []protocol.Artifact{}
	if chat == "" {
		return artifacts, nil
	}
	for _, plan := range s.terminal.cursorPlans() {
		if plan.chat != chat {
			continue
		}
		artifacts = append(artifacts, protocol.Artifact{
			Name: plan.name, Kind: "plan", Title: plan.title, Summary: plan.summary,
			UpdatedAt: plan.mod.UnixMilli(), Size: plan.size, MIME: "text/markdown",
		})
	}
	return artifacts, nil
}

// OpenArtifact implements ArtifactSource. Only a plan of this chat opens, and
// only as a regular file directly in Cursor's plans directory.
func (s *CursorCLIGroup) OpenArtifact(ctx context.Context, sessionID, name string) (*os.File, os.FileInfo, error) {
	artifacts, _ := s.Artifacts(ctx, sessionID)
	found := false
	for _, artifact := range artifacts {
		found = found || artifact.Name == name
	}
	if !found || filepath.Base(name) != name {
		return nil, nil, errors.New("source: that plan is not one of this chat's")
	}
	root, err := os.OpenRoot(s.terminal.plansDir())
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("source: that plan is not a regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		file.Close()
		return nil, nil, errors.New("source: that plan changed while it was opened")
	}
	return file, opened, nil
}

// ReviewArtifact implements ArtifactSource. Cursor takes a plan's approval
// only at its "Ready to build?" question, which the phone answers directly.
func (s *CursorCLIGroup) ReviewArtifact(context.Context, string, string, bool, string) error {
	return errors.New("source: build or revise a Cursor plan from the question it asks")
}

// countPlans fills in how many plans each discovered session has.
func (s *CursorCLIGroup) countPlans(sessions []protocol.Session) {
	plans := s.terminal.cursorPlans()
	if len(plans) == 0 {
		return
	}
	counts := make(map[string]int, len(plans))
	for _, plan := range plans {
		counts[plan.chat]++
	}
	for i := range sessions {
		// A pane with no chat yet carries the pane's name, which no plan has.
		sessions[i].Artifacts = counts[sessions[i].NativeID]
	}
}
