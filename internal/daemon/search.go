package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/search"
	"github.com/lenajeremy/agentman/internal/source"
)

const (
	// maxSearchQuery bounds a query. Nobody types more into a search box,
	// and every word in it is looked up.
	maxSearchQuery = 200
	// searchDelay leaves the daemon's start to discovery and the apps; the
	// first sweep reads every transcript, and can wait a few seconds.
	searchDelay = 10 * time.Second
	// searchInterval is how often conversations are read for what changed.
	// A search reads the running ones first anyway; this catches the rest.
	searchInterval = 2 * time.Minute
)

// errSearchStarting answers a search asked before the index is open.
var errSearchStarting = errors.New("daemon: search is starting, try again in a moment")

// SetSearchFile keeps the search index at path. Without one, ReqSearch is
// answered with an error.
func (d *Daemon) SetSearchFile(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.searchPath = path
}

// runSearch opens the index and keeps it current until ctx ends.
func (d *Daemon) runSearch(ctx context.Context) {
	d.mu.Lock()
	path := d.searchPath
	d.mu.Unlock()
	if path == "" {
		return
	}
	index, err := search.Open(path)
	if err != nil {
		Logf("search: %v", err)
		return
	}
	defer index.Close()
	indexer := search.NewIndexer(index, searchReader{d}, Logf)
	d.mu.Lock()
	d.search = indexer
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.search = nil
		d.mu.Unlock()
	}()
	indexer.Run(ctx, searchDelay, searchInterval)
}

func (d *Daemon) searchIndexer() *search.Indexer {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.search
}

func validateSearch(req protocol.Request) error {
	if req.Text == "" || len(req.Text) > maxSearchQuery || !utf8.ValidString(req.Text) {
		return fmt.Errorf("daemon: a search is 1 to %d characters", maxSearchQuery)
	}
	if len(req.SessionID) > maxSessionIDBytes {
		return fmt.Errorf("daemon: invalid session id")
	}
	return nil
}

// searchEvent answers ReqSearch.
func (d *Daemon) searchEvent(ctx context.Context, req protocol.Request) protocol.Event {
	indexer := d.searchIndexer()
	if indexer == nil {
		return protocol.Event{Type: protocol.EvtError, Error: errSearchStarting.Error()}
	}
	query := search.Query{Text: req.Text, Limit: req.Limit}
	if req.SessionID != "" {
		query.Key = d.searchKey(req.SessionID)
	}
	hits, err := indexer.Search(ctx, query)
	if err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: "daemon: search: " + err.Error()}
	}
	results := protocol.SearchResults{
		Query:     req.Text,
		SessionID: req.SessionID,
		Hits:      make([]protocol.SearchHit, 0, len(hits)),
		Indexing:  indexer.Building(),
	}
	for _, hit := range hits {
		found := protocol.SearchHit{
			MessageID: hit.Message.ID,
			Role:      hit.Message.Role,
			Ts:        hit.Message.Ts,
			Snippet:   hit.Snippet,
			Matches:   hit.Matches,
		}
		if req.SessionID == "" {
			session := d.names.apply(hit.Session)
			found.Session = &session
		}
		results.Hits = append(results.Hits, found)
	}
	return protocol.Event{Type: protocol.EvtSearch, Search: &results}
}

// searchKey is what the index keeps a session under: its agent's own id for
// the conversation, which a live session running in a pane does not carry
// in its id.
func (d *Daemon) searchKey(sessionID string) string {
	d.mu.Lock()
	session, ok := d.sessions[sessionID]
	d.mu.Unlock()
	if ok {
		return search.Key(session)
	}
	return sessionID
}

// reopenFromSearch helps a page of an ended conversation that was found by
// a search but has since been forgotten: adapters remember where a finished
// transcript is only once a listing has named it, and keep the latest
// thousands. Listing the conversation's folder again names it. Reports
// whether there was anything to try.
func (d *Daemon) reopenFromSearch(ctx context.Context, sessionID string) bool {
	indexer := d.searchIndexer()
	if indexer == nil {
		return false
	}
	d.mu.Lock()
	_, running := d.sessions[sessionID]
	d.mu.Unlock()
	if running {
		return false
	}
	session, ok := indexer.Index().Session(ctx, sessionID)
	if !ok || session.Cwd == "" {
		return false
	}
	_, err := d.registry.Past(ctx, session.Cwd, source.MaxPastLimit)
	return err == nil
}

// searchReader is what the index reads conversations through.
type searchReader struct{ d *Daemon }

func (r searchReader) Folders(ctx context.Context) ([]protocol.Folder, error) {
	index, err := r.d.registry.Folders(ctx)
	return index.Recent(0), err
}

func (r searchReader) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	return r.d.registry.Past(ctx, dir, limit)
}

func (r searchReader) Live() []protocol.Session {
	return r.d.snapshot()
}

func (r searchReader) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	return r.d.registry.Page(ctx, sessionID, before, limit)
}
