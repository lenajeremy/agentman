package search

import (
	"strings"
	"testing"
)

func grade(t *testing.T, query, text string) (graded, bool) {
	t.Helper()
	return newMatcher(splitQuery(query)).grade(text)
}

// A word typed is best found as a word, then as the start of one, and only
// a long word counts found inside another.
func TestWordsAreGradedByWhereTheyMatch(t *testing.T) {
	cases := []struct {
		query, text string
		want        float64
		found       bool
	}{
		{"tmux", "attach with tmux now", wholeWord, true},
		{"notar", "we notarized it", wordStart, true},
		{"parser", "the codexParser test", wholeWord, true},
		{"ars", "the parser", 0, false},
		{"arse", "the parser", midWord, true},
		{"parse", "reparse it", midWord, true},
		{"del", "the model", 0, false},
		{"cafe", "a café downtown", wholeWord, true},
		{"don't", "I don’t know", wholeWord, true},
		{"usage page", "the page for usage", wholeWord, true},
		{"usage page", "only usage here", 0, false},
	}
	for _, c := range cases {
		got, ok := grade(t, c.query, c.text)
		if ok != c.found || (ok && got.quality < c.want) || (ok && got.quality >= c.want+phraseBonus) {
			t.Errorf("%q in %q = %v, %v; want %v, %v", c.query, c.text, got.quality, ok, c.want, c.found)
		}
	}
}

// The words side by side outrank the same words apart, which outrank the
// same words far apart; the snippet starts at the phrase.
func TestTogetherOutranksApart(t *testing.T) {
	phrase, _ := grade(t, "usage page", "I built the usage page today")
	near, _ := grade(t, "usage page", "the page that shows usage")
	far, _ := grade(t, "usage page", "page "+strings.Repeat("x ", 50)+"usage")
	if !(phrase.quality > near.quality && near.quality > far.quality) {
		t.Fatalf("phrase %v, near %v, far %v", phrase.quality, near.quality, far.quality)
	}
	if phrase.at != len([]rune("I built the ")) {
		t.Fatalf("phrase at %d", phrase.at)
	}
}

func TestShortWordsNarrowWithoutGrading(t *testing.T) {
	q := splitQuery(`"Go to notarize"`)
	if strings.Join(q.long, ",") != "notarize" || strings.Join(q.short, ",") != "go,to" {
		t.Fatalf("split %+v", q)
	}
	if _, ok := grade(t, "go notarize", "notarize it"); ok {
		t.Fatal("a short word must still be present")
	}
	if len(splitQuery("to a").long) != 0 {
		t.Fatal("nothing to look up")
	}
	if !searchable("日本") {
		t.Fatal("two CJK characters are a word")
	}
}

func TestSnippetStartsAWordBeforeTheMatch(t *testing.T) {
	text := "First we did a lot of setup work on the **relay**, then we ran `tmux attach` and it worked."
	at := strings.Index(text, "tmux")
	got := snippet(text, len([]rune(text[:at])))
	if !strings.HasPrefix(got, "…") || !strings.Contains(got, "ran tmux attach") || strings.Contains(got, "`") {
		t.Fatalf("snippet %q", got)
	}
	long := strings.Repeat("word ", 200)
	if s := snippet(long, 0); !strings.HasSuffix(s, "…") || len([]rune(s)) > 240 {
		t.Fatalf("long snippet %d runes", len([]rune(s)))
	}
	if got := readable("see [the docs](https://example.com/x) now"); got != "see the docs now" {
		t.Fatalf("readable %q", got)
	}
}
