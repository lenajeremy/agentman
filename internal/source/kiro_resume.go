package source

import "github.com/lenajeremy/agentman/internal/protocol"

// ResumedSession implements ResumeNamer.
//
// A Kiro session running in a pane Agentman owns is keyed on the pane, not on
// Kiro's own id: a phone launch knows its pane before Kiro has chosen an id.
// A resume has to be named the same way. Named "kiro:<id>", it left the phone
// on the ended session's read-only entry, waiting for that id to come back,
// while discovery listed the reopened session as a second, different one.
func (s *KiroSource) ResumedSession(native, defaultPane string) (pane, sessionID string) {
	return defaultPane, string(protocol.KindKiro) + ":" + tmuxID(defaultPane)
}

var _ ResumeNamer = (*KiroSource)(nil)
