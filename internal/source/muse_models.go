package source

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Switching a Muse session's model from the apps: the choices are Muse's
// catalog, and a switch is session/setModel, which changes that session
// alone. Muse lets only a session's driver switch it, so Agentman takes the
// session for the switch and lets it go after, as it does a send; a session
// a terminal drives offers no choices.
//
// Muse's shared daemon answers model/list from the catalog bundled with Muse
// (one model on a contributor account), while Muse in a terminal offers the
// provider's live catalog (muse-wonder-1.0, muse-spark-1.3, muse-spark-1.2).
// When the daemon only has the bundled one, a short-lived Muse host of
// Agentman's own is asked instead: once, in the background, then every half
// hour, so listing agents never waits on it.

// museCatalogFresh is how long a read of Muse's catalog is used: it changes
// when Muse updates or the account does.
const museCatalogFresh = 30 * time.Minute

// museCatalogWait bounds a catalog read, a standalone host's start included.
const museCatalogWait = 30 * time.Second

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
	// fetch reads the catalog; tests replace it.
	fetch func(context.Context) ([]museModel, error)
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
	fetch := s.catalog.fetch
	if fetch == nil {
		fetch = s.fetchDaemonCatalog
	}
	models, err := fetch(ctx)
	s.catalog.mu.Lock()
	defer s.catalog.mu.Unlock()
	s.catalog.fetching = false
	// A failed read waits for the next refresh too; the last list stands.
	s.catalog.read = time.Now()
	if err == nil && len(models) > 0 {
		s.catalog.models = models
	}
}

type museModelList struct {
	Models []museModel `json:"models"`
	Source string      `json:"source"`
}

// fetchDaemonCatalog asks the daemon alone.
func (s *MuseSource) fetchDaemonCatalog(ctx context.Context) ([]museModel, error) {
	var list museModelList
	err := s.host.call(ctx, "model/list", map[string]any{}, &list)
	return list.Models, err
}

// fetchCatalog asks the daemon, and a standalone host when the daemon only
// has Muse's bundled catalog. Only the real adapter uses it (NewMuseSource),
// so tests never start Muse.
func (s *MuseSource) fetchCatalog(ctx context.Context) ([]museModel, error) {
	var list museModelList
	err := s.host.call(ctx, "model/list", map[string]any{}, &list)
	if err == nil && list.Source != "bundledCatalog" && len(list.Models) > 0 {
		return list.Models, nil
	}
	if standalone, standaloneErr := museStandaloneCatalog(ctx); standaloneErr == nil && len(standalone.Models) > 0 {
		return standalone.Models, nil
	}
	return list.Models, err
}

// museStandaloneCatalog starts a Muse host of its own (`muse serve` over
// stdio, no session) just to read model/list, and ends it.
func museStandaloneCatalog(ctx context.Context) (museModelList, error) {
	var list museModelList
	binary, err := MuseBinary()
	if err != nil {
		return list, err
	}
	cmd := exec.CommandContext(ctx, binary, "serve")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return list, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return list, err
	}
	if err := cmd.Start(); err != nil {
		return list, err
	}
	defer func() {
		_ = in.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	send := func(message map[string]any) error {
		line, _ := json.Marshal(message)
		_, err := in.Write(append(line, '\n'))
		return err
	}
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"clientInfo": map[string]any{"name": "agentman", "title": "Agentman", "version": "1"}, "capabilities": map[string]any{}}}); err != nil {
		return list, err
	}
	replies := make(chan museMessage, 4)
	go func() {
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 0, 1<<16), 1<<24)
		for scanner.Scan() {
			var message museMessage
			if json.Unmarshal(scanner.Bytes(), &message) == nil && len(message.ID) > 0 && message.Method == "" {
				replies <- message
			}
		}
		close(replies)
	}()
	wait := func(id string) (museMessage, error) {
		for {
			select {
			case <-ctx.Done():
				return museMessage{}, ctx.Err()
			case message, ok := <-replies:
				if !ok {
					return museMessage{}, io.EOF
				}
				if string(message.ID) == id {
					return message, nil
				}
			}
		}
	}
	if _, err := wait("1"); err != nil {
		return list, err
	}
	_ = send(map[string]any{"jsonrpc": "2.0", "method": "initialized"})
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "model/list", "params": map[string]any{}}); err != nil {
		return list, err
	}
	reply, err := wait("2")
	if err != nil {
		return list, err
	}
	if reply.Error != nil {
		return list, reply.Error
	}
	err = json.Unmarshal(reply.Result, &list)
	return list, err
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
	if len(offered) == 0 {
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
