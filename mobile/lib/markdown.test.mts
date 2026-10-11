import assert from "node:assert/strict";
import test from "node:test";

import {
  fenceLanguage,
  inlineWork,
  MAX_COLUMNS,
  MAX_MARKDOWN_CHARS,
  MAX_NESTING,
  MAX_ROWS,
  parseMarkdown,
  plainText,
  safeHref,
  type Block,
  type Inline,
} from "./markdown.ts";

const text = (value: string): Inline => ({ kind: "text", text: value });
const paragraph = (...content: Inline[]): Block => ({ kind: "paragraph", content });

/** The inline content of a source that should parse to one paragraph. */
function inline(source: string): Inline[] {
  const blocks = parseMarkdown(source);
  assert.equal(blocks.length, 1, source);
  const [block] = blocks;
  assert.equal(block.kind, "paragraph", source);
  return block.kind === "paragraph" ? block.content : [];
}

function hasLink(content: Inline[]): boolean {
  return content.some(
    (node) => node.kind === "link" || ("children" in node && hasLink(node.children)),
  );
}

/** How deeply quotes and lists nest in a parse. */
function nesting(blocks: Block[]): number {
  let deepest = 0;
  for (const block of blocks) {
    if (block.kind === "quote") deepest = Math.max(deepest, 1 + nesting(block.blocks));
    if (block.kind === "list") {
      for (const item of block.items) deepest = Math.max(deepest, 1 + nesting(item.blocks));
    }
  }
  return deepest;
}

/** Every piece of text a parse would put on screen. */
function shown(blocks: Block[]): string {
  return blocks
    .map((block) => {
      switch (block.kind) {
        case "paragraph":
        case "heading":
          return plainText(block.content);
        case "code":
          return block.text;
        case "quote":
          return shown(block.blocks);
        case "list":
          return block.items.map((item) => shown(item.blocks)).join(" ");
        case "table":
          return [block.table.header, ...block.table.rows].flat().map(plainText).join(" ");
        case "rule":
          return "";
      }
    })
    .join(" ");
}

// Links

test("renders a markdown link as a tappable link", () => {
  assert.deepEqual(inline("see [the docs](https://example.com/x?a=1#b)"), [
    text("see "),
    { kind: "link", href: "https://example.com/x?a=1#b", children: [text("the docs")] },
  ]);
});

test("keeps bold and code working alongside links", () => {
  assert.deepEqual(inline("**bold** `npm run x` [go](http://a.dev)"), [
    { kind: "bold", children: [text("bold")] },
    text(" "),
    { kind: "code", text: "npm run x" },
    text(" "),
    { kind: "link", href: "http://a.dev", children: [text("go")] },
  ]);
});

// Regression: Claude bolds its links, and the old parser read a bold span as
// plain text, so a reply listing Mobbin screens showed every [label](https://…)
// raw on the phone.
test("a link inside bold is still a link", () => {
  assert.deepEqual(
    inline(
      "**[Copilot categories](https://mobbin.com/screens/319f)**, **[Origin](https://mobbin.com/screens/2d9a)** and more",
    ),
    [
      {
        kind: "bold",
        children: [
          {
            kind: "link",
            href: "https://mobbin.com/screens/319f",
            children: [text("Copilot categories")],
          },
        ],
      },
      text(", "),
      {
        kind: "bold",
        children: [
          { kind: "link", href: "https://mobbin.com/screens/2d9a", children: [text("Origin")] },
        ],
      },
      text(" and more"),
    ],
  );
});

// The other way round showed its asterisks and backticks inside the link.
test("a link's label keeps its formatting", () => {
  assert.deepEqual(inline("[**b**](https://b.dev) [`npm`](https://npmjs.com)"), [
    { kind: "link", href: "https://b.dev", children: [{ kind: "bold", children: [text("b")] }] },
    text(" "),
    { kind: "link", href: "https://npmjs.com", children: [{ kind: "code", text: "npm" }] },
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
    assert.deepEqual(inline(`[click](${href})`), [text("click")], href);
    assert.equal(safeHref(href), null, href);
  }
});

// Bolding a link must not launder it past the check.
test("a bolded link with an unsafe target is still refused", () => {
  assert.deepEqual(inline("**[pair](agentman://pair?token=x)**"), [
    { kind: "bold", children: [text("pair")] },
  ]);
});

test("refuses targets carrying whitespace or control characters", () => {
  assert.equal(safeHref("https://a.dev/ b"), null);
  assert.equal(safeHref("https://a.dev/\nx"), null);
  assert.equal(safeHref("java\tscript:alert(1)"), null);
  assert.equal(safeHref("https://a.dev/\u0000"), null);
  assert.equal(safeHref("https://a.dev/\u0085"), null);
  assert.equal(safeHref(`https://a.dev/${"x".repeat(4000)}`), null);
  assert.equal(safeHref("https://"), null);
});

test("a bare web address becomes a link, and an email address does not", () => {
  assert.deepEqual(inline("see https://a.dev/x and www.b.dev"), [
    text("see "),
    { kind: "link", href: "https://a.dev/x", children: [text("https://a.dev/x")] },
    text(" and "),
    { kind: "link", href: "http://www.b.dev", children: [text("www.b.dev")] },
  ]);
  assert.deepEqual(inline("write to me@a.dev"), [text("write to me@a.dev")]);
});

test("leaves malformed link syntax as ordinary text", () => {
  for (const line of ["[no href]", "[a](b)(c", "just [brackets]"]) {
    assert.ok(!hasLink(inline(line)), line);
  }
  // The label is not a link, though the bare address after it is one.
  assert.deepEqual(inline("[unclosed](https://a.dev"), [
    text("[unclosed]("),
    { kind: "link", href: "https://a.dev", children: [text("https://a.dev")] },
  ]);
});

test("a bracket inside a code span is not a link", () => {
  assert.deepEqual(inline("`[x](https://a.dev)`"), [{ kind: "code", text: "[x](https://a.dev)" }]);
});

test("an empty label falls back to showing the url", () => {
  assert.deepEqual(inline("[](https://a.dev)"), [
    { kind: "link", href: "https://a.dev", children: [text("https://a.dev")] },
  ]);
});

test("keeps balanced parentheses inside a url", () => {
  assert.deepEqual(inline("[Mercury](https://en.wikipedia.org/wiki/Mercury_(planet))"), [
    {
      kind: "link",
      href: "https://en.wikipedia.org/wiki/Mercury_(planet)",
      children: [text("Mercury")],
    },
  ]);
});

// Regression: a refused target used to emit the label as one raw text token, so
// its own markdown showed through. A real reply wrote
// [`mobile/`](file:///Users/…/mobile) and the backticks rendered on the phone.
test("a refused link keeps its label's formatting", () => {
  assert.deepEqual(
    inline("the app ([`mobile/`](file:///Users/mac/Desktop/agentman/mobile)) exists"),
    [text("the app ("), { kind: "code", text: "mobile/" }, text(") exists")],
  );
  assert.deepEqual(inline("[**careful**](javascript:alert(1))"), [
    { kind: "bold", children: [text("careful")] },
  ]);
});

// Images and HTML: nothing is fetched and nothing is interpreted.

test("an image is a link to it, never a fetch", () => {
  assert.deepEqual(inline("![chart](https://img.dev/a.png)"), [
    { kind: "link", href: "https://img.dev/a.png", children: [text("chart")] },
  ]);
  assert.deepEqual(inline("![](https://img.dev/a.png)"), [
    { kind: "link", href: "https://img.dev/a.png", children: [text("https://img.dev/a.png")] },
  ]);
  assert.deepEqual(inline("![secret](file:///a.png)"), [text("secret")]);
});

test("HTML is shown as written, except a line break", () => {
  assert.deepEqual(inline("a <b>bold</b> c"), [text("a <b>bold</b> c")]);
  assert.deepEqual(inline("one<br>two<br/>three"), [
    text("one"),
    { kind: "break" },
    text("two"),
    { kind: "break" },
    text("three"),
  ]);
  assert.deepEqual(parseMarkdown("<script>alert(1)</script>"), [
    paragraph(text("<script>alert(1)</script>")),
  ]);
});

// Emphasis

test("emphasis nests, and code inside it stays literal", () => {
  assert.deepEqual(inline("**run `a *b*` *now* please**"), [
    {
      kind: "bold",
      children: [
        text("run "),
        { kind: "code", text: "a *b*" },
        text(" "),
        { kind: "italic", children: [text("now")] },
        text(" please"),
      ],
    },
  ]);
});

// Gemini leans on constructs the first renderer had no support for: one reply
// in testing carried eleven single-asterisk emphases and twenty-five inline
// TeX spans, every one of which reached the phone as literal punctuation.
test("single-asterisk and underscore emphasis are italic", () => {
  assert.deepEqual(inline("*emphasis*"), [{ kind: "italic", children: [text("emphasis")] }]);
  assert.deepEqual(inline("_emphasis_"), [{ kind: "italic", children: [text("emphasis")] }]);
  assert.deepEqual(inline("spot it *before* it melts"), [
    text("spot it "),
    { kind: "italic", children: [text("before")] },
    text(" it melts"),
  ]);
});

test("bold still wins over italic", () => {
  assert.deepEqual(inline("**bold**"), [{ kind: "bold", children: [text("bold")] }]);
  assert.deepEqual(inline("**a** and *b*"), [
    { kind: "bold", children: [text("a")] },
    text(" and "),
    { kind: "italic", children: [text("b")] },
  ]);
});

test("strikethrough", () => {
  assert.deepEqual(inline("~~old~~ new"), [
    { kind: "strike", children: [text("old")] },
    text(" new"),
  ]);
});

// The delimiters have to hug their text, or arithmetic and globbing become
// emphasis on their way to the next asterisk.
test("loose asterisks are not emphasis", () => {
  for (const plain of ["a * b * c", "2 * 3 * 4", "ls *.go and *.ts"]) {
    assert.deepEqual(inline(plain), [text(plain)], plain);
  }
});

// The rule that keeps an identifier intact.
test("underscores inside a word are not emphasis", () => {
  for (const plain of ["snake_case_name", "MAX_BUFFER_SIZE", "a_b_c"]) {
    assert.deepEqual(inline(plain), [text(plain)], plain);
  }
  assert.deepEqual(inline("a _real_ word"), [
    text("a "),
    { kind: "italic", children: [text("real")] },
    text(" word"),
  ]);
});

test("an unpaired marker does not reach across a line", () => {
  assert.deepEqual(inline("an *unpaired marker\nand a later * one"), [
    text("an *unpaired marker"),
    { kind: "break" },
    text("and a later * one"),
  ]);
});

test("a newline inside a paragraph is a line break", () => {
  assert.deepEqual(inline("line one\nline two"), [text("line one"), { kind: "break" }, text("line two")]);
});

test("escaped markers are text", () => {
  assert.deepEqual(inline("\\*not italic\\*"), [text("*not italic*")]);
});

// Inline TeX is set as the machine notation it is, without the dollars that
// were never meant to be read.

test("inline TeX becomes a math span", () => {
  assert.deepEqual(inline("hidden $O(N^2)$ loops"), [
    text("hidden "),
    { kind: "math", text: "O(N^2)" },
    text(" loops"),
  ]);
  assert.deepEqual(inline("passes for $N = 10$"), [text("passes for "), { kind: "math", text: "N = 10" }]);
});

// A dollar is a currency sign far more often than it opens a formula.
test("money stays text", () => {
  for (const plain of ["costs $5 to run", "between $5 and $10", "$$"]) {
    assert.deepEqual(inline(plain), [text(plain)], plain);
  }
});

// Headings and rules

// Regression: the first parser capped headings at three levels, so a real
// reply's `#### 5. Pair with Your Phone` rendered as literal hashes.
test("reads all six heading levels", () => {
  for (let level = 1; level <= 6; level += 1) {
    assert.deepEqual(parseMarkdown(`${"#".repeat(level)} Section`), [
      { kind: "heading", level, content: [text("Section")] },
    ]);
  }
  assert.deepEqual(parseMarkdown("#### 5. Pair with Your Phone (iOS / Android Expo App)\ntext after"), [
    { kind: "heading", level: 4, content: [text("5. Pair with Your Phone (iOS / Android Expo App)")] },
    paragraph(text("text after")),
  ]);
});

test("hashes that are not a heading stay text", () => {
  for (const line of ["####### too deep", "#include <stdio.h>", "#hashtag"]) {
    assert.deepEqual(parseMarkdown(line), [paragraph(text(line))], line);
  }
  // An empty heading draws nothing rather than an empty line.
  assert.deepEqual(parseMarkdown("####"), []);
});

test("recognises thematic breaks and not lookalikes", () => {
  for (const line of ["---", "***", "___", "- - -", "-----"]) {
    assert.deepEqual(parseMarkdown(line), [{ kind: "rule" }], line);
  }
  for (const line of ["--", "|---|---|", "—-"]) {
    assert.deepEqual(parseMarkdown(line), [paragraph(text(line))], line);
  }
});

test("a rule between paragraphs is its own block", () => {
  assert.deepEqual(parseMarkdown("before\n\n---\n\nafter"), [
    paragraph(text("before")),
    { kind: "rule" },
    paragraph(text("after")),
  ]);
});

// Code

test("hashes inside a fenced block stay code", () => {
  assert.deepEqual(parseMarkdown("```bash\n# or follow a specific session:\nam watch\n```"), [
    { kind: "code", text: "# or follow a specific session:\nam watch", language: "bash" },
  ]);
});

// A fence names its language far more often than not, and it is the only
// thing that can tell a block of Java from a block of prose.
test("a fence's language is carried onto its block, and only its own", () => {
  assert.deepEqual(parseMarkdown("```python {highlight=3}\nx = 1\n```"), [
    { kind: "code", text: "x = 1", language: "python" },
  ]);
  assert.deepEqual(parseMarkdown("```java\nA\n```\n\ntext\n\n```\nB\n```"), [
    { kind: "code", text: "A", language: "java" },
    paragraph(text("text")),
    { kind: "code", text: "B" },
  ]);
});

// A reply still streaming in has opened its fence and not closed it yet.
test("an unclosed fence is code to the end", () => {
  assert.deepEqual(parseMarkdown("Here:\n\n```ts\nconst a = 1;"), [
    paragraph(text("Here:")),
    { kind: "code", text: "const a = 1;", language: "ts" },
  ]);
});

test("fenceLanguage takes only the language", () => {
  assert.equal(fenceLanguage("java"), "java");
  assert.equal(fenceLanguage("JAVA"), "java");
  assert.equal(fenceLanguage("````python"), "python");
  assert.equal(fenceLanguage(" go "), "go");
  assert.equal(fenceLanguage("c++"), "c++");
  assert.equal(fenceLanguage("objective-c"), "objective-c");
  assert.equal(fenceLanguage("ts twoslash"), "ts");
  assert.equal(fenceLanguage("python {highlight=3}"), "python");
  assert.equal(fenceLanguage("js,jsx"), "js");
  assert.equal(fenceLanguage(""), "");
  assert.equal(fenceLanguage("   "), "");
});

// Lists and quotes

test("lists carry their markers, numbering from where the agent started", () => {
  assert.deepEqual(parseMarkdown("* item\n\n3. three\n4. four"), [
    { kind: "list", items: [{ marker: "•", blocks: [paragraph(text("item"))] }] },
    {
      kind: "list",
      items: [
        { marker: "3.", blocks: [paragraph(text("three"))] },
        { marker: "4.", blocks: [paragraph(text("four"))] },
      ],
    },
  ]);
});

// The first parser read every line on its own, so a nested list came out flat.
test("a nested list is nested", () => {
  assert.deepEqual(parseMarkdown("- a\n  - b"), [
    {
      kind: "list",
      items: [
        {
          marker: "•",
          blocks: [
            paragraph(text("a")),
            { kind: "list", items: [{ marker: "◦", blocks: [paragraph(text("b"))] }] },
          ],
        },
      ],
    },
  ]);
});

test("task items say whether they are done", () => {
  assert.deepEqual(parseMarkdown("- [ ] todo\n- [x] done"), [
    {
      kind: "list",
      items: [
        { marker: "•", checked: false, blocks: [paragraph(text("todo"))] },
        { marker: "•", checked: true, blocks: [paragraph(text("done"))] },
      ],
    },
  ]);
});

test("a quote holds blocks of its own", () => {
  assert.deepEqual(parseMarkdown("> quote\n>\n> - in a list"), [
    {
      kind: "quote",
      blocks: [
        paragraph(text("quote")),
        { kind: "list", items: [{ marker: "•", blocks: [paragraph(text("in a list"))] }] },
      ],
    },
  ]);
});

// Tables

test("parses a table with its alignment and formatted cells", () => {
  assert.deepEqual(parseMarkdown("| Agent | State |\n|:------|------:|\n| **claude** | busy |"), [
    {
      kind: "table",
      table: {
        header: [[text("Agent")], [text("State")]],
        align: ["left", "right"],
        rows: [[[{ kind: "bold", children: [text("claude")] }], [text("busy")]]],
        hiddenRows: 0,
      },
    },
  ]);
});

// Dropping short rows would lose data the agent actually produced.
test("pads and trims ragged rows to the header width", () => {
  const [block] = parseMarkdown("| a | b | c |\n|---|---|---|\n| 1 |\n| 1 | 2 | 3 | 4 |");
  assert.equal(block.kind, "table");
  if (block.kind !== "table") return;
  assert.deepEqual(block.table.rows.map((row) => row.map(plainText)), [
    ["1", "", ""],
    ["1", "2", "3"],
  ]);
});

test("bounds a table an agent might emit without end", () => {
  const long = parseMarkdown(["| a | b |", "|---|---|", ...Array(500).fill("| x | y |")].join("\n"));
  assert.equal(long[0].kind, "table");
  if (long[0].kind === "table") {
    assert.equal(long[0].table.rows.length, MAX_ROWS);
    assert.equal(long[0].table.hiddenRows, 500 - MAX_ROWS);
  }
  const columns = MAX_COLUMNS + 8;
  const wide = parseMarkdown(`| ${"c | ".repeat(columns)}\n|${"---|".repeat(columns)}`);
  assert.equal(wide.length, 1);
  assert.equal(wide[0].kind, "paragraph");
});

test("plainText drops the markup", () => {
  assert.equal(plainText(inline("**bold** `code` [l](https://a.dev)")), "bold code l");
});

// Agent output is untrusted. marked is not linear on every input, so these are
// the shapes measured to blow up, each at the largest size a message can be.

test("nesting is bounded, and the text inside it still shows", () => {
  for (const source of [
    ">".repeat(5000) + " deep",
    "- ".repeat(5000) + "deep",
    "1. ".repeat(5000) + "deep",
    Array.from({ length: 400 }, (_, index) => `${"  ".repeat(index)}- deep`).join("\n"),
  ]) {
    const blocks = parseMarkdown(source);
    assert.ok(nesting(blocks) <= MAX_NESTING + 1, source.slice(0, 20));
    assert.ok(shown(blocks).includes("deep"), source.slice(0, 20));
  }
});

test("pathological input parses quickly", () => {
  const size = MAX_MARKDOWN_CHARS;
  const inputs = {
    "unclosed emphasis": "**a *".repeat(size / 5),
    "emphasis per paragraph": ("**a *".repeat(120) + "\n\n").repeat(size / 602),
    "a word before an @": "a-".repeat(size / 2) + "@",
    "words before @s": ("a+".repeat(200) + "@ ").repeat(size / 402),
    "unclosed links": "[a](".repeat(size / 4),
    "nested emphasis": "*a ".repeat(size / 6) + "b" + " a*".repeat(size / 6),
    "deep quotes": ">".repeat(size - 2) + " x",
    "deep lists": Array.from({ length: 440 }, (_, index) => `${"  ".repeat(index)}- x`).join("\n"),
    "bullets on one line": "- ".repeat(size / 2) + "x",
  };
  for (const [name, source] of Object.entries(inputs)) {
    const started = performance.now();
    const blocks = parseMarkdown(source);
    const elapsed = performance.now() - started;
    // Before the guards the first of these took minutes and several threw.
    assert.ok(elapsed < 500, `${name}: ${Math.round(elapsed)} ms`);
    assert.ok(shown(blocks).length > 0, name);
  }
});

// Text over budget is shown without its formatting, never dropped.
test("text too costly to parse is still shown", () => {
  const source = "**a *".repeat(5000);
  assert.deepEqual(parseMarkdown(source), [paragraph(text(source))]);
});

// The budget is sized from real replies; a long, heavily formatted one must
// keep its formatting to the end.
test("a long real-shaped reply keeps its formatting throughout", () => {
  const section = "Some **bold** text with `code` and [a link](https://a.dev) and *it*.\n\n- item\n- item\n\n";
  const blocks = parseMarkdown(section.repeat(Math.floor(MAX_MARKDOWN_CHARS / section.length)));
  const last = blocks.findLast((block) => block.kind === "paragraph");
  assert.ok(last && last.kind === "paragraph");
  assert.equal(last.content[1].kind, "bold");
});

test("inlineWork refuses a long word before an @, not an address", () => {
  assert.equal(inlineWork(`${"a".repeat(300)}@b.dev`), Infinity);
  assert.ok(Number.isFinite(inlineWork("mail first.last+tag@example.com")));
  assert.ok(Number.isFinite(inlineWork(`${"a".repeat(300)} @b.dev`)));
});

// Random markup characters, seeded so a failure reproduces.
test("no arrangement of markup characters throws or stalls", () => {
  const alphabet = "*_[]()`<>!\\$@~#-|:.+=1a \n\n>";
  let seed = 42;
  const random = () => {
    seed = (seed * 1_103_515_245 + 12_345) % 2_147_483_648;
    return seed / 2_147_483_648;
  };
  const started = performance.now();
  for (let round = 0; round < 300; round += 1) {
    let source = "";
    for (let index = 0; index < 2000; index += 1) {
      source += alphabet[Math.floor(random() * alphabet.length)];
    }
    parseMarkdown(source);
  }
  assert.ok(performance.now() - started < 5000);
});

test("plain text passes through untouched", () => {
  assert.deepEqual(inline("nothing to mark up"), [text("nothing to mark up")]);
  assert.deepEqual(parseMarkdown(""), []);
});
