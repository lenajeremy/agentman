package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// releasesJSON is shaped like GitHub's answer, with notes as GoReleaser
// writes them: a Changelog of commits, then an Install section.
const releasesJSON = `[
  {"tag_name": "v0.16.0-rc.1", "prerelease": true, "body": "## Changelog\n* aaaaaaa: Not out yet (@lenajeremy)"},
  {"tag_name": "v0.15.0", "html_url": "https://github.com/lenajeremy/agentman/releases/tag/v0.15.0",
   "published_at": "2026-10-09T10:00:00Z",
   "body": "## Changelog\n* 1111111111111111111111111111111111111111: Show Claude Code's own dialogs on the phone (@lenajeremy)\n* 2222222222222222222222222222222222222222: Merge pull request #80 from lenajeremy/fix/claude-dialogs (@lenajeremy)\n* 3333333333333333333333333333333333333333: Ship iOS build 26 (@lenajeremy)\n\n## Install\n\n` + "```bash\\nbrew install lenajeremy/agentman/agentman\\n```" + `"},
  {"tag_name": "v0.14.2", "draft": true, "body": "## Changelog\n* bbbbbbb: A draft (@lenajeremy)"},
  {"tag_name": "v0.14.2", "html_url": "https://github.com/lenajeremy/agentman/releases/tag/v0.14.2",
   "published_at": "2026-10-07T09:30:00Z",
   "body": "## Changelog\n* 4444444444444444444444444444444444444444: Give alerts Agentman's own sounds (@lenajeremy)"},
  {"tag_name": "v0.14.1", "body": "## Changelog\n* 5555555555555555555555555555555555555555: Already here (@lenajeremy)"},
  {"tag_name": "nightly", "body": ""}
]`

func releasesServer(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent; GitHub refuses API requests without one")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(releasesJSON))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestABehindDaemonLearnsWhatItIsMissing(t *testing.T) {
	calls := 0
	server := releasesServer(t, &calls)
	status, err := Check(context.Background(), server.Client(), server.URL, "0.14.1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Current != "0.14.1" || status.Latest != "0.15.0" || status.Behind != 2 {
		t.Fatalf("status = %+v, want 0.14.1, two behind 0.15.0", status)
	}
	var versions []string
	for _, release := range status.Releases {
		versions = append(versions, release.Version)
	}
	if !slices.Equal(versions, []string{"0.15.0", "0.14.2"}) {
		t.Fatalf("releases = %v: drafts, prereleases, odd tags and older ones must be left out", versions)
	}
	if got := status.Releases[0].Changes; !slices.Equal(got, []string{"Show Claude Code's own dialogs on the phone"}) {
		t.Fatalf("changes = %q, want the commit subject alone, without merges or build numbers", got)
	}
	if status.Releases[0].Date.IsZero() || status.Releases[0].URL == "" {
		t.Fatalf("release = %+v, want its date and link", status.Releases[0])
	}
}

func TestAnUpToDateDaemonIsNotBehind(t *testing.T) {
	calls := 0
	server := releasesServer(t, &calls)
	status, err := Check(context.Background(), server.Client(), server.URL, "v0.15.0")
	if err != nil {
		t.Fatal(err)
	}
	if status.Behind != 0 || status.Latest != "0.15.0" || len(status.Releases) != 0 {
		t.Fatalf("status = %+v, want up to date", status)
	}
}

// A build from a clone says "dev". There is nothing to compare, so GitHub is
// not asked at all.
func TestADevBuildDoesNotAsk(t *testing.T) {
	calls := 0
	server := releasesServer(t, &calls)
	if _, err := Check(context.Background(), server.Client(), server.URL, "dev"); err == nil {
		t.Fatal("a dev build was compared with releases")
	}
	if calls != 0 {
		t.Fatalf("GitHub was asked %d times for a dev build", calls)
	}
}

func TestAFailedCheckIsAnErrorNotAnAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer server.Close()
	if status, err := Check(context.Background(), server.Client(), server.URL, "0.14.1"); err == nil {
		t.Fatalf("a refused request produced %+v", status)
	}
}

// The phone is told when the answer changes, not on every check.
func TestTheCheckerReportsOnlyChanges(t *testing.T) {
	calls := 0
	server := releasesServer(t, &calls)
	checker := NewChecker("0.14.1")
	checker.URL, checker.Client = server.URL, server.Client()
	var reported []string
	checker.OnChange = func(status Status) { reported = append(reported, status.Latest) }
	for range 2 {
		if err := checker.CheckNow(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(reported, []string{"0.15.0"}) {
		t.Fatalf("reported %v, want one report", reported)
	}
	if status, ok := checker.Status(); !ok || status.Behind != 2 {
		t.Fatalf("Status() = %+v, %v", status, ok)
	}
}

func TestTheUpgradeCommandMatchesHowAmWasInstalled(t *testing.T) {
	for path, want := range map[string]string{
		"/opt/homebrew/Caskroom/agentman/0.14.1/am":                                   "brew upgrade --cask lenajeremy/agentman/agentman",
		"/home/linuxbrew/.linuxbrew/Caskroom/agentman/0.14.1/am":                      "brew upgrade --cask lenajeremy/agentman/agentman",
		"/usr/local/lib/node_modules/agentman/node_modules/agentman-linux-x64/bin/am": "npm install -g agentman@latest",
		"/usr/local/bin/am":      "curl -fsSL https://agentman-nu.vercel.app/install | sh",
		"/home/me/.local/bin/am": "curl -fsSL https://agentman-nu.vercel.app/install | sh",
	} {
		if got := UpgradeCommand(path); got != want {
			t.Errorf("UpgradeCommand(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestChecksCanBeSwitchedOff(t *testing.T) {
	t.Setenv(DisableEnv, "")
	if !(Config{}).Enabled() || (Config{Off: true}).Enabled() {
		t.Fatal("the config switch is not respected")
	}
	t.Setenv(DisableEnv, "1")
	if (Config{}).Enabled() {
		t.Fatalf("%s did not switch checks off", DisableEnv)
	}
}
