#!/usr/bin/env node
// Pictures a page in headless Chrome, driven through the DevTools protocol:
// it waits in real time (the page's WebSockets keep real time, which
// Chrome's --virtual-time-budget does not), can run steps on the page, and
// takes the picture. Headless Chrome draws off screen: nothing shows.
//
//   node shot.mjs <url> <out.png> [--size 1560x980] [--wait 6] [--dark] [--blur]
//                 [--step "<js>"]... [--after 1.5] [--init "<js>"]
//                 [--grant notifications]...
//
// Each --step is JavaScript run in the page, --after seconds apart, once
// the first --wait is over; a step's value, if any, is printed. A step can
// instead be real input: "click:<x>,<y>" (CSS pixels), "type:<text>", or
// "key:<Meta+Shift+s>" (Meta, Ctrl, Alt, Shift and a key); or
// "open:<command>", which runs the command then and opens the link it
// prints in a second tab of the same browser, as am web would. --init runs
// before the page's own scripts; --grant gives the page a permission (as
// if the user had allowed it). Used by browser.sh.
import { execSync, spawn } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const args = process.argv.slice(2);
const url = args[0];
const out = args[1];
const opt = (name, fallback) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : fallback;
};
const all = (name) => args.flatMap((a, i) => (a === name ? [args[i + 1]] : []));
const steps = all("--step");
const inits = all("--init");
const grants = all("--grant");
const [width, height] = opt("--size", "1560x980").split("x").map(Number);
const wait = Number(opt("--wait", "6")) * 1000;
const after = Number(opt("--after", "1.5")) * 1000;
const dark = args.includes("--dark");
// The page has focus, as the window in front does (the screens draw an
// inactive window's look otherwise, as the Mac's do); --blur for that look.
const blur = args.includes("--blur");
if (!url || !out) {
  console.error("usage: shot.mjs <url> <out.png> [--size WxH] [--wait s] [--dark] [--step js]...");
  process.exit(2);
}

const chrome = process.env.CHROME ||
  ["/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/usr/bin/google-chrome", "/usr/bin/chromium"].find(existsSync);
const profile = mkdtempSync(join(tmpdir(), "amx-chrome-"));
const proc = spawn(chrome, [
  "--headless=new", "--disable-gpu", "--hide-scrollbars", "--no-first-run", "--no-default-browser-check",
  `--user-data-dir=${profile}`, "--remote-debugging-port=0", `--window-size=${width},${height}`, "about:blank",
], { stdio: "ignore" });
const done = (code) => {
  proc.kill();
  setTimeout(() => rmSync(profile, { recursive: true, force: true }), 300);
  process.exitCode = code;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

try {
  // Chrome writes its port to DevToolsActivePort once it is listening.
  let port;
  for (let i = 0; i < 100 && !port; i++) {
    await sleep(100);
    try {
      port = readFileSync(join(profile, "DevToolsActivePort"), "utf8").split("\n")[0];
    } catch {}
  }
  if (!port) throw new Error("Chrome didn't start");
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  const page = targets.find((t) => t.type === "page");
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((r, j) => ((ws.onopen = r), (ws.onerror = j)));
  let id = 0;
  const pending = new Map();
  ws.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.id && pending.has(msg.id)) {
      pending.get(msg.id)(msg);
      pending.delete(msg.id);
    }
  };
  const send = (method, params = {}) =>
    new Promise((resolve, reject) => {
      const n = ++id;
      pending.set(n, (msg) => (msg.error ? reject(new Error(`${method}: ${msg.error.message}`)) : resolve(msg.result)));
      ws.send(JSON.stringify({ id: n, method, params }));
    });

  await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 2, mobile: false });
  if (!blur) await send("Emulation.setFocusEmulationEnabled", { enabled: true });
  if (dark) await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: "dark" }] });
  await send("Page.enable");
  for (const source of inits) await send("Page.addScriptToEvaluateOnNewDocument", { source });
  if (grants.length) await send("Browser.grantPermissions", { permissions: grants, origin: new URL(url).origin });
  await send("Page.navigate", { url });
  await sleep(wait);
  const input = async (step) => {
    if (step.startsWith("click:")) {
      const [x, y] = step.slice(6).split(",").map(Number);
      for (const type of ["mousePressed", "mouseReleased"]) {
        await send("Input.dispatchMouseEvent", { type, x, y, button: "left", clickCount: 1 });
      }
    } else if (step.startsWith("type:")) {
      await send("Input.insertText", { text: step.slice(5) });
    } else {
      const parts = step.slice(4).split("+");
      const key = parts.pop();
      const bits = { Alt: 1, Ctrl: 2, Meta: 4, Shift: 8 };
      const modifiers = parts.reduce((m, p) => m | (bits[p] ?? 0), 0);
      const letter = key.length === 1;
      const code = letter ? (/[a-z]/i.test(key) ? `Key${key.toUpperCase()}` : /\d/.test(key) ? `Digit${key}` : "") : key;
      const keyCode = letter ? key.toUpperCase().charCodeAt(0) : { Enter: 13, Escape: 27, Tab: 9, Backspace: 8, ArrowUp: 38, ArrowDown: 40, ArrowLeft: 37, ArrowRight: 39 }[key] ?? 0;
      const base = { modifiers, key, code, windowsVirtualKeyCode: keyCode, nativeVirtualKeyCode: keyCode };
      await send("Input.dispatchKeyEvent", { type: "rawKeyDown", ...base });
      await send("Input.dispatchKeyEvent", { type: "keyUp", ...base });
    }
  };
  for (const step of steps) {
    if (step.startsWith("open:")) {
      const link = execSync(step.slice(5), { encoding: "utf8" }).trim().split("\n").pop();
      await send("Target.createTarget", { url: link });
      await sleep(after);
      continue;
    }
    if (/^(click|type|key):/.test(step)) {
      await input(step);
      await sleep(after);
      continue;
    }
    const result = await send("Runtime.evaluate", { expression: step, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) console.error(`step failed: ${step}\n  ${result.exceptionDetails.exception?.description ?? result.exceptionDetails.text}`);
    else if (result.result?.value !== undefined) console.log(JSON.stringify(result.result.value));
    await sleep(after);
  }
  const shot = await send("Page.captureScreenshot", { format: "png" });
  writeFileSync(out, Buffer.from(shot.data, "base64"));
  console.log(out);
  ws.close();
  done(0);
} catch (err) {
  console.error(`shot: ${err.message}`);
  done(1);
}
