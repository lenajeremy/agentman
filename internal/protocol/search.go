package protocol

// ReqSearch finds words in what was said in every conversation on this Mac,
// running or long over: Text is the query, at least one word of three or
// more characters. With SessionID it searches that conversation alone and
// returns every message that matched, oldest first, for its find bar.
// Answered with EvtSearch.
const (
	ReqSearch RequestType = "search"
	EvtSearch EventType   = "search"
)

// SearchResults answers ReqSearch.
type SearchResults struct {
	// Query is the text searched for, so an answer to an older query can
	// be told apart from the newest.
	Query string `json:"query"`
	// SessionID is the conversation searched, when it was one.
	SessionID string      `json:"sessionId,omitempty"`
	Hits      []SearchHit `json:"hits"`
	// Indexing is true while conversations are still being read, the first
	// time the daemon runs above all, so the answer may be missing some.
	Indexing bool `json:"indexing,omitempty"`
}

// SearchHit is one message that matched.
type SearchHit struct {
	// Session is the conversation, running or ended, as the folder list
	// would describe it, so one the app has never seen can be shown and
	// opened. Absent from a search in one conversation.
	Session *Session `json:"session,omitempty"`
	// MessageID is the message's id in that conversation's pages.
	MessageID string `json:"messageId"`
	Role      Role   `json:"role"`
	Ts        int64  `json:"ts,omitempty"`
	// Snippet is about two lines of the message from a few words before
	// the match. The app finds the query's words in it to highlight them.
	Snippet string `json:"snippet"`
	// Matches is how many of the conversation's messages matched as well as
	// this one, the best.
	Matches int `json:"matches,omitempty"`
}
