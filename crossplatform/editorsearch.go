package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

// Search in Folder (⇧⌘F), as the Mac app's FolderSearch: every line of the
// folder's files the words are on, at most 2000 matches, with columns in
// UTF-16 units as the editor counts them.

const (
	searchMaxMatches = 2000
	searchMaxBytes   = 2 << 20
	searchPreview    = 160
)

// SearchOptions say how the words match.
type SearchOptions struct {
	CaseSensitive bool `json:"caseSensitive"`
	WholeWord     bool `json:"wholeWord"`
	Regex         bool `json:"regex"`
}

// SearchRange is where a match is in a preview, in UTF-16 units.
type SearchRange struct {
	Location int `json:"location"`
	Length   int `json:"length"`
}

// SearchMatch is a line the words are on, as many times as they are on it.
type SearchMatch struct {
	Path string `json:"path"`
	// Line and Column are from 1; Column and Length in UTF-16 units.
	Line       int           `json:"line"`
	Column     int           `json:"column"`
	Length     int           `json:"length"`
	Preview    string        `json:"preview"`
	Highlights []SearchRange `json:"highlights"`
	Count      int           `json:"count"`
}

// SearchFile is a file's matching lines.
type SearchFile struct {
	Path    string        `json:"path"`
	Matches []SearchMatch `json:"matches"`
}

// SearchResults is a whole search.
type SearchResults struct {
	Query     string        `json:"query"`
	Options   SearchOptions `json:"options"`
	Files     []SearchFile  `json:"files"`
	Total     int           `json:"total"`
	Truncated bool          `json:"truncated"`
	// Problem says why there are none: words that aren't a regular expression.
	Problem string `json:"problem"`
	// Cancelled: a newer search for the folder started.
	Cancelled bool `json:"cancelled"`
}

var searches struct {
	sync.Mutex
	latest map[string]int64
}

// Search searches files (relative to root) for query. edited holds open
// files' unsaved text, searched instead of the disk's. id orders searches
// of one folder: a newer one stops an older one.
func (e *EditorFiles) Search(root, query string, options SearchOptions, files []string, edited map[string]string, id int64) SearchResults {
	searches.Lock()
	if searches.latest == nil {
		searches.latest = map[string]int64{}
	}
	searches.latest[root] = id
	searches.Unlock()
	stale := func() bool {
		searches.Lock()
		defer searches.Unlock()
		return searches.latest[root] != id
	}
	return runSearch(root, query, options, files, edited, stale)
}

func searchExpression(query string, options SearchOptions) (*regexp.Regexp, error) {
	pattern := query
	if !options.Regex {
		pattern = regexp.QuoteMeta(query)
	}
	if options.WholeWord {
		pattern = `\b(?:` + pattern + `)\b`
	}
	if !options.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

func runSearch(root, query string, options SearchOptions, files []string, edited map[string]string, stale func() bool) SearchResults {
	results := SearchResults{Query: query, Options: options, Files: []SearchFile{}}
	if query == "" {
		return results
	}
	expression, err := searchExpression(query, options)
	if err != nil {
		results.Problem = "Not a regular expression"
		return results
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	for i, path := range sorted {
		if results.Truncated {
			break
		}
		if i%64 == 0 && stale != nil && stale() {
			results.Cancelled = true
			return results
		}
		// A folder walked, not listed by git: what git would ignore.
		parts := strings.Split(path, "/")
		quiet := false
		for _, part := range parts[:len(parts)-1] {
			if editorQuietFolders[part] {
				quiet = true
				break
			}
		}
		if quiet {
			continue
		}
		var text string
		if open, ok := edited[path]; ok {
			text = open
		} else {
			data, err := os.ReadFile(filepath.Join(root, path))
			if err != nil || len(data) > searchMaxBytes {
				continue
			}
			head := data
			if len(head) > 8000 {
				head = head[:8000]
			}
			if bytes.IndexByte(head, 0) >= 0 {
				continue
			}
			if !utf8.Valid(data) {
				data = bytes.ToValidUTF8(data, []byte("�"))
			}
			text = string(data)
		}
		found, more := searchMatches(expression, text, path, searchMaxMatches-results.Total)
		if len(found) == 0 {
			continue
		}
		results.Files = append(results.Files, SearchFile{Path: path, Matches: found})
		for _, m := range found {
			results.Total += m.Count
		}
		results.Truncated = more
	}
	return results
}

// units16 is how many UTF-16 units a string takes.
func units16(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// searchMatches finds the lines of one file the words are on, with at most
// room matches between them, and whether there were more.
func searchMatches(expression *regexp.Regexp, text, path string, room int) ([]SearchMatch, bool) {
	out := []SearchMatch{}
	more := false
	line := 1
	counted := 0 // bytes of text whose newlines are counted
	found := 0
	shift := 0
	lineStart := 0
	for _, loc := range expression.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		if end == start {
			continue
		}
		if found == room {
			more = true
			break
		}
		found++
		between := text[counted:start]
		line += strings.Count(between, "\n")
		if nl := strings.LastIndexByte(between, '\n'); nl >= 0 {
			lineStart = counted + nl + 1
		}
		counted = start
		lineEnd := len(text)
		if nl := strings.IndexByte(text[start:], '\n'); nl >= 0 {
			lineEnd = start + nl + 1
		}
		column := units16(text[lineStart:start])
		matchLength := units16(text[start:end])
		length := matchLength
		if rest := units16(text[start:lineEnd]); length > rest {
			length = rest
		}
		if n := len(out); n > 0 && out[n-1].Line == line {
			last := &out[n-1]
			last.Count++
			s := column - shift
			if s >= 0 && s+length <= units16(last.Preview) {
				last.Highlights = append(last.Highlights, SearchRange{Location: s, Length: length})
			}
			continue
		}
		preview, highlight, newShift := searchPreviewOf(text[lineStart:lineEnd], column, length)
		shift = newShift
		out = append(out, SearchMatch{
			Path: path, Line: line, Column: column + 1, Length: matchLength,
			Preview: preview, Highlights: []SearchRange{highlight}, Count: 1,
		})
	}
	return out, more
}

// searchPreviewOf is the line as a result shows it: without its indent or
// line end, and a long one cut down around the match. shift is how much
// sooner a column of the line is in the preview.
func searchPreviewOf(line string, column, length int) (string, SearchRange, int) {
	text := utf16.Encode([]rune(line))
	start := column
	for len(text) > 0 && (text[len(text)-1] == '\n' || text[len(text)-1] == '\r') {
		text = text[:len(text)-1]
	}
	indent := 0
	for indent < len(text) && indent < start && (text[indent] == ' ' || text[indent] == '\t') {
		indent++
	}
	text = text[indent:]
	start -= indent
	prefix := ""
	cut := 0
	if len(text) > searchPreview && start > 40 {
		cut = start - 40
		text = text[cut:]
		start -= cut
		prefix = "…"
	}
	if len(text) > searchPreview {
		text = append(append([]uint16(nil), text[:searchPreview]...), '…')
	}
	if length > len(text)-start {
		length = len(text) - start
	}
	if length < 0 {
		length = 0
	}
	lead := units16(prefix)
	return prefix + string(utf16.Decode(text)), SearchRange{Location: start + lead, Length: length}, indent + cut - lead
}
