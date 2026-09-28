import assert from "node:assert/strict";
import test from "node:test";

import { safeHref, tokenizeInline } from "./markdown-inline.ts";

test("renders a markdown link as a tappable token", () => {
  assert.deepEqual(tokenizeInline("see [the docs](https://example.com/x?a=1#b)"), [
    { kind: "text", text: "see " },
    { kind: "link", text: "the docs", href: "https://example.com/x?a=1#b" },
  ]);
});

test("keeps bold and code working alongside links", () => {
  assert.deepEqual(tokenizeInline("**bold** `npm run x` [go](http://a.dev)"), [
    { kind: "bold", text: "bold" },
    { kind: "text", text: " " },
    { kind: "code", text: "npm run x" },
    { kind: "text", text: " " },
    { kind: "link", text: "go", href: "http://a.dev" },
  ]);
});

// The app registers the agentman:// scheme, so a link that opens it could try
// to re-pair the phone against someone else's relay. Every scheme but http(s)
// degrades to plain text, and the label is still shown.
test("refuses every scheme but http(s), keeping the label visible", () => {
  for (const href of [
    "agentman://pair?token=stolen",
    "javascript:alert(1)",
    "file:///etc/passwd",
    "data:text/html,<script>",
    "JavaScript:alert(1)",
    "//evil.example",
    "/relative/path",
    "mailto:a@b.co",
  ]) {
    assert.deepEqual(
      tokenizeInline(`[click](${href})`),
      [{ kind: "text", text: "click" }],
      href,
    );
    assert.equal(safeHref(href), null, href);
  }
});

test("refuses targets carrying whitespace or control characters", () => {
  assert.equal(safeHref("https://a.dev/ b"), null);
  assert.equal(safeHref("https://a.dev/\nx"), null);
  assert.equal(safeHref("java\tscript:alert(1)"), null);
  assert.equal(safeHref(`https://a.dev/${"x".repeat(4000)}`), null);
  assert.equal(safeHref("https://"), null);
});

test("leaves malformed link syntax as ordinary text", () => {
  for (const line of ["[no href]", "[unclosed](https://a.dev", "[a](b)(c", "just [brackets]"]) {
    const tokens = tokenizeInline(line);
    assert.ok(!tokens.some((t) => t.kind === "link"), line);
  }
  // A nested bracket is not a link label; the line stays intact as text.
  assert.deepEqual(tokenizeInline("[a [b]](https://a.dev)"), [
    { kind: "text", text: "[a [b]](https://a.dev)" },
  ]);
});

test("a bracket inside a code span is not a link", () => {
  assert.deepEqual(tokenizeInline("`[x](https://a.dev)`"), [
    { kind: "code", text: "[x](https://a.dev)" },
  ]);
});

test("an empty label falls back to showing the url", () => {
  assert.deepEqual(tokenizeInline("[](https://a.dev)"), [
    { kind: "link", text: "https://a.dev", href: "https://a.dev" },
  ]);
});

test("plain text passes through untouched", () => {
  assert.deepEqual(tokenizeInline("nothing to mark up"), [
    { kind: "text", text: "nothing to mark up" },
  ]);
  assert.deepEqual(tokenizeInline(""), []);
});

// One left-to-right pass: a line of unclosed markers must not rescan.
test("pathological markers stay linear", () => {
  const started = Date.now();
  tokenizeInline("*".repeat(20_000) + "[".repeat(20_000) + "`".repeat(20_000));
  assert.ok(Date.now() - started < 1000);
});

test("keeps balanced parentheses inside a url", () => {
  assert.deepEqual(tokenizeInline("[Mercury](https://en.wikipedia.org/wiki/Mercury_(planet))"), [
    {
      kind: "link",
      text: "Mercury",
      href: "https://en.wikipedia.org/wiki/Mercury_(planet)",
    },
  ]);
});

// Regression: a refused target used to emit the label as one raw text token, so
// its own markdown showed through. A real reply wrote
// [`mobile/`](file:///Users/…/mobile) and the backticks rendered on the phone.
// The target must still be refused; only the label's formatting is restored.
test("a refused link keeps its label's formatting", () => {
  assert.deepEqual(
    tokenizeInline("the app ([`mobile/`](file:///Users/mac/Desktop/agentman/mobile)) exists"),
    [
      { kind: "text", text: "the app (" },
      { kind: "code", text: "mobile/" },
      { kind: "text", text: ") exists" },
    ],
  );
  assert.equal(safeHref("file:///Users/mac/Desktop/agentman/mobile"), null);
});

test("a refused link with a bold label keeps the bold", () => {
  assert.deepEqual(tokenizeInline("[**careful**](javascript:alert(1))"), [
    { kind: "bold", text: "careful" },
  ]);
});

// Gemini leans on constructs the renderer had no support for: one reply in
// testing carried eleven single-asterisk emphases and twenty-five inline TeX
// spans, every one of which reached the phone as literal punctuation.
test("single-asterisk and underscore emphasis are italic", () => {
  assert.deepEqual(tokenizeInline("*emphasis*"), [{ kind: "italic", text: "emphasis" }]);
  assert.deepEqual(tokenizeInline("_emphasis_"), [{ kind: "italic", text: "emphasis" }]);
  assert.deepEqual(tokenizeInline("spot it *before* it melts"), [
    { kind: "text", text: "spot it " },
    { kind: "italic", text: "before" },
    { kind: "text", text: " it melts" },
  ]);
});

test("bold still wins over italic", () => {
  assert.deepEqual(tokenizeInline("**bold**"), [{ kind: "bold", text: "bold" }]);
  assert.deepEqual(tokenizeInline("**a** and *b*"), [
    { kind: "bold", text: "a" },
    { kind: "text", text: " and " },
    { kind: "italic", text: "b" },
  ]);
});

// The delimiters have to hug their text, or arithmetic and globbing become
// emphasis on their way to the next asterisk.
test("loose asterisks are not emphasis", () => {
  for (const plain of ["a * b * c", "2 * 3 * 4", "ls *.go and *.ts"]) {
    assert.deepEqual(tokenizeInline(plain), [{ kind: "text", text: plain }], plain);
  }
});

// The rule that keeps an identifier intact.
test("underscores inside a word are not emphasis", () => {
  for (const plain of ["snake_case_name", "MAX_BUFFER_SIZE", "a_b_c"]) {
    assert.deepEqual(tokenizeInline(plain), [{ kind: "text", text: plain }], plain);
  }
  assert.deepEqual(tokenizeInline("a _real_ word"), [
    { kind: "text", text: "a " },
    { kind: "italic", text: "real" },
    { kind: "text", text: " word" },
  ]);
});

// Nothing typesets this; it is set as the machine notation it is, without
// the dollars that were never meant to be read.
test("inline TeX becomes a math span", () => {
  assert.deepEqual(tokenizeInline("hidden $O(N^2)$ loops"), [
    { kind: "text", text: "hidden " },
    { kind: "math", text: "O(N^2)" },
    { kind: "text", text: " loops" },
  ]);
  assert.deepEqual(tokenizeInline("passes for $N = 10$"), [
    { kind: "text", text: "passes for " },
    { kind: "math", text: "N = 10" },
  ]);
});

// A lone dollar is a currency sign far more often than an unclosed formula.
test("a single dollar stays text", () => {
  assert.deepEqual(tokenizeInline("costs $5 to run"), [
    { kind: "text", text: "costs $5 to run" },
  ]);
  assert.deepEqual(tokenizeInline("$$"), [{ kind: "text", text: "$$" }]);
});

// Emphasis does not cross a line break; without that an unpaired marker
// reaches forward and italicises a paragraph.
test("emphasis does not span a line break", () => {
  const text = "an *unpaired marker\nand a later * one";
  assert.deepEqual(tokenizeInline(text), [{ kind: "text", text }]);
});
