package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// message is one JSON-RPC message, kept raw where am only passes it on.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func (m message) isRequest() bool  { return m.Method != "" && len(m.ID) > 0 }
func (m message) isResponse() bool { return m.Method == "" && len(m.ID) > 0 }

// maxMessage bounds one message from a server: a misbehaving one mustn't
// make am allocate without end.
const maxMessage = 64 << 20

// readMessage reads one message in LSP's base protocol: headers, a blank
// line, then Content-Length bytes of JSON.
func readMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q", value)
			}
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: message without a Content-Length")
	}
	if length > maxMessage {
		return nil, fmt.Errorf("lsp: message of %d bytes is too large", length)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// writeMessage writes one message in LSP's base protocol.
func writeMessage(w io.Writer, body []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// Glob is a file watcher's pattern, as servers register them:
// "**/*.go", "**/*.{ts,tsx}", "src/[a-z]*.py".
type Glob struct {
	re *regexp.Regexp
	// nameOnly is set for a pattern with no folder in it.
	nameOnly bool
}

// CompileGlob turns an LSP glob into something to match paths with.
func CompileGlob(pattern string) (Glob, error) {
	var b strings.Builder
	b.WriteString("^")
	depth := 0
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			i++
			if i+1 < len(pattern) && pattern[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '{':
			depth++
			b.WriteString("(?:")
		case c == '}' && depth > 0:
			depth--
			b.WriteString(")")
		case c == ',' && depth > 0:
			b.WriteString("|")
		case c == '[':
			end := strings.IndexByte(pattern[i:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := pattern[i+1 : i+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteByte('[')
			b.WriteString(class)
			b.WriteByte(']')
			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return Glob{}, err
	}
	return Glob{re: re, nameOnly: !strings.Contains(pattern, "/")}, nil
}

// Match says whether a path, absolute or relative to the pattern's base,
// matches. A pattern without a folder in it, like "*.go", matches the name
// anywhere, as editors do.
func (g Glob) Match(path string) bool {
	if g.re == nil {
		return false
	}
	if g.re.MatchString(path) {
		return true
	}
	if i := strings.LastIndexByte(path, '/'); g.nameOnly && i >= 0 {
		return g.re.MatchString(path[i+1:])
	}
	return false
}
