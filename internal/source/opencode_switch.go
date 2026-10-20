package source

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// OpenCode picks a model per message: prompt_async takes a model as
// {providerID, modelID}, and a session has no model of its own to set (PATCH
// /session takes none). Its own TUI works the same way, keeping the choice on
// its side. So a switch from the phone is remembered here and sent with every
// message after it, and only those messages use it.
//
// The models offered are the ones OpenCode's configured providers carry,
// from GET /config/providers — the providers the user has connected, not the
// several hundred /provider knows about. Each is offered by its own id, the
// form OpenCode reports a session's model in, so the phone can mark the
// current one; an id two providers share is offered as "provider/id".

type openCodeModelRef struct {
	providerID string
	modelID    string
}

type openCodeCatalog struct {
	fetched time.Time
	// names are offered to the phone as Session.Models, in this order.
	names []string
	refs  map[string]openCodeModelRef
}

// openCodeChoice is a model chosen from the phone.
type openCodeChoice struct {
	name string
	ref  openCodeModelRef
	// reported is the model OpenCode reported when the choice was made. If it
	// later reports another one that isn't the choice, the model was switched
	// somewhere else — its own TUI — and the choice gives way.
	reported string
}

// openCodeCatalogTTL is how long a server's model list is reused. Providers
// change when the user connects one; minutes late is fine.
const openCodeCatalogTTL = 5 * time.Minute

type ocConfigProviders struct {
	Providers []struct {
		ID     string `json:"id"`
		Models map[string]struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"models"`
	} `json:"providers"`
}

// openCodeCatalogFrom turns a /config/providers answer into the names offered.
func openCodeCatalogFrom(response ocConfigProviders) *openCodeCatalog {
	type entry struct{ provider, model string }
	var entries []entry
	owners := map[string]int{}
	for _, provider := range response.Providers {
		for key, model := range provider.Models {
			id := model.ID
			if id == "" {
				id = key
			}
			if id == "" || provider.ID == "" || model.Status == "deprecated" {
				continue
			}
			entries = append(entries, entry{provider.ID, id})
			owners[id]++
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].provider != entries[j].provider {
			return entries[i].provider < entries[j].provider
		}
		return entries[i].model < entries[j].model
	})
	catalog := &openCodeCatalog{fetched: time.Now(), refs: map[string]openCodeModelRef{}}
	for _, e := range entries {
		name := e.model
		if owners[e.model] > 1 {
			name = e.provider + "/" + e.model
		}
		if _, taken := catalog.refs[name]; taken {
			continue
		}
		catalog.names = append(catalog.names, name)
		catalog.refs[name] = openCodeModelRef{providerID: e.provider, modelID: e.model}
	}
	return catalog
}

// catalogFor returns a server's model list, fetched at most every few minutes.
func (s *OpenCodeSource) catalogFor(ctx context.Context, base string) (*openCodeCatalog, error) {
	s.switchMu.Lock()
	cached := s.catalogs[base]
	s.switchMu.Unlock()
	if cached != nil && time.Since(cached.fetched) < openCodeCatalogTTL {
		return cached, nil
	}
	var response ocConfigProviders
	if _, err := s.doAt(ctx, base, http.MethodGet, "/config/providers", nil, &response); err != nil {
		if cached != nil {
			return cached, nil // a failed refresh keeps the list it had
		}
		return nil, err
	}
	catalog := openCodeCatalogFrom(response)
	s.switchMu.Lock()
	if s.catalogs == nil {
		s.catalogs = map[string]*openCodeCatalog{}
	}
	s.catalogs[base] = catalog
	s.switchMu.Unlock()
	return catalog, nil
}

// offerModels lists a live session's models and reports the one chosen from
// the phone, if any, as its model. reported is the model OpenCode reports.
func (s *OpenCodeSource) offerModels(ctx context.Context, id string, session *openCodeSession) {
	if catalog, err := s.catalogFor(ctx, session.baseURL); err == nil && len(catalog.names) > 0 {
		session.meta.Models = catalog.names
		session.meta.ModelScope = protocol.ModelScopeSession
	}
	reported := session.meta.Model
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	if s.reported == nil {
		s.reported = map[string]string{}
	}
	choice, ok := s.choices[id]
	if !ok {
		s.reported[id] = reported
		return
	}
	switch {
	case reported == choice.ref.modelID || reported == choice.name:
		// OpenCode now reports the choice itself.
		session.meta.Model = choice.name
	case reported != "" && reported != choice.reported:
		// Switched elsewhere since: that wins.
		delete(s.choices, id)
	default:
		session.meta.Model = choice.name
	}
	s.reported[id] = reported
}

// forgetChoices drops choices for sessions that have gone.
func (s *OpenCodeSource) forgetChoices(live map[string]bool) {
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	for id := range s.choices {
		if !live[id] {
			delete(s.choices, id)
		}
	}
	for id := range s.reported {
		if !live[id] {
			delete(s.reported, id)
		}
	}
}

// SetModel implements ModelSetter: the messages the phone sends to this
// session from now on ask OpenCode for this model.
func (s *OpenCodeSource) SetModel(ctx context.Context, sessionID, model string) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown opencode session %q", sessionID)
	}
	if session.baseURL == "" {
		return fmt.Errorf("source: OpenCode isn't running for this session; open it to switch models")
	}
	catalog, err := s.catalogFor(ctx, session.baseURL)
	if err != nil {
		return err
	}
	ref, ok := catalog.refs[strings.TrimSpace(model)]
	if !ok {
		return fmt.Errorf("source: %q is not one of OpenCode's models", model)
	}
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	if s.choices == nil {
		s.choices = map[string]openCodeChoice{}
	}
	s.choices[sessionID] = openCodeChoice{name: model, ref: ref, reported: s.reported[sessionID]}
	return nil
}

// chosenModel is the model the phone chose for a session, for Inject.
func (s *OpenCodeSource) chosenModel(sessionID string) (openCodeModelRef, bool) {
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	choice, ok := s.choices[sessionID]
	return choice.ref, ok
}
