package daemon

import (
	"context"
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Bounds on an artifact list. The app accepts a little more of each, so a
// list the daemon sends is never one the app refuses.
const (
	maxWireArtifacts       = 200
	maxArtifactNameBytes   = 255
	maxWireArtifactKind    = 64
	maxWireArtifactTitle   = 2 << 10
	maxWireArtifactSummary = 4 << 10
	maxWireMIMEBytes       = 128
)

// validArtifactName accepts only a plain file name.
//
// The name comes back from the phone, and the adapter turns it into a file to
// open. Each adapter confines it to its own store as well, but nothing that
// could name another place should reach one: no separator of either kind, no
// parent reference anywhere in it, and nothing a terminal would interpret.
func validArtifactName(name string) bool {
	if name == "" || len(name) > maxArtifactNameBytes || !utf8.ValidString(name) ||
		strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

// listArtifacts answers ReqListArtifacts.
func (d *Daemon) listArtifacts(ctx context.Context, req protocol.Request) protocol.Event {
	artifacts, err := d.registry.Artifacts(ctx, req.SessionID)
	if err != nil {
		return workspaceError(req, err.Error())
	}
	return protocol.Event{
		Type: protocol.EvtArtifacts, SessionID: req.SessionID, Artifacts: fitArtifacts(artifacts),
	}
}

// fitArtifacts bounds an adapter's list for the wire.
//
// An artifact with a name the phone could not send back is left out rather
// than renamed: it could be neither opened nor reviewed, and a renamed one
// would open something else. Always non-nil, so a session with none is sent
// as [] rather than as no answer.
func fitArtifacts(artifacts []protocol.Artifact) []protocol.Artifact {
	fitted := make([]protocol.Artifact, 0, min(len(artifacts), maxWireArtifacts))
	for _, artifact := range artifacts {
		if len(fitted) == maxWireArtifacts {
			break
		}
		if !validArtifactName(artifact.Name) {
			continue
		}
		artifact.Kind = truncateUTF8(strings.TrimSpace(artifact.Kind), maxWireArtifactKind, "")
		if artifact.Kind == "" {
			artifact.Kind = "file"
		}
		artifact.Title = truncateUTF8(artifact.Title, maxWireArtifactTitle, "…")
		artifact.Summary = truncateUTF8(artifact.Summary, maxWireArtifactSummary, "…")
		if len(artifact.MIME) > maxWireMIMEBytes {
			artifact.MIME = ""
		}
		artifact.UpdatedAt = max(artifact.UpdatedAt, 0)
		artifact.Size = max(artifact.Size, 0)
		fitted = append(fitted, artifact)
	}
	return fitted
}

// readArtifact answers ReqReadArtifact with the same view a workspace file
// gets: text up to the same ceiling, or a preview of an image with the same
// downscaling. A recording or anything else binary is refused, as it is there.
func (d *Daemon) readArtifact(ctx context.Context, req protocol.Request) protocol.Event {
	file, info, err := d.registry.OpenArtifact(ctx, req.SessionID, req.Path)
	if err != nil {
		return workspaceError(req, err.Error())
	}
	defer file.Close()
	if info == nil {
		if info, err = file.Stat(); err != nil {
			return workspaceError(req, "that artifact could not be read")
		}
	}
	result := &protocol.WorkspaceResult{Kind: "artifact", SessionID: req.SessionID, Path: req.Path}
	result.Text, result.Image, result.MIME, result.Source, result.Truncated, err = readDisplayable(file, info)
	if err != nil {
		return workspaceError(req, err.Error())
	}
	// Detection reads every text file as plain text, and most artifacts are
	// Markdown; saying so lets the phone render it rather than guess.
	if result.Image == "" && strings.EqualFold(path.Ext(req.Path), ".md") {
		result.MIME = "text/markdown"
	}
	return protocol.Event{Type: protocol.EvtWorkspace, Workspace: result}
}

// reviewArtifact answers ReqReviewArtifact.
//
// It types into the session like a send, so it is held to the send's rules:
// it runs under the session's action lock, in arrival order with the other
// mutations, and never while a question is pending, where the text would
// land in the menu instead of the prompt.
func (d *Daemon) reviewArtifact(ctx context.Context, req protocol.Request) protocol.Event {
	result := protocol.Event{
		Type: protocol.EvtSendResult, SessionID: req.SessionID,
		ClientID: req.ClientID, Status: protocol.StatusDelivered,
	}
	d.mu.Lock()
	current, known := d.sessions[req.SessionID]
	d.mu.Unlock()
	var err error
	if known && current.Question != nil {
		err = errReviewDuringQuestion
	} else {
		err = d.registry.ReviewArtifact(ctx, req.SessionID, req.Path, req.Approve, strings.TrimSpace(req.Text))
	}
	if err != nil {
		result.Status = protocol.StatusFailed
		result.Error = err.Error()
	}
	return result
}

var errReviewDuringQuestion = errors.New("daemon: answer the pending question before reviewing")
