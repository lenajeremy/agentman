// Colours for code from VS Code's own grammars instead of Monaco's simpler
// tokenizers: each language's tokens are read by role (see roles.ts), and the
// theme gives each role a colour from the app's palette. The same roles
// colour what language servers say about names, once they say it.

import * as monaco from 'monaco-core'
import { INITIAL, type StateStack } from '@shikijs/vscode-textmate'
import { GRAMMARS, ROLES, SEMANTIC, highlighter, tokenizeLine } from './roles'

type Conf = monaco.languages.LanguageConfiguration

const brackets: Conf['brackets'] = [['{', '}'], ['[', ']'], ['(', ')']]
const pairs = [
  { open: '{', close: '}' },
  { open: '[', close: ']' },
  { open: '(', close: ')' },
  { open: '"', close: '"', notIn: ['string'] },
  { open: "'", close: "'", notIn: ['string', 'comment'] },
]
const hashes: Conf = { comments: { lineComment: '#' }, brackets, autoClosingPairs: pairs, surroundingPairs: pairs }
const slashes: Conf = { comments: { lineComment: '//', blockComment: ['/*', '*/'] }, brackets, autoClosingPairs: pairs, surroundingPairs: pairs }
const json: Conf = {
  comments: { lineComment: '//', blockComment: ['/*', '*/'] },
  brackets: [['{', '}'], ['[', ']']],
  autoClosingPairs: [
    { open: '{', close: '}', notIn: ['string'] },
    { open: '[', close: ']', notIn: ['string'] },
    { open: '"', close: '"', notIn: ['string'] },
  ],
}

/** Settings borrowed from a language Monaco already has. */
const like = (load: () => Promise<{ conf: Conf }>) => async () => (await load()).conf

interface Language {
  grammar: string
  /** For a language Monaco doesn't know: how files are recognised. */
  files?: Omit<monaco.languages.ILanguageExtensionPoint, 'id'>
  conf?: Conf | (() => Promise<Conf>)
}

/** Monaco's language for each grammar, and the languages it lacks. */
const LANGUAGES: Record<string, Language> = {
  typescript: { grammar: 'typescript' },
  typescriptreact: {
    grammar: 'tsx',
    files: { extensions: ['.tsx'], aliases: ['TypeScript JSX'] },
    conf: like(() => import('monaco-editor/esm/vs/languages/definitions/typescript/typescript.js')),
  },
  javascript: { grammar: 'javascript' },
  javascriptreact: {
    grammar: 'jsx',
    files: { extensions: ['.jsx'], aliases: ['JavaScript JSX'] },
    conf: like(() => import('monaco-editor/esm/vs/languages/definitions/javascript/javascript.js')),
  },
  json: {
    grammar: 'json',
    files: {
      extensions: ['.json', '.jsonl', '.webmanifest', '.code-workspace', '.map', '.har'],
      filenames: ['.babelrc', '.eslintrc', '.prettierrc', 'composer.lock', 'package.json', 'package-lock.json'],
      aliases: ['JSON'],
    },
    conf: json,
  },
  jsonc: {
    grammar: 'jsonc',
    files: {
      extensions: ['.jsonc'],
      filenames: ['tsconfig.json', 'jsconfig.json', 'devcontainer.json', '.swcrc', 'turbo.json', 'biome.json'],
      filenamePatterns: ['tsconfig.*.json'],
      aliases: ['JSON with Comments'],
    },
    conf: json,
  },
  go: { grammar: 'go' },
  python: { grammar: 'python' },
  swift: { grammar: 'swift' },
  rust: { grammar: 'rust' },
  css: { grammar: 'css' },
  scss: { grammar: 'scss' },
  less: { grammar: 'less' },
  html: { grammar: 'html' },
  markdown: { grammar: 'markdown' },
  mdx: { grammar: 'mdx' },
  yaml: { grammar: 'yaml' },
  shell: { grammar: 'shellscript', files: { extensions: ['.zsh', '.ksh'], filenames: ['.zshrc', '.bashrc', '.zprofile', '.bash_profile', '.profile'] } },
  c: { grammar: 'c' },
  cpp: { grammar: 'cpp' },
  'objective-c': { grammar: 'objective-c' },
  java: { grammar: 'java' },
  kotlin: { grammar: 'kotlin' },
  scala: { grammar: 'scala' },
  csharp: { grammar: 'csharp' },
  fsharp: { grammar: 'fsharp' },
  ruby: { grammar: 'ruby' },
  php: { grammar: 'php' },
  perl: { grammar: 'perl' },
  lua: { grammar: 'lua' },
  r: { grammar: 'r' },
  julia: { grammar: 'julia' },
  dart: { grammar: 'dart' },
  elixir: { grammar: 'elixir' },
  clojure: { grammar: 'clojure' },
  sql: { grammar: 'sql' },
  mysql: { grammar: 'sql' },
  pgsql: { grammar: 'sql' },
  dockerfile: { grammar: 'docker', files: { filenamePatterns: ['Dockerfile.*', '*.Dockerfile'], filenames: ['Containerfile'] } },
  graphql: { grammar: 'graphql' },
  xml: { grammar: 'xml' },
  ini: { grammar: 'ini' },
  hcl: { grammar: 'hcl' },
  protobuf: { grammar: 'proto' },
  powershell: { grammar: 'powershell' },
  solidity: { grammar: 'solidity' },
  toml: { grammar: 'toml', files: { extensions: ['.toml'], filenames: ['Cargo.lock', 'uv.lock', 'poetry.lock', 'Pipfile'], aliases: ['TOML'] }, conf: hashes },
  makefile: { grammar: 'make', files: { extensions: ['.mk', '.mak'], filenames: ['Makefile', 'makefile', 'GNUmakefile'], aliases: ['Makefile'] }, conf: hashes },
  dotenv: { grammar: 'dotenv', files: { filenames: ['.env'], filenamePatterns: ['.env.*', '*.env'], aliases: ['Environment'] }, conf: hashes },
  diff: { grammar: 'diff', files: { extensions: ['.diff', '.patch'], aliases: ['Diff'] } },
  prisma: { grammar: 'prisma', files: { extensions: ['.prisma'], aliases: ['Prisma'] }, conf: slashes },
  zig: { grammar: 'zig', files: { extensions: ['.zig', '.zon'], aliases: ['Zig'] }, conf: slashes },
  nix: { grammar: 'nix', files: { extensions: ['.nix'], aliases: ['Nix'] }, conf: hashes },
  terraform: { grammar: 'terraform', files: { extensions: ['.tf', '.tfvars'], aliases: ['Terraform'] }, conf: hashes },
  svelte: {
    grammar: 'svelte',
    files: { extensions: ['.svelte'], aliases: ['Svelte'] },
    conf: like(() => import('monaco-editor/esm/vs/languages/definitions/html/html.js')),
  },
  vue: {
    grammar: 'vue',
    files: { extensions: ['.vue'], aliases: ['Vue'] },
    conf: like(() => import('monaco-editor/esm/vs/languages/definitions/html/html.js')),
  },
  astro: {
    grammar: 'astro',
    files: { extensions: ['.astro'], aliases: ['Astro'] },
    conf: like(() => import('monaco-editor/esm/vs/languages/definitions/html/html.js')),
  },
}

class LineState implements monaco.languages.IState {
  constructor(readonly stack: StateStack) {}
  clone() {
    return this
  }
  equals(other: monaco.languages.IState) {
    return other instanceof LineState && (other.stack === this.stack || other.stack.equals(this.stack))
  }
}

async function tokensProvider(name: string, fail: (message: string) => void): Promise<monaco.languages.TokensProvider | null> {
  try {
    const core = await highlighter()
    if (!core.getLoadedLanguages().includes(name)) await core.loadLanguage((await GRAMMARS[name]()).default)
    const grammar = core.getLanguage(name)
    return {
      getInitialState: () => new LineState(INITIAL),
      tokenize(line, state) {
        const { tokens, stack } = tokenizeLine(grammar, line, (state as LineState).stack)
        return { tokens: tokens.map((t) => ({ startIndex: t.start, scopes: t.role })), endState: new LineState(stack) }
      },
    }
  } catch (e) {
    fail(`grammar ${name}: ${e}`)
    return null
  }
}

/** Puts the grammars in place of Monaco's tokenizers, and teaches Monaco the
 * languages it doesn't know. */
export function install(fail: (message: string) => void) {
  for (const [id, language] of Object.entries(LANGUAGES)) {
    if (language.files) monaco.languages.register({ id, ...language.files })
    const conf = language.conf
    if (conf) {
      monaco.languages.onLanguageEncountered(id, async () => {
        monaco.languages.setLanguageConfiguration(id, typeof conf === 'function' ? await conf() : conf)
      })
    }
    // Monaco's own tokenizers are factories too; this one takes their place.
    monaco.languages.registerTokensProviderFactory(id, {
      create: () => tokensProvider(language.grammar, fail),
    })
  }
}

/** The language for a file where its extension alone would mislead Monaco:
 * JSX is its own grammar. */
export function languageFor(uri: monaco.Uri): string | undefined {
  const path = uri.path.toLowerCase()
  if (path.endsWith('.tsx')) return 'typescriptreact'
  if (path.endsWith('.jsx')) return 'javascriptreact'
  return undefined
}

/** The editor's colours for each role, from the app's palette. */
export interface Syntax {
  [role: string]: string
}

/** Monaco's rules for a palette: the roles the grammars give, then what
 * language servers say, in the same colours. */
export function rules(syntax: Syntax): monaco.editor.ITokenThemeRule[] {
  const colour = (role: string) => (syntax[role] ?? syntax.text ?? '').replace('#', '')
  const styles: Record<string, string> = { heading: 'bold', 'markup.bold': 'bold', 'markup.italic': 'italic', 'markup.strike': 'strikethrough', link: 'underline' }
  return [
    ...ROLES.map((role) => ({ token: role, foreground: colour(role), fontStyle: styles[role] })),
    ...Object.entries(SEMANTIC).map(([type, role]) => ({ token: type, foreground: colour(role) })),
  ]
}
