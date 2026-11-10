#!/usr/bin/env node
// Pictures a page in headless Chrome, driven through the DevTools protocol:
// it waits in real time (the page's WebSockets keep real time, which
// Chrome's --virtual-time-budget does not), can run steps on the page, and
// takes the picture. Headless Chrome draws off screen: nothing shows.
//
//   node shot.mjs <url> <out.png> [--size 1560x980] [--wait 6] [--dark]
//                 [--step "<js>"]... [--after 1.5]
//
// Each --step is JavaScript run in the page, --after seconds apart, once
// the first --wait is over. Used by browser.sh.
import { spawn } from "node:child_process";
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
const steps = args.flatMap((a, i) => (a === "--step" ? [args[i + 1]] : []));
const [width, height] = opt("--size", "1560x980").split("x").map(Number);
const wait = Number(opt("--wait", "6")) * 1000;
const after = Number(opt("--after", "1.5")) * 1000;
const dark = args.includes("--dark");
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
  if (dark) await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: "dark" }] });
  await send("Page.enable");
  await send("Page.navigate", { url });
  await sleep(wait);
  for (const step of steps) {
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
