import assert from "node:assert/strict";
import { test } from "node:test";

import {
  appendPage,
  clockTime,
  commitDate,
  commitMatches,
  dayLabel,
  groupByDay,
  historyError,
  initials,
  isHistoryRequest,
  shortHash,
  splitTrailers,
  trailerValue,
  unwrapMessage,
} from "./commits.ts";
import type { WorkspaceCommit } from "./protocol.ts";

const commit = (hash: string, subject = "Subject"): WorkspaceCommit => ({
  hash: hash.padEnd(40, "0"),
  subject,
  author: "Jeremiah Lena",
  authoredAt: 0,
});

test("an older Mac's refusal says how to get commit history", () => {
  assert.equal(
    historyError("unsupported request: list_commits"),
    "Update agentman on your Mac to see commits from your phone.",
  );
  assert.equal(historyError("that commit is not in this repository"), "that commit is not in this repository");
  assert.ok(isHistoryRequest("commit_diff"));
  assert.ok(!isHistoryRequest("file_diff"));
});

test("hashes are cut to git's short form", () => {
  assert.equal(shortHash("50812ec4f1a2b3c4d5e6f708192a3b4c5d6e7f80"), "50812ec");
});

test("a commit's date leaves the year out only when it is this one", () => {
  const now = new Date(2026, 9, 12, 9, 0);
  assert.equal(commitDate(new Date(2026, 9, 11, 14, 5).getTime(), now), "11 Oct at 14:05");
  assert.equal(commitDate(new Date(2025, 0, 3, 9, 7).getTime(), now), "3 Jan 2025 at 09:07");
});

test("a hand-wrapped message is re-flowed but keeps its structure", () => {
  const body = [
    "iOS doesn't slant custom fonts, so italics",
    "rendered upright.",
    "",
    "- Added sansItalic and sansBoldItalic to the",
    "  theme, loaded in useFonts.",
    "- Headings pass bold.",
    "  - A nested point",
    "",
    "    indented code",
    "    more code",
    "",
    "Changelog: Italic text in agent replies now",
    "looks italic.",
    "Co-Authored-By: Someone <a@example.com>",
  ].join("\n");
  assert.equal(unwrapMessage(body), [
    "iOS doesn't slant custom fonts, so italics rendered upright.",
    "",
    "- Added sansItalic and sansBoldItalic to the theme, loaded in useFonts.",
    "- Headings pass bold.",
    "  - A nested point",
    "",
    "    indented code",
    "    more code",
    "",
    "Changelog: Italic text in agent replies now looks italic.",
    "Co-Authored-By: Someone <a@example.com>",
  ].join("\n"));
  assert.equal(unwrapMessage("one\r\ntwo\r\n\r\n"), "one two");
  assert.equal(unwrapMessage(""), "");
});

test("the filter matches a subject, an author or the start of a hash", () => {
  const shown = commit("50812ec", "Show links that an agent wraps in bold");
  assert.ok(commitMatches(shown, "bold"));
  assert.ok(commitMatches(shown, "jeremiah"));
  assert.ok(commitMatches(shown, "50812"));
  assert.ok(!commitMatches(shown, "812ec"));
  assert.ok(commitMatches(shown, ""));
});

test("a page that repeats a commit already shown adds it once", () => {
  const shown = [commit("a1"), commit("b2")];
  const merged = appendPage(shown, [commit("b2"), commit("c3")]);
  assert.deepEqual(merged.map((c) => c.hash.slice(0, 2)), ["a1", "b2", "c3"]);
});

test("days are named the way people say them", () => {
  const now = new Date(2026, 9, 12, 9, 0); // a Monday
  assert.equal(dayLabel(new Date(2026, 9, 12, 0, 1).getTime(), now), "Today");
  assert.equal(dayLabel(new Date(2026, 9, 11, 23, 59).getTime(), now), "Yesterday");
  assert.equal(dayLabel(new Date(2026, 9, 8, 14, 0).getTime(), now), "Thursday");
  assert.equal(dayLabel(new Date(2026, 9, 5, 14, 0).getTime(), now), "5 Oct");
  assert.equal(dayLabel(new Date(2025, 11, 31, 14, 0).getTime(), now), "31 Dec 2025");
  // A clock that runs ahead of the phone's is not "today".
  assert.equal(dayLabel(new Date(2026, 9, 14, 9, 0).getTime(), now), "14 Oct");
  assert.equal(clockTime(new Date(2026, 9, 12, 7, 5).getTime()), "07:05");
});

test("commits are grouped by day, without being reordered", () => {
  const now = new Date(2026, 9, 12, 9, 0);
  const at = (day: number, hash: string) => ({
    ...commit(hash),
    authoredAt: new Date(2026, 9, day, 8, 0).getTime(),
  });
  const days = groupByDay([at(12, "a"), at(12, "b"), at(11, "c"), at(3, "d"), at(12, "e")], now);
  assert.deepEqual(days.map((day) => [day.label, day.commits.length]), [
    ["Today", 2], ["Yesterday", 1], ["3 Oct", 1], ["Today", 1],
  ]);
  assert.equal(new Set(days.map((day) => day.key)).size, days.length);
  assert.deepEqual(groupByDay([], now), []);
});

test("a person's mark is their first and last initial", () => {
  assert.equal(initials("Jeremiah Lena"), "JL");
  assert.equal(initials("Ada Byron Lovelace"), "AL");
  assert.equal(initials("lenajeremy"), "L");
  assert.equal(initials("  "), "?");
  assert.equal(initials("Émile Zola"), "ÉZ");
});

test("trailers are split from the message only when they end it", () => {
  const body = [
    "Italic text rendered upright.",
    "",
    "Changelog: Italic text in replies is now",
    "  actually italic.",
    "Co-Authored-By: Claude <noreply@anthropic.com>",
  ].join("\n");
  assert.deepEqual(splitTrailers(body), {
    text: "Italic text rendered upright.",
    trailers: [
      { key: "Changelog", value: "Italic text in replies is now actually italic." },
      { key: "Co-Authored-By", value: "Claude <noreply@anthropic.com>" },
    ],
  });
  // A last paragraph that is prose, even one beginning "Note:", stays put.
  const prose = "First.\n\nNote: this matters\nbecause of that.";
  assert.deepEqual(splitTrailers(prose), { text: prose, trailers: [] });
  assert.deepEqual(splitTrailers("Changelog: Only a trailer."), {
    text: "",
    trailers: [{ key: "Changelog", value: "Only a trailer." }],
  });
  assert.deepEqual(splitTrailers(""), { text: "", trailers: [] });
  assert.equal(trailerValue("Claude <noreply@anthropic.com>"), "Claude");
  assert.equal(trailerValue("Fixes a crash on <launch>"), "Fixes a crash on <launch>");
});
