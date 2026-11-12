// The web app end to end, in headless Chromium and WebKit (Safari's engine),
// against the test stack run.sh prepares. Each check prints PASS or FAIL
// and why; the count comes last, and the exit code says whether any failed.
//
//   node e2e.mjs [chromium|webkit|all] [area…]
//
// Pages are signed in as `am web` signs a browser in. What the test daemon
// can't give (a pairing code, plan limits, an agent's question, an offer) is
// faked in the page (fakes/); what costs or publishes is a stand-in.
import { chromium, webkit } from "playwright-core";
import { execFileSync, execSync, spawn } from "node:child_process";
import { appendFileSync, copyFileSync, existsSync, readFileSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";

const HERE = new URL(".", import.meta.url).pathname;
const T = `${(process.env.TMPDIR || tmpdir()).replace(/\/$/, "")}/amx`;
const H = `${T}/home`;
const BASE = "http://127.0.0.1:18797";
const DEMO_DIR = `${H}/projects/demo`;
const ID = {
  demo: "claude:6f1c2a9e-3b7d-4c55-9a10-2d8e4f6b7c01",
  bugs: "claude:0b9d4e21-7c3a-4f6e-8d12-5a6b7c8d9e10",
  pick: "claude:2f6a0b5c-7d9e-4f1a-8b3c-6e7f8a9b0c1d",
  empty: "claude:0d4b8f3e-5a7c-4b9d-8e1f-4c5d6e7f8a9b",
};
const fake = (name) => readFileSync(`${HERE}fakes/${name}.js`, "utf8");
// Copying goes to a stub, never the Mac's clipboard; new windows are noted.
const STUBS = `
  Object.defineProperty(navigator, "clipboard", { value: { writeText: async (t) => { window.__copied = t; }, write: async (items) => { window.__copiedItems = items; }, readText: async () => "", read: async () => [] } });
  window.__opened = []; window.open = (u) => { window.__opened.push(String(u)); return null; };
  window.__links = []; { const P = BroadcastChannel.prototype.postMessage; BroadcastChannel.prototype.postMessage = function (m) { if (this.name === "agentman-links") window.__links.push(String(m)); return P.call(this, m); }; }
`;

const args = process.argv.slice(2);
const which = args[0] && !["chromium", "webkit", "all"].includes(args[0]) ? "all" : args[0] || "all";
const areas = args.filter((a) => !["chromium", "webkit", "all"].includes(a));
const want = (area) => !areas.length || areas.includes(area);

let passed = 0;
const failures = [];
// What any page threw or logged as an error during the check running.
let pageErrors = [];
const errorsSeen = [];
let context;
let engineName = "";

function signInLink() {
  return execFileSync(`${T}/am`, ["web", "--print", "--config-home", H], { encoding: "utf8" }).trim().split("\n").pop();
}

/** A page of the app, signed in, with fakes set up before its scripts. */
async function open(query = "", { init = [], size = [1400, 900], dark = false, wait = "" } = {}) {
  const page = await context.newPage();
  page.on("pageerror", (e) => pageErrors.push(`thrown: ${String(e.message).split("\n")[0].slice(0, 200)}`));
  // A failed load is told by its address below, not by the console's line.
  page.on("console", (m) => m.type() === "error" && !m.text().startsWith("Failed to load resource") && pageErrors.push(`console: ${m.text().split("\n")[0].slice(0, 200)}`));
  page.on("response", (r) => r.status() >= 400 && pageErrors.push(`http ${r.status()} ${r.request().method()} ${r.url().replace(BASE, "").slice(0, 160)}`));
  page.on("requestfailed", (r) => !/ERR_ABORTED|cancelled/i.test(r.failure()?.errorText ?? "") && pageErrors.push(`failed: ${r.url().replace(BASE, "").slice(0, 120)} (${r.failure()?.errorText})`));
  await page.setViewportSize({ width: size[0], height: size[1] });
  await page.emulateMedia({ colorScheme: dark ? "dark" : "light" });
  await page.addInitScript(STUBS);
  for (const script of init) await page.addInitScript(script);
  await page.goto(`${BASE}/${query ? "?" + query : ""}`);
  await page.waitForFunction(() => document.querySelector(".sidebar-footer, .board-column, .settings, .editor-window, .new-agent-line, .usage-page"), null, { timeout: 15000 });
  if (wait) await page.waitForSelector(wait, { timeout: 15000 });
  return page;
}

/** One check. `expected`: errors it causes on purpose (am serve stopped, a
 *  picture the test project lacks), as patterns; any other is reported. */
async function check(name, run, { expected = [] } = {}) {
  const started = Date.now();
  pageErrors = [];
  try {
    await run();
    passed++;
    console.log(`PASS ${engineName} ${name} (${((Date.now() - started) / 1000).toFixed(1)}s)`);
  } catch (err) {
    // Playwright's first line, and which element it was waiting for.
    const lines = String(err?.message ?? err).split("\n");
    const waiting = lines.find((l) => /waiting for|intercepts pointer/.test(l))?.trim();
    const why = (lines[0] + (waiting ? ` [${waiting}]` : "")).slice(0, 320);
    failures.push(`${engineName} ${name}: ${why}`);
    console.log(`FAIL ${engineName} ${name}: ${why}`);
    // What the page showed, for working out why: a picture and a few facts.
    const page = context.pages().at(-1);
    if (page) {
      const shot = `${T}/e2e-fail-${engineName}-${failures.length}.png`;
      await page.screenshot({ path: shot }).catch(() => {});
      const state = await page.evaluate(() => ({
        url: location.search.slice(0, 80),
        focus: (document.activeElement?.tagName ?? "") + "." + String(document.activeElement?.className ?? "").slice(0, 30),
        menu: [...document.querySelectorAll(".page-menu-item")].map((m) => m.innerText.trim()).slice(0, 8),
        sheet: document.querySelector(".page-sheet, .sheet-backdrop")?.innerText.slice(0, 80) ?? null,
      })).catch(() => ({}));
      console.log(`     ${shot} ${JSON.stringify(state)}`);
    }
  } finally {
    for (const page of context.pages()) await page.close().catch(() => {});
    // A browser's notice, not an error: a ResizeObserver's work ran into the
    // next frame.
    const always = [
      /ResizeObserver loop/,
      // The worktrees the worktree check made and removed: am serve still
      // lists their folders, and the logo it found in one while it was there.
      /^http 404 GET \/__editor\/file\?path=[^ ]*Worktrees%2Fdemo%2Fe2e-wt-/,
    ];
    const unexpected = [...new Set(pageErrors)].filter((e) => ![...always, ...expected].some((re) => re.test(e)));
    if (unexpected.length) {
      errorsSeen.push(`${engineName} ${name}: ${unexpected.join(" | ")}`);
      console.log(`ERRORS ${engineName} ${name}: ${unexpected.join(" | ")}`);
    }
  }
}

function expect(ok, what) {
  if (!ok) throw new Error(what);
}

/** Waits until fn, run in the page, is truthy; its value. */
async function until(page, fn, arg, what, timeout = 10000) {
  try {
    const handle = await page.waitForFunction(fn, arg, { timeout });
    return await handle.jsonValue();
  } catch {
    const now = await page.evaluate(fn, arg).catch((e) => "error: " + e.message);
    throw new Error(`${what} (got ${JSON.stringify(now)?.slice(0, 160)})`);
  }
}

/** Clicks the first element matching selector whose words are exactly text. */
async function click(page, selector, text) {
  const exact = text === undefined ? null : new RegExp(`^\\s*${text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`);
  const target = exact ? page.locator(selector).filter({ hasText: exact }).first() : page.locator(selector).first();
  await target.click({ timeout: 8000 });
}

/** A menu item by its words; a ticked one reads "✓ Muse". */
async function menu(page, text) {
  const words = new RegExp(`^\\s*(✓\\s*)?${text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`);
  await page.locator(".page-menu-item").filter({ hasText: words }).first().click({ timeout: 8000 });
}

/** Waits until the editor's code is drawn, then puts the keyboard in it. */
async function intoCode(page) {
  await until(page, () => { try { return document.querySelector("iframe.monaco").contentDocument.querySelectorAll(".view-line").length > 0; } catch { return false; } }, null, "the code drawn", 20000);
  await page.locator("iframe.monaco").click();
}

/** Waits until a palette over the page has the keyboard. */
const paletteReady = (page) => until(page, () => document.activeElement?.tagName === "INPUT" && !!document.activeElement.closest(".palette-layer, .palette"), null, "the palette has the keyboard");

/** Picks a session menu's item once it's offered: End Session only once the
 *  agent has stopped (Muse holds its turn open a few seconds after it). */
async function sessionMenu(page, item, timeout = 30000) {
  const until = Date.now() + timeout;
  for (;;) {
    await click(page, "button.ellipsis");
    // Offered, and not greyed out while the turn finishes.
    const has = await page.locator(".page-menu-item:not(.disabled)").filter({ hasText: item }).count();
    if (has) return menu(page, item);
    await page.keyboard.press("Escape");
    if (Date.now() > until) throw new Error(`the menu never offered ${item}`);
    await sleep(1000);
  }
}

/** Waits until a selector's count stops changing for a moment. */
async function settled(page, selector) {
  let last = -1;
  for (let i = 0; i < 20; i++) {
    const now = await page.locator(selector).count();
    if (now === last && now > 0) return;
    last = now;
    await sleep(500);
  }
}
const sheetButton = (page, text) => click(page, ".page-sheet-buttons button", text);

/** One request to the test am serve over its app socket, and the answer. */
async function daemon(request) {
  const endpoint = JSON.parse(readFileSync(`${H}/.agentman/app.json`, "utf8"));
  const ws = new WebSocket(`${endpoint.url}?token=${encodeURIComponent(endpoint.token)}`);
  return new Promise((resolve, reject) => {
    let buffer = "";
    const timer = setTimeout(() => reject(new Error(`am serve didn't answer ${request.type}`)), 20000);
    ws.onopen = () => ws.send(JSON.stringify({ v: 2, id: "e2e-1", to: "daemon", payload: request }) + "\n");
    ws.onmessage = (e) => {
      buffer += String(e.data);
      for (let i; (i = buffer.indexOf("\n")) >= 0; ) {
        const env = JSON.parse(buffer.slice(0, i));
        buffer = buffer.slice(i + 1);
        if (env.replyTo === "e2e-1") {
          clearTimeout(timer);
          ws.close();
          resolve(env.payload);
        }
      }
    };
    ws.onerror = () => reject(new Error("couldn't reach am serve"));
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
/** Empties the message box as typing would, so no draft is kept. */
async function clearBox(page) {
  await page.locator(".message-field").click();
  await page.keyboard.press("Meta+a");
  await page.keyboard.press("Backspace");
}
const keyboardIn = (page) => page.evaluate(() => document.activeElement?.className?.toString() || document.activeElement?.tagName);

// MARK: - The checks

async function shell() {
  await check("shell: signed in, connected, every agent listed", async () => {
    const page = await open(`agent=${ID.bugs}`);
    await until(page, () => document.querySelector(".sidebar-footer")?.innerText.includes("Connected"), null, "the footer says Connected");
    const agents = await until(page, () => document.querySelectorAll(".sidebar .agent-line1").length >= 8 && document.querySelectorAll(".sidebar .agent-line1").length, null, "eight agents or more");
    expect(agents >= 8, `${agents} agents`);
  });

  await check("shell: an agent waiting on a question has the Needs you section", async () => {
    const page = await open(`agent=${ID.bugs}`, { init: [fake("question")] });
    await until(page, () => /Needs you[\s\S]*pick/.test(document.querySelector(".sidebar")?.innerText ?? ""), null, "Needs you lists pick");
  });

  await check("shell: the address follows the page; Back, Forward and reload keep it", async () => {
    const page = await open("");
    await page.keyboard.press("Meta+k");
    await page.keyboard.type("demo");
    await page.keyboard.press("Enter");
    await until(page, (id) => decodeURIComponent(location.search) === `?agent=${id}`, ID.demo, "the address names demo");
    await page.keyboard.press("Meta+k");
    await page.keyboard.type("usage");
    await page.keyboard.press("Enter");
    await until(page, () => location.search === "?page=usage" && !!document.querySelector(".usage-page"), null, "the address names Usage");
    await page.goBack();
    await until(page, () => location.search.startsWith("?agent=") && !!document.querySelector(".thread-item"), null, "Back shows demo");
    await page.goForward();
    await until(page, () => !!document.querySelector(".usage-page"), null, "Forward shows Usage");
    await page.goBack();
    await page.reload();
    await until(page, () => !!document.querySelector(".thread-item"), null, "a reload shows demo again");
  });

  await check("shell: ⌘K, then Escape, gives the message box the keyboard back, the cursor where it was", async () => {
    const page = await open(`agent=${ID.bugs}`, { wait: ".message-field" });
    await click(page, ".message-field");
    await page.keyboard.type("hello wor");
    await page.keyboard.press("Meta+k");
    await until(page, () => !!document.querySelector('input[placeholder^="Go to an agent"]'), null, "the palette is open");
    await page.keyboard.press("Escape");
    await page.keyboard.type("ld");
    const text = await page.evaluate(() => document.querySelector(".message-field").innerText);
    await clearBox(page);
    expect(text === "hello world", `the box reads ${JSON.stringify(text)}`);
  });

  await check("shell: Escape clears the sidebar's search; a message search finds and opens at the message", async () => {
    const page = await open(`agent=${ID.bugs}`);
    await click(page, ".sidebar input");
    await page.keyboard.type("uploader");
    await until(page, () => !!document.querySelector(".sidebar .agent-match") && document.querySelector(".sidebar").innerText.includes("In messages"), null, "a matching line and In messages");
    await click(page, ".sidebar .agent-match");
    await until(page, () => /\d+\/\d+/.test(document.querySelector(".find-bar")?.innerText ?? ""), null, "the find bar counts the matches");
    await click(page, ".sidebar input");
    await page.keyboard.press("Escape");
    await until(page, () => document.querySelector(".sidebar input").value === "" && document.querySelectorAll(".sidebar .agent-line1").length >= 8, null, "the search is cleared");
  });

  await check("shell: Connect a Device… opens Settings pairing", async () => {
    const page = await open(`agent=${ID.bugs}`);
    await page.keyboard.press("Meta+k");
    await page.keyboard.type("connect a device");
    await page.keyboard.press("Enter");
    await until(page, () => window.__opened.some((u) => u.includes("view=settings") && u.includes("page=pair")), null, "Settings opened on pair");
  });
}

async function board() {
  await check("board: columns, cards and headline", async () => {
    const page = await open("", { wait: ".board-card" });
    const columns = await page.evaluate(() => [...document.querySelectorAll(".board-column")].map((c) => c.innerText.split("\n")[0]));
    expect(columns.join() === "Needs you,Working,Idle", `columns ${columns}`);
    expect(await page.locator(".board-card").count() >= 8, "eight cards or more");
  });

  await check("board: the folder menu lists the folders and shows one", async () => {
    const page = await open("", { wait: ".board-card" });
    await click(page, '[title="Show one folder\'s agents"]');
    await until(page, () => [...document.querySelectorAll(".page-menu-item")].some((m) => m.innerText.trim() === "pics"), null, "folders in the menu");
    await menu(page, "pics");
    await until(page, () => [...document.querySelectorAll(".board-card .board-card-name")].every((e) => e.closest(".board-card").innerText.includes("pics")), null, "only pics' agents");
  });

  await check("board: ⌘F searches, Escape clears", async () => {
    const page = await open("", { wait: ".board-card" });
    const all = await page.locator(".board-card").count();
    await page.keyboard.press("Meta+f");
    await page.keyboard.type("shots");
    await until(page, (n) => document.querySelectorAll(".board-card").length < n, all, "fewer cards");
    await page.keyboard.press("Escape");
    await until(page, (n) => document.querySelectorAll(".board-card").length === n, all, "every card again");
  });

  await check("board: the commit sheet opens, closes on Escape, and gives the keyboard back", async () => {
    const page = await open("", { wait: ".board-card" });
    // The cards' git buttons come as each folder's status does.
    await settled(page, ".board-card button");
    // From the keyboard: Safari's buttons don't take it when clicked.
    await page.locator(".board-card button").filter({ hasText: /^\s*Commit\s*$/ }).first().focus();
    await page.keyboard.press("Enter");
    await until(page, () => !!document.querySelector(".sheet-backdrop"), null, "the sheet");
    await page.keyboard.press("Escape");
    await until(page, () => !document.querySelector(".sheet-backdrop") && document.activeElement?.innerText?.trim() === "Commit", null, "closed, the keyboard on Commit");
  });

  await check("board: Tonight opens, takes a task, and closes on Escape", async () => {
    const page = await open("", { wait: ".board-card" });
    await page.locator('[title="Work lined up for tonight"]').focus();
    await page.keyboard.press("Enter");
    await until(page, () => !!document.querySelector(".board-popover"), null, "the popover");
    await click(page, ".board-popover textarea, .board-popover input");
    await page.keyboard.type("Nothing, it's a test");
    await page.keyboard.press("Escape");
    await until(page, () => !document.querySelector(".board-popover") && document.activeElement?.getAttribute("title") === "Work lined up for tonight", null, "closed, the keyboard on its button");
  });

  await check("board: an offer reads as Agentman's and is answered with the option chosen", async () => {
    const page = await open("", { init: [fake("offer")], wait: ".am-compact" });
    const card = await page.evaluate(() => document.querySelector(".am-compact").innerText);
    expect(card.startsWith("Agentman offers"), `the card begins ${JSON.stringify(card.slice(0, 30))}`);
    await page.locator(".am-compact button").filter({ hasText: "Nudge it" }).click();
    await until(page, () => window.__sentCards?.[0]?.optionKey === "nudge" && !document.querySelector(".am-compact"), null, "sent nudge, card gone");
  });

  await check("board: asking Agentman answers in the chat beside", async () => {
    const page = await open("", { wait: ".board-card" });
    const box = page.locator('[placeholder="Ask Agentman about your agents"], [data-placeholder="Ask Agentman about your agents"]').first();
    await box.click();
    await page.keyboard.type("What needs me?");
    await page.keyboard.press("Enter");
    await until(page, () => (document.querySelector(".board-chat, [class*=board-chat]")?.innerText ?? "").includes("Fake Agentman here"), null, "the fake model's reply", 20000);
  });

  await check("board: Ship commits, pushes and opens a pull request (stand-in gh)", async () => {
    const page = await open("", { wait: ".board-card" });
    await click(page, ".board-column-action", "Ship");
    await until(page, () => [...document.querySelectorAll(".board-ship-row")].some((r) => r.innerText.startsWith("demo") && r.querySelector("input")?.checked), null, "demo chosen", 15000);
    await page.locator(".board-ship-buttons button").filter({ hasText: /^\s*Ship \d+\s*$/ }).click();
    await until(page, () => [...document.querySelectorAll(".board-ship-row")].some((r) => r.innerText.includes("Pull request open")), null, "the pull request is open", 40000);
    const log = readFileSync(`${T}/fakebin/gh.log`, "utf8").trim().split("\n").pop();
    expect(log.includes("pr create --fill --head e2e-ship-"), `gh was asked: ${log}`);
  });
}

async function thread() {
  await check("thread: messages, worked lines and code blocks", async () => {
    const page = await open(`agent=${ID.demo}`, { wait: ".thread-item" });
    const counts = await until(page, () => {
      const c = { items: document.querySelectorAll(".thread-item").length, worked: document.querySelectorAll(".worked-line").length, code: document.querySelectorAll(".md-code-wrap").length };
      return c.items > 10 && c.worked > 0 && c.code > 0 && c;
    }, null, "messages, worked lines and code");
    expect(counts.worked >= 3, `${counts.worked} worked lines`);
  });

  await check("thread: a code block's Copy copies it", async () => {
    const page = await open(`agent=${ID.demo}`, { wait: ".md-code-wrap" });
    // The newest code block brought into view, its corner clear of the header.
    await page.evaluate(() => [...document.querySelectorAll(".md-code-wrap")].at(-1).scrollIntoView({ block: "center" }));
    const index = await until(page, () => [...document.querySelectorAll(".md-code-wrap")].findIndex((w) => { const r = w.getBoundingClientRect(); return r.top > 90 && r.top < 650; }) + 1, null, "a code block in view") - 1;
    await page.locator(".md-code-wrap").nth(index).hover();
    await page.locator(".md-code-wrap").nth(index).locator(".code-copy").click();
    await until(page, () => (window.__copied ?? "").length > 20, null, "copied the code");
  });

  await check("thread: ⌘F finds, and Escape gives the keyboard back", async () => {
    const page = await open(`agent=${ID.demo}`, { wait: ".message-field" });
    await click(page, ".message-field");
    await page.keyboard.press("Meta+f");
    await page.keyboard.type("retries");
    await until(page, () => /^\d+\/\d+/.test(document.querySelector(".find-bar")?.innerText ?? "") && !document.querySelector(".find-bar").innerText.startsWith("0/"), null, "matches counted");
    await page.keyboard.press("Escape");
    await until(page, () => !document.querySelector(".find-bar") && document.activeElement?.classList.contains("message-field"), null, "closed, the keyboard in the box");
  });

  await check("thread: a typed answer goes as the answer; the box keeps the keyboard after sending", async () => {
    const page = await open(`agent=${ID.pick}`, { init: [fake("question")], wait: ".message-field" });
    await until(page, () => document.querySelector(".message-field").dataset.placeholder === "Type an answer, or pick one above", null, "the box asks for an answer");
    await click(page, ".message-field");
    await page.keyboard.type("Use the second one");
    await page.keyboard.press("Enter");
    await until(page, () => window.__sent?.[0]?.type === "answer_question" && window.__sent[0].answerText === "Use the second one", null, "sent as the answer");
    // The box is off while a message sends, as the Mac's is; on again, it
    // has the keyboard without a click.
    await until(page, () => document.querySelector(".message-field").dataset.placeholder === "Message pick" && document.querySelector(".message-field").isContentEditable && document.activeElement?.classList.contains("message-field"), null, "the question gone, the box on again with the keyboard");
    await page.keyboard.type("thanks");
    await page.keyboard.press("Enter");
    await until(page, () => window.__sent?.[1]?.type === "send_message" && window.__sent[1].text === "thanks", null, "typed and sent without a click");
  });

  await check("thread: @ suggests files", async () => {
    const page = await open(`agent=${ID.demo}`, { wait: ".message-field" });
    await click(page, ".message-field");
    await page.keyboard.type("look at @READ");
    await until(page, () => (document.querySelector('[aria-label="Files to mention"]')?.innerText ?? "").includes("README.md"), null, "README.md suggested");
    await page.keyboard.press("Escape");
    await clearBox(page);
  });

  await check("thread: a refused move says why", async () => {
    const page = await open(`agent=${ID.demo}`, { init: ["window.__refuse = true;", fake("handoff")], wait: "button.ellipsis" });
    await click(page, "button.ellipsis");
    await menu(page, "Open in Terminal");
    await until(page, () => (document.querySelector(".composer-wrap")?.innerText ?? "").includes("in the middle of a turn"), null, "the reason under the box");
  });

  await check("thread: Open in Terminal moves it, and then offers Take Over (faked move)", async () => {
    const page = await open(`agent=${ID.demo}`, { init: [fake("handoff")], wait: "button.ellipsis" });
    await click(page, "button.ellipsis");
    await menu(page, "Open in Terminal");
    await until(page, () => window.__handoffs.includes("claude:6f1c2a9e-3b7d-4c55-9a10-2d8e4f6b7c01") && window.__arrived?.had, null, "moved, and arrived in its terminal");
    await page.waitForTimeout(300);
    await click(page, "button.ellipsis");
    await until(page, () => [...document.querySelectorAll(".page-menu-item")].some((m) => m.innerText.trim() === "Take Over from Terminal…"), null, "Take Over offered now");
    await page.keyboard.press("Escape");
  });

  await check("thread: a watched session's Take Over asks first; Cancel keeps it, Take Over moves it (faked move)", async () => {
    const page = await open(`agent=${ID.bugs}`, { init: [fake("handoff")], wait: ".resume-bar" });
    const bar = await page.evaluate(() => document.querySelector(".resume-bar")?.innerText.replace(/\s+/g, " ").trim());
    expect(bar === "You're watching this session It runs in a terminal. Take it over to message it here; its terminal closes. Take Over…", `the bar: ${bar}`);
    await click(page, ".resume-bar button", "Take Over…");
    await until(page, () => (document.querySelector(".page-sheet")?.innerText ?? "").includes("Take this session over from its terminal?"), null, "the question");
    await sheetButton(page, "Cancel");
    await page.waitForTimeout(400);
    expect(await page.evaluate(() => window.__handoffs.length === 0), "Cancel moved it");
    await click(page, ".resume-bar button", "Take Over…");
    await sheetButton(page, "Take Over");
    await until(page, () => window.__handoffs.includes("claude:0b9d4e21-7c3a-4f6e-8d12-5a6b7c8d9e10"), null, "asked am serve to move it");
  });

  await check("thread: the right panel's tabs", async () => {
    const page = await open(`agent=${ID.demo}`, { wait: ".thread-item" });
    await page.keyboard.press("Meta+Shift+d");
    await until(page, () => document.querySelectorAll(".right-panel .panel-tab").length > 0, null, "the panel's tabs");
  });

  await check("thread: a working agent counts its time", async () => {
    const page = await open(`agent=${ID.demo}`, { init: [fake("busy")], wait: ".thread-item" });
    await until(page, () => [...document.querySelectorAll(".worked-line")].some((w) => w.innerText.startsWith("Working for") && w.querySelector(".ring-loader")), null, "Working for, with the loader");
  });

  await check("thread: ending the agent you're on keeps its page", async () => {
    const page = await open(`agent=${ID.empty}`, { wait: "button.ellipsis" });
    await click(page, "button.ellipsis");
    await menu(page, "End Session…");
    await sheetButton(page, "End Session");
    await until(page, () => (document.querySelector(".resume-bar")?.innerText ?? "").startsWith("Off your list"), null, "Off your list, on the same page");
  });

  await check("thread: an agent that quits in its terminal keeps its page, to resume", async () => {
    // A throwaway agent: bugs' conversation under an id of its own, its
    // process a sleep that is then killed, as a Ctrl-C or a crash would.
    const sid = "5c3e7a10-9d2b-4f1e-8a6c-3b4d5e6f7a8b";
    const cwd = `${H}/projects/bugs`;
    const dir = `${H}/.claude/projects/${cwd.replace(/[^A-Za-z0-9]/g, "-")}`;
    const transcript = `${dir}/${sid}.jsonl`;
    copyFileSync(`${dir}/${ID.bugs.slice("claude:".length)}.jsonl`, transcript);
    const child = spawn("sleep", ["600"], { detached: true, stdio: "ignore" });
    child.unref();
    const registry = `${H}/.claude/sessions/${child.pid}.json`;
    const now = Date.now();
    writeFileSync(registry, JSON.stringify({ pid: child.pid, sessionId: sid, cwd, startedAt: now, kind: "interactive", status: "idle", updatedAt: now }) + "\n");
    try {
      const page = await open(`agent=claude:${sid}`, { wait: ".thread-item" });
      expect(!(await page.locator(".resume-bar").count()), "ended before it quit");
      process.kill(child.pid);
      await until(page, () => {
        const bar = document.querySelector(".resume-bar")?.innerText ?? "";
        return bar.startsWith("This session has ended") && [...document.querySelectorAll(".resume-bar button")].some((b) => b.innerText.trim() === "Resume");
      }, null, "This session has ended, with Resume, on the same page");
      expect(!(await page.locator(".gone").count()) && (await page.locator(".thread-item").count()) > 0, "the conversation went");
    } finally {
      try { process.kill(child.pid); } catch {}
      for (const file of [registry, transcript]) if (existsSync(file)) unlinkSync(file);
    }
  });
}

async function newAgent() {
  await check("new agent: Muse is named and drawn as Muse", async () => {
    const page = await open("page=new", { wait: ".new-agent-line" });
    await page.locator("button.pill").first().click();
    await menu(page, "Muse");
    const pill = await page.evaluate(() => ({ text: document.querySelector("button.pill").innerText.trim(), logo: document.querySelector("button.pill img")?.getAttribute("src") }));
    expect(pill.text === "Muse" && pill.logo?.endsWith("muse.svg"), JSON.stringify(pill));
  });

  if (!process.env.NO_MUSE) {
    await check("new agent: a Muse session starts, answers, ends with Resume, and resumes (echo provider)", async () => {
      const page = await open("page=new", { wait: ".new-agent-line" });
      await page.locator("button.pill").first().click();
      await menu(page, "Muse");
      await page.locator("button.pill").nth(1).click();
      await menu(page, "~/projects/pics");
      await page.locator('[placeholder^="Describe the task"], [data-placeholder^="Describe the task"]').first().click();
      await page.keyboard.type("Hello from the end-to-end run");
      await click(page, ".start-button");
      await until(page, () => [...document.querySelectorAll(".thread-item")].some((e) => e.innerText.includes("echo: Hello from the end-to-end run")), null, "Muse's echo", 30000);
      await sessionMenu(page, "End Session…");
      await sheetButton(page, "End Session");
      await until(page, () => (document.querySelector(".resume-bar")?.innerText ?? "").startsWith("This session has ended"), null, "ended, with Resume", 15000);
      await click(page, ".resume-bar button", "Resume");
      await until(page, () => !document.querySelector(".resume-bar") && (document.querySelector(".message-field")?.dataset.placeholder ?? "").startsWith("Message "), null, "live again", 30000);
      // Off the list again, so runs don't pile Muse sessions up.
      await sessionMenu(page, "End Session…");
      await sheetButton(page, "End Session");
    });

    await check("new agent: a new worktree, named in place, with a Muse agent working in it (echo provider)", async () => {
      const name = `e2e-wt-${Date.now()}`;
      const place = `${H}/Agentman/Worktrees/demo/${name}`;
      try {
        // In demo from the start: each run's worktree, gone but still listed by
        // am serve, is newer than demo, and Recent shows ten.
        const page = await open(`page=new&prefs=${encodeURIComponent(JSON.stringify({ newAgentFolder: DEMO_DIR }))}`, { wait: ".new-agent-line" });
        await page.locator("button.pill").first().click();
        await menu(page, "Muse");
        // Its worktrees, once listed, and New Worktree… with them.
        for (let i = 0; i < 20; i++) {
          await page.locator("button.pill").nth(1).click();
          if (await page.locator(".page-menu-item").filter({ hasText: /^\s*New Worktree…\s*$/ }).count()) break;
          await page.keyboard.press("Escape");
          await sleep(500);
        }
        await menu(page, "New Worktree…");
        await page.waitForSelector(".namer-field");
        await page.locator(".namer-field").fill(name);
        await page.keyboard.press("Enter");
        await until(page, (n) => (document.querySelector(".new-agent-line")?.innerText ?? "").startsWith(`Starts in a new worktree, ~/Agentman/Worktrees/demo/${n}, on a new branch ${n} from`), name, "where it starts, said");
        await page.locator('[placeholder^="Describe the task"]').first().click();
        await page.keyboard.type("Hello from a worktree");
        await click(page, ".start-button");
        await until(page, () => [...document.querySelectorAll(".thread-item")].some((e) => e.innerText.includes("echo: Hello from a worktree")), null, "Muse's echo", 30000);
        const muse = ((await daemon({ type: "list_sessions" })).sessions ?? []).find((x) => x.kind === "muse" && x.cwd.replace(/^\/private/, "") === place.replace(/^\/private/, ""));
        expect(muse, `a Muse agent working in ${place}`);
        expect(existsSync(place) && execSync(`git -C "${DEMO_DIR}" branch --list ${name}`, { encoding: "utf8" }).includes(name), "the worktree's folder and branch");
        await sessionMenu(page, "End Session…");
        await sheetButton(page, "End Session");
      } finally {
        execSync(`git -C "${DEMO_DIR}" worktree remove --force "${place}" 2>/dev/null; git -C "${DEMO_DIR}" branch -D ${name} 2>/dev/null; true`);
      }
    });

    await check("palette: what an ended conversation said, handed from an editor, opens in a new main window", async () => {
      const words = "Hello from the end-to-end run";
      // The index is rebuilt for each run (prepare.sh), and the session just
      // ended can still be listed for a moment: asked until one is found.
      // With none of Muse's found at all, its history waits for am serve's
      // next sweep (every 2 minutes; the first can miss Muse while a Muse
      // session runs, and doesn't say so), so up to one sweep more.
      let hit, found = 0, indexing = true;
      for (let tries = 0; !hit && (tries < 20 || ((indexing || found === 0) && tries < 140)); tries++) {
        if (tries) await new Promise((r) => setTimeout(r, 1000));
        const live = new Set(((await daemon({ type: "list_sessions" })).sessions ?? []).map((s) => s.id));
        const said = await daemon({ type: "search", text: words, limit: 50 });
        found = said.search?.hits?.length ?? 0;
        indexing = said.search?.indexing === true;
        hit = said.search?.hits?.find((h) => h.session && !live.has(h.session.id));
      }
      expect(hit, `an ended conversation that said it (${found} found)`);
      const link = `agentman://found/${encodeURIComponent(hit.session.id)}?${new URLSearchParams({ message: hit.messageId, q: words })}`;
      const main = await open(`open=${encodeURIComponent(link)}`);
      await until(main, () => /\d+\/\d+/.test(document.querySelector(".find-bar")?.innerText ?? "") && !!document.querySelector(".resume-bar"), null, "the ended conversation at the message, with Resume", 20000);
    });
  }
}

async function usage() {
  await check("usage: the real page says nothing was used", async () => {
    const page = await open("page=usage", { wait: ".usage-page" });
    await until(page, () => /Nothing used/.test(document.querySelector(".usage-scroll")?.innerText ?? ""), null, "Nothing used");
  });

  await check("usage: spending, the chart's tooltip, and Limits (faked numbers)", async () => {
    const page = await open("page=usage", { init: [fake("usage")], wait: ".usage-page" });
    await click(page, ".usage-top button", "Spending");
    await click(page, ".usage-top button", "30d");
    await click(page, ".usage-top button", "Cost");
    await page.waitForSelector(".usage-chart > svg");
    await page.locator(".usage-chart > svg").hover();
    await until(page, () => (document.querySelector(".chart-tip")?.innerText ?? "").includes("Total"), null, "the tooltip's total");
    await click(page, ".usage-top button", "Limits");
    await until(page, () => (document.querySelector(".usage-scroll")?.innerText ?? "").includes("Claude Code"), null, "Claude Code's limits");
  });
}

async function connection() {
  await check("connection: losing am serve says so; coming back, the page is signed in and follows the thread", async () => {
    const page = await open(`agent=${ID.demo}`, { wait: ".thread-item" });
    execSync(`pkill -f "${T}/am serve"`);
    await until(page, () => !!document.querySelector(".connection-banner") && document.querySelector(".message-field")?.dataset.placeholder === "Can't send while Agentman isn't connected", null, "the banner, and the box can't send", 15000);
    execFileSync(`${HERE}restart.sh`);
    await until(page, () => !document.querySelector(".connection-banner") && document.querySelector(".sidebar-footer").innerText.includes("Connected"), null, "connected again", 20000);
    const file = execSync(`ls "${H}/.claude/projects/"*demo*/*.jsonl | head -1`, { encoding: "utf8" }).trim();
    const before = readFileSync(file, "utf8");
    const lines = before.trim().split("\n");
    const last = JSON.parse(lines.at(-1));
    const said = `Reconnect check ${Date.now()}`;
    appendFileSync(file, JSON.stringify({ ...last, uuid: crypto.randomUUID(), parentUuid: last.uuid, timestamp: new Date().toISOString(), message: { role: "assistant", model: "claude-opus-5-5", content: [{ type: "text", text: said }] } }) + "\n");
    await until(page, (s) => [...document.querySelectorAll(".thread-item")].some((e) => e.innerText.includes(s)), said, "the new message in the thread", 20000);
    // demo as it was: runs adding a line each crowd its worked lines out.
    writeFileSync(file, before);
  }, { expected: [/^console: WebSocket connection to .* failed/, /^failed: .*(REFUSED|Could not connect|network connection was lost)/, /^http 401 GET \/app\/(session|whoami)/] });
}

async function editor() {
  const folder = encodeURIComponent(DEMO_DIR);
  await check("editor: Monaco, the tree, tabs and ⌘P", async () => {
    const page = await open(`view=editor&folder=${folder}&file=src/App.tsx`, { wait: "iframe.monaco" });
    await intoCode(page);
    await page.keyboard.press("Meta+p");
    await paletteReady(page);
    await page.keyboard.type("TODO");
    // The folder's files are read as the palette opens.
    await until(page, () => (document.querySelector(".palette-layer")?.innerText ?? "").includes("TODO.txt"), null, "TODO.txt among the files");
    await page.keyboard.press("Enter");
    await until(page, () => [...document.querySelectorAll(".tab")].some((t) => t.innerText.includes("TODO.txt") && t.classList.contains("active")), null, "TODO.txt open in front");
  });

  await check("editor: an edit saved with ⌘S reaches the disk", async () => {
    const before = readFileSync(`${DEMO_DIR}/TODO.txt`, "utf8");
    try {
      const page = await open(`view=editor&folder=${folder}&file=TODO.txt`, { wait: "iframe.monaco" });
      await intoCode(page);
      await page.keyboard.press("Meta+ArrowDown");
      await page.keyboard.type("Saved by the e2e run.");
      await page.keyboard.press("Meta+s");
      for (let i = 0; i < 40 && !readFileSync(`${DEMO_DIR}/TODO.txt`, "utf8").includes("Saved by the e2e run."); i++) await sleep(250);
      expect(readFileSync(`${DEMO_DIR}/TODO.txt`, "utf8").includes("Saved by the e2e run."), "the file on disk didn't change");
    } finally {
      writeFileSync(`${DEMO_DIR}/TODO.txt`, before);
    }
  });

  await check("editor: ⌘K opens agents in the panel and asks Agentman there", async () => {
    const page = await open(`view=editor&folder=${folder}&file=TODO.txt`, { wait: "iframe.monaco" });
    await intoCode(page);
    await page.keyboard.press("Meta+k");
    // What's typed as the palette comes stays out of the file.
    await page.keyboard.type("x");
    await paletteReady(page);
    expect(await page.evaluate(() => !document.querySelector(".tab.active")?.classList.contains("dirty")), "a key typed as the palette opened went into the file");
    // The x may have reached the palette's field instead, which is fine.
    await page.keyboard.press("Meta+a");
    await page.keyboard.type("bugs");
    // As a person would: once the agents are listed (they come after the
    // folders, which match "bugs" too).
    await until(page, () => document.querySelector(".palette-row.selected")?.innerText.split("\n")[0] === "bugs", null, "the bugs agent first");
    await page.keyboard.press("Enter");
    await until(page, () => (document.querySelector(".agent-panel-column")?.innerText ?? "").startsWith("bugs"), null, "bugs in the panel");
    await page.keyboard.press("Meta+k");
    await paletteReady(page);
    await page.keyboard.type("Which agent changed README?");
    await page.keyboard.press("Enter");
    await until(page, () => (document.querySelector(".agent-panel-column")?.innerText ?? "").includes("Fake Agentman here"), null, "Agentman's reply in the panel", 20000);
  });

  await check("editor: Usage takes and gives back the keyboard; New Agent goes to a main window", async () => {
    const page = await open(`view=editor&folder=${folder}&file=TODO.txt`, { init: [fake("usage")], wait: "iframe.monaco" });
    await intoCode(page);
    await click(page, ".status-usage");
    await until(page, () => (document.querySelector(".usage-popover")?.innerText ?? "").includes("in 30 days") && document.activeElement?.classList.contains("usage-popover"), null, "the popover has the keyboard");
    await page.keyboard.press("Escape");
    await until(page, () => !document.querySelector(".usage-popover") && document.activeElement?.tagName === "IFRAME", null, "the code has the keyboard back");
    await page.keyboard.press("Meta+k");
    await paletteReady(page);
    await page.keyboard.type("new agent");
    await page.keyboard.press("Enter");
    await until(page, () => window.__opened.some((u) => u.includes("agentman%3A%2F%2Fnew")) || window.__links.includes("agentman://new"), null, "New Agent handed to a main window");
  });

  await check("editor: an agent's Diff and History in tabs; ⌘W closes them; Open in Editor stays here", async () => {
    const readme = `${DEMO_DIR}/README.md`;
    const before = readFileSync(readme, "utf8");
    writeFileSync(readme, before + "\nA change for the editor's Diff.\n");
    try {
    const page = await open(`view=editor&folder=${folder}&file=TODO.txt&agent=here&panel=1`, { wait: ".agent-panel .thread-bar" });
    await click(page, ".agent-panel .thread-bar");
    await until(page, () => [...document.querySelectorAll(".tab-strip .panel-tab")].map((t) => t.innerText.trim()).join() === "History,Diff" && !!document.querySelector(".editor-changes"), null, "History and Diff, the Diff in front");
    await page.waitForSelector(".diff-file-header", { timeout: 15000 });
    await page.evaluate(() => { window.__marker = 1; const h = document.querySelector(".diff-file-header"); const r = h.getBoundingClientRect(); h.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: r.x + 40, clientY: r.y + 10 })); });
    await menu(page, "Open in Editor");
    await until(page, () => window.__marker === 1 && !document.querySelector(".editor-changes") && document.querySelectorAll(".tab").length >= 2, null, "the file opened in this window, unreloaded");
    await click(page, ".tab-strip .panel-tab", "Diff");
    await page.keyboard.press("Meta+w");
    await page.keyboard.press("Meta+w");
    await until(page, () => document.querySelectorAll(".tab-strip .panel-tab").length === 0, null, "both closed");
    } finally {
      writeFileSync(readme, before);
    }
  });

  await check("editor and Agentman follow the Appearance chosen, not the computer's, both ways", async () => {
    // The code's background, the editor's label colour and Agentman's: with
    // `dark` given, whether all three are dark (or light); without, what they are.
    const look = (dark) => {
      // rgb(…) or #rrggbb(aa), as the minifier may write either.
      const lum = (c) => {
        c = c.trim();
        const [r, g, b] = c.startsWith("#") ? [1, 3, 5].map((i) => parseInt(c.slice(i, i + 2), 16)) : (c.match(/\d+(\.\d+)?/g) ?? []).map(Number);
        return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
      };
      let code = null;
      try { const m = document.querySelector("iframe.monaco").contentDocument.querySelector(".monaco-editor"); if (m) code = lum(getComputedStyle(m).backgroundColor); } catch {}
      const labelIsLight = lum(getComputedStyle(document.querySelector(".editor-window")).getPropertyValue("--label")) > 0.5;
      const am = getComputedStyle(document.documentElement).getPropertyValue("--am-inverse").trim();
      const seen = { code, labelIsLight, am };
      if (dark === undefined) return seen;
      return code !== null && (code < 0.5) === dark && labelIsLight === dark && am === (dark ? "#f2f2f2" : "#101010");
    };
    for (const [computer, chosen] of [["light", "dark"], ["dark", "light"]]) {
      const page = await open(`view=editor&folder=${folder}&file=TODO.txt&prefs=${encodeURIComponent(JSON.stringify({ appearance: chosen }))}`, { dark: computer === "dark", wait: "iframe.monaco" });
      await intoCode(page);
      try {
        await until(page, look, chosen === "dark", `${chosen} chosen on a ${computer} computer`);
      } catch (e) {
        throw new Error(`${e.message}: ${JSON.stringify(await page.evaluate(look))}`);
      }
      await page.close();
    }
  });

  await check("editor: the terminal runs a shell", async () => {
    const page = await open(`view=editor&folder=${folder}&file=TODO.txt&terminal=1&renderer=dom`, { wait: ".editor-terminal" });
    const newButton = page.locator(".term-note button");
    if (await newButton.count()) await newButton.click();
    await page.waitForSelector(".editor-terminal .shell-host", { timeout: 10000 });
    await page.locator(".editor-terminal .shell-host").click();
    await page.keyboard.type("echo e2e-$((6*7))");
    await page.keyboard.press("Enter");
    await until(page, () => [...document.querySelectorAll(".xterm-rows > div")].some((d) => d.innerText.trim() === "e2e-42"), null, "the shell's answer", 15000);
  });

  await check("editor: a narrow window keeps the agent panel inside it", async () => {
    const page = await open(`view=editor&folder=${folder}&file=TODO.txt&agent=here&panel=1`, { size: [800, 700], wait: ".agent-panel-column" });
    const fit = await page.evaluate(() => ({ right: document.querySelector(".agent-panel-column").getBoundingClientRect().right, width: innerWidth, scroll: document.documentElement.scrollWidth }));
    expect(fit.right <= fit.width && fit.scroll <= fit.width, JSON.stringify(fit));
  });
}

async function settings() {
  for (const [page, title] of [["general", "General"], ["notifications", "Notifications"], ["devices", "Devices"], ["permissions", "Permissions"], ["personality", "Personality"], ["knowledge", "Knowledge"], ["keys", "Keys"], ["languages", "Languages"]]) {
    await check(`settings: ${title} opens`, async () => {
      const p = await open(`view=settings&page=${page}`, { size: [800, 612] });
      await until(p, (t) => [...document.querySelectorAll(".settings-display, h1")].some((h) => h.innerText.trim() === t), title, `the ${title} page`);
    });
  }

  await check("settings: pairing a device shows a code that scans, and names the phone that connects", async () => {
    const page = await open("view=settings&page=pair", { size: [800, 612], init: [fake("devices")], wait: ".code-pill" });
    if (process.platform === "darwin") {
      const shot = `${T}/e2e-qr.png`;
      await page.screenshot({ path: shot });
      const read = execFileSync("swift", [`${HERE}qrread.swift`, shot], { encoding: "utf8" }).trim();
      expect(read.startsWith("agentman://pair?token="), `the QR reads ${read}`);
    }
    await page.evaluate(() => window.__connectPhone());
    await until(page, () => (document.querySelector(".pairing-panel")?.innerText ?? "").includes("Grace's Pixel is connected"), null, "the phone named");
  });

  await check("settings: Permissions' switches save, survive a reload, and reach am serve", async () => {
    const before = (await daemon({ type: "manager_settings" })).managerSettings;
    try {
      const page = await open("view=settings&page=permissions", { size: [900, 1400] });
      await page.waitForSelector(".settings-row");
      const row = (title) => page.locator(".settings-row").filter({ has: page.locator(".row-title", { hasText: new RegExp(`^${title}$`) }) });
      const was = !!before.autopilot?.unstick;
      await row("Notice stuck agents").locator("button, input").first().click();
      await row("Ends").locator(".mac-popup").click();
      await menu(page, "06:00");
      await click(page, ".save-bar button", "Save");
      await until(page, () => !document.querySelector(".save-bar"), null, "saved");
      await page.reload();
      await page.waitForSelector(".settings-row");
      const saved = (await daemon({ type: "manager_settings" })).managerSettings.autopilot;
      expect(!!saved.unstick === !was && saved.nightEnd === "06:00", `am serve has ${JSON.stringify({ unstick: saved.unstick, nightEnd: saved.nightEnd })}`);
    } finally {
      await daemon({ type: "manager_set_settings", manager: before });
    }
  });

  await check("settings: Personality's edits revert", async () => {
    const page = await open("view=settings&page=personality", { size: [800, 612], wait: "textarea" });
    const before = await page.locator("textarea").first().inputValue();
    await page.locator("textarea").first().click();
    await page.keyboard.type(" And a test line.");
    await until(page, () => !!document.querySelector(".save-bar"), null, "the save bar");
    await click(page, ".save-bar button", "Revert");
    expect((await page.locator("textarea").first().inputValue()) === before, "the text came back");
  });
}



async function paletteSaid() {
  await check("palette: groups its rows, and finds what was said under In messages", async () => {
    const page = await open(`agent=${ID.bugs}`, { wait: ".message-field" });
    await page.keyboard.press("Meta+k");
    await paletteReady(page);
    const headings = await until(page, () => { const h = [...document.querySelectorAll(".palette-heading")].map((e) => e.innerText); return h.includes("Agents") && h.includes("Commands") && h; }, null, "Agents and Commands headings");
    expect(headings[0] === "This agent", `the first heading is ${headings[0]}`);
    await page.keyboard.type("uploader");
    await until(page, () => [...document.querySelectorAll(".palette-heading")].some((e) => e.innerText === "In messages") && !!document.querySelector(".palette-snippet b"), null, "an In messages row, its words marked", 15000);
    const foot = await page.evaluate(() => document.querySelector(".palette-status")?.innerText);
    expect(foot === "↑↓ to move · ↵ to open · esc to close", `the foot reads ${foot}`);
    await page.locator(".palette-row.with-snippet").first().click();
    await until(page, () => /\d+\/\d+/.test(document.querySelector(".find-bar")?.innerText ?? ""), null, "opened at the message, the find bar counting");
  });

  await check("palette: a message found from an editor opens in a main window", async () => {
    const page = await open(`view=editor&folder=${encodeURIComponent(DEMO_DIR)}&file=TODO.txt`, { wait: "iframe.monaco" });
    await intoCode(page);
    await page.keyboard.press("Meta+k");
    await paletteReady(page);
    await page.keyboard.type("uploader");
    await page.locator(".palette-row.with-snippet").first().click({ timeout: 15000 });
    const handed = await until(page, () => [...window.__links, ...window.__opened.map((u) => new URL(u, location.href).searchParams.get("open") ?? "")].find((l) => l.startsWith("agentman://found/")), null, "handed to a main window");
    // A main window opened with it (none was open): at the message.
    const main = await open(`open=${encodeURIComponent(handed)}`, { wait: ".message-field" });
    await until(main, () => /\d+\/\d+/.test(document.querySelector(".find-bar")?.innerText ?? ""), null, "the new main window at the message, the find bar counting");
  });

  await check("palette: rows that come in under a still pointer don't take the selection", async () => {
    const page = await open("", { wait: ".board-card" });
    await page.keyboard.press("Meta+k");
    await paletteReady(page);
    // Where the message found will be: under Ask Agentman and its heading.
    const box = await page.locator(".palette-rows").boundingBox();
    await page.mouse.move(box.x + 200, box.y + 6 + 34 + 28 + 26);
    await page.keyboard.type("uploader");
    await until(page, () => !!document.querySelector(".palette-row.with-snippet") && document.querySelector(".palette-status")?.innerText.startsWith("↑↓"), null, "the message found", 15000);
    await page.waitForTimeout(300);
    const selected = await page.evaluate(() => document.querySelector(".palette-row.selected")?.innerText.split("\n")[0]);
    expect(selected?.startsWith("Ask Agentman"), `the selection moved to ${selected}`);
    // A real move still selects.
    await page.mouse.move(box.x + 210, box.y + 6 + 34 + 28 + 30);
    await until(page, () => document.querySelector(".palette-row.selected")?.classList.contains("with-snippet"), null, "the row moved onto selected");
  });

  await check("palette: short words aren't searched; an agent listed by name isn't listed again for what it said", async () => {
    const said = await daemon({ type: "search", text: "pics", limit: 50 });
    const pics = await sessionId("pics");
    expect(said.search?.hits?.some((h) => h.session?.id === pics), "the pics agent's own messages say pics (else this proves nothing)");
    const page = await open("", { wait: ".board-card" });
    await page.keyboard.press("Meta+k");
    await paletteReady(page);
    await page.keyboard.type("pi");
    await page.waitForTimeout(600);
    const short = await page.evaluate(() => ({ foot: document.querySelector(".palette-status")?.innerText, said: document.querySelectorAll(".palette-row.with-snippet").length }));
    expect(short.foot === "↑↓ to move · ↵ to open · esc to close" && short.said === 0, `with two letters: ${JSON.stringify(short)}`);
    await page.keyboard.type("cs");
    await until(page, () => document.querySelector(".palette-status")?.innerText === "↑↓ to move · ↵ to open · esc to close", null, "the search over");
    const rows = await page.evaluate(() => [...document.querySelectorAll(".palette-rows > *")].map((e) => (e.classList.contains("palette-heading") ? "## " : "") + e.innerText.split("\n")[0]));
    expect(rows.includes("pics"), `pics is listed: ${rows.join(", ")}`);
    expect(!rows.includes("## In messages") || !rows.slice(rows.indexOf("## In messages")).includes("pics"), `pics isn't listed again: ${rows.join(", ")}`);
  });
}


// MARK: - Agentman's key prompt (never saved: on a Mac that's the Keychain)

async function keyPrompt() {
  const words = () => ({
    heading: document.querySelector(".am-key-heading")?.innerText,
    why: document.querySelector(".am-key-why")?.innerText,
    placeholder: document.querySelector(".am-key-field")?.placeholder,
    note: document.querySelector(".am-box-note")?.innerText.replace(/\s+/g, " ").trim(),
  });
  await check("key prompt: what Agentman is, why it needs a key, where it's kept, and where to get one", async () => {
    const page = await open("", { init: [fake("nokey")], wait: ".am-keybox" });
    const w = await page.evaluate(words);
    expect(w.heading === "Agentman needs an API key", `heading: ${w.heading}`);
    expect(w.why === "It reads what your agents are doing, answers their routine requests, and tells you what needs you. It runs on your own Anthropic key, kept in your Mac's Keychain and only sent to Anthropic.", `why: ${w.why}`);
    expect(w.placeholder === "Paste your Anthropic API key", `placeholder: ${w.placeholder}`);
    expect(w.note === "Don't have one? Get an Anthropic key", `note: ${w.note}`);
    await click(page, ".am-box-note a");
    await until(page, () => window.__opened.includes("https://console.anthropic.com/settings/keys"), null, "Anthropic's keys page opened");
    expect(!(await page.evaluate(() => window.__keySetTried)), "a key was sent");
  });

  await check("key prompt: Gemini's words, and a file only you can read where am serve isn't on a Mac", async () => {
    const page = await open("", { init: ["window.__gemini = true; window.__system = 'linux';", fake("system"), fake("nokey")], wait: ".am-keybox" });
    const w = await until(page, () => { const t = document.querySelector(".am-key-why")?.innerText ?? ""; return t.includes("a file only you can read") && t; }, null, "the words for a system that isn't a Mac");
    expect(w.endsWith("It runs on your own Gemini key, kept in a file only you can read and only sent to Google."), `why: ${w}`);
    const rest = await page.evaluate(words);
    expect(rest.placeholder === "Paste your Gemini API key" && rest.note === "Don't have one? Get a Gemini key", JSON.stringify(rest));
    await click(page, ".am-box-note a");
    await until(page, () => window.__opened.includes("https://aistudio.google.com/apikey"), null, "Google's keys page opened");
  });

  await check("key prompt: cut off from am serve, the box waits and its note says Not connected, then Connecting…", async () => {
    const page = await open("", { init: [fake("nokey"), fake("net")], wait: ".am-keybox" });
    await page.evaluate(() => { window.__net = "down"; window.__cut(); });
    await until(page, () => document.querySelector(".am-box-note")?.innerText === "Not connected." && !document.querySelector(".am-keybox")
      && !![...document.querySelectorAll(".am-box [placeholder], .am-box [data-placeholder]")].find((e) => (e.getAttribute("placeholder") ?? e.dataset.placeholder) === "Waiting for Agentman to connect…"), null, "Not connected, the box waiting");
    await page.evaluate(() => { window.__net = "hang"; });
    await until(page, () => document.querySelector(".am-box-note")?.innerText === "Connecting…", null, "Connecting…", 12000);
  }, { expected: [/^console: WebSocket connection to 'ws:\/\/127\.0\.0\.1:9\/' failed/] });
}

async function generalPage() {
  const keySections = () => [...document.querySelectorAll(".settings-section")].map((e) => e.innerText.replace(/\s+/g, " ")).filter((t) => / API key /.test(t + " "));
  // Anything the page says on its way to the right words, from its first frame.
  const watchWords = `window.__said = new Set(); new MutationObserver(() => {
    const anthropic = [...document.querySelectorAll(".settings-section")].find((e) => e.innerText.includes("Anthropic API key"))?.innerText ?? "";
    if (anthropic.includes("on this computer")) window.__said.add("Kept on this computer");
    if (anthropic.includes("Not set yet")) window.__said.add("Not set yet, for the saved key");
  }).observe(document, { subtree: true, childList: true, characterData: true });`;
  await check("settings: General's keys say where they're kept, on a Mac and off one, with no other words first", async () => {
    let page = await open("view=settings&page=general", { size: [800, 700], init: [watchWords] });
    let [anthropic, gemini] = await until(page, () => { const k = [...document.querySelectorAll(".settings-section")].map((e) => e.innerText.replace(/\s+/g, " ")).filter((t) => / API key /.test(t + " ")); return k.length === 2 && k[0].includes("Keychain") && k; }, null, "the key sections, saying Keychain");
    expect(anthropic.includes("Saved in your Keychain") && anthropic.includes("Kept in your Mac's Keychain and only ever sent to Anthropic."), `Anthropic: ${anthropic}`);
    expect(gemini.includes("Not set yet") && gemini.includes("Kept in your Mac's Keychain and only ever sent to Google."), `Gemini: ${gemini}`);
    const before = await page.evaluate(() => [...window.__said]);
    expect(!before.length, `said first: ${before.join(", ")}`);
    await page.close();
    page = await open("view=settings&page=general", { size: [800, 700], init: ["window.__system = 'linux';", fake("system")] });
    [anthropic] = await until(page, () => { const k = [...document.querySelectorAll(".settings-section")].map((e) => e.innerText.replace(/\s+/g, " ")).filter((t) => t.includes("a file only you can read")); return k.length === 2 && k; }, null, "both keys' words off a Mac");
    expect(/Anthropic API key Saved (?!in your Keychain)/.test(anthropic) && anthropic.includes("Kept in a file only you can read and only ever sent to Anthropic."), `Anthropic off a Mac: ${anthropic}`);
  });

  await check("settings: Appearance switches the look, keeps it across a reload, and goes back", async () => {
    const page = await open("view=settings&page=general", { size: [800, 700] });
    const segment = (label) => page.locator(".mac-segmented .segment", { hasText: label }).first();
    await segment("Dark").click();
    await until(page, () => document.documentElement.dataset.theme === "dark" && getComputedStyle(document.body).backgroundColor !== "rgb(255, 255, 255)", null, "dark");
    await page.reload();
    await until(page, () => document.documentElement.dataset.theme === "dark" && document.querySelector('.mac-segmented [aria-checked="true"]')?.innerText === "Dark", null, "dark after a reload");
    await segment("Light").click();
    await until(page, () => document.documentElement.dataset.theme === "light", null, "light");
    await segment("Match the Mac").click();
    await until(page, () => !("theme" in document.documentElement.dataset), null, "following the system again");
  });
}


// MARK: - The sidebar's filters, and the summary for coming back

async function filtersAndAway() {
  const item = (page, title) => page.locator(".page-menu-item").filter({ hasText: new RegExp(`^\\s*(✓\\s*)?${title}\\s+\\d+\\s*$`) }).first().click({ timeout: 8000 });
  await check("sidebar: the filter shows one state, then one folder, each undone with its ×", async () => {
    // On an agent's page: the Board keeps the sidebar folded away.
    const page = await open(`agent=${ID.bugs}`, { init: [fake("question")], wait: ".sidebar .agent-row" });
    await until(page, () => { const n = [...document.querySelectorAll(".sidebar .agent-row:not(.earlier) .agent-line2 .name")].map((e) => e.innerText.trim()); return n.length >= 6 && n.includes("pick"); }, null, "every agent");
    const count = await page.evaluate(() => document.querySelectorAll(".sidebar .agent-row:not(.earlier)").length);
    await click(page, ".filter-button");
    await item(page, "Needs you");
    await until(page, () => { const n = [...document.querySelectorAll(".sidebar .agent-row:not(.earlier) .agent-line2 .name")].map((e) => e.innerText.trim()); return n.length === 1 && n[0] === "pick"; }, null, "only pick, who needs you");
    expect((await page.evaluate(() => document.querySelector(".active-filters")?.innerText.trim())) === "Needs you", "the Needs you token");
    await click(page, ".filter-token");
    await until(page, (c) => document.querySelectorAll(".sidebar .agent-row:not(.earlier)").length === c && !document.querySelector(".active-filters"), count, "every agent again");
    await click(page, ".filter-button");
    await item(page, "shots");
    await until(page, () => { const n = [...document.querySelectorAll(".sidebar .agent-row:not(.earlier) .agent-line2 .name")].map((e) => e.innerText.trim()); return n.length === 1 && n[0] === "shots"; }, null, "only the shots folder's agent");
    expect((await page.evaluate(() => document.querySelector(".active-filters")?.innerText.trim())) === "shots", "the shots token");
    await click(page, ".filter-token");
    await until(page, (c) => document.querySelectorAll(".sidebar .agent-row:not(.earlier)").length === c, count, "every agent again");
  });

  await check("away: the summary since an hour ago lists who was busy, closes on Escape, comes back from ⌘K, and opens an agent", async () => {
    const page = await open("away=60", { wait: ".away-panel" });
    const who = await page.evaluate(() => [...document.querySelectorAll(".away-name")].map((e) => e.innerText.trim()));
    expect(who.includes("shots") && (await page.evaluate(() => document.querySelector(".away-title")?.innerText)) === "While you were away", `the summary lists: ${who.join(", ")}`);
    await page.keyboard.press("Escape");
    await until(page, () => !document.querySelector(".away-panel"), null, "closed");
    await page.keyboard.press("Meta+k");
    await paletteReady(page);
    await page.keyboard.type("What Happened");
    await until(page, () => document.querySelector(".palette-row.selected")?.innerText.split("\n")[0] === "What Happened While I Was Away", null, "the command first");
    await page.keyboard.press("Enter");
    await page.waitForSelector(".away-panel", { timeout: 10000 });
    await page.locator(".away-row").filter({ hasText: "shots" }).first().click();
    await until(page, () => !document.querySelector(".away-panel") && new URLSearchParams(location.search).get("agent")?.startsWith("claude:"), null, "the agent open, the summary closed");
  });
}

// MARK: - The second batch: the rest of each screen

// A test agent's id by its name: a Claude one, as a Muse session started in
// its folder goes by the folder's name too until it has its own.
const sessionId = async (name) => (await daemon({ type: "list_sessions" })).sessions?.find((s) => s.name === name && s.kind === "claude")?.id;

async function sidebar() {
  await check("sidebar: rename, then back", async () => {
    const page = await open(`agent=${ID.bugs}`);
    const row = page.locator(".sidebar .agent-line1").filter({ hasText: "shots" }).first();
    await row.click({ button: "right" });
    await menu(page, "Rename…");
    await page.waitForSelector(".rename-field");
    await page.keyboard.press("Meta+a");
    await page.keyboard.type("shots renamed");
    await page.keyboard.press("Enter");
    await until(page, () => [...document.querySelectorAll(".sidebar .agent-line1 + *, .sidebar .agent-name, .sidebar .name")].some((e) => e.innerText.trim() === "shots renamed") || document.querySelector(".sidebar").innerText.includes("shots renamed"), null, "the new name in the sidebar");
    await page.locator(".sidebar").getByText("shots renamed", { exact: true }).first().click({ button: "right" });
    await menu(page, "Rename…");
    await page.waitForSelector(".rename-field");
    await page.keyboard.press("Meta+a");
    await page.keyboard.type("shots");
    await page.keyboard.press("Enter");
    await until(page, () => !document.querySelector(".sidebar").innerText.includes("shots renamed"), null, "the old name back");
  });

  await check("sidebar: pin and archive, then undo both", async () => {
    const page = await open(`agent=${ID.bugs}`);
    const rowOf = (name) => page.locator(".sidebar .agent-line1").filter({ hasText: name }).first();
    await rowOf("modes").click({ button: "right" });
    await menu(page, "Pin");
    await until(page, () => /Pinned[\s\S]*modes/.test(document.querySelector(".sidebar").innerText), null, "modes under Pinned");
    await rowOf("modes").click({ button: "right" });
    await menu(page, "Unpin");
    await until(page, () => !document.querySelector(".sidebar").innerText.includes("Pinned"), null, "Pinned gone");
    await rowOf("modes").click({ button: "right" });
    await menu(page, "Archive");
    await until(page, () => /Archived\s*1/.test(document.querySelector(".sidebar").innerText), null, "one archived");
    await page.locator(".sidebar").getByText("Archived", { exact: true }).first().click();
    await rowOf("modes").click({ button: "right" });
    await menu(page, "Unarchive");
    await until(page, () => !/Archived\s*\d/.test(document.querySelector(".sidebar").innerText), null, "none archived");
  });
}

/** pick's questions as the agents ask them now (QuestionCard.swift): forms
 *  of several, their review, ticks, an answer of your own. am serve's side
 *  is the form fake's. */
async function forms() {
  const FORM = { id: "q-form", title: "Size", prompt: "Which size should the cards be?", custom: true, closable: true, reviewStep: true, step: 2,
    options: [{ key: "1", label: "Small" }, { key: "2", label: "Medium", checked: true }, { key: "3", label: "Large" }],
    steps: [{ title: "Colour", answered: true }, { title: "Size", answered: true }, { title: "Finish" }] };
  const asking = (q) => `window.__q = ${JSON.stringify(q)};`;
  const sent = (page) => page.evaluate(() => window.__sent);
  const card = ".thread .question-card";

  await check("forms: a form names its questions, ticks the answered, and moves with ‹ › and its tabs", async () => {
    const page = await open(`agent=${ID.pick}`, { init: [asking(FORM), fake("form")], wait: card });
    const tabs = await page.evaluate(() => [...document.querySelectorAll(".question-steps .step-tab")].map((t) => `${t.querySelector(".step-tab-tick") ? "✓" : ""}${t.innerText.trim()}${t.classList.contains("current") ? "*" : ""}`));
    expect(tabs.join(" ") === "✓Colour ✓Size* Finish Review", `the tabs: ${tabs.join(" ")}`);
    expect(await page.evaluate(() => document.querySelector(".step-pager-count")?.innerText === "2 of 3"), "the pager says 2 of 3");
    expect(await page.evaluate(() => document.querySelector(".question-heading")?.innerText === "Which size should the cards be?"), "the question as the heading");
    expect(await page.evaluate(() => !!document.querySelector(".option-row.leading .option-chosen") && document.querySelector(".option-row.leading .option-label")?.innerText === "Medium"), "the answer given before, ticked and filled");
    await page.evaluate(() => { window.__delay = 1200; });
    await click(page, '.step-pager [aria-label="Review your answers"], .step-pager [aria-label="Next question"]');
    await until(page, () => window.__sent.some((r) => r.type === "move_question" && r.step === 3 && r.questionId === "q-form"), null, "a move to question 3");
    expect(await page.evaluate(() => !!document.querySelector(".question-choices.stepping")), "the choices step back while it moves");
    await until(page, () => document.querySelector(".step-pager-count")?.innerText === "3 of 3" && !document.querySelector(".question-choices.stepping"), null, "on question 3, the choices back");
    await page.evaluate(() => { window.__delay = 100; });
    await page.locator(".question-steps .step-tab", { hasText: "Colour" }).click();
    await until(page, () => window.__sent.some((r) => r.type === "move_question" && r.step === 1), null, "a move to question 1, from its tab");
    await until(page, () => document.querySelector(".step-pager-count")?.innerText === "1 of 3" && document.querySelector('.step-pager [aria-label="Previous question"]').disabled, null, "on question 1, ‹ off");
  });

  await check("forms: ✕ closes a question unanswered", async () => {
    const page = await open(`agent=${ID.pick}`, { init: [asking(FORM), fake("form")], wait: card });
    await click(page, '.question-header-button[aria-label^="Close the question unanswered"]');
    await until(page, () => !document.querySelector(".thread .question-card"), null, "the card gone");
    const requests = await sent(page);
    expect(requests.length === 1 && requests[0].type === "close_question" && requests[0].questionId === "q-form", JSON.stringify(requests));
  });

  await check("forms: a form's review lists the answers given, with its warning, and goes no further", async () => {
    const review = { ...FORM, id: "q-review", title: "Review your answers", prompt: "Ready to submit your answers?", custom: false, step: 4,
      detail: "● Which colour?\n  → Red\n● Which size?\n  → Medium\n\n⚠ You have not answered all questions",
      options: [{ key: "1", label: "Submit answers" }, { key: "2", label: "Cancel" }] };
    const page = await open(`agent=${ID.pick}`, { init: [asking(review), fake("form")], wait: card });
    const rows = await page.evaluate(() => [...document.querySelectorAll(".review-answer")].map((r) => r.innerText.replace(/\s+/g, " ").trim()));
    expect(rows.join(" | ") === "Which colour? Red | Which size? Medium", `the answers: ${rows.join(" | ")}`);
    expect(await page.evaluate(() => document.querySelector(".review-warning")?.innerText.trim() === "You have not answered all questions"), "the warning");
    expect(await page.evaluate(() => !document.querySelector(".thread .question-card .code-box")), "the review shown as terminal text");
    expect(await page.evaluate(() => document.querySelector(".step-pager-count")?.innerText === "Review" && document.querySelector('.step-pager [aria-label="Next question"], .step-pager [aria-label="Review your answers"]').disabled), "Review, › off");
  });

  await check("forms: several answers are ticked in rows, counted, and sent in the order listed", async () => {
    const ticks = { id: "q-ticks", title: "Checks", prompt: "Which checks should run?", multiple: true,
      options: [{ key: "1", label: "Unit tests" }, { key: "2", label: "Lint" }, { key: "3", label: "End-to-end" }] };
    const page = await open(`agent=${ID.pick}`, { init: [asking(ticks), fake("form")], wait: card });
    expect(await page.evaluate(() => document.querySelector(".choices-submit button").disabled && document.querySelector(".choices-count").innerText === "Tick one or more"), "Submit off until one is ticked");
    await page.locator(".check-row", { hasText: "End-to-end" }).click();
    await page.locator(".check-row", { hasText: "Unit tests" }).click();
    await until(page, () => document.querySelector(".choices-count").innerText === "2 chosen" && document.querySelectorAll('.check-row[aria-checked="true"]').length === 2, null, "2 chosen, ticked");
    await page.locator(".check-row", { hasText: "Unit tests" }).click();
    await page.locator(".check-row", { hasText: "Unit tests" }).click();
    await click(page, ".choices-submit button", "Submit");
    await until(page, () => window.__sent.length === 1, null, "the answer sent");
    const [answer] = await sent(page);
    expect(answer.type === "answer_question" && JSON.stringify(answer.optionKeys) === '["1","3"]', JSON.stringify(answer));
  });

  await check("forms: an answer of your own is written in the card; Return sends it, Shift-Return starts a line", async () => {
    const page = await open(`agent=${ID.pick}`, { init: [asking({ ...FORM, steps: undefined, step: undefined }), fake("form")], wait: card });
    expect(await page.evaluate(() => !document.querySelector(".write-own-send") || getComputedStyle(document.querySelector(".write-own-send")).opacity === "0"), "a send arrow with nothing written");
    await page.locator(".write-own-field").click();
    await page.keyboard.type("Extra");
    await page.keyboard.press("Shift+Enter");
    await page.keyboard.type("large");
    expect(await page.evaluate(() => !!document.querySelector(".choices-single.stepping")), "the choices step back while writing");
    expect(await page.evaluate(() => window.__sent.length === 0), "Shift-Return sent it");
    await page.keyboard.press("Enter");
    await until(page, () => window.__sent.length === 1, null, "sent on Return");
    const [answer] = await sent(page);
    expect(answer.type === "answer_question" && answer.answerText === "Extra\nlarge" && !answer.optionKey, JSON.stringify(answer));
  });

  await check("forms: a refused move says why, and the choices come back", async () => {
    const page = await open(`agent=${ID.pick}`, { init: [asking(FORM), "window.__refuse = 'The form on screen has changed.';", fake("form")], wait: card });
    await page.locator(".question-steps .step-tab", { hasText: "Finish" }).click();
    await until(page, () => (document.querySelector(".composer-wrap")?.innerText ?? "").includes("The form on screen has changed."), null, "the reason under the box");
    await until(page, () => !document.querySelector(".question-choices.stepping") && document.querySelector(".step-pager-count")?.innerText === "2 of 3", null, "still on 2 of 3, the choices back");
  });

  await check("forms: Antigravity's form, named by its whole questions, says which it is on and stops at its last", async () => {
    const agy = { id: "q-agy", title: "Question 3/3", prompt: "Which finish should the buttons have?", reviewStep: false, step: 3,
      options: [{ key: "1", label: "Matte" }, { key: "2", label: "Gloss" }],
      steps: [{ title: "Which colour should the cards be on the Board?", answered: true }, { title: "Which size should they be?", answered: true }, { title: "Which finish should the buttons have?" }] };
    const page = await open(`agent=${ID.pick}`, { init: [asking(agy), fake("form")], wait: card });
    expect(await page.evaluate(() => !document.querySelector(".question-steps") && document.querySelector(".question-title-text")?.innerText === "Question 3/3"), "its title, no tabs");
    expect(await page.evaluate(() => document.querySelector(".step-pager-count")?.innerText === "3 of 3" && document.querySelector('.step-pager [aria-label="Next question"], .step-pager [aria-label="Review your answers"]').disabled), "3 of 3, › off: no review");
    expect(await page.evaluate(() => !document.querySelector(".question-header-button[aria-label^='Close']")), "a ✕ on a question that can't be closed");
  });

  await check("forms: short names that don't fit say the one on screen", async () => {
    const many = { ...FORM, steps: [{ title: "Branch to ship", answered: true }, { title: "Deploy target" }, { title: "Rollback plan" }, { title: "Who to notify" }], title: "Deploy target" };
    // From the default settings: a panel left open by another check would
    // leave the thread no room at all.
    const page = await open(`agent=${ID.pick}&fresh`, { init: [asking(many), fake("form")], wait: card, size: [900, 700] });
    await until(page, () => {
      const wide = document.querySelector(".question-fits-wide");
      return wide?.classList.contains("measuring") && [...document.querySelectorAll(".question-fits > .question-title-text")].map((t) => t.innerText).join() === "Deploy target";
    }, null, "the one on screen's name, the tabs hidden");
    await page.setViewportSize({ width: 1500, height: 700 });
    await until(page, () => !document.querySelector(".question-fits-wide.measuring") && document.querySelectorAll(".question-steps .step-tab").length === 5, null, "wider, every name");
  });
}

async function threadMore() {
  await check("thread: Stop asks am serve to interrupt the turn", async () => {
    const page = await open(`agent=${ID.demo}`, { init: [fake("record"), fake("busy")], wait: '[aria-label="Stop the agent\'s turn"]' });
    await click(page, '[aria-label="Stop the agent\'s turn"]');
    await until(page, (id) => window.__requests.some((r) => r.type === "interrupt" && r.sessionId === id), ID.demo, "an interrupt for demo");
  });

  await check("thread: the picture viewer goes through the run with ← and →, and closes on Escape", async () => {
    const id = await sessionId("pics");
    const page = await open(`agent=${encodeURIComponent(id)}`, { wait: ".thumb" });
    // From the keyboard, so the keyboard has somewhere to come back to in
    // Safari, whose buttons don't take it when clicked.
    await page.locator(".thumb").first().focus();
    await page.keyboard.press("Enter");
    const first = await until(page, () => { const c = document.querySelector(".lightbox-caption")?.innerText ?? ""; return document.querySelector(".lightbox img") && /1 of \d+/.test(c) && c; }, null, "the picture, captioned 1 of n");
    await page.keyboard.press("ArrowRight");
    await until(page, () => /2 of \d+/.test(document.querySelector(".lightbox-caption")?.innerText ?? ""), null, "→ shows the next");
    await page.keyboard.press("ArrowLeft");
    await until(page, () => /1 of \d+/.test(document.querySelector(".lightbox-caption")?.innerText ?? ""), null, "← goes back");
    await page.keyboard.press("Escape");
    await until(page, () => !document.querySelector(".lightbox") && document.activeElement?.classList.contains("thumb"), null, "closed, the keyboard back on the thumbnail");
    expect(first.includes("of"), first);
  });

  await check("thread: a picture with a space in its name shows as one, and opens under its name", async () => {
    const id = await sessionId("pics");
    const page = await open(`agent=${encodeURIComponent(id)}`, { wait: ".thumb" });
    // The run of three the agent read last: composer, header, "09 Menu.png".
    await until(page, () => [...document.querySelectorAll(".thumb img")].filter((img) => img.naturalWidth === 420).length === 1, null, "the 420-wide picture, 09 Menu.png");
    expect(!(await page.locator(".run-summary, .call-row").filter({ hasText: "09 Menu" }).count()), "09 Menu.png also listed as a plain read");
    await page.evaluate(() => [...document.querySelectorAll(".thumb")].find((t) => t.querySelector("img")?.naturalWidth === 420).focus());
    await page.keyboard.press("Enter");
    await until(page, () => document.querySelector(".lightbox-title")?.textContent === "09 Menu.png", null, "the viewer titled 09 Menu.png");
    await page.keyboard.press("Escape");
  });

  await check("thread: / lists the agent's commands", async () => {
    const id = await sessionId("ask");
    const page = await open(`agent=${encodeURIComponent(id)}`, { wait: ".message-field" });
    await click(page, ".message-field");
    await page.keyboard.type("/");
    await until(page, () => !!document.querySelector(".completion-list"), null, "a list of commands");
    await page.keyboard.press("Escape");
    await clearBox(page);
  });

  await check("thread: the mode picker offers the agent's modes and asks for the one chosen", async () => {
    const id = await sessionId("modes");
    const page = await open(`agent=${encodeURIComponent(id)}`, { init: [fake("record")], wait: ".message-field" });
    const pill = page.locator('.composer-wrap [title="Switch the agent\'s mode"]').first();
    await pill.click();
    const items = await until(page, () => { const m = [...document.querySelectorAll(".page-menu-item")].map((e) => e.innerText.trim()); return m.length > 1 && m; }, null, "modes in the menu");
    const other = items.find((m) => !m.startsWith("✓"));
    await menu(page, other);
    await until(page, () => window.__requests.some((r) => r.type === "set_mode"), null, "a set_mode request");
  });
}

async function boardMore() {
  await check("board: checks and missions (faked)", async () => {
    const page = await open("", { init: [fake("board")], wait: ".board-card" });
    await until(page, () => { const t = document.querySelector(".board")?.innerText ?? document.body.innerText; return t.includes("Ship transcript search on every app") && t.includes("All 214 tests pass") && t.includes("Something looks off"); }, null, "the mission and both checks");
  });
}

async function editorMore() {
  const folder = encodeURIComponent(DEMO_DIR);
  await check("editor: New File and Rename in the tree reach the disk", async () => {
    const made = `${DEMO_DIR}/docs/e2e-new.md`, moved = `${DEMO_DIR}/docs/e2e-renamed.md`;
    try {
      const page = await open(`view=editor&folder=${folder}`, { wait: ".tree-row-body" });
      await page.locator(".tree-row-body").filter({ hasText: /^\s*docs\s*$/ }).first().click({ button: "right" });
      await menu(page, "New File…");
      await until(page, () => document.activeElement?.tagName === "INPUT", null, "a name to type");
      await page.keyboard.type("e2e-new.md");
      await page.keyboard.press("Enter");
      for (let i = 0; i < 40 && !existsSync(made); i++) await sleep(250);
      expect(existsSync(made), "no file made");
      await page.locator(".tree-row-body").filter({ hasText: /^\s*e2e-new\.md\s*$/ }).first().click({ button: "right" });
      await menu(page, "Rename…");
      await until(page, () => document.activeElement?.tagName === "INPUT", null, "the name to edit");
      await page.keyboard.press("Meta+a");
      await page.keyboard.type("e2e-renamed.md");
      await page.keyboard.press("Enter");
      for (let i = 0; i < 40 && !existsSync(moved); i++) await sleep(250);
      expect(existsSync(moved) && !existsSync(made), "not renamed on disk");
    } finally {
      for (const f of [made, moved]) if (existsSync(f)) unlinkSync(f);
    }
  });

  await check("editor: Search in the folder finds and opens a match", async () => {
    const page = await open(`view=editor&folder=${folder}&file=TODO.txt`, { wait: "iframe.monaco" });
    await intoCode(page);
    await page.keyboard.press("Meta+Shift+f");
    await paletteReady(page);
    await page.keyboard.type("License");
    await until(page, () => (document.querySelector(".palette-layer")?.innerText ?? "").includes("README.md"), null, "a match in README.md", 15000);
    await page.keyboard.press("Enter");
    await until(page, () => [...document.querySelectorAll(".tab")].some((t) => t.innerText.includes("README.md") && t.classList.contains("active")), null, "README.md open");
  });

  await check("editor: split, and the Markdown preview", async () => {
    const page = await open(`view=editor&folder=${folder}&file=README.md`, { wait: "iframe.monaco" });
    await intoCode(page);
    await page.keyboard.press("Meta+\\");
    await until(page, () => { try { return document.querySelector("iframe.monaco").contentDocument.querySelectorAll(".monaco-editor").length >= 2; } catch { return false; } }, null, "two editors side by side");
    await page.keyboard.press("Meta+Shift+v");
    await until(page, () => (document.querySelector(".md-preview")?.innerText ?? "").length > 20, null, "the preview");
  }, { expected: [/^http 404 .*hero\.png/] });
}

async function settingsMore() {
  await check("settings: a note is added, then deleted", async () => {
    // Notes an earlier run left, gone first.
    for (const note of (await daemon({ type: "knowledge_list" })).notes ?? []) {
      if (/e2e/i.test(note.title + note.body)) await daemon({ type: "knowledge_delete", note });
    }
    const page = await open("view=settings&page=knowledge", { size: [900, 700] });
    await click(page, ".mac-button", "New note");
    await page.locator(".note-title-field").fill("E2E note");
    await page.locator(".note-editor textarea:not(.note-title-field)").first().fill("Written and removed by the e2e run.");
    await click(page, ".mac-button", "Add note");
    await until(page, () => [...document.querySelectorAll(".note-title")].some((e) => e.innerText === "E2E note"), null, "the note in the list");
    await page.locator(".note-row").filter({ hasText: "E2E note" }).first().click();
    await click(page, ".mac-button", "Delete");
    await until(page, () => ![...document.querySelectorAll(".note-title")].some((e) => e.innerText === "E2E note"), null, "the note gone");
  });

  await check("settings: Languages lists the servers", async () => {
    const page = await open("view=settings&page=languages", { size: [800, 700] });
    await until(page, () => /TypeScript/.test(document.querySelector(".settings-page, .settings-scroll")?.innerText ?? ""), null, "TypeScript among them", 15000);
  });

  await check("settings: a Notifications switch is kept", async () => {
    const page = await open("view=settings&page=notifications", { size: [800, 700] });
    await page.waitForSelector(".settings-row button, .settings-row input");
    const first = page.locator(".settings-row").filter({ has: page.locator(".mac-switch, [role=switch], input[type=checkbox]") }).first();
    const title = await first.locator(".row-title").innerText();
    const was = await first.locator(".mac-switch, [role=switch], input[type=checkbox]").first().getAttribute("aria-checked");
    await first.locator(".mac-switch, [role=switch], input[type=checkbox]").first().click();
    await page.reload();
    const again = page.locator(".settings-row").filter({ has: page.locator(".row-title", { hasText: title }) }).first();
    await again.waitFor();
    const now = await again.locator(".mac-switch, [role=switch], input[type=checkbox]").first().getAttribute("aria-checked");
    await again.locator(".mac-switch, [role=switch], input[type=checkbox]").first().click();
    expect(now !== was, `${title} went back to ${now} after a reload`);
  });
}

async function notify() {
  await check("notifications: an agent asked to notify tells the user when its turn ends", async () => {
    await context.grantPermissions(["notifications"], { origin: BASE });
    const page = await open(`agent=${ID.demo}`, {
      init: ["window.__notes = []; window.Notification = class { constructor(t, o) { window.__notes.push(t + ': ' + (o?.body ?? '')); } static permission = 'granted'; static requestPermission = async () => 'granted'; close() {} };"],
    });
    // While Agentman is on only it notifies, as on the Mac; an agent the user
    // asked to hear from notifies for itself.
    const row = page.locator(".sidebar .agent-line1").filter({ hasText: "bugs" }).first();
    await row.click({ button: "right" });
    await menu(page, "Notify Me About This Agent");
    try {
      await sleep(1000);
      execFileSync(`${HERE}stop-hook.sh`, ["0b9d4e21-7c3a-4f6e-8d12-5a6b7c8d9e10", "bugs", "Fixed the flaky resize test."]);
      await until(page, () => window.__notes.length > 0 && window.__notes, null, "a notification", 25000);
    } finally {
      await row.click({ button: "right" });
      await menu(page, "Notify Me About This Agent");
    }
  });
}

// MARK: - Run

const engines = which === "all" ? [["chromium", chromium], ["webkit", webkit]] : [[which, which === "webkit" ? webkit : chromium]];
for (const [index, [name, engine]] of engines.entries()) {
  engineName = name;
  // Each engine on a stack set up afresh: the last one ended an agent and
  // shipped the change.
  if (index > 0 || process.env.PREPARE) execFileSync(`${HERE}prepare.sh`, { stdio: "inherit" });
  const browser = await engine.launch({ headless: true });
  context = await browser.newContext({ deviceScaleFactor: 1 });
  const signIn = await context.newPage();
  await signIn.goto(signInLink());
  await signIn.waitForURL(`${BASE}/**`, { timeout: 15000 });
  await signIn.close();
  // Order matters only at the end: ending "empty" and dropping the
  // connection come last, after the rest has used them.
  if (want("shell")) { await shell(); await paletteSaid(); }
  if (want("sidebar")) { await sidebar(); await filtersAndAway(); }
  if (want("board")) { await board(); await boardMore(); await keyPrompt(); }
  if (want("usage")) await usage();
  if (want("settings")) { await settings(); await settingsMore(); await generalPage(); }
  if (want("editor")) { await editor(); await editorMore(); }
  if (want("notify")) await notify();
  if (want("newagent")) await newAgent();
  if (want("thread")) { await threadMore(); await thread(); await forms(); }
  if (want("connection")) await connection();
  await browser.close();
}
console.log(`\n${passed} passed, ${failures.length} failed, ${errorsSeen.length} with page errors`);
for (const e of errorsSeen) console.log("  errors: " + e);
for (const f of failures) console.log("  " + f);
process.exit(failures.length ? 1 : 0);
