package source

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// claudeModelCatalog is the list of models Claude Code offers this account,
// asked of Claude Code itself: started headless in an empty folder, it
// answers the same initialize request the Agent SDK sends, whose reply
// carries the rows its /model picker lists. No message is sent, so nothing
// counts against the account; the user's settings, and so their hooks, are
// not loaded, so no agent appears for it; and no session is kept.
//
// The list was a copy of one Claude Code release's picker, which went stale
// as Anthropic added models (Fable 5.2 was missing) and never dropped the
// ones it retired. It is asked again when Claude Code itself changes, every
// few hours for what Anthropic changes on its side, and kept on disk so a
// restart offers the right list at once.
type claudeModelCatalog struct {
	path string
	// look finds the claude program; probe asks it for its models.
	look  func() (string, error)
	probe func(ctx context.Context, binary string) (claudeModelList, error)
	now   func() time.Time

	mu     sync.Mutex
	list   claudeModelList
	loaded bool
	// running is set while a probe is out; checked is when the program was
	// last looked at; failed is when the last probe failed, and failedFor
	// the program it failed for, so a retry waits whether or not a list was
	// ever had for it.
	running   bool
	checked   time.Time
	failed    time.Time
	failedFor string
}

// claudeModelList is what a probe found, as kept on disk.
type claudeModelList struct {
	// Version is the Claude Code that answered; Binary the program it ran
	// from, with its time, so an update is seen.
	Version string    `json:"version"`
	Binary  string    `json:"binary"`
	At      time.Time `json:"at"`
	// Labels are the picker's rows, as it names them: "Fable 5.2".
	Labels []string `json:"labels"`
}

const (
	// claudeModelsEvery is how long a list is trusted for what Anthropic
	// changes on its side without a new Claude Code.
	claudeModelsEvery = 6 * time.Hour
	// claudeModelsLook is how often the program is looked at for an update.
	claudeModelsLook = time.Minute
	// claudeModelsRetry is how long after a failed probe the next is tried.
	claudeModelsRetry = 10 * time.Minute
	// claudeModelsTimeout bounds one probe.
	claudeModelsTimeout = 45 * time.Second
)

func newClaudeModelCatalog(home string) *claudeModelCatalog {
	c := &claudeModelCatalog{
		path:  filepath.Join(home, ".agentman", "claude-models.json"),
		probe: probeClaudeModels,
		now:   time.Now,
		look:  func() (string, error) { return exec.LookPath("claude") },
	}
	// The daemon's own tests never start the user's Claude Code.
	if testing.Testing() {
		c.look = func() (string, error) { return "", errors.New("not while testing") }
	}
	return c
}

// labels are the picker rows Claude Code offers, newest list known; empty
// before Claude Code has ever been asked.
func (c *claudeModelCatalog) labels() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()
	return append([]string(nil), c.list.Labels...)
}

// refreshIfDue asks Claude Code again, in the background, when the list is
// missing, its program has changed or it has grown old. Cheap to call often:
// the program is looked at once a minute at most.
func (c *claudeModelCatalog) refreshIfDue() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.loadLocked()
	now := c.now()
	if c.running || now.Sub(c.checked) < claudeModelsLook {
		c.mu.Unlock()
		return
	}
	c.checked = now
	c.mu.Unlock()

	binary, err := c.look()
	if err != nil {
		return
	}
	fingerprint := programFingerprint(binary)

	c.mu.Lock()
	due := len(c.list.Labels) == 0 || c.list.Binary != fingerprint || now.Sub(c.list.At) >= claudeModelsEvery
	if !due || c.running || (!c.failed.IsZero() && now.Sub(c.failed) < claudeModelsRetry && c.failedFor == fingerprint) {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), claudeModelsTimeout)
		defer cancel()
		list, err := c.probe(ctx, binary)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.running = false
		if err != nil {
			c.failed, c.failedFor = c.now(), fingerprint
			return
		}
		list.Binary = fingerprint
		list.At = c.now()
		c.list = list
		c.failed, c.failedFor = time.Time{}, ""
		c.saveLocked()
	}()
}

func (c *claudeModelCatalog) loadLocked() {
	if c.loaded {
		return
	}
	c.loaded = true
	data, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	var list claudeModelList
	if json.Unmarshal(data, &list) == nil {
		c.list = list
	}
}

func (c *claudeModelCatalog) saveLocked() {
	data, err := json.MarshalIndent(c.list, "", "  ")
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(c.path), 0o700) != nil {
		return
	}
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, c.path)
	}
}

// programFingerprint is the program's real path and when it was written: a
// new Claude Code is a new file (each version its own), or the same one
// replaced.
func programFingerprint(binary string) string {
	real, err := filepath.EvalSymlinks(binary)
	if err != nil {
		real = binary
	}
	info, err := os.Stat(real)
	if err != nil {
		return real
	}
	return fmt.Sprintf("%s@%d", real, info.ModTime().Unix())
}

// probeClaudeModels starts Claude Code headless in an empty folder, sends
// the initialize request and reads its reply's models, then lets it go.
func probeClaudeModels(ctx context.Context, binary string) (claudeModelList, error) {
	dir, err := os.MkdirTemp("", "agentman-claude-models-")
	if err != nil {
		return claudeModelList{}, err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, binary, "-p",
		"--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		// Not the user's settings, so not their hooks: no agent shows up for
		// this. Claude Code still signs in as it always does.
		"--setting-sources", "project",
		"--no-session-persistence")
	cmd.Dir = dir
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return claudeModelList{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return claudeModelList{}, err
	}
	if err := cmd.Start(); err != nil {
		return claudeModelList{}, err
	}
	defer func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	const requestID = "agentman-models"
	request := map[string]any{"type": "control_request", "request_id": requestID, "request": map[string]any{"subtype": "initialize"}}
	line, _ := json.Marshal(request)
	if _, err := stdin.Write(append(line, '\n')); err != nil {
		return claudeModelList{}, err
	}
	return readClaudeModels(stdout, requestID)
}

// readClaudeModels reads Claude Code's stream until its answer to the
// initialize request, and takes the picker's rows from it.
func readClaudeModels(r io.Reader, requestID string) (claudeModelList, error) {
	reader := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var message struct {
				Type     string `json:"type"`
				Response struct {
					Subtype   string `json:"subtype"`
					RequestID string `json:"request_id"`
					Error     string `json:"error"`
					Response  struct {
						Version string `json:"claude_code_version"`
						Models  []struct {
							Value       string `json:"value"`
							DisplayName string `json:"displayName"`
						} `json:"models"`
					} `json:"response"`
				} `json:"response"`
			}
			if json.Unmarshal(line, &message) == nil && message.Type == "control_response" && message.Response.RequestID == requestID {
				if message.Response.Subtype != "success" {
					return claudeModelList{}, fmt.Errorf("claude: %s", message.Response.Error)
				}
				list := claudeModelList{Version: message.Response.Response.Version}
				seen := map[string]bool{}
				for _, model := range message.Response.Response.Models {
					// "Default (recommended)" and the like are not a model
					// of their own.
					if claudeModelID(model.DisplayName) == "" || seen[model.DisplayName] {
						continue
					}
					seen[model.DisplayName] = true
					list.Labels = append(list.Labels, model.DisplayName)
				}
				if len(list.Labels) == 0 {
					return claudeModelList{}, errors.New("claude listed no models")
				}
				return list, nil
			}
		}
		if err != nil {
			return claudeModelList{}, fmt.Errorf("claude ended before listing its models: %w", err)
		}
	}
}
