import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  completionKind,
  completionRange,
  editsByFile,
  escapeMarkdown,
  fromRange,
  lspLanguage,
  markdown,
  severity,
  symbolKind,
  target,
  toRange,
} from '../src/convert'

test('ranges move between counting from 0 and from 1, in UTF-16 units', () => {
  const lsp = { start: { line: 0, character: 8 }, end: { line: 2, character: 0 } }
  assert.deepEqual(toRange(lsp), { startLineNumber: 1, startColumn: 9, endLineNumber: 3, endColumn: 1 })
  assert.deepEqual(fromRange(toRange(lsp)), lsp)
  // "emoji 😀 here": the emoji is two units, so " here" starts at 8.
  assert.equal('emoji 😀'.length, 8)
})

test('files get the language id their server expects', () => {
  assert.equal(lspLanguage('/a/App.tsx'), 'typescriptreact')
  assert.equal(lspLanguage('/a/index.mjs'), 'javascript')
  assert.equal(lspLanguage('/a/main.go'), 'go')
  assert.equal(lspLanguage('/a/View.swift'), 'swift')
  assert.equal(lspLanguage('/a/x.h'), 'c')
  assert.equal(lspLanguage('/a/Makefile'), null)
  assert.equal(lspLanguage('/a/notes.txt'), null)
})

test('hover contents become markdown, plain text kept plain', () => {
  assert.deepEqual(markdown({ kind: 'markdown', value: '**bold**' }), [{ value: '**bold**' }])
  assert.deepEqual(markdown({ kind: 'plaintext', value: 'a*b' }), [{ value: 'a\\*b' }])
  assert.deepEqual(markdown([{ language: 'go', value: 'func f()' }, '']), [{ value: '```go\nfunc f()\n```' }])
  assert.deepEqual(markdown(null), [])
  assert.equal(escapeMarkdown('[x](y)'), '\\[x\\]\\(y\\)')
})

test('kinds and severities line up', () => {
  assert.equal(completionKind(3), 'Function')
  assert.equal(completionKind(25), 'TypeParameter')
  assert.equal(completionKind(undefined), 'Text')
  assert.equal(completionKind(99), 'Text')
  assert.equal(symbolKind(12), 11) // Function
  assert.equal(severity(1), 8) // Error
  assert.equal(severity(2), 4) // Warning
  assert.equal(severity(undefined), 8)
})

test('completions go where the server says, or over the word being typed', () => {
  const position = { lineNumber: 4, column: 7 }
  const word = { startColumn: 3, endColumn: 9 }
  assert.deepEqual(completionRange(undefined, word, position), {
    insert: { startLineNumber: 4, startColumn: 3, endLineNumber: 4, endColumn: 7 },
    replace: { startLineNumber: 4, startColumn: 3, endLineNumber: 4, endColumn: 9 },
  })
  const range = { start: { line: 3, character: 1 }, end: { line: 3, character: 6 } }
  assert.deepEqual(completionRange({ range }, word, position), toRange(range))
  const replace = { start: { line: 3, character: 1 }, end: { line: 3, character: 9 } }
  assert.deepEqual(completionRange({ insert: range, replace }, word, position), { insert: toRange(range), replace: toRange(replace) })
})

test('workspace edits group by file, from either form', () => {
  const edit = { range: { start: { line: 0, character: 0 }, end: { line: 0, character: 1 } }, newText: 'x' }
  const changes = editsByFile({ changes: { 'file:///a.go': [edit], 'file:///b.go': [edit, edit] } })
  assert.equal(changes.get('file:///b.go')?.length, 2)
  const documentChanges = editsByFile({
    documentChanges: [{ textDocument: { uri: 'file:///a.go', version: 3 }, edits: [edit] }, { kind: 'create' }],
    changes: { 'file:///ignored.go': [edit] },
  })
  assert.deepEqual([...documentChanges.keys()], ['file:///a.go'])
  assert.equal(editsByFile(null).size, 0)
})

test('locations and links both become a file and range to go to', () => {
  const range = { start: { line: 9, character: 5 }, end: { line: 9, character: 12 } }
  assert.deepEqual(target({ uri: 'file:///a.go', range }), { uri: 'file:///a.go', range: toRange(range) })
  const link = target({ targetUri: 'file:///b.go', targetRange: range, targetSelectionRange: range })
  assert.equal(link.uri, 'file:///b.go')
  assert.deepEqual(link.targetSelectionRange, toRange(range))
})
