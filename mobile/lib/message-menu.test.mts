import assert from "node:assert/strict";
import { test } from "node:test";

import {
  layoutMessageMenu,
  MENU_EDGE,
  MENU_GAP,
  menuHeight,
} from "./message-menu.ts";

// An iPhone 17 Pro-ish screen, with the notch and home indicator.
const screen = { width: 402, height: 874 };
const insets = { top: 62, bottom: 34 };

test("a message with room below keeps its place and the menu opens under it", () => {
  const anchor = { x: 180, y: 300, width: 200, height: 44 };
  const layout = layoutMessageMenu({ anchor, rows: 2, screen, insets });
  assert.deepEqual(layout.bubble, anchor, "the message must not move when held");
  assert.equal(layout.placement, "below");
  assert.equal(layout.menu.y, anchor.y + anchor.height + MENU_GAP);
  assert.equal(layout.clipped, false);
});

// The composer sits at the bottom, so the most recent message — the one you
// just sent — is the one most often held, and it has no room below.
test("a message near the bottom keeps its place and the menu opens above", () => {
  const anchor = { x: 180, y: 760, width: 200, height: 44 };
  const layout = layoutMessageMenu({ anchor, rows: 2, screen, insets });
  assert.deepEqual(layout.bubble, anchor);
  assert.equal(layout.placement, "above");
  assert.equal(layout.menu.y + layout.menu.height + MENU_GAP, anchor.y);
});

// A sent message is on the right, so its menu lines up with its right edge.
test("the menu lines up with a sent message's right edge", () => {
  const anchor = { x: 180, y: 300, width: 200, height: 44 };
  const layout = layoutMessageMenu({ anchor, rows: 2, screen, insets });
  assert.equal(layout.menu.x + layout.menu.width, anchor.x + anchor.width);
});

// A short message on the right would otherwise drag its menu off-screen.
test("the menu never leaves the screen", () => {
  const anchor = { x: 360, y: 300, width: 30, height: 44 };
  const layout = layoutMessageMenu({ anchor, rows: 2, screen, insets });
  assert.ok(layout.menu.x >= MENU_EDGE, "off the left edge");
  assert.ok(layout.menu.x + layout.menu.width <= screen.width - MENU_EDGE, "off the right edge");
});

// A message scrolled half under the header has no room above or where it is:
// it moves down just enough, and still does not overlap the menu.
test("a message partly off the top moves down into view", () => {
  const anchor = { x: 180, y: 20, width: 200, height: 120 };
  const layout = layoutMessageMenu({ anchor, rows: 2, screen, insets });
  assert.ok(layout.bubble.y >= insets.top + MENU_EDGE, "still under the notch");
  assert.equal(layout.bubble.height, anchor.height);
  assert.ok(layout.menu.y >= layout.bubble.y + layout.bubble.height, "menu overlaps the message");
  assert.equal(layout.clipped, false);
});

// A pasted page of instructions is taller than the screen. It is clipped from
// the bottom — its opening line is what identifies it — and the menu still fits.
test("a message taller than the screen is clipped and the menu still fits", () => {
  const anchor = { x: 40, y: 80, width: 340, height: 1600 };
  const rows = 3;
  const layout = layoutMessageMenu({ anchor, rows, screen, insets });
  const bottom = screen.height - insets.bottom - MENU_EDGE;
  assert.equal(layout.clipped, true);
  assert.ok(layout.bubble.height < anchor.height);
  assert.ok(layout.menu.y + menuHeight(rows) <= bottom, "menu runs off the bottom");
  assert.equal(layout.menu.y, layout.bubble.y + layout.bubble.height + MENU_GAP);
});

test("more rows make a taller menu", () => {
  assert.ok(menuHeight(3) > menuHeight(2));
});
