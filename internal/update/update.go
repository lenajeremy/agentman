// Package update finds out whether a newer agentman has been released, and
// what changed in the releases this one is missing.
//
// It asks GitHub, where releases are published, when the daemon starts and
// every twelve hours after. The request carries nothing about the user: no
// token and no identifier, only the version in the User-Agent, which GitHub
// asks every API client to send. It can be switched off with
// "updates": {"off": true} in ~/.agentman/config.json, or by setting
// AGENTMAN_NO_UPDATE_CHECK.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// ReleasesURL lists published releases, newest first.
	ReleasesURL = "https://api.github.com/repos/lenajeremy/agentman/releases?per_page=30"
	// ChangelogURL is where every release's notes can be read in full.
	ChangelogURL = "https://github.com/lenajeremy/agentman/releases"
	// DisableEnv switches checking off when set to anything.
	DisableEnv = "AGENTMAN_NO_UPDATE_CHECK"

	// Every is how often a running daemon checks again. Releases come a few
	// times a week at most, and GitHub allows sixty unauthenticated requests
	// an hour, so this is far below anything that could be throttled.
	Every = 12 * time.Hour

	// maxReleases and maxChanges bound what travels to the phone. Someone ten
	// releases behind needs to know that, not to read every one.
	maxReleases = 10
	maxChanges  = 15
	maxBody     = 2 << 20
	timeout     = 15 * time.Second
)

// Config is the "updates" section of the daemon's config file.
type Config struct {
	// Off stops the daemon asking GitHub for releases.
	Off bool `json:"off,omitempty"`
}

// Enabled reports whether checking is on, by config and by environment.
func (c Config) Enabled() bool {
	return !c.Off && os.Getenv(DisableEnv) == ""
}

// Release is one published version this daemon is missing.
type Release struct {
	Version string
	Date    time.Time
	// Changes are the release's own notes, one line per change.
	Changes []string
	URL     string
}

// Status is what a check found.
type Status struct {
	Current string
	// Latest is the newest release. It equals Current when up to date.
	Latest string
	// Behind counts the releases newer than Current.
	Behind int
	// Releases are those newer releases, newest first, at most maxReleases.
	Releases []Release
}

// Checker keeps the result of the latest check.
type Checker struct {
	Current string
	URL     string
	Client  *http.Client
	// OnChange is called after a check whose result differs from the last.
	OnChange func(Status)

	mu      sync.Mutex
	status  Status
	checked bool
}

// NewChecker returns a checker for the running version.
func NewChecker(current string) *Checker {
	return &Checker{
		Current: current,
		URL:     ReleasesURL,
		Client:  &http.Client{Timeout: timeout},
	}
}

// Status returns the last check's result, and whether one has succeeded.
func (c *Checker) Status() (Status, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status, c.checked
}

// Run checks now and then every interval until ctx ends. A failed check is
// not reported: the next one will try again, and an offline Mac is not news.
func (c *Checker) Run(ctx context.Context, interval time.Duration) {
	for {
		_ = c.CheckNow(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// CheckNow asks once and records the answer.
func (c *Checker) CheckNow(ctx context.Context) error {
	status, err := Check(ctx, c.Client, c.URL, c.Current)
	if err != nil {
		return err
	}
	c.mu.Lock()
	changed := !c.checked || status.Latest != c.status.Latest
	c.status, c.checked = status, true
	onChange := c.OnChange
	c.mu.Unlock()
	if changed && onChange != nil {
		onChange(status)
	}
	return nil
}

// errNotARelease means this build has no version to compare, such as one
// built from a clone, which reports "dev".
var errNotARelease = errors.New("update: this build is not a release")

// Check fetches the release list and compares it with current.
func Check(ctx context.Context, client *http.Client, url, current string) (Status, error) {
	running, ok := parseVersion(current)
	if !ok {
		return Status{}, errNotARelease
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Status{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agentman/"+current)
	resp, err := client.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("update: GitHub answered %s", resp.Status)
	}
	var listed []struct {
		Tag         string    `json:"tag_name"`
		Body        string    `json:"body"`
		URL         string    `json:"html_url"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&listed); err != nil {
		return Status{}, fmt.Errorf("update: reading releases: %w", err)
	}

	status := Status{Current: strings.TrimPrefix(current, "v"), Latest: strings.TrimPrefix(current, "v")}
	newest := running
	for _, release := range listed {
		version, ok := parseVersion(release.Tag)
		if release.Draft || release.Prerelease || !ok || !version.after(running) {
			continue
		}
		status.Behind++
		if version.after(newest) {
			newest, status.Latest = version, strings.TrimPrefix(release.Tag, "v")
		}
		if len(status.Releases) < maxReleases {
			status.Releases = append(status.Releases, Release{
				Version: strings.TrimPrefix(release.Tag, "v"),
				Date:    release.PublishedAt,
				Changes: changes(release.Body),
				URL:     release.URL,
			})
		}
	}
	return status, nil
}

// changeLine is one entry of GoReleaser's changelog:
// "* 6d163f4…: Give alerts Agentman's own sounds (@lenajeremy)".
var changeLine = regexp.MustCompile(`^[*-]\s+(?:[0-9a-f]{7,40}:?\s+)?(.+?)(?:\s+\(@[\w-]+\))?\s*$`)

// changes reads a release's notes into one line per change, leaving out the
// bookkeeping a reader does not care about: merges and app build numbers.
func changes(body string) []string {
	var out []string
	inChangelog := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			inChangelog = strings.Contains(strings.ToLower(trimmed), "changelog")
			continue
		}
		if !inChangelog {
			continue
		}
		match := changeLine.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		change := match[1]
		if strings.HasPrefix(change, "Merge pull request") || strings.HasPrefix(change, "Merge branch") ||
			strings.HasPrefix(change, "Ship iOS build") {
			continue
		}
		if len(out) == maxChanges {
			break
		}
		out = append(out, change)
	}
	return out
}

// UpgradeCommand is the command that upgrades the am at executable, judged by
// where it was installed.
func UpgradeCommand(executable string) string {
	switch {
	case strings.Contains(executable, "/Caskroom/"):
		return "brew upgrade --cask lenajeremy/agentman/agentman"
	case strings.Contains(executable, "/node_modules/"):
		return "npm install -g agentman@latest"
	default:
		return "curl -fsSL https://agentman-nu.vercel.app/install | sh"
	}
}

type version [3]int

func parseVersion(text string) (version, bool) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(text), "v"), ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var v version
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return version{}, false
		}
		v[i] = n
	}
	return v, true
}

func (v version) after(other version) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] > other[i]
		}
	}
	return false
}
