package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Goal to team: the user names a goal, the model drafts which agents do
// what and in what order, and the user approves the plan. Nothing starts
// before that. Each agent starts when the ones it depends on have finished,
// and, when checks are on, when their work has held up.

const (
	minMissionTasks = 2
	maxMissionTasks = 6
	maxMissions     = 50
	maxTaskText     = 4000
	// missionSettle is how long a finished turn waits for its check to
	// begin before the next agent is started.
	missionSettle = 15 * time.Second
	offerMission  = "mission"
)

type missionStore struct {
	path string
	mu   sync.Mutex
	list []protocol.Mission
}

func loadMissions(dir string) *missionStore {
	store := &missionStore{path: filepath.Join(dir, "missions.json")}
	if raw, err := os.ReadFile(store.path); err == nil {
		_ = json.Unmarshal(raw, &store.list)
	}
	return store
}

func (s *missionStore) saveLocked() {
	if len(s.list) > maxMissions {
		s.list = s.list[len(s.list)-maxMissions:]
	}
	raw, err := json.MarshalIndent(s.list, "", "  ")
	if err != nil || os.MkdirAll(filepath.Dir(s.path), 0o700) != nil {
		return
	}
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

// all is a copy that shares nothing with the store.
func (s *missionStore) all() []protocol.Mission {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.list)
	for i := range out {
		out[i].Tasks = slices.Clone(out[i].Tasks)
	}
	return out
}

func (s *missionStore) add(mission protocol.Mission) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, mission)
	s.saveLocked()
}

// change edits one mission under the lock.
func (s *missionStore) change(id string, edit func(*protocol.Mission)) (protocol.Mission, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == id {
			edit(&s.list[i])
			s.saveLocked()
			return s.list[i], true
		}
	}
	return protocol.Mission{}, false
}

func (m *Manager) emitMissions() {
	m.emit(protocol.Event{Type: protocol.EvtManagerMissions, Missions: m.missions.all()})
}

// runPlanTeam is the plan_team tool: it puts the plan in front of the user.
func runPlanTeam(_ context.Context, m *Manager, input map[string]any) toolResult {
	goal, _ := input["goal"].(string)
	goal = strings.TrimSpace(goal)
	raw, _ := input["tasks"].([]any)
	if goal == "" {
		return toolResult{text: "Say what the team is for.", isError: true}
	}
	tasks, err := m.parseTasks(raw)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	mission := protocol.Mission{ID: newID(), Goal: goal, State: protocol.MissionProposed, Tasks: tasks, CreatedAt: m.now().UnixMilli()}
	m.missions.add(mission)
	m.emitMissions()

	var plan strings.Builder
	for i, task := range tasks {
		fmt.Fprintf(&plan, "%d. %s, %s in %s", i+1, task.Title, agentLabel(launchKinds[task.Agent]), shortPath(task.Folder))
		if len(task.DependsOn) > 0 {
			plan.WriteString(" (after " + strings.Join(taskTitles(tasks, task.DependsOn), ", ") + ")")
		}
		plan.WriteString("\n")
	}
	m.cardMu.Lock()
	defer m.cardMu.Unlock()
	id := newID()
	card := &protocol.ManagerCard{
		ID: id, Kind: protocol.CardOffer, State: protocol.CardOpen, AgentName: "Agentman",
		Title:  fmt.Sprintf("A team of %d for: %s", len(tasks), goal),
		Detail: strings.TrimSpace(plan.String()),
		Offer:  map[string]string{"type": offerMission, "mission": mission.ID},
		Options: []protocol.QuestionOption{
			{Key: "start", Label: fmt.Sprintf("Start the %d agents", len(tasks))},
			{Key: "no", Label: "Not now"},
		},
	}
	m.addEntry(protocol.ManagerEntry{ID: id, Role: "card", Text: card.Title, Card: card})
	return toolResult{text: "The plan is in front of the user. Nothing starts unless they approve it."}
}

func taskTitles(tasks []protocol.MissionTask, ids []string) []string {
	var out []string
	for _, task := range tasks {
		if slices.Contains(ids, task.ID) {
			out = append(out, task.Title)
		}
	}
	return out
}

// parseTasks reads and checks a plan: known agents, folders agents can start
// in, and an order that can be followed.
func (m *Manager) parseTasks(raw []any) ([]protocol.MissionTask, error) {
	if len(raw) < minMissionTasks || len(raw) > maxMissionTasks {
		return nil, fmt.Errorf("a team is %d to %d agents; for one, use start_agent", minMissionTasks, maxMissionTasks)
	}
	tasks := make([]protocol.MissionTask, 0, len(raw))
	ids := map[string]bool{}
	for _, item := range raw {
		fields, _ := item.(map[string]any)
		text := func(name string) string { value, _ := fields[name].(string); return strings.TrimSpace(value) }
		task := protocol.MissionTask{ID: text("id"), Title: text("title"), Agent: strings.ToLower(text("agent")), Task: text("task"),
			Worktree: text("worktree"), State: protocol.TaskPlanned}
		if task.ID == "" || task.Title == "" || task.Task == "" || ids[task.ID] {
			return nil, errors.New("every task needs its own id, a title and what to do")
		}
		if len(task.Task) > maxTaskText {
			return nil, errors.New("keep each task to a few paragraphs")
		}
		if _, ok := launchKinds[task.Agent]; !ok {
			return nil, fmt.Errorf("%q isn't an agent; choose from %s", task.Agent, strings.Join(launchKindNames, ", "))
		}
		folder, _, err := m.launchFolder(text("folder"))
		if err != nil {
			return nil, err
		}
		task.Folder = folder
		if after, ok := fields["depends_on"].([]any); ok {
			for _, each := range after {
				if id, ok := each.(string); ok {
					task.DependsOn = append(task.DependsOn, id)
				}
			}
		}
		ids[task.ID] = true
		tasks = append(tasks, task)
	}
	for _, task := range tasks {
		for _, id := range task.DependsOn {
			if !ids[id] {
				return nil, fmt.Errorf("%s depends on %q, which isn't in the plan", task.Title, id)
			}
		}
	}
	if hasCycle(tasks) {
		return nil, errors.New("the tasks depend on each other in a circle")
	}
	return tasks, nil
}

func hasCycle(tasks []protocol.MissionTask) bool {
	done := map[string]bool{}
	for range tasks {
		for _, task := range tasks {
			if !done[task.ID] && !slices.ContainsFunc(task.DependsOn, func(id string) bool { return !done[id] }) {
				done[task.ID] = true
			}
		}
	}
	return len(done) != len(tasks)
}

func (m *Manager) takeMissionOffer(ctx context.Context, card *protocol.ManagerCard, option string) (string, error) {
	id := card.Offer["mission"]
	if option != "start" {
		m.missions.change(id, func(mission *protocol.Mission) { mission.State = protocol.MissionDismissed })
		m.emitMissions()
		return "Left the plan", nil
	}
	if _, ok := m.missions.change(id, func(mission *protocol.Mission) { mission.State = protocol.MissionRunning }); !ok {
		return "", errors.New("that plan has gone")
	}
	go m.advanceMissions(context.WithoutCancel(ctx))
	return "Started the team", nil
}

// missionTurnEnded notes that a team member finished a turn.
func (m *Manager) missionTurnEnded(sessionID string) {
	now := m.now().UnixMilli()
	for _, mission := range m.missions.all() {
		if mission.State != protocol.MissionRunning {
			continue
		}
		m.missions.change(mission.ID, func(mission *protocol.Mission) {
			for i := range mission.Tasks {
				if mission.Tasks[i].SessionID == sessionID && mission.Tasks[i].State == protocol.TaskRunning {
					mission.Tasks[i].TurnEndedAt = now
				}
			}
		})
	}
}

// advanceMissions moves every running team on: finished work is marked,
// and whoever was waiting for it is started.
func (m *Manager) advanceMissions(ctx context.Context) {
	m.missionMu.Lock()
	defer m.missionMu.Unlock()
	for _, mission := range m.missions.all() {
		if mission.State != protocol.MissionRunning {
			continue
		}
		before, _ := json.Marshal(mission)
		for i := range mission.Tasks {
			m.settleTask(ctx, &mission.Tasks[i])
		}
		for i := range mission.Tasks {
			task := &mission.Tasks[i]
			if task.State == protocol.TaskPlanned && taskIsReady(mission.Tasks, *task) {
				m.startTask(ctx, mission, task)
			}
		}
		finished := missionFinished(mission.Tasks)
		if finished {
			mission.State = protocol.MissionDone
		}
		if after, _ := json.Marshal(mission); string(after) == string(before) {
			continue
		}
		next := mission
		m.missions.change(mission.ID, func(stored *protocol.Mission) { *stored = next })
		m.emitMissions()
		if finished {
			m.reportMission(mission)
		}
	}
}

// settleTask marks a running task finished, failed, or leaves it running.
func (m *Manager) settleTask(ctx context.Context, task *protocol.MissionTask) {
	if task.State != protocol.TaskRunning {
		return
	}
	session, live := m.session(task.SessionID)
	if task.TurnEndedAt == 0 && m.workedSince(ctx, session, live, task.StartedAt) {
		task.TurnEndedAt = m.now().UnixMilli()
	}
	if !live {
		if task.TurnEndedAt > 0 {
			task.State = protocol.TaskDone
			if check, ok := m.verifies.get(task.SessionID); ok && check.State == protocol.VerifyFailed {
				task.State, task.Problem = protocol.TaskFailed, "It ended with its tests failing"
			}
		} else if m.now().Sub(time.UnixMilli(task.StartedAt)) > missionSettle*4 {
			// Not registered in a minute or so, or ended without a turn.
			task.State, task.Problem = protocol.TaskFailed, "It stopped before finishing a turn"
		}
		return
	}
	if task.TurnEndedAt == 0 || session.State != protocol.StateIdle || session.Question != nil {
		return
	}
	if m.now().Sub(time.UnixMilli(task.TurnEndedAt)) < missionSettle {
		return
	}
	if check, ok := m.verifies.get(task.SessionID); ok {
		switch {
		case check.State == protocol.VerifyChecking, check.State == protocol.VerifyFailed && check.SentBack:
			return
		case check.State == protocol.VerifyFailed:
			task.State, task.Problem = protocol.TaskFailed, "Its tests fail"
			return
		}
	}
	task.State = protocol.TaskDone
}

// workedSince reports an agent, now between turns, that has done something
// since it was started: a turn that ended while the daemon wasn't watching,
// so it was never told.
func (m *Manager) workedSince(ctx context.Context, session protocol.Session, live bool, started int64) bool {
	if !live || session.State != protocol.StateIdle || session.Question != nil ||
		m.now().Sub(time.UnixMilli(started)) < missionSettle*4 {
		return false
	}
	for _, message := range m.tailMessages(ctx, session.ID) {
		if message.Ts >= started && message.Role != protocol.RoleUser {
			return true
		}
	}
	return false
}

func taskIsReady(tasks []protocol.MissionTask, task protocol.MissionTask) bool {
	for _, other := range tasks {
		if slices.Contains(task.DependsOn, other.ID) && other.State != protocol.TaskDone {
			return false
		}
	}
	return true
}

// missionFinished: nothing is running, and nothing still waiting can start.
func missionFinished(tasks []protocol.MissionTask) bool {
	for _, task := range tasks {
		if task.State == protocol.TaskRunning || (task.State == protocol.TaskPlanned && !dependsOnFailure(tasks, task)) {
			return false
		}
	}
	return true
}

// dependsOnFailure follows the plan back, so what depends on something that
// can never start can't start either.
func dependsOnFailure(tasks []protocol.MissionTask, task protocol.MissionTask) bool {
	for _, other := range tasks {
		if slices.Contains(task.DependsOn, other.ID) && (other.State == protocol.TaskFailed || dependsOnFailure(tasks, other)) {
			return true
		}
	}
	return false
}

func (m *Manager) startTask(ctx context.Context, mission protocol.Mission, task *protocol.MissionTask) {
	folder := task.Folder
	home, err := os.UserHomeDir()
	if err != nil {
		task.State, task.Problem = protocol.TaskFailed, err.Error()
		return
	}
	if task.Worktree != "" {
		rel, err := filepath.Rel(home, folder)
		if err != nil {
			task.State, task.Problem = protocol.TaskFailed, err.Error()
			return
		}
		made := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqCreateWorktree, Path: rel, Text: task.Worktree})
		if made.Type == protocol.EvtError || made.Worktree == nil {
			task.State, task.Problem = protocol.TaskFailed, "Couldn't make its worktree: "+plainError(made.Error)
			return
		}
		folder = made.Worktree.Path
	}
	text := fmt.Sprintf("You are one of %d agents working towards: %s\n\nYour part: %s", len(mission.Tasks), mission.Goal, task.Task)
	sessionID, err := m.launch(ctx, task.Agent, folder, text)
	if err != nil {
		task.State, task.Problem = protocol.TaskFailed, err.Error()
		return
	}
	task.State, task.SessionID, task.StartedAt = protocol.TaskRunning, sessionID, m.now().UnixMilli()
	m.addEntry(protocol.ManagerEntry{Role: "activity", Tool: "start_agent", SessionID: sessionID,
		Text: fmt.Sprintf("Started %s on “%s”", agentLabel(launchKinds[task.Agent]), task.Title)})
}

func (m *Manager) reportMission(mission protocol.Mission) {
	done, failed := 0, []string{}
	for _, task := range mission.Tasks {
		switch task.State {
		case protocol.TaskDone:
			done++
		default:
			failed = append(failed, task.Title)
		}
	}
	text := fmt.Sprintf("The team for “%s” has finished: %d of %d done.", mission.Goal, done, len(mission.Tasks))
	if len(failed) > 0 {
		text += " Not done: " + strings.Join(failed, ", ") + "."
	}
	m.addEntry(protocol.ManagerEntry{Role: "brief", Text: text})
	m.note(text)
}
