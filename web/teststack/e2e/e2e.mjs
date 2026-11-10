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
import { execFileSync, execSync } from "node:child_process";
import { appendFileSync, existsSync, readFileSync, unlinkSync, writeFileSync } from "node:fs";
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
  Object.defineProperty(navigator, "clipboard", { value: { writeText: async (t) => { window.__copied = t; }, readText: async () => "" } });
  window.__opened = []; window.open = (u) => { window.__opened.push(String(u)); return null; };
  window.__links = []; { const P = BroadcastChannel.prototype.postMessage; BroadcastChannel.prototype.postMessage = function (m) { if (this.name === "agentman-links") window.__links.push(String(m)); return P.call(this, m); }; }
`;

const args = process.argv.slice(2);
const which = args[0] && !["chromium", "webkit", "all"].includes(args[0]) ? "all" : args[0] || "all";
const areas = args.filter((a) => !["chromium", "webkit", "all"].includes(a));
const want = (area) => !areas.length || areas.includes(area);

let passed = 0;
const failures = [];
let context;
let engineName = "";

function signInLink() {
  return execFileSync(`${T}/am`, ["web", "--print", "--config-home", H], { encoding: "utf8" }).trim().split("\n").pop();
}

/** A page of the app, signed in, with fakes set up before its scripts. */
async function open(query = "", { init = [], size = [1400, 900], dark = false, wait = "" } = {}) {
  const page = await context.newPage();
  await page.setViewportSize({ width: size[0], height: size[1] });
  await page.emulateMedia({ colorScheme: dark ? "dark" : "light" });
  await page.addInitScript(STUBS);
  for (const script of init) await page.addInitScript(script);
  await page.goto(`${BASE}/${query ? "?" + query : ""}`);
  await page.waitForFunction(() => document.querySelector(".sidebar-footer, .board-column, .settings, .editor-window, .new-agent-line, .usage-page"), null, { timeout: 15000 });
  if (wait) await page.waitForSelector(wait, { timeout: 15000 });
  return page;
}

async function check(name, run) {
  const started = Date.now();
  try {
    await run();
    passed++;
    console.log(`PASS ${engineName} ${name} (${((Date.now() - started) / 1000).toFixed(1)}s)`);
  } catch (err) {
    const why = String(err?.message ?? err).split("\n")[0].slice(0, 240);
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
    const has = await page.locator(".page-menu-item").filter({ hasText: item }).count();
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
    const lines = readFileSync(file, "utf8").trim().split("\n");
    const last = JSON.parse(lines.at(-1));
    const said = `Reconnect check ${Date.now()}`;
    appendFileSync(file, JSON.stringify({ ...last, uuid: crypto.randomUUID(), parentUuid: last.uuid, timestamp: new Date().toISOString(), message: { role: "assistant", model: "claude-opus-5-5", content: [{ type: "text", text: said }] } }) + "\n");
    await until(page, (s) => [...document.querySelectorAll(".thread-item")].some((e) => e.innerText.includes(s)), said, "the new message in the thread", 20000);
  });
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
    await page.keyboard.type("bugs");
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
  });
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
  if (want("shell")) await shell();
  if (want("sidebar")) await sidebar();
  if (want("board")) { await board(); await boardMore(); }
  if (want("usage")) await usage();
  if (want("settings")) { await settings(); await settingsMore(); }
  if (want("editor")) { await editor(); await editorMore(); }
  if (want("notify")) await notify();
  if (want("newagent")) await newAgent();
  if (want("thread")) { await threadMore(); await thread(); }
  if (want("connection")) await connection();
  await browser.close();
}
console.log(`\n${passed} passed, ${failures.length} failed`);
for (const f of failures) console.log("  " + f);
process.exit(failures.length ? 1 : 0);
