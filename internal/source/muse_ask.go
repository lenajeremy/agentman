package source

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// What a Muse agent waits on the user for: an approval to run a tool, or a
// question with options. Both come from approval/listPending and become the
// apps' question card; answers go back as approval/decide and
// userInput/answer. A request with several questions is asked one at a time
// and answered in one reply once the last is answered.

type musePending struct {
	Approvals  []museApproval  `json:"approvals"`
	UserInputs []museUserInput `json:"userInputs"`
}

type museApproval struct {
	ApprovalID       string             `json:"approvalId"`
	ToolName         string             `json:"toolName"`
	RawArgs          string             `json:"rawArgs"`
	ReviewerReason   string             `json:"reviewerReason"`
	Subject          museApprovalTarget `json:"subject"`
	AvailableChoices []struct {
		ChoiceID    string `json:"choiceId"`
		Label       string `json:"label"`
		RulePreview string `json:"rulePreview"`
	} `json:"availableChoices"`
	Requirement struct {
		ApprovalID  string `json:"approvalId"`
		SourceIndex int    `json:"sourceIndex"`
	} `json:"currentRequirementId"`
}

type museApprovalTarget struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Path    string `json:"path"`
	Target  string `json:"target"`
	Host    string `json:"host"`
}

type museUserInput struct {
	UserInputID string `json:"userInputId"`
	Questions   []struct {
		ID        string `json:"id"`
		Header    string `json:"header"`
		Question  string `json:"question"`
		Selection struct {
			Mode          string `json:"mode"`
			MaxSelections int    `json:"maxSelections"`
		} `json:"selection"`
		Options []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

// museAnswers holds the answers given so far to a request of several
// questions, until the last is answered.
type museAnswers struct {
	mu      sync.Mutex
	partial map[string]map[string]map[string]any // userInputId → questionId → answer
}

// museAskTimeout bounds a pending-requests read, which Muse answers at once
// for a session it can see into.
const museAskTimeout = 3 * time.Second

// CurrentQuestion implements QuestionInspector. A session a terminal drives
// is not asked about: Muse never answers that read (in a lab it hung and then
// dropped the connection), and the terminal is where it is answered.
func (s *MuseSource) CurrentQuestion(ctx context.Context, sessionID string) (*protocol.Question, error) {
	native := strings.TrimPrefix(sessionID, musePrefix)
	if s.openInTerminal()[native] && !s.holding(native) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, museAskTimeout)
	defer cancel()
	var pending musePending
	if err := s.host.call(ctx, "approval/listPending", map[string]any{"sessionId": native}, &pending); err != nil {
		return nil, err
	}
	if len(pending.Approvals) > 0 {
		return museApprovalQuestion(pending.Approvals[0]), nil
	}
	for _, input := range pending.UserInputs {
		if question := s.museInputQuestion(input); question != nil {
			return question, nil
		}
	}
	return nil, nil
}

func museApprovalQuestion(approval museApproval) *protocol.Question {
	subject := approval.Subject
	prompt, detail := "Allow "+museToolWord(approval.ToolName)+"?", ""
	switch {
	case subject.Command != "":
		prompt, detail = "Run this command?", subject.Command
	case subject.Path != "":
		prompt, detail = "Allow "+museToolWord(approval.ToolName)+" on this file?", subject.Path
	case subject.Host != "" || subject.Target != "":
		prompt, detail = "Allow "+museToolWord(approval.ToolName)+"?", strings.TrimSpace(subject.Host+" "+subject.Target)
	case approval.RawArgs != "":
		detail = museClip(approval.RawArgs, 400)
	}
	// The ask is the title (lists and alerts show it), what it would run
	// the detail, and the prompt only Muse's reviewer's reason, when it gave
	// one: repeating the ask under the command said it twice.
	question := &protocol.Question{
		ID:     "approval:" + approval.ApprovalID + ":" + strconv.Itoa(approval.Requirement.SourceIndex),
		Title:  prompt,
		Prompt: strings.TrimSpace(approval.ReviewerReason),
		Detail: detail,
	}
	for _, choice := range approval.AvailableChoices {
		question.Options = append(question.Options, protocol.QuestionOption{
			Key: choice.ChoiceID, Label: choice.Label, Description: choice.RulePreview,
		})
	}
	return question
}

// museInputQuestion is the first question of a request not yet answered.
func (s *MuseSource) museInputQuestion(input museUserInput) *protocol.Question {
	s.answers.mu.Lock()
	given := s.answers.partial[input.UserInputID]
	s.answers.mu.Unlock()
	for _, q := range input.Questions {
		if _, answered := given[q.ID]; answered {
			continue
		}
		question := &protocol.Question{
			ID:       "input:" + input.UserInputID + ":" + q.ID,
			Title:    q.Header,
			Prompt:   q.Question,
			Multiple: q.Selection.Mode == "multiple" || q.Selection.MaxSelections > 1,
			Custom:   true,
		}
		for _, option := range q.Options {
			question.Options = append(question.Options, protocol.QuestionOption{
				Key: option.Label, Label: option.Label, Description: option.Description,
			})
		}
		return question
	}
	return nil
}

// Answer implements Answerer.
func (s *MuseSource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	native := strings.TrimPrefix(sessionID, musePrefix)
	kind, rest, _ := strings.Cut(answer.QuestionID, ":")
	switch kind {
	case "approval":
		approvalID, index, _ := strings.Cut(rest, ":")
		sourceIndex, _ := strconv.Atoi(index)
		return s.museDrive(ctx, native, "approval/decide", map[string]any{
			"sessionId":     native,
			"approvalId":    approvalID,
			"choiceId":      answer.OptionKey,
			"requirementId": map[string]any{"approvalId": approvalID, "sourceIndex": sourceIndex},
			"feedback":      museNullable(answer.Text),
		})
	case "input":
		userInputID, questionID, _ := strings.Cut(rest, ":")
		return s.museAnswerInput(ctx, native, userInputID, questionID, answer)
	}
	return fmt.Errorf("source: not a Muse question: %q", answer.QuestionID)
}

// museAnswerInput keeps one question's answer, and sends them all once every
// question of the request has one.
func (s *MuseSource) museAnswerInput(ctx context.Context, native, userInputID, questionID string, answer protocol.QuestionAnswer) error {
	given := map[string]any{"questionId": questionID}
	switch {
	case len(answer.Options) > 0:
		given["selectedLabels"] = answer.Options
	case answer.OptionKey != "":
		given["selectedLabel"] = answer.OptionKey
	}
	if strings.TrimSpace(answer.Text) != "" {
		given["freeText"] = answer.Text
	}

	if s.openInTerminal()[native] && !s.holding(native) {
		return errMuseInTerminal
	}
	listCtx, cancel := context.WithTimeout(ctx, museAskTimeout)
	defer cancel()
	var pending musePending
	if err := s.host.call(listCtx, "approval/listPending", map[string]any{"sessionId": native}, &pending); err != nil {
		return err
	}
	var request *museUserInput
	for i := range pending.UserInputs {
		if pending.UserInputs[i].UserInputID == userInputID {
			request = &pending.UserInputs[i]
		}
	}
	if request == nil {
		return fmt.Errorf("source: Muse's question was already answered")
	}

	s.answers.mu.Lock()
	if s.answers.partial == nil {
		s.answers.partial = map[string]map[string]map[string]any{}
	}
	if s.answers.partial[userInputID] == nil {
		s.answers.partial[userInputID] = map[string]map[string]any{}
	}
	s.answers.partial[userInputID][questionID] = given
	answers := make([]any, 0, len(request.Questions))
	complete := true
	for _, q := range request.Questions {
		a, ok := s.answers.partial[userInputID][q.ID]
		if !ok {
			complete = false
			break
		}
		answers = append(answers, a)
	}
	if complete {
		delete(s.answers.partial, userInputID)
	}
	s.answers.mu.Unlock()
	if !complete {
		return nil
	}
	return s.museDrive(ctx, native, "userInput/answer", map[string]any{
		"sessionId": native, "userInputId": userInputID, "answers": answers,
	})
}

// museDrive sends a command that needs the session's driver. A turn
// Agentman started is already its own; a request left by a driver that has
// gone (its connection dropped mid-turn) is taken over, and let go of as a
// send is; one a terminal holds is the terminal's to answer.
func (s *MuseSource) museDrive(ctx context.Context, native, method string, params map[string]any) error {
	if s.openInTerminal()[native] && !s.holding(native) {
		return errMuseInTerminal
	}
	params["commandId"] = museCommandID()
	err := s.host.call(ctx, method, params, nil)
	if !museInUse(err) {
		return err
	}
	if err := s.take(ctx, native); err != nil {
		return err
	}
	params["commandId"] = museCommandID()
	if err := s.host.call(ctx, method, params, nil); err != nil {
		if museInUse(err) {
			return errMuseInTerminal
		}
		return err
	}
	return nil
}

func museToolWord(tool string) string {
	tool = strings.TrimSpace(strings.ReplaceAll(tool, "_", " "))
	if tool == "" {
		return "this tool"
	}
	return tool
}

func museClip(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n] + "…"
}

func museNullable(text string) any {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return text
}
