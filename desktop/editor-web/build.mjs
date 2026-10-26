// Builds the editor page that Agentman for Mac shows in each editor window:
// Monaco, without its own language services (language servers supply those),
// into ../Agentman/Resources/EditorWeb, which the app serves from its
// agentman-editor:// scheme.
//
//   npm run build            once
//   npm run build -- --dev   unminified, with source maps

import { build } from 'esbuild'
import { copyFile, mkdir, readFile, rm } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const out = join(here, '../Agentman/Resources/EditorWeb')
const dev = process.argv.includes('--dev')
const monaco = join(here, 'node_modules/monaco-editor/esm/vs')

// Everything editor.main.js brings in except the TypeScript, JSON, CSS and
// HTML language services and Monaco's own LSP client: completions and errors
// come from the language servers `am` runs, and both at once would double them.
const core = {
  name: 'monaco-core',
  setup(b) {
    b.onResolve({ filter: /^monaco-core$/ }, () => ({ path: join(monaco, 'editor/editor.main.js'), namespace: 'monaco-core' }))
    b.onLoad({ filter: /.*/, namespace: 'monaco-core' }, async ({ path }) => {
      const source = await readFile(path, 'utf8')
      const kept = source
        .split('\n')
        .filter((line) => !/languages\/features\/|monaco-lsp-client|__src_languages_features|index as lsp/.test(line))
        .join('\n')
      return { contents: kept, resolveDir: dirname(path), loader: 'js' }
    })
  },
}

await rm(out, { recursive: true, force: true })
await mkdir(out, { recursive: true })

// The worker that diffs and finds links off the main thread. getWorker is
// synchronous, so its source travels inside the page and starts from a blob.
const worker = await build({
  entryPoints: [join(monaco, 'editor/editor.worker.js')],
  bundle: true,
  format: 'iife',
  minify: !dev,
  write: false,
  logLevel: 'warning',
})

await build({
  entryPoints: [join(here, 'src/main.ts')],
  bundle: true,
  format: 'esm',
  splitting: true,
  outdir: out,
  entryNames: '[name]',
  chunkNames: 'chunks/[name]-[hash]',
  minify: !dev,
  sourcemap: dev,
  target: 'safari17',
  loader: { '.ttf': 'file' },
  assetNames: '[name]',
  define: { WORKER_SOURCE: JSON.stringify(worker.outputFiles[0].text) },
  plugins: [core],
  logLevel: 'warning',
})

await copyFile(join(here, 'src/index.html'), join(out, 'index.html'))
console.log(`editor → ${out}`)
