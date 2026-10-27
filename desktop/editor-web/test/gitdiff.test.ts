import assert from 'node:assert/strict'
import { test } from 'node:test'
import { lineChanges, splitLines } from '../src/linediff'

const lines = (text: string) => splitLines(text)

test('new lines, changed lines and lines taken out', () => {
  const base = lines('package main\n\nfunc main() {\n\tprintln("a")\n\tprintln("b")\n}\n')
  const now = lines('package main\n\nimport "os"\n\nfunc main() {\n\tprintln("A")\n}\n')
  assert.deepEqual(lineChanges(base, now), [
    { kind: 'added', start: 2, end: 3, original: [] },
    { kind: 'modified', start: 6, end: 6, original: ['\tprintln("a")', '\tprintln("b")'] },
  ])
})

test('lines taken out sit after the line above them, or at the top', () => {
  const base = lines('one\ntwo\nthree\nfour\n')
  assert.deepEqual(lineChanges(base, lines('one\nfour\n')), [{ kind: 'deleted', start: 1, end: 1, original: ['two', 'three'] }])
  assert.deepEqual(lineChanges(base, lines('three\nfour\n')), [{ kind: 'deleted', start: 0, end: 0, original: ['one', 'two'] }])
})

test('a file git has nothing of is all new; an unchanged one has no changes', () => {
  assert.deepEqual(lineChanges([], lines('a\nb')), [{ kind: 'added', start: 1, end: 2, original: [] }])
  assert.deepEqual(lineChanges([], ['']), [])
  assert.deepEqual(lineChanges(lines('a\r\nb\r\n'), lines('a\nb\n')), [])
})

test('a line changed with new lines under it: the line changed, the rest new', () => {
  const base = lines('func Greet(name string) string {\n\treturn "hello " + name\n}\n\nfunc main() {\n}\n')
  const now = lines('func Greet(name string) string {\n\treturn "Hello, " + strings.TrimSpace(name) + "!"\n}\n\n// Farewell says goodbye.\nfunc Farewell(name string) string {\n\treturn "Goodbye, " + name\n}\n\nfunc main() {\n}\n')
  assert.deepEqual(lineChanges(base, now), [
    { kind: 'modified', start: 2, end: 2, original: ['\treturn "hello " + name'] },
    { kind: 'added', start: 3, end: 7, original: [] },
  ])
  // New lines above the one changed.
  assert.deepEqual(lineChanges(lines('a\nlet total = 1\nz'), lines('a\nlet first = 0\nlet second = 0\nlet total = 2\nz')), [
    { kind: 'added', start: 2, end: 3, original: [] },
    { kind: 'modified', start: 4, end: 4, original: ['let total = 1'] },
  ])
  // Rewritten through, with nothing of the old: all changed.
  assert.deepEqual(lineChanges(lines('a\nxyz\nb'), lines('a\n123\n456\nb')).map((c) => c.kind), ['modified'])
})

test('whitespace changes count', () => {
  assert.deepEqual(lineChanges(lines('if x {\n  y()\n}'), lines('if x {\n    y()\n}')), [
    { kind: 'modified', start: 2, end: 2, original: ['  y()'] },
  ])
})
