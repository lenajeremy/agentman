package daemon

import (
	"runtime"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
)

// macOSNotice says what macOS itself will ask if the user agrees to q.
func macOSNotice(q *protocol.Question) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	return question.MacOSNotice(q)
}
