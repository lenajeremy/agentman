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
