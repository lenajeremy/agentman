package protocol

// `@` and part of a file's name in a message box: the session's files that
// match, best first, so the message can name one as `@path`. The agent reads
// what it names (Claude Code attaches the file when the message arrives), so
// only the path travels.

const (
	// ReqFindFiles finds files in a session's folder whose names match Text,
	// answered with an EvtFiles. An empty Text asks for the files changed
	// since the last commit, the likeliest to be mentioned.
	ReqFindFiles RequestType = "find_files"
	EvtFiles     EventType   = "files"
)
