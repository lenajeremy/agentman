package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Switching a phone-launched chat's mode and model, over ACP.
//
// Both go through Cursor's own ACP methods rather than through anything the
// adapter keeps: session/set_mode for the mode (agent, plan or ask), and
// session/set_config_option with config id "model" for the model. Checked in
// the CLI's ACP module:
//
//   - The mode is the chat's own: it is stored in the session's metadata,
//     and Cursor announces it with current_mode_update. That announcement is
//     the read-back.
//   - The model is not. Every way Cursor sets a model — the TUI's picker and
//     /model, --model at launch, and this config option — ends in its model
//     manager writing selectedModel and hasChangedDefaultModel to
//     ~/.cursor/cli-config.json. A model chosen for one chat becomes the
//     default for every chat after it, which is why its scope is reported as
//     "default". The reply's config options name the model now selected,
//     which is the read-back.
//
// Between turns no ACP process runs, so a switch for an idle chat starts one,
// loads the session (which replays history, quietly, and spends no request),
// switches, and closes it again.

const (
	cursorACPModelConfig = "model"
	// cursorACPSwitchWait bounds how long a switch waits for Cursor to
	// confirm it.
	cursorACPSwitchWait = 5 * time.Second
)

// cursorACPModes is what Cursor's ACP server offers when a response does
// not say: its own mapping takes these three.
var cursorACPDefaultModes = []string{"agent", "plan", "ask"}

// cursorACPModel is one model a chat can be switched to: Cursor's model name,
// which set_config_option takes, and the name it shows.
type cursorACPModel struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// cursorACPSessionState is the part of a session/new or session/load reply
// the adapter keeps.
type cursorACPSessionState struct {
	Modes struct {
		CurrentModeID  string `json:"currentModeId"`
		AvailableModes []struct {
			ID string `json:"id"`
		} `json:"availableModes"`
	} `json:"modes"`
	Models struct {
		CurrentModelID  string `json:"currentModelId"`
		AvailableModels []struct {
			ModelID string `json:"modelId"`
			Name    string `json:"name"`
		} `json:"availableModels"`
	} `json:"models"`
}

// noteSessionState records the modes and models a reply offers.
func (s *CursorACPSource) noteSessionState(st *cursorACPState, state cursorACPSessionState) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if mode := clipRunes(strings.TrimSpace(state.Modes.CurrentModeID), 40); mode != "" {
		st.record.Mode = mode
	}
	var modes []string
	for _, mode := range state.Modes.AvailableModes {
		if id := strings.TrimSpace(mode.ID); id != "" && len(id) <= 64 && len(modes) < 16 {
			modes = append(modes, id)
		}
	}
	if len(modes) > 0 {
		st.record.Modes = modes
	}
	var models []cursorACPModel
	for _, model := range state.Models.AvailableModels {
		id := strings.TrimSpace(model.ModelID)
		// Ids are the parameterized model names this adapter asks for
		// (see newCursorACPClient). A variant id ("grok-4.7[effort=high]")
		// means an older CLI answered in the other shape; it is not one
		// set_config_option would take back, so it is left out.
		if id == "" || len(id) > 128 || strings.ContainsAny(id, "[]") || len(models) == 64 {
			continue
		}
		models = append(models, cursorACPModel{ID: id, Name: clipRunes(strings.TrimSpace(model.Name), 128)})
	}
	if len(models) > 0 {
		st.record.Models = models
	}
}

// connect starts an ACP child for a chat.
func (s *CursorACPSource) connect(cwd string, handle func(cursorACPEnvelope)) (*cursorACPClient, error) {
	if s.dial != nil {
		return s.dial(cwd, handle)
	}
	return newCursorACPClient(cwd, handle)
}

// withSession runs fn with an ACP client that has the chat's session loaded:
// the running turn's, or one started for the purpose and closed after.
func (s *CursorACPSource) withSession(ctx context.Context, st *cursorACPState, fn func(*cursorACPClient, string) error) error {
	st.startMu.Lock()
	defer st.startMu.Unlock()
	st.mu.Lock()
	if st.question != nil {
		st.mu.Unlock()
		return errors.New("answer Cursor's pending question first")
	}
	client, busy, nativeID, cwd := st.client, st.busy, st.record.NativeID, st.record.Cwd
	st.mu.Unlock()
	if busy && client != nil {
		return fn(client, nativeID)
	}
	lock, err := s.lockTurn(nativeID)
	if err != nil {
		return err
	}
	defer releaseCursorACPLock(lock)
	st.mu.Lock()
	st.replaying = true
	st.mu.Unlock()
	client, err = s.connect(cwd, func(event cursorACPEnvelope) { s.handle(st, event) })
	if err != nil {
		st.mu.Lock()
		st.replaying = false
		st.mu.Unlock()
		return err
	}
	defer client.close()
	var loaded cursorACPSessionState
	err = client.call(ctx, "session/load", map[string]any{"sessionId": nativeID, "cwd": cwd, "mcpServers": []any{}}, &loaded)
	st.mu.Lock()
	st.replaying = false
	// Events from this client reach the chat only while it is attached.
	st.client = client
	st.mu.Unlock()
	defer func() {
		st.mu.Lock()
		if st.client == client {
			st.client = nil
		}
		st.mu.Unlock()
	}()
	if err != nil {
		return fmt.Errorf("load Cursor session: %w", err)
	}
	s.noteSessionState(st, loaded)
	return fn(client, nativeID)
}

// SetMode switches a phone-launched chat's mode and waits for Cursor to
// announce it.
func (s *CursorACPSource) SetMode(ctx context.Context, id, mode string) error {
	st, err := s.get(id)
	if err != nil {
		return err
	}
	mode = strings.TrimSpace(mode)
	if !slices.Contains(s.modes(st), mode) {
		return fmt.Errorf("Cursor does not offer %q mode here", mode)
	}
	err = s.withSession(ctx, st, func(client *cursorACPClient, nativeID string) error {
		if err := client.call(ctx, "session/set_mode", map[string]string{"sessionId": nativeID, "modeId": mode}, nil); err != nil {
			return fmt.Errorf("switch Cursor's mode: %w", err)
		}
		// Cursor answers before it announces; the announcement is what says
		// the chat is now in that mode.
		deadline := time.Now().Add(cursorACPSwitchWait)
		for {
			st.mu.Lock()
			current := st.record.Mode
			st.mu.Unlock()
			if current == mode {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("Cursor did not confirm %s mode", mode)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	})
	if err == nil {
		_ = s.save(st)
	}
	return err
}

// SetModel switches the model and checks Cursor reports it selected. The
// choice becomes Cursor's default too; see the note at the top of the file.
func (s *CursorACPSource) SetModel(ctx context.Context, id, model string) error {
	st, err := s.get(id)
	if err != nil {
		return err
	}
	model = strings.TrimSpace(model)
	st.mu.Lock()
	offered := slices.ContainsFunc(st.record.Models, func(m cursorACPModel) bool { return m.ID == model })
	st.mu.Unlock()
	if !offered {
		return fmt.Errorf("Cursor does not offer %q here", model)
	}
	return s.withSession(ctx, st, func(client *cursorACPClient, nativeID string) error {
		var reply struct {
			ConfigOptions []struct {
				ID           string          `json:"id"`
				CurrentValue json.RawMessage `json:"currentValue"`
			} `json:"configOptions"`
		}
		if err := client.call(ctx, "session/set_config_option", map[string]string{
			"sessionId": nativeID, "configId": cursorACPModelConfig, "value": model,
		}, &reply); err != nil {
			return fmt.Errorf("switch Cursor's model: %w", err)
		}
		for _, option := range reply.ConfigOptions {
			var current string
			if option.ID == cursorACPModelConfig && json.Unmarshal(option.CurrentValue, &current) == nil {
				if current == model {
					return nil
				}
				return fmt.Errorf("Cursor selected %q instead of %q", current, model)
			}
		}
		return errors.New("Cursor did not say which model it selected")
	})
}

// modes is what a chat may switch to.
func (s *CursorACPSource) modes(st *cursorACPState) []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.record.Modes) > 0 {
		return slices.Clone(st.record.Modes)
	}
	return slices.Clone(cursorACPDefaultModes)
}

// Catalogue is the newest model list any ACP chat was offered. Models are
// the account's, not a chat's, so a terminal chat can use it too.
func (s *CursorACPSource) catalogue() []cursorACPModel {
	var newest []cursorACPModel
	var at int64
	for _, st := range s.states() {
		st.mu.Lock()
		if len(st.record.Models) > 0 && st.record.LastActivityAt >= at {
			newest, at = slices.Clone(st.record.Models), st.record.LastActivityAt
		}
		st.mu.Unlock()
	}
	return newest
}
