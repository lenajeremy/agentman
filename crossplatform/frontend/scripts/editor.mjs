// Brings the Mac app's editor page into this app, unchanged: Monaco, the
// language-server client, Vim and the git bars, from desktop/editor-web.
//
// Its build (desktop/editor-web/build.sh, which builds only when the page's
// sources changed) writes desktop/Agentman/Resources/EditorWeb. That folder
// is copied to public/editor, with two additions: the code font the page
// asks for, and host-shim.js, loaded before the page's own script, which
// stands in for WKWebView's message handlers by talking to this app's
// window (src/Editor.tsx) instead.

import { execFileSync } from 'node:child_process'
import { cp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const desktop = join(here, '../../../desktop')
const built = join(desktop, 'Agentman/Resources/EditorWeb')
const fonts = join(desktop, 'Agentman/Resources/Fonts')
const out = join(here, '../public/editor')
const appFonts = join(here, '../public/fonts')

execFileSync('sh', [join(desktop, 'editor-web/build.sh')], { stdio: 'inherit' })

await rm(out, { recursive: true, force: true })
await cp(built, out, { recursive: true })
await mkdir(join(out, 'fonts'), { recursive: true })
await mkdir(appFonts, { recursive: true })
for (const font of ['GoogleSansCode.ttf', 'GoogleSansCode-Italic.ttf', 'OFL-GoogleSansCode.txt']) {
  await cp(join(fonts, font), join(out, 'fonts', font))
}
for (const font of ['GoogleSans.ttf', 'GoogleSans-Italic.ttf', 'GoogleSansCode.ttf', 'OFL-GoogleSans.txt', 'OFL-GoogleSansCode.txt']) {
  await cp(join(fonts, font), join(appFonts, font))
}

const page = join(out, 'index.html')
const html = await readFile(page, 'utf8')
const entry = '<script type="module" src="main.js"></script>'
if (!html.includes(entry)) throw new Error('editor page: no main.js entry to put the shim before')
await writeFile(page, html.replace(entry, `<script src="host-shim.js"></script>\n${entry}`))
await cp(join(here, 'host-shim.js'), join(out, 'host-shim.js'))
console.log('editor page copied to public/editor')
