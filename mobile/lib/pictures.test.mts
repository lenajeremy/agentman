import assert from "node:assert/strict";
import test from "node:test";

import { looksLikeSvg, MAX_SVG_CHARS, svgSize } from "./pictures.ts";

test("badges and .svg files are drawn as SVG", () => {
  assert.equal(looksLikeSvg("https://img.shields.io/badge/license-MIT-blue.svg"), true);
  assert.equal(looksLikeSvg("https://img.shields.io/github/stars/a/b"), true);
  assert.equal(looksLikeSvg("https://github.com/a/b/actions/workflows/ci.yml/badge.svg"), true);
  assert.equal(looksLikeSvg("https://example.com/logo.SVG?raw=1"), true);
  assert.equal(looksLikeSvg("https://example.com/hero.png"), false);
  assert.equal(looksLikeSvg("not a url"), false);
});

test("an SVG's size comes from its root element", () => {
  assert.deepEqual(svgSize('<svg xmlns="http://www.w3.org/2000/svg" width="78" height="20" role="img">…</svg>'), { width: 78, height: 20 });
  assert.deepEqual(svgSize('<?xml version="1.0"?>\n<svg viewBox="0 0 120 30"><rect/></svg>'), { width: 120, height: 30 });
  assert.deepEqual(svgSize('<svg width="100%" height="100%" viewBox="0, 0, 64, 32"/>'), { width: 64, height: 32 });
  assert.deepEqual(svgSize("<svg width='50px' height='10px'/>"), { width: 50, height: 10 });
  assert.equal(svgSize("<html><body>Not found</body></html>"), null);
  assert.equal(svgSize("<svg><g/></svg>"), null);
  assert.equal(svgSize(`<svg width="1" height="1">${"x".repeat(MAX_SVG_CHARS)}</svg>`), null);
});
