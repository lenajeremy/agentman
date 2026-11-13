// node --test web/src/thread/pictures.test.mts (Node strips the types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { imageFileInSummary, pictureRead } from "./pictures.ts";

const read = (summary: string | undefined, text = "") =>
  ({ id: "m", role: "tool", text, tool: { name: "Read", ...(summary === undefined ? {} : { summary }) } }) as never;

test("a picture with spaces in its name is a picture, as the Mac reads it", () => {
  assert.equal(imageFileInSummary("/Users/me/Desktop/09 Waitlist.png"), "/Users/me/Desktop/09 Waitlist.png");
  const shot = "/var/folders/x/T/Screenshot 2026-10-05 at 10.00.00 AM.png";
  assert.equal(imageFileInSummary(shot), shot);
  assert.equal(imageFileInSummary("  /w/a b.JPG  \nmore"), "/w/a b.JPG");
});

test("a path a shell would act on, or one needing cleaning, is no picture", () => {
  assert.equal(imageFileInSummary("/w/$(rm x).png"), null);
  assert.equal(imageFileInSummary("/w/a;b.png"), null);
  assert.equal(imageFileInSummary("/w//a.png"), null);
  assert.equal(imageFileInSummary("/w/../a.png"), null);
  assert.equal(imageFileInSummary("/w/a​.png"), null);
  assert.equal(imageFileInSummary("/w/notes.md"), null);
});

test("a summary that isn't a path is searched, without guessing at spaces", () => {
  assert.equal(imageFileInSummary("Read /w/shot.png lines 1-20"), "/w/shot.png");
  assert.equal(imageFileInSummary("Read 09 Waitlist.png"), null);
});

test("a file that mentions a picture is still the file read", () => {
  assert.equal(pictureRead(read("/w/README.md", "![Preview](/work/shot.png)")), null);
  assert.equal(pictureRead(read(undefined, "/work/shot.png")), "/work/shot.png");
  assert.equal(pictureRead(read("/w/my shot.png")), "/w/my shot.png");
});
