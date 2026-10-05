package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// agy's artifacts are the documents it writes for the user rather than for the
// project: an implementation plan, a task list, a walkthrough, and the images
// and recordings it makes. They live at the top of the conversation's brain
// folder, each beside a metadata file (agy 1.2.17):
//
//	brain/<conversation>/implementation_plan.md
//	brain/<conversation>/implementation_plan.md.metadata.json
//	  {"summary": "…", "updatedAt": "2026-10-05T09:36:54.945482Z",
//	   "requestFeedback": true, "userFacing": true}
//
// requestFeedback is the agent asking the user to approve the document. agy's
// review panel answers by sending the conversation a message —
// "[Approved] implementation_plan.md" or "[Rejected] implementation_plan.md" —
// so an artifact still waits for review until such a message is newer than
// it. The Antigravity IDE writes the same files with an artifactType and
// numbered .resolved copies of each version; those copies are not artifacts.

var _ ArtifactSource = (*AntigravitySource)(nil)

// antigravityReviewPrefixes open the message a review sends.
var antigravityReviewPrefixes = []string{"[Approved] ", "[Rejected] "}

// antigravityArtifactMeta is one metadata file, cached by its mtime.
type antigravityArtifactMeta struct {
	mtime           time.Time
	summary         string
	updatedAt       int64
	requestFeedback bool
	userFacing      *bool
	kind            string
	title           string
	titleFrom       time.Time
}

// antigravityReviews is what a transcript says about reviews: when each
// artifact was last approved or rejected. Read incrementally, since a
// transcript only grows.
type antigravityReviews struct {
	offset int64
	latest map[string]int64
}

type antigravityArtifactCache struct {
	mu      sync.Mutex
	meta    map[string]antigravityArtifactMeta
	reviews map[string]*antigravityReviews
}

// Artifacts implements ArtifactSource.
func (s *AntigravitySource) Artifacts(ctx context.Context, sessionID string) ([]protocol.Artifact, error) {
	session, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	return s.conversationArtifacts(ctx, session.meta.NativeID, session.transcript)
}

func (s *AntigravitySource) conversationArtifacts(
	ctx context.Context, conversation, transcript string,
) ([]protocol.Artifact, error) {
	if !isUUID(conversation) {
		return []protocol.Artifact{}, nil
	}
	dir := filepath.Join(s.root(), "brain", conversation)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []protocol.Artifact{}, nil
	}
	if err != nil {
		return nil, err
	}
	reviewed := s.artifactReviews(transcript)
	found := make([]protocol.Artifact, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if !isAntigravityArtifactName(name) || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		meta, hasMeta := s.artifactMeta(dir, name, info)
		kind := antigravityArtifactKind(name, meta.kind)
		if !hasMeta && kind != "image" && kind != "video" {
			// A document without agy's metadata is not one it wrote for the
			// user. Pictures and recordings are kept regardless.
			continue
		}
		if meta.userFacing != nil && !*meta.userFacing {
			continue
		}
		updated := meta.updatedAt
		if updated == 0 {
			updated = info.ModTime().UnixMilli()
		}
		found = append(found, protocol.Artifact{
			Name:      name,
			Kind:      kind,
			Title:     meta.title,
			Summary:   meta.summary,
			UpdatedAt: updated,
			Size:      info.Size(),
			MIME:      antigravityArtifactMIME(name),
			Review:    meta.requestFeedback && reviewed[name] < updated,
		})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].UpdatedAt > found[j].UpdatedAt })
	return found, nil
}

// isAntigravityArtifactName rules out what sits beside artifacts without
// being one: metadata, the IDE's version copies, and hidden folders' names.
func isAntigravityArtifactName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	if strings.HasSuffix(name, ".metadata.json") || strings.Contains(name, ".resolved") {
		return false
	}
	return true
}

func antigravityArtifactKind(name, declared string) string {
	switch declared {
	case "ARTIFACT_TYPE_IMPLEMENTATION_PLAN":
		return "plan"
	case "ARTIFACT_TYPE_TASK":
		return "task"
	case "ARTIFACT_TYPE_WALKTHROUGH":
		return "walkthrough"
	}
	lower := strings.ToLower(name)
	switch filepath.Ext(lower) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return "image"
	case ".webm", ".mp4", ".mov":
		return "video"
	}
	switch {
	case strings.Contains(lower, "plan"):
		return "plan"
	case strings.HasPrefix(lower, "task"):
		return "task"
	case strings.Contains(lower, "walkthrough"):
		return "walkthrough"
	}
	return "file"
}

func antigravityArtifactMIME(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md":
		return "text/markdown"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".webm":
		return "video/webm"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".txt":
		return "text/plain"
	}
	return ""
}

// artifactMeta reads name's metadata file, and its title from the document's
// first heading, each re-read only when its file changes.
func (s *AntigravitySource) artifactMeta(dir, name string, info os.FileInfo) (antigravityArtifactMeta, bool) {
	metaPath := filepath.Join(dir, name+".metadata.json")
	metaInfo, err := os.Lstat(metaPath)
	hasMeta := err == nil && metaInfo.Mode().IsRegular()
	key := filepath.Join(dir, name)

	s.artifacts.mu.Lock()
	defer s.artifacts.mu.Unlock()
	if s.artifacts.meta == nil {
		s.artifacts.meta = map[string]antigravityArtifactMeta{}
	}
	meta, cached := s.artifacts.meta[key]
	if hasMeta && (!cached || !meta.mtime.Equal(metaInfo.ModTime())) {
		meta = antigravityArtifactMeta{mtime: metaInfo.ModTime(), title: meta.title, titleFrom: meta.titleFrom}
		if raw, err := readBoundedFile(metaPath, 64<<10); err == nil {
			var fields struct {
				Summary         string `json:"summary"`
				UpdatedAt       string `json:"updatedAt"`
				RequestFeedback bool   `json:"requestFeedback"`
				UserFacing      *bool  `json:"userFacing"`
				ArtifactType    string `json:"artifactType"`
			}
			if json.Unmarshal(raw, &fields) == nil {
				meta.summary = strings.TrimSpace(fields.Summary)
				if t, err := time.Parse(time.RFC3339Nano, fields.UpdatedAt); err == nil {
					meta.updatedAt = t.UnixMilli()
				}
				meta.requestFeedback = fields.RequestFeedback
				meta.userFacing = fields.UserFacing
				meta.kind = fields.ArtifactType
			}
		}
	}
	if strings.EqualFold(filepath.Ext(name), ".md") && !meta.titleFrom.Equal(info.ModTime()) {
		meta.title = antigravityArtifactTitle(key)
		meta.titleFrom = info.ModTime()
	}
	s.artifacts.meta[key] = meta
	return meta, hasMeta
}

// antigravityArtifactTitle is a markdown document's first heading:
// "# Implementation Plan: Add README.md".
func antigravityArtifactTitle(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, 8<<10))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if heading, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(heading)
		}
	}
	return ""
}

// artifactReviews returns, for each artifact name, when the conversation last
// approved or rejected it, reading only what the transcript gained since the
// last call.
func (s *AntigravitySource) artifactReviews(transcript string) map[string]int64 {
	if transcript == "" {
		return nil
	}
	info, err := os.Stat(transcript)
	if err != nil {
		return nil
	}
	s.artifacts.mu.Lock()
	defer s.artifacts.mu.Unlock()
	if s.artifacts.reviews == nil {
		s.artifacts.reviews = map[string]*antigravityReviews{}
	}
	state := s.artifacts.reviews[transcript]
	if state == nil || info.Size() < state.offset {
		state = &antigravityReviews{latest: map[string]int64{}}
		s.artifacts.reviews[transcript] = state
	}
	if info.Size() == state.offset {
		return state.latest
	}
	file, err := os.Open(transcript)
	if err != nil {
		return state.latest
	}
	defer file.Close()
	if _, err := file.Seek(state.offset, io.SeekStart); err != nil {
		return state.latest
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	offset := state.offset
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			break // an incomplete last line is read again next time
		}
		offset += int64(len(line))
		if !bytes.Contains(line, []byte(`"USER_INPUT"`)) ||
			(!bytes.Contains(line, []byte("[Approved] ")) && !bytes.Contains(line, []byte("[Rejected] "))) {
			continue
		}
		var step struct {
			Type      string `json:"type"`
			CreatedAt string `json:"created_at"`
			Content   string `json:"content"`
		}
		if json.Unmarshal(line, &step) != nil || step.Type != "USER_INPUT" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, step.CreatedAt)
		if err != nil {
			continue
		}
		request := parser.AntigravityRequest(step.Content)
		first, _, _ := strings.Cut(request, "\n")
		for _, prefix := range antigravityReviewPrefixes {
			if name, ok := strings.CutPrefix(strings.TrimSpace(first), prefix); ok {
				state.latest[strings.TrimSpace(name)] = max(state.latest[strings.TrimSpace(name)], at.UnixMilli())
			}
		}
	}
	state.offset = offset
	return state.latest
}

// OpenArtifact implements ArtifactSource. Only a top-level regular file in the
// conversation's brain folder, never a link or anything beneath it.
func (s *AntigravitySource) OpenArtifact(ctx context.Context, sessionID, name string) (*os.File, os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	path, err := s.artifactPath(sessionID, name)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("source: that artifact is not a file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	// The name could have been swapped for a link between the check and the
	// open; the file actually opened must be the one that was checked.
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		file.Close()
		return nil, nil, errors.New("source: that artifact changed while it was being opened")
	}
	return file, opened, nil
}

func (s *AntigravitySource) artifactPath(sessionID, name string) (string, error) {
	if !isAntigravityArtifactName(name) || name != filepath.Base(name) {
		return "", fmt.Errorf("source: %q is not an artifact name", name)
	}
	session, err := s.session(sessionID)
	if err != nil {
		return "", err
	}
	if !isUUID(session.meta.NativeID) {
		return "", errors.New("source: this session has no artifacts yet")
	}
	return filepath.Join(s.root(), "brain", session.meta.NativeID, name), nil
}

// ReviewArtifact implements ArtifactSource, sending the message agy's own
// review panel sends.
func (s *AntigravitySource) ReviewArtifact(
	ctx context.Context, sessionID, name string, approve bool, comment string,
) error {
	path, err := s.artifactPath(sessionID, name)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("source: %s is no longer there", name)
	}
	text := "[Approved] " + name
	if !approve {
		text = "[Rejected] " + name
		if comment = strings.TrimSpace(comment); comment != "" {
			text += "\n\n" + comment
		}
	}
	_, err = s.Inject(ctx, sessionID, text)
	return err
}

// forgetArtifacts drops cached reviews and metadata for conversations no
// longer live.
func (s *AntigravitySource) forgetArtifacts(transcripts map[string]bool, conversations map[string]bool) {
	s.artifacts.mu.Lock()
	defer s.artifacts.mu.Unlock()
	for path := range s.artifacts.reviews {
		if !transcripts[path] {
			delete(s.artifacts.reviews, path)
		}
	}
	for path := range s.artifacts.meta {
		if !conversations[filepath.Base(filepath.Dir(path))] {
			delete(s.artifacts.meta, path)
		}
	}
}
