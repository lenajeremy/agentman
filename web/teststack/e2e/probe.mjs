// A one-off look at a page, for working out why a check failed:
//   node probe.mjs <chromium|webkit> <query> [step]...  (steps as in webkit.mjs)
import { chromium, webkit } from "playwright-core";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
const T = `${(process.env.TMPDIR || tmpdir()).replace(/\/$/, "")}/amx`;
const [engineName, query, ...steps] = process.argv.slice(2);
const browser = await (engineName === "webkit" ? webkit : chromium).launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1400, height: 900 } });
const page = await context.newPage();
await page.addInitScript(`window.__opened=[]; window.open=(u)=>{window.__opened.push(String(u));return null;};`);
const link = execFileSync(`${T}/am`, ["web", "--print", "--config-home", `${T}/home`], { encoding: "utf8" }).trim().split("\n").pop();
await page.goto(link + (query ? "&next=" + encodeURIComponent("/?" + query) : ""));
await page.waitForTimeout(6000);
for (const step of steps) {
  try {
    if (step.startsWith("key:")) await page.keyboard.press(step.slice(4));
    else if (step.startsWith("type:")) await page.keyboard.type(step.slice(5));
    else if (step.startsWith("click:")) await page.locator(step.slice(6)).first().click({ timeout: 5000 });
    else console.log(JSON.stringify(await page.evaluate(step)));
  } catch (e) { console.log("step failed: " + step.slice(0, 50) + " :: " + String(e.message).split("\n")[0]); }
  await page.waitForTimeout(1000);
}
await browser.close();
