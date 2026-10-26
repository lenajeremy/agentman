import assert from 'node:assert/strict'
import { test } from 'node:test'
import { INITIAL } from '@shikijs/vscode-textmate'
import { GRAMMARS, LONGEST_LINE, ROLES, SEMANTIC, highlighter, tokenizeLine } from '../src/roles'

/** Each word of some lines with the role the grammar gives it. */
async function roles(language: string, source: string): Promise<Map<string, string>> {
  const core = await highlighter()
  await core.loadLanguage((await GRAMMARS[language]()).default)
  const grammar = core.getLanguage(language)
  const found = new Map<string, string>()
  let stack = INITIAL
  for (const line of source.split('\n')) {
    const result = tokenizeLine(grammar, line, stack)
    stack = result.stack
    result.tokens.forEach((token, i) => {
      const end = result.tokens[i + 1]?.start ?? line.length
      const text = line.slice(token.start, end)
      for (const word of text.split(/[^\w$#@"'.:-]+/).filter(Boolean)) if (!found.has(word)) found.set(word, token.role)
    })
  }
  return found
}

test('TypeScript reads as VS Code reads it: declarations, calls, types, keys, literals', async () => {
  const found = await roles('tsx', [
    'const [captions, setCaptions] = useState<TranscriptTurn[]>([]);',
    'const editorRef = useRef<MonacoEditor.IStandaloneCodeEditor | null>(null);',
    'const pending = useRef({ notes: [], label: "x", count: 45, done: true });',
    '// Headphones or speakers',
    'const timer = useRef<ReturnType<typeof setTimeout> | null>(null);',
    'const minutes = view?.config.minutes ?? 45;',
    'export function useOpenAIRealtime(opts: GeminiLiveOptions) {',
    '  return <LiveRoom title="Call" />',
    '}',
  ].join('\n'))
  assert.equal(found.get('const'), 'keyword')
  assert.equal(found.get('captions'), 'text')
  assert.equal(found.get('useState'), 'function')
  assert.equal(found.get('useRef'), 'function')
  assert.equal(found.get('TranscriptTurn'), 'type')
  assert.equal(found.get('MonacoEditor'), 'type')
  assert.equal(found.get('IStandaloneCodeEditor'), 'type')
  // In a type, null is a type, as in VS Code; both read as keywords.
  assert.equal(found.get('null'), 'type.primitive')
  assert.equal(found.get('true'), 'constant')
  assert.equal(found.get('notes'), 'property')
  assert.equal(found.get('"x"'), 'string')
  assert.equal(found.get('45'), 'number')
  assert.equal(found.get('Headphones'), 'comment')
  assert.equal(found.get('typeof'), 'keyword')
  assert.equal(found.get('ReturnType'), 'type')
  assert.equal(found.get('config'), 'property')
  assert.equal(found.get('useOpenAIRealtime'), 'function')
  assert.equal(found.get('opts'), 'text')
  assert.equal(found.get('LiveRoom'), 'type')
  assert.equal(found.get('title'), 'property')
})

test('primitive types read as keywords, as in the editor the app had', async () => {
  const found = await roles('typescript', 'let a: string | number = 1; type B = Record<string, boolean>; let c = null')
  assert.equal(found.get('string'), 'type.primitive')
  assert.equal(found.get('number'), 'type.primitive')
  assert.equal(found.get('Record'), 'type')
  assert.equal(found.get('null'), 'constant')
})

test('Go, Python, Swift and Rust name their functions, types and keywords', async () => {
  const go = await roles('go', 'func (s *Server) Run(ctx context.Context) error {\n\treturn fmt.Errorf("x %d", 1)\n}')
  assert.equal(go.get('func'), 'keyword')
  assert.equal(go.get('Run'), 'function')
  assert.equal(go.get('Errorf'), 'function')
  assert.equal(go.get('error'), 'type.primitive')
  const python = await roles('python', 'def run(self, items: list[str]) -> None:\n    print("hi")  # done')
  assert.equal(python.get('def'), 'keyword')
  assert.equal(python.get('run'), 'function')
  assert.equal(python.get('print'), 'function')
  assert.equal(python.get('#'), 'comment')
  const swift = await roles('swift', 'struct Tab: Equatable {\n    let id: UUID\n    func save() -> Bool { true }\n}')
  assert.equal(swift.get('struct'), 'keyword')
  assert.equal(swift.get('Tab'), 'type')
  assert.equal(swift.get('save'), 'function')
  const rust = await roles('rust', 'fn main() { let x: u32 = 5; println!("{}", x); }')
  assert.equal(rust.get('fn'), 'keyword')
  assert.equal(rust.get('main'), 'function')
})

test('keys in data files read as properties', async () => {
  const json = await roles('json', '{ "name": "agentman", "private": true }')
  assert.equal(json.get('"name"'), 'property')
  assert.equal(json.get('"agentman"'), 'string')
  const yaml = await roles('yaml', 'jobs:\n  build:\n    runs-on: macos-15')
  assert.equal(yaml.get('jobs'), 'property')
})

test('every grammar loads and reads a line', async () => {
  const core = await highlighter()
  for (const [name, load] of Object.entries(GRAMMARS)) {
    await core.loadLanguage((await load()).default)
    const { tokens } = tokenizeLine(core.getLanguage(name), 'let x = "a" // b', INITIAL)
    assert.ok(tokens.length > 0, name)
  }
})

test('a very long line is left plain', async () => {
  const core = await highlighter()
  await core.loadLanguage((await GRAMMARS.typescript()).default)
  const { tokens } = tokenizeLine(core.getLanguage('typescript'), 'x'.repeat(LONGEST_LINE + 1), INITIAL)
  assert.deepEqual(tokens, [{ start: 0, role: '' }])
})

test('what language servers say maps to roles the grammars use', () => {
  for (const role of Object.values(SEMANTIC)) assert.ok(ROLES.includes(role), role)
})
