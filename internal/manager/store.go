package manager

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// conversation is the user's chat with Agentman, kept two ways:
//
//   - entries, what the apps show: the user's messages, Agentman's replies,
//     what it did, and news from agents;
//   - history, what the model sees: the same conversation as API messages,
//     with its tool calls and their results.
//
// Both are JSON lines in the manager directory, only ever appended to, so a
// restart continues the same conversation, and the model's history is never
// rewritten: Claude checks that the turns its thinking was written against
// have not changed. An entry that changes later, such as a card once it is
// answered, is appended again under the same ID, and the latest copy wins in
// the first one's place.
type conversation struct {
	mu          sync.Mutex
	entriesPath string
	historyPath string
	entries     []protocol.ManagerEntry
	history     []anthropic.BetaMessageParam
}

// maxEntriesInMemory bounds what is kept for paging back. The file keeps
// everything.
const maxEntriesInMemory = 5000

func loadConversation(dir string) (*conversation, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c := &conversation{
		entriesPath: filepath.Join(dir, "entries.jsonl"),
		historyPath: filepath.Join(dir, "history.jsonl"),
	}
	at := map[string]int{}
	if err := readLines(c.entriesPath, func(line []byte) {
		var entry protocol.ManagerEntry
		if json.Unmarshal(line, &entry) != nil || entry.ID == "" {
			return
		}
		if i, ok := at[entry.ID]; ok {
			c.entries[i] = entry
			return
		}
		at[entry.ID] = len(c.entries)
		c.entries = append(c.entries, entry)
	}); err != nil {
		return nil, err
	}
	if len(c.entries) > maxEntriesInMemory {
		c.entries = c.entries[len(c.entries)-maxEntriesInMemory:]
	}
	if err := readLines(c.historyPath, func(line []byte) {
		var message anthropic.BetaMessageParam
		if json.Unmarshal(line, &message) == nil {
			c.history = append(c.history, message)
		}
	}); err != nil {
		return nil, err
	}
	// A daemon stopped in the middle of a tool call left its call without a
	// result, which the API refuses. The call is closed, appended rather than
	// edited, the same way a stop does it.
	if ids := openToolUses(c.history); len(ids) > 0 {
		if err := c.appendHistory(interruptedResults(ids)); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func readLines(path string, each func([]byte)) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	// A reader rather than a scanner: a line holding an image the model
	// looked at can be megabytes long.
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 1 {
			each(line[:len(line)-1])
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func appendLine(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(raw, '\n'))
	return err
}

// addEntry keeps a new entry, or the new state of one already kept.
func (c *conversation) addEntry(entry protocol.ManagerEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.entries) - 1; i >= 0; i-- {
		if c.entries[i].ID == entry.ID {
			c.entries[i] = entry
			return appendLine(c.entriesPath, entry)
		}
	}
	c.entries = append(c.entries, entry)
	if len(c.entries) > maxEntriesInMemory {
		c.entries = c.entries[len(c.entries)-maxEntriesInMemory:]
	}
	return appendLine(c.entriesPath, entry)
}

// entry is the entry with this ID, if it is still kept.
func (c *conversation) entry(id string) (protocol.ManagerEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.entries) - 1; i >= 0; i-- {
		if c.entries[i].ID == id {
			return c.entries[i], true
		}
	}
	return protocol.ManagerEntry{}, false
}

// page returns up to limit entries before the one with ID before, oldest
// first, and whether older ones remain. Empty before reads from the end.
func (c *conversation) page(before string, limit int) ([]protocol.ManagerEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	end := len(c.entries)
	if before != "" {
		for i, entry := range c.entries {
			if entry.ID == before {
				end = i
				break
			}
		}
	}
	start := max(0, end-limit)
	return append([]protocol.ManagerEntry(nil), c.entries[start:end]...), start > 0
}

// openCards is the cards still waiting on the user, oldest first.
func (c *conversation) openCards() []protocol.ManagerEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	var open []protocol.ManagerEntry
	for _, entry := range c.entries {
		if entry.Card != nil && entry.Card.State == protocol.CardOpen {
			open = append(open, entry)
		}
	}
	return open
}

func (c *conversation) appendHistory(message anthropic.BetaMessageParam) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.appendHistoryLocked(message)
}

func (c *conversation) appendHistoryLocked(message anthropic.BetaMessageParam) error {
	c.history = append(c.history, message)
	return appendLine(c.historyPath, message)
}

func (c *conversation) messages() []anthropic.BetaMessageParam {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]anthropic.BetaMessageParam(nil), c.history...)
}

// openToolUses lists the tool calls in the last message that have no result
// after them.
func openToolUses(history []anthropic.BetaMessageParam) []string {
	if len(history) == 0 {
		return nil
	}
	last := history[len(history)-1]
	if last.Role != anthropic.BetaMessageParamRoleAssistant {
		return nil
	}
	var ids []string
	for _, block := range last.Content {
		if block.OfToolUse != nil {
			ids = append(ids, block.OfToolUse.ID)
		}
	}
	return ids
}

// interruptedResults answers tool calls that never ran, so the history stays
// one the API accepts.
func interruptedResults(ids []string) anthropic.BetaMessageParam {
	blocks := make([]anthropic.BetaContentBlockParamUnion, 0, len(ids))
	for _, id := range ids {
		blocks = append(blocks, anthropic.NewBetaToolResultBlock(id, "Not run: the user stopped this reply.", true))
	}
	return anthropic.NewBetaUserMessage(blocks...)
}

// maxArchives bounds the archived conversations kept beside the current one.
const maxArchives = 5

// archive moves a conversation file aside under the time it ended, and
// trims the oldest archives of that kind.
func archive(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	base := strings.TrimSuffix(path, ".jsonl")
	if err := os.Rename(path, base+"-"+time.Now().Format("20060102-150405")+".jsonl"); err != nil {
		return err
	}
	old, _ := filepath.Glob(base + "-*.jsonl")
	sort.Strings(old)
	for len(old) > maxArchives {
		_ = os.Remove(old[0])
		old = old[1:]
	}
	return nil
}

// writeLines writes values to path whole, in place of what was there.
func writeLines[T any](path string, values []T) error {
	var b strings.Builder
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// replaceHistory puts history in place of the model's history, after a
// compaction. The history before is archived.
func (c *conversation) replaceHistory(history []anthropic.BetaMessageParam) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := archive(c.historyPath); err != nil {
		return err
	}
	if err := writeLines(c.historyPath, history); err != nil {
		return err
	}
	c.history = append([]anthropic.BetaMessageParam(nil), history...)
	return nil
}

// reset starts a new conversation: the history and the entries are
// archived, and the entries start again with the cards still open.
func (c *conversation) reset() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var open []protocol.ManagerEntry
	for _, entry := range c.entries {
		if entry.Card != nil && entry.Card.State == protocol.CardOpen {
			open = append(open, entry)
		}
	}
	if err := archive(c.historyPath); err != nil {
		return err
	}
	if err := archive(c.entriesPath); err != nil {
		return err
	}
	if err := writeLines(c.entriesPath, open); err != nil {
		return err
	}
	c.history = nil
	c.entries = open
	return nil
}
