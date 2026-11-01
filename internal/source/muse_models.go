package source

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Switching a Muse session's model from the apps: the choices are the
// models Muse's daemon serves (model/list), and a switch is session/setModel,
// which changes that session alone. Muse lets only a session's driver switch
// it, so Agentman takes the session for the switch and lets it go after, as
// it does a send; a session a terminal drives offers no choices.
//
// The daemon checks a switch against its own catalog, read at its start from
// the cache Muse's terminal writes (~/.local/share/muse/model-catalog). One
// started before that cache existed serves only the model bundled with Muse
// until it restarts, so the apps offer exactly what it serves: never a model
// it would refuse.

// museCatalogFresh is how long a read of Muse's catalog is used: it changes
// when Muse's daemon restarts.
const museCatalogFresh = 5 * time.Minute

// museCatalogWait bounds a catalog read.
const museCatalogWait = 10 * time.Second

type museModel struct {
	ModelID    string  `json:"modelId"`
	ProviderID string  `json:"providerId"`
	ProfileID  *string `json:"profileId"`
}

type museCatalog struct {
	mu       sync.Mutex
	models   []museModel
	read     time.Time
	fetching bool
}

// museModels is Muse's catalog as last read, refreshed in the background
// when it is stale. Empty until the first read lands.
func (s *MuseSource) museModels() []museModel {
	s.catalog.mu.Lock()
	defer s.catalog.mu.Unlock()
	if !s.catalog.fetching && (s.catalog.read.IsZero() || time.Since(s.catalog.read) > museCatalogFresh) {
		s.catalog.fetching = true
		go s.refreshCatalog()
	}
	return s.catalog.models
}

func (s *MuseSource) refreshCatalog() {
	ctx, cancel := context.WithTimeout(context.Background(), museCatalogWait)
	defer cancel()
	var list struct {
		Models []museModel `json:"models"`
	}
	err := s.host.call(ctx, "model/list", map[string]any{}, &list)
	models := list.Models
	s.catalog.mu.Lock()
	defer s.catalog.mu.Unlock()
	s.catalog.fetching = false
	// A failed read waits for the next refresh too; the last list stands.
	s.catalog.read = time.Now()
	if err == nil && len(models) > 0 {
		s.catalog.models = models
	}
}

// museContributor is Muse's suffix for a model run in contributor mode,
// the account setting that lets Meta use the content; the terminal shows
// it as the plain model.
const museContributor = "-contributor"

// offerModels lists the catalog on a session Agentman can drive: the models
// in the session's own mode (contributor or not), as Muse's terminal does.
func (s *MuseSource) offerModels(session *protocol.Session) {
	if session.Inject != protocol.InjectAPI {
		return
	}
	contributor := strings.HasSuffix(session.Model, museContributor)
	var offered []string
	for _, model := range s.museModels() {
		if strings.HasSuffix(model.ModelID, museContributor) == contributor {
			offered = append(offered, model.ModelID)
		}
	}
	// One model is no choice: the apps then show it as a plain label.
	if len(offered) < 2 {
		return
	}
	session.Models = offered
	session.ModelScope = protocol.ModelScopeSession
}

// museSwitchWait bounds the wait for a switch to show in the session.
const museSwitchWait = 3 * time.Second

// SetModel implements ModelSetter: switch the session to one of Muse's
// models and read it back. A turn under way takes it at its next model call.
func (s *MuseSource) SetModel(ctx context.Context, sessionID, model string) error {
	native := strings.TrimPrefix(sessionID, musePrefix)
	if s.openInTerminal()[native] && !s.holding(native) {
		return errMuseInTerminal
	}
	var chosen *museModel
	for _, entry := range s.museModels() {
		if entry.ModelID == model {
			chosen = &entry
			break
		}
	}
	if chosen == nil {
		return fmt.Errorf("source: Muse doesn't offer %s", model)
	}
	if err := s.take(ctx, native); err != nil {
		return err
	}
	selection := map[string]any{"modelId": chosen.ModelID, "providerId": chosen.ProviderID}
	if chosen.ProfileID != nil {
		selection["profileId"] = *chosen.ProfileID
	}
	if err := s.host.call(ctx, "session/setModel", map[string]any{
		"commandId": museCommandID(), "sessionId": native, "model": selection,
	}, nil); err != nil {
		if museInUse(err) {
			return errMuseInTerminal
		}
		return err
	}
	deadline := time.Now().Add(museSwitchWait)
	for {
		var read museRead
		if err := s.host.call(ctx, "session/read", map[string]any{"sessionId": native, "excludeItems": true}, &read); err == nil &&
			read.Session.ModelID == model {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("source: Muse did not switch to %s", model)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}
