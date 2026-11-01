package source

import (
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// kiroChatPanePrefix starts the name of a pane Agentman opened to carry on a
// chat it already knows: the chat's id follows, then the pane's own suffix.
const kiroChatPanePrefix = kiroPanePrefix + "chat-"

// kiroChatIDLength is the length of Kiro's chat ids, which are UUIDs.
const kiroChatIDLength = 36

// ResumedSession implements ResumeNamer.
//
// A pane opened to carry on a chat is named for that chat, and the session in
// it goes by the chat's own id, "kiro:<id>": the id it had ended, or while
// Agentman ran it over ACP. So the thread a phone has open stays its thread
// when the chat moves into a terminal. Named "kiro:<id>" while discovery
// keyed every pane on the pane, it was left waiting on the ended entry while
// the reopened session was listed as another; discovery now reads the chat
// from the pane's name (kiroPaneSessionID).
func (s *KiroSource) ResumedSession(native, defaultPane string) (pane, sessionID string) {
	if !validKiroChatID(native) {
		return defaultPane, string(protocol.KindKiro) + ":" + tmuxID(defaultPane)
	}
	pane = kiroChatPanePrefix + native + "-" + strings.TrimPrefix(defaultPane, kiroPanePrefix)
	return pane, string(protocol.KindKiro) + ":" + native
}

var _ ResumeNamer = (*KiroSource)(nil)

// kiroPaneChat returns the chat a pane was opened to carry on, if it was.
func kiroPaneChat(pane string) string {
	rest, ok := strings.CutPrefix(pane, kiroChatPanePrefix)
	if !ok || len(rest) < kiroChatIDLength {
		return ""
	}
	native := rest[:kiroChatIDLength]
	if !validKiroChatID(native) || (len(rest) > kiroChatIDLength && rest[kiroChatIDLength] != '-') {
		return ""
	}
	return native
}

// kiroPaneSessionID is the id a session in one of Agentman's panes goes by,
// with native the chat Kiro holds there ("" before it holds one): the chat's
// own id in a pane opened for it, the pane otherwise. A phone launch knows its
// pane before Kiro has chosen an id, so that pane is the only name that stays
// the same across that moment. A pane opened for one chat that has moved on
// to another (/chat new) is keyed on the pane, and previous is the chat it
// was opened for, so a thread showing that chat follows it there.
func kiroPaneSessionID(pane, native string) (id, previous string) {
	kind := string(protocol.KindKiro) + ":"
	chat := kiroPaneChat(pane)
	switch {
	case chat != "" && (native == "" || native == chat):
		return kind + chat, ""
	case chat != "":
		return kind + tmuxID(pane), kind + chat
	default:
		return kind + tmuxID(pane), ""
	}
}

// validKiroChatID accepts a chat id as Kiro writes them: a lowercase or
// uppercase UUID, 8-4-4-4-12.
func validKiroChatID(id string) bool {
	if len(id) != kiroChatIDLength {
		return false
	}
	for i, r := range id {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
				return false
			}
		}
	}
	return true
}
