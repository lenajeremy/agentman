package search

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The grades a word can match with. A match at a whole word is what someone
// typing a word means; the start of a longer word ("notar" in "notarize") is
// nearly as good; inside a word it is noise unless the word is long enough
// to be deliberate, so "del" never finds "model".
const (
	wholeWord = 1.0
	wordStart = 0.8
	midWord   = 0.3

	// The query's words side by side, as typed, outrank the same words
	// scattered through a message; near each other outranks far apart.
	phraseBonus = 0.25
	nearBonus   = 0.1
	nearSpan    = 60

	// A word shorter than this has to start a word to count.
	midWordMinLength = 4
	// The index works on trigrams, so a word needs three letters to be
	// looked up; shorter ones only narrow what the longer ones found.
	searchableLength = 3
)

// terms is a query split for the index and for grading.
type terms struct {
	// long are looked up in the index, and each must match.
	long []string
	// short must also be present, but are too short to look up.
	short []string
	// words is everything, in order, for the phrase test.
	words []string
}

var wrappingQuotes = regexp.MustCompile("^\\s*[\"'`“”‘’]+|[\"'`“”‘’]+\\s*$")

// splitQuery lowercases a query and splits it into words. Quotes around the
// whole query are dropped, and curly apostrophes made straight, since phone
// keyboards type "’" where everyone else types "'".
func splitQuery(query string) terms {
	query = wrappingQuotes.ReplaceAllString(query, "")
	query = straightApostrophes(strings.ToLower(query))
	var t terms
	for _, word := range strings.Fields(query) {
		t.words = append(t.words, word)
		if searchable(word) {
			t.long = append(t.long, word)
		} else {
			t.short = append(t.short, word)
		}
	}
	return t
}

// searchable reports whether a word can be looked up in the index. Two
// Chinese, Japanese or Korean characters are a word, though the index needs
// three to look one up, so those are scanned for instead.
func searchable(word string) bool {
	n := utf8.RuneCountInString(word)
	return n >= searchableLength || (n == 2 && hasCJK(word))
}

func hasCJK(word string) bool {
	for _, r := range word {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			return true
		}
	}
	return false
}

func straightApostrophes(s string) string {
	return strings.NewReplacer("’", "'", "‘", "'").Replace(s)
}

// markdownLink is a link or picture written in Markdown. Only its words are
// searched, so a match is never inside a URL nobody reads.
var markdownLink = regexp.MustCompile(`!?\[([^\]\n]*)\]\([^)\s]*\)`)

// readable is a message's text as it reads on screen: links as their words.
func readable(text string) string {
	if !strings.Contains(text, "](") {
		return text
	}
	return markdownLink.ReplaceAllString(text, "$1")
}

// graded is how well one message matched, and where.
type graded struct {
	quality float64
	// at is the rune where the match starts, for the snippet.
	at int
}

// matcher grades messages against one query. Every word must be found, as
// a whole word, a word's start, or (for a long word) inside one; the worst
// of the long words is the message's grade.
//
// Most messages are plain ASCII, and those are graded byte by byte, where a
// byte is a character: turning megabytes of text into runes was most of what
// a search cost. Anything else is graded rune by rune.
type matcher struct {
	runes matchText[rune]
	bytes matchText[byte]
	// asciiOK is false when a word is not ASCII even folded, so it cannot be
	// in ASCII text.
	asciiOK bool
}

type unit interface{ ~byte | ~rune }

// matchText is the query's words in one of the two units.
type matchText[T unit] struct {
	words  []matchWord[T]
	phrase [][]T
}

type matchWord[T unit] struct {
	text []T
	// grades is false for short words, which narrow without grading.
	grades bool
}

func newMatcher(t terms) matcher {
	m := matcher{asciiOK: true}
	add := func(word string, grades bool) {
		folded := foldRunes([]rune(word))
		m.runes.words = append(m.runes.words, matchWord[rune]{text: folded, grades: grades})
		bytes, ok := asciiBytes(folded)
		m.asciiOK = m.asciiOK && ok
		m.bytes.words = append(m.bytes.words, matchWord[byte]{text: bytes, grades: grades})
	}
	for _, word := range t.long {
		add(word, true)
	}
	for _, word := range t.short {
		add(word, false)
	}
	if len(t.words) > 1 {
		for _, word := range t.words {
			folded := foldRunes([]rune(word))
			m.runes.phrase = append(m.runes.phrase, folded)
			bytes, _ := asciiBytes(folded)
			m.bytes.phrase = append(m.bytes.phrase, bytes)
		}
	}
	return m
}

func asciiBytes(runes []rune) ([]byte, bool) {
	bytes := make([]byte, len(runes))
	for i, r := range runes {
		if r >= utf8.RuneSelf {
			return nil, false
		}
		bytes[i] = byte(r)
	}
	return bytes, true
}

func isASCII(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// grade reports how text matched, or false when a word is missing.
func (m matcher) grade(text string) (graded, bool) {
	if isASCII(text) {
		if !m.asciiOK {
			return graded{}, false
		}
		return gradeIn([]byte(text), []byte(strings.ToLower(text)), m.bytes)
	}
	original := []rune(text)
	return gradeIn(original, foldRunes(original), m.runes)
}

func gradeIn[T unit](original, folded []T, m matchText[T]) (graded, bool) {
	quality := wholeWord
	places := make([][]int, 0, len(m.words))
	for _, word := range m.words {
		best, at := findWord(original, folded, word.text)
		if len(at) == 0 {
			return graded{}, false
		}
		if best == midWord && len(word.text) < midWordMinLength {
			return graded{}, false
		}
		if word.grades {
			quality = min(quality, best)
		}
		places = append(places, at)
	}
	if at, ok := phraseAt(folded, m.phrase); ok {
		return graded{quality: quality + phraseBonus, at: at}, true
	}
	start, span := shortestSpan(places)
	if len(m.phrase) > 1 && span <= nearSpan {
		quality += nearBonus
	}
	return graded{quality: quality, at: start}, true
}

// findWord returns a word's best grade in text and every place it has it.
func findWord[T unit](original, folded, word []T) (float64, []int) {
	best := 0.0
	var at []int
	for i := index(folded, word, 0); i >= 0; i = index(folded, word, i+1) {
		grade := midWord
		if startsWord(original, i) {
			grade = wordStart
			if endsWord(original, i+len(word)) {
				grade = wholeWord
			}
		}
		switch {
		case grade > best:
			best, at = grade, []int{i}
		case grade == best:
			at = append(at, i)
		}
	}
	return best, at
}

// phraseAt finds the query's words side by side, as whole words with only
// space between them.
func phraseAt[T unit](folded []T, phrase [][]T) (int, bool) {
	if len(phrase) < 2 {
		return 0, false
	}
	first := phrase[0]
	for i := index(folded, first, 0); i >= 0; i = index(folded, first, i+1) {
		if i > 0 && isWordRune(rune(folded[i-1])) {
			continue
		}
		at := i + len(first)
		matched := true
		for _, next := range phrase[1:] {
			gap := at
			for gap < len(folded) && unicode.IsSpace(rune(folded[gap])) {
				gap++
			}
			if gap == at || !hasAt(folded, next, gap) {
				matched = false
				break
			}
			at = gap + len(next)
		}
		if matched && (at >= len(folded) || !isWordRune(rune(folded[at]))) {
			return i, true
		}
	}
	return 0, false
}

// shortestSpan finds the narrowest stretch holding one place of every word,
// returning where it starts and how long it is.
func shortestSpan(places [][]int) (int, int) {
	type hit struct{ at, word int }
	var hits []hit
	for word, at := range places {
		for _, i := range at {
			hits = append(hits, hit{i, word})
		}
	}
	if len(hits) == 0 {
		return 0, 0
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	counts := make([]int, len(places))
	covered, left := 0, 0
	bestStart, bestLength := hits[0].at, int(^uint(0)>>1)
	for _, h := range hits {
		counts[h.word]++
		if counts[h.word] == 1 {
			covered++
		}
		for covered == len(places) {
			first := hits[left]
			if h.at-first.at < bestLength {
				bestStart, bestLength = first.at, h.at-first.at
			}
			counts[first.word]--
			if counts[first.word] == 0 {
				covered--
			}
			left++
		}
	}
	return bestStart, bestLength
}

func index[T unit](text, word []T, from int) int {
	if len(word) == 0 {
		return -1
	}
	for i := from; i+len(word) <= len(text); i++ {
		if text[i] == word[0] && hasAt(text, word, i) {
			return i
		}
	}
	return -1
}

func hasAt[T unit](text, word []T, at int) bool {
	if at+len(word) > len(text) {
		return false
	}
	for j, r := range word {
		if text[at+j] != r {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// startsWord reports whether a word starts at i: after anything that is not
// a letter or digit, or where camel case turns upper ("Parser" in
// "codexParser"), or at a symbol, which is its own word.
func startsWord[T unit](text []T, i int) bool {
	if i == 0 || !isWordRune(rune(text[i])) || !isWordRune(rune(text[i-1])) {
		return true
	}
	return unicode.IsUpper(rune(text[i])) && unicode.IsLower(rune(text[i-1]))
}

func endsWord[T unit](text []T, end int) bool {
	return end >= len(text) || !isWordRune(rune(text[end])) || startsWord(text, end)
}

// foldRunes lowercases and strips accents one rune for one, so a place
// found in the folded text is the same place in the original.
func foldRunes(runes []rune) []rune {
	folded := make([]rune, len(runes))
	for i, r := range runes {
		r = unicode.ToLower(r)
		if base, ok := latinBase[r]; ok {
			r = base
		}
		if r == '’' || r == '‘' {
			r = '\''
		}
		folded[i] = r
	}
	return folded
}

// latinBase maps accented Latin letters to the letters under them, as the
// index's remove_diacritics does, so "cafe" grades "café" as a whole word.
var latinBase = func() map[rune]rune {
	groups := map[rune]string{
		'a': "àáâãäåāăą", 'c': "çćĉċč", 'd': "ďđ", 'e': "èéêëēĕėęě",
		'g': "ĝğġģ", 'h': "ĥħ", 'i': "ìíîïĩīĭįı", 'j': "ĵ", 'k': "ķ",
		'l': "ĺļľŀł", 'n': "ñńņňŉ", 'o': "òóôõöøōŏő", 'r': "ŕŗř",
		's': "śŝşš", 't': "ţťŧ", 'u': "ùúûüũūŭůűų", 'w': "ŵ", 'y': "ýÿŷ",
		'z': "źżž",
	}
	m := map[rune]rune{}
	for base, accented := range groups {
		for _, r := range accented {
			m[r] = base
		}
	}
	return m
}()

// snippet is up to about two lines of text from a few words before the
// match, with Markdown's emphasis marks dropped, its spacing collapsed, and
// an ellipsis wherever it was cut. The apps find the words again to
// highlight them.
func snippet(text string, at int) string {
	const lead, window, shown = 24, 480, 238
	runes := []rune(text)
	start := max(0, at-lead)
	if start > 0 {
		for i := start; i < at; i++ {
			if unicode.IsSpace(runes[i]) {
				start = i + 1
				break
			}
		}
	}
	end := min(len(runes), start+window)
	body := strings.NewReplacer("**", "", "`", "").Replace(string(runes[start:end]))
	body = strings.Join(strings.Fields(body), " ")
	cut := end < len(runes)
	if bodyRunes := []rune(body); len(bodyRunes) > shown {
		body = string(bodyRunes[:shown])
		cut = true
	}
	if start > 0 {
		body = "…" + body
	}
	if cut {
		body += "…"
	}
	return body
}
