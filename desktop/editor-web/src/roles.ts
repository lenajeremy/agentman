// What each piece of code is, by the grammars VS Code itself uses (TextMate,
// through Shiki): a keyword, a function, a type, a property, a string. The
// grammar names a token with scopes ("entity.name.function.ts"); a theme
// whose every colour stands for a role turns those scopes into the role, and
// the app's palette colours the role. Language servers' semantic tokens use
// the same role names, so a name keeps its colour when the server starts.

import { createHighlighterCore, type HighlighterCore, type LanguageRegistration } from '@shikijs/core'
import { createJavaScriptRegexEngine } from '@shikijs/engine-javascript'
import type { StateStack } from '@shikijs/vscode-textmate'

/** Each scope selector the roles stand for, the more particular after the
 * more general; within a role, VS Code's Dark+ is the guide. */
const SCOPES: Record<string, string[]> = {
  comment: ['comment', 'punctuation.definition.comment', 'markup.quote', 'comment variable', 'comment entity.name.type.instance'],
  string: [
    'string', 'punctuation.definition.string', 'markup.inline.raw', 'markup.fenced_code.block', 'markup.raw',
    'support.constant.property-value', 'string.unquoted', 'meta.attribute-selector string',
  ],
  'string.escape': ['constant.character.escape', 'constant.character.format.placeholder', 'constant.other.placeholder'],
  regexp: [
    'string.regexp', 'string.regexp keyword.operator', 'string.regexp punctuation', 'string.regexp constant.character.character-class',
    'string.regexp support.other', 'string.regexp constant.other', 'string.regexp keyword.control',
  ],
  number: ['constant.numeric', 'keyword.other.unit', 'constant.other.color', 'constant.character.numeric'],
  constant: [
    'constant.language', 'support.constant', 'variable.other.enummember', 'constant.other.enum',
    'constant.other.symbol', 'constant.other.key', 'variable.other.constant.ruby',
  ],
  keyword: [
    'keyword', 'storage', 'storage.type', 'storage.modifier', 'keyword.control', 'keyword.operator.new',
    'keyword.operator.expression', 'keyword.operator.wordlike', 'keyword.operator.logical.python', 'keyword.operator.instanceof',
    'keyword.operator.delete', 'keyword.operator.sizeof', 'keyword.operator.alignof', 'keyword.operator.cast',
    'keyword.operator.word', 'keyword.operator.borrow.and', 'variable.language', 'support.variable.self',
    'punctuation.definition.template-expression', 'punctuation.section.embedded', 'storage.type.class.jsdoc',
    'punctuation.definition.list.begin.markdown', 'markup.list punctuation.definition', 'keyword.other.import',
    'entity.name.function.preprocessor', 'meta.preprocessor punctuation.definition.directive',
    'punctuation.definition.heading', 'keyword.operator.channel.go',
  ],
  // Operators and arrows read as the text they join.
  operator: [
    'keyword.operator', 'storage.type.function.arrow', 'keyword.operator.type', 'keyword.operator.optional',
    'keyword.operator.rest', 'keyword.operator.spread', 'keyword.operator.ternary', 'punctuation.separator',
    'punctuation.accessor', 'keyword.operator.type.annotation', 'keyword.operator.definiteassignment',
  ],
  type: [
    'entity.name.type', 'entity.name.class', 'entity.name.namespace', 'entity.name.scope-resolution',
    'entity.other.inherited-class', 'support.class', 'support.type', 'entity.name.type.module', 'entity.name.struct',
    'entity.name.enum', 'entity.name.union', 'entity.name.trait', 'entity.name.interface', 'entity.name.impl',
    'storage.type.cs', 'storage.type.java', 'storage.type.generic', 'entity.name.type.instance.jsdoc',
    'support.class.component', 'meta.diff.header', 'meta.diff.range', 'entity.name.package.go',
    'entity.name.type.class.python', 'support.other.namespace', 'entity.name.label',
  ],
  // Built-in types (string, number, boolean, any, void), said as keywords.
  'type.primitive': [
    'support.type.primitive', 'support.type.builtin', 'storage.type.primitive', 'storage.type.built-in',
    'entity.name.type.primitive', 'storage.type.numeric.go', 'storage.type.string.go', 'storage.type.boolean.go',
    'storage.type.byte.go', 'storage.type.error.go', 'storage.type.rune.go', 'storage.type.uintptr.go',
    'entity.name.type.numeric.rust', 'entity.name.type.primitive.rust',
  ],
  function: [
    'entity.name.function', 'support.function', 'meta.function-call entity.name.function', 'meta.decorator',
    'entity.name.function.decorator', 'punctuation.decorator', 'entity.name.method', 'support.function.builtin',
    'meta.function-call.generic', 'entity.name.function.macro', 'entity.name.function.member',
    'entity.other.attribute-name.class.css', 'entity.other.attribute-name.id.css', 'entity.other.attribute-name.pseudo-class',
    'entity.other.attribute-name.pseudo-element', 'keyword.other.special-method', 'meta.attribute.rust',
    'storage.type.annotation', 'punctuation.definition.annotation', 'entity.name.function.target.makefile',
  ],
  property: [
    'variable.other.property', 'variable.other.object.property', 'support.variable.property', 'meta.object-literal.key',
    'support.type.property-name', 'entity.other.attribute-name', 'entity.name.tag.yaml', 'variable.other.member',
    'variable.other.constant.property', 'entity.name.variable.field', 'variable.other.field', 'keyword.other.definition.ini',
    'variable.other.env', 'variable.object.property', 'meta.definition.property variable', 'support.type.property-name.json',
    'meta.structure.dictionary.key.python', 'variable.other.key.toml', 'support.type.property-name.toml',
    'variable.other.dotenv',
  ],
  tag: ['entity.name.tag', 'entity.name.tag.html', 'meta.tag.sgml.doctype', 'entity.name.tag.css'],
  'delimiter.tag': ['punctuation.definition.tag'],
  heading: ['markup.heading', 'entity.name.section', 'markup.heading entity.name'],
  'markup.bold': ['markup.bold'],
  'markup.italic': ['markup.italic'],
  'markup.strike': ['markup.strikethrough'],
  link: ['markup.underline.link', 'string.other.link', 'meta.link.inline.markdown string.other.link.title'],
  'diff.inserted': ['markup.inserted', 'meta.diff.header.to-file', 'punctuation.definition.inserted'],
  'diff.deleted': ['markup.deleted', 'meta.diff.header.from-file', 'punctuation.definition.deleted'],
  'diff.changed': ['markup.changed'],
  invalid: ['invalid', 'invalid.illegal'],
  // What a language embeds in another reads as plain code.
  text: ['meta.embedded', 'source.groovy.embedded', 'variable.parameter', 'variable.other.readwrite', 'variable.other.object', 'variable'],
}

/** The roles, in a fixed order: each one's place is its stand-in colour. */
export const ROLES = Object.keys(SCOPES)

const standIn = (index: number) => `#0000${(index + 1).toString(16).padStart(2, '0').toUpperCase()}`

/** A theme whose every colour is a role. */
export const ROLES_THEME = {
  name: 'agentman-roles',
  type: 'dark' as const,
  fg: '#000000',
  bg: '#FFFFFF',
  settings: [
    { settings: { foreground: '#000000', background: '#FFFFFF' } },
    ...ROLES.map((role, index) => ({ scope: SCOPES[role], settings: { foreground: standIn(index) } })),
  ],
}

/** How the semantic tokens of language servers read, by type and by type
 * with modifiers: the same roles the grammars give. A type not here keeps
 * the grammar's colour only if the theme has no rule for it, so every one a
 * server is known to send is named. */
export const SEMANTIC: Record<string, string> = {
  namespace: 'type',
  type: 'type',
  class: 'type',
  enum: 'type',
  interface: 'type',
  struct: 'type',
  typeParameter: 'type',
  typeAlias: 'type',
  concept: 'type',
  builtinType: 'type.primitive',
  'type.defaultLibrary': 'type.primitive',
  parameter: 'text',
  variable: 'text',
  'variable.readonly.defaultLibrary': 'constant',
  'variable.defaultLibrary.readonly': 'constant',
  property: 'property',
  enumMember: 'constant',
  event: 'property',
  function: 'function',
  method: 'function',
  macro: 'function',
  decorator: 'function',
  attribute: 'function',
  derive: 'function',
  label: 'text',
  keyword: 'keyword',
  modifier: 'keyword',
  selfKeyword: 'keyword',
  selfTypeKeyword: 'keyword',
  lifetime: 'keyword',
  boolean: 'constant',
  comment: 'comment',
  string: 'string',
  escapeSequence: 'string.escape',
  formatSpecifier: 'string.escape',
  number: 'number',
  regexp: 'regexp',
  operator: 'operator',
}

let shiki: Promise<HighlighterCore> | null = null
let roleOf: string[] = []

/** One highlighter for the page, its theme the roles. */
export function highlighter(): Promise<HighlighterCore> {
  shiki ??= (async () => {
    const core = await createHighlighterCore({ themes: [ROLES_THEME], langs: [], engine: createJavaScriptRegexEngine({ forgiving: true }) })
    const { colorMap } = core.setTheme(ROLES_THEME.name)
    const byColor = new Map(ROLES.map((role, index) => [standIn(index), role]))
    roleOf = colorMap.map((color) => byColor.get(color.toUpperCase()) ?? '')
    return core
  })()
  return shiki
}

export interface RoleToken {
  start: number
  role: string
}

/** Past this, a line is left plain: a minified file shouldn't hold the page up. */
export const LONGEST_LINE = 10_000

/** A line's tokens by role, and the grammar's state after it. */
export function tokenizeLine(grammar: ReturnType<HighlighterCore['getLanguage']>, line: string, stack: StateStack): { tokens: RoleToken[]; stack: StateStack } {
  if (line.length > LONGEST_LINE) return { tokens: [{ start: 0, role: '' }], stack }
  const result = grammar.tokenizeLine2(line, stack, 200)
  const tokens: RoleToken[] = []
  const data = result.tokens
  for (let i = 0; i < data.length; i += 2) {
    // The foreground's place in the colour map: bits 15–23 of the metadata.
    const role = roleOf[(data[i + 1] & 0x00ff8000) >>> 15] ?? ''
    if (tokens.length && tokens[tokens.length - 1].role === role) continue
    tokens.push({ start: data[i], role })
  }
  return { tokens, stack: result.ruleStack }
}

type Grammars = () => Promise<{ default: LanguageRegistration[] }>

/** Each language's grammar, by the name Shiki gives it, loaded the first
 * time a file needs it. */
export const GRAMMARS: Record<string, Grammars> = {
  typescript: () => import('@shikijs/langs/typescript'),
  tsx: () => import('@shikijs/langs/tsx'),
  javascript: () => import('@shikijs/langs/javascript'),
  jsx: () => import('@shikijs/langs/jsx'),
  json: () => import('@shikijs/langs/json'),
  jsonc: () => import('@shikijs/langs/jsonc'),
  go: () => import('@shikijs/langs/go'),
  python: () => import('@shikijs/langs/python'),
  swift: () => import('@shikijs/langs/swift'),
  rust: () => import('@shikijs/langs/rust'),
  css: () => import('@shikijs/langs/css'),
  scss: () => import('@shikijs/langs/scss'),
  less: () => import('@shikijs/langs/less'),
  html: () => import('@shikijs/langs/html'),
  markdown: () => import('@shikijs/langs/markdown'),
  mdx: () => import('@shikijs/langs/mdx'),
  yaml: () => import('@shikijs/langs/yaml'),
  toml: () => import('@shikijs/langs/toml'),
  shellscript: () => import('@shikijs/langs/shellscript'),
  c: () => import('@shikijs/langs/c'),
  cpp: () => import('@shikijs/langs/cpp'),
  'objective-c': () => import('@shikijs/langs/objective-c'),
  java: () => import('@shikijs/langs/java'),
  kotlin: () => import('@shikijs/langs/kotlin'),
  scala: () => import('@shikijs/langs/scala'),
  csharp: () => import('@shikijs/langs/csharp'),
  fsharp: () => import('@shikijs/langs/fsharp'),
  ruby: () => import('@shikijs/langs/ruby'),
  php: () => import('@shikijs/langs/php'),
  perl: () => import('@shikijs/langs/perl'),
  lua: () => import('@shikijs/langs/lua'),
  r: () => import('@shikijs/langs/r'),
  julia: () => import('@shikijs/langs/julia'),
  dart: () => import('@shikijs/langs/dart'),
  elixir: () => import('@shikijs/langs/elixir'),
  clojure: () => import('@shikijs/langs/clojure'),
  sql: () => import('@shikijs/langs/sql'),
  docker: () => import('@shikijs/langs/docker'),
  graphql: () => import('@shikijs/langs/graphql'),
  xml: () => import('@shikijs/langs/xml'),
  diff: () => import('@shikijs/langs/diff'),
  make: () => import('@shikijs/langs/make'),
  dotenv: () => import('@shikijs/langs/dotenv'),
  ini: () => import('@shikijs/langs/ini'),
  prisma: () => import('@shikijs/langs/prisma'),
  zig: () => import('@shikijs/langs/zig'),
  nix: () => import('@shikijs/langs/nix'),
  proto: () => import('@shikijs/langs/proto'),
  hcl: () => import('@shikijs/langs/hcl'),
  terraform: () => import('@shikijs/langs/terraform'),
  svelte: () => import('@shikijs/langs/svelte'),
  vue: () => import('@shikijs/langs/vue'),
  astro: () => import('@shikijs/langs/astro'),
  powershell: () => import('@shikijs/langs/powershell'),
  solidity: () => import('@shikijs/langs/solidity'),
}
