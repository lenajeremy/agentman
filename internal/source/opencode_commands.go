package source

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// OpenCode's commands. OpenCode 2's terminal app reads a "/" at the start
// of what is typed itself; its service takes a message as words. So a
// message that starts with one of OpenCode's commands is sent as that
// command: POST /api/session/{id}/command, which runs the command's prompt
// in the session as the terminal app would, or POST .../compact for
// /compact. OpenCode 1's command endpoint answers only when the whole turn
// is over, so OpenCode 1 servers keep taking words, as before.

// ocCommand is one of OpenCode's commands, as GET /command lists them.
type ocCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// openCodeCompact is OpenCode's /compact, which its service runs on its own.
var openCodeCompact = protocol.Command{
	Name: "compact", Description: "compact the session", Source: "builtin",
}

// openCodeCommands lists the commands an OpenCode 2 session takes, in
// OpenCode's order, /compact last. A session on OpenCode 1 has none.
func (s *OpenCodeSource) openCodeCommands(ctx context.Context, session openCodeSession) ([]protocol.Command, error) {
	if _, service := s.servicePassword(session.baseURL); !service {
		return nil, nil
	}
	query := url.Values{}
	if session.directory != "" {
		query.Set("directory", session.directory)
	}
	var listed []ocCommand
	if _, err := s.doAt(ctx, session.baseURL, http.MethodGet, openCodePath("/command", query), nil, &listed); err != nil {
		return nil, err
	}
	commands := make([]protocol.Command, 0, len(listed)+1)
	for _, command := range listed {
		if name := strings.TrimSpace(command.Name); name != "" && name != openCodeCompact.Name {
			commands = append(commands, protocol.Command{Name: name, Description: command.Description})
		}
	}
	return append(commands, openCodeCompact), nil
}

// openCodeSlash splits a message that starts with "/name" into the name and
// the words after it.
func openCodeSlash(text string) (name, rest string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return "", "", false
	}
	name, rest, _ = strings.Cut(trimmed[1:], " ")
	if before, after, found := strings.Cut(name, "\n"); found {
		name, rest = before, strings.TrimSpace(after+" "+rest)
	}
	if name == "" {
		return "", "", false
	}
	return name, strings.TrimSpace(rest), true
}

// runCommand sends a message that starts with one of an OpenCode 2
// session's commands as that command. It reports false, sending nothing,
// for any other message: one that names no command is words, and so is a
// "/" OpenCode doesn't know, as its own terminal app would leave it.
func (s *OpenCodeSource) runCommand(ctx context.Context, session openCodeSession, text string) (bool, error) {
	name, rest, ok := openCodeSlash(text)
	if !ok {
		return false, nil
	}
	commands, err := s.openCodeCommands(ctx, session)
	if err != nil {
		// Unable to tell, it goes as words, as it did before commands.
		return false, nil
	}
	known := false
	for _, command := range commands {
		known = known || command.Name == name
	}
	if !known {
		return false, nil
	}
	query := url.Values{}
	if session.directory != "" {
		query.Set("directory", session.directory)
	}
	path := "/session/" + openCodeSegment(session.nativeID)
	if name == openCodeCompact.Name {
		_, err = s.doAt(ctx, session.baseURL, http.MethodPost, openCodePath(path+"/summarize", query), map[string]any{}, nil)
		return true, err
	}
	_, err = s.doAt(ctx, session.baseURL, http.MethodPost, openCodePath(path+"/command", query),
		map[string]string{"command": name, "arguments": rest}, nil)
	return true, err
}
