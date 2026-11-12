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
import { cp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const desktop = join(here, '../../desktop')
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
// Rubik, the website's headline face, for the New theme's titles.
for (const font of ['GoogleSans.ttf', 'GoogleSans-Italic.ttf', 'GoogleSansCode.ttf', 'OFL-GoogleSans.txt', 'OFL-GoogleSansCode.txt', 'Rubik-Bold.ttf', 'Rubik-SemiBold.ttf', 'OFL-Rubik.txt']) {
  await cp(join(fonts, font), join(appFonts, font))
}

const page = join(out, 'index.html')
const html = await readFile(page, 'utf8')
const entry = '<script type="module" src="main.js"></script>'
if (!html.includes(entry)) throw new Error('editor page: no main.js entry to put the shim before')
await writeFile(page, html.replace(entry, `<script src="host-shim.js"></script>\n${entry}`))
await cp(join(here, 'host-shim.js'), join(out, 'host-shim.js'))
// Inter, for interface text where SF Pro isn't available (Linux, Windows).
await cp(join(desktop, '../site/fonts/inter-latin-variable.woff2'), join(appFonts, 'inter-latin-variable.woff2'))
await cp(join(desktop, '../site/fonts/OFL-Inter.txt'), join(appFonts, 'OFL-Inter.txt'))
console.log('editor page copied to public/editor')

// The Mac app's pictures, as they are: the Agentman mark, the agents' marks
// and the file icons, so both apps draw the same ones.
const assets = join(desktop, 'Agentman/Resources/Assets.xcassets')
const pictures = join(here, '../public/assets')
await rm(pictures, { recursive: true, force: true })
await mkdir(join(pictures, 'files'), { recursive: true })
await mkdir(join(pictures, 'agents'), { recursive: true })
for (const set of await readdir(assets)) {
  if (!set.endsWith('.imageset')) continue
  const name = set.slice(0, -'.imageset'.length)
  const files = (await readdir(join(assets, set))).filter((f) => f !== 'Contents.json')
  for (const file of files) {
    const ext = file.slice(file.lastIndexOf('.'))
    if (name === 'AgentmanBrand') await cp(join(assets, set, file), join(pictures, `agentman${ext}`))
    else if (name.startsWith('Agent-')) await cp(join(assets, set, file), join(pictures, 'agents', `${name.slice(6)}${ext}`))
    else if (name.startsWith('FileIcon-')) await cp(join(assets, set, file), join(pictures, 'files', `${name.slice(9)}${ext}`))
  }
}
// The website's stickers the New theme's empty pages show, animated, with
// a still frame for Reduce Motion (Design/Sticker.swift).
await mkdir(join(pictures, 'stickers'), { recursive: true })
for (const name of ['terminal', 'mascot']) {
  for (const ext of ['.webp', '.png']) {
    await cp(join(desktop, 'Agentman/Resources/Stickers', name + ext), join(pictures, 'stickers', name + ext))
  }
}
console.log('pictures copied to public/assets')

// The file icon theme's rules (desktop/Agentman/Design/FileIconMap.swift,
// generated from material-icon-theme), as JSON for src/editor/fileIcons.ts:
// by exact name, by extension, and by folder name, closed and open.
const iconMap = await readFile(join(desktop, 'Agentman/Design/FileIconMap.swift'), 'utf8')
const tables = {}
let table = null
for (const line of iconMap.split('\n')) {
  const start = line.match(/static let (\w+): \[String: String\] = \[/)
  if (start) {
    table = tables[start[1]] = {}
    continue
  }
  if (table && /^\s*\]/.test(line)) {
    table = null
    continue
  }
  const pair = table && line.match(/^\s*"((?:[^"\\]|\\.)*)": "((?:[^"\\]|\\.)*)",?$/)
  if (pair) table[JSON.parse(`"${pair[1]}"`)] = JSON.parse(`"${pair[2]}"`)
}
for (const name of ['fileNames', 'extensions', 'folderNames', 'openFolderNames']) {
  if (!tables[name] || Object.keys(tables[name]).length === 0) throw new Error(`FileIconMap.swift: no ${name}`)
}
await writeFile(join(here, '../src/editor/fileIconMap.json'), JSON.stringify(tables))
console.log('file icon map written to src/editor/fileIconMap.json')
