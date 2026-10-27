// Which lines changed between two versions of a file, by the line diff VS
// Code shows changes with: lines new, lines changed, and lines taken out.
// Apart from the editor, so tests can run it.

import { linesDiffComputers } from 'monaco-editor/editor/common/diff/linesDiffComputers'

export type Kind = 'added' | 'modified' | 'deleted'

/** One change, by line from 1 in the file as it is. A deletion has no lines
 * of its own: it sits after line `start`, 0 for the top of the file. */
export interface Change {
  kind: Kind
  start: number
  end: number
  /** The lines git has in its place. */
  original: string[]
}

/** The changes between git's lines and the file's, as VS Code finds them. */
export function lineChanges(base: string[], lines: string[]): Change[] {
  // A file git has nothing of yet, every line of it new.
  if (base.length === 0) {
    const empty = lines.length === 0 || (lines.length === 1 && lines[0] === '')
    return empty ? [] : [{ kind: 'added', start: 1, end: lines.length, original: [] }]
  }
  const diff = linesDiffComputers.getDefault().computeDiff(base, lines, {
    ignoreTrimWhitespace: false,
    maxComputationTimeMs: 500,
    computeMoves: false,
  })
  return diff.changes.flatMap((change): Change[] => {
    const original = base.slice(change.original.startLineNumber - 1, change.original.endLineNumberExclusive - 1)
    const start = change.modified.startLineNumber
    const end = change.modified.endLineNumberExclusive - 1
    if (change.original.isEmpty) return [{ kind: 'added', start, end, original }]
    if (change.modified.isEmpty) return [{ kind: 'deleted', start: start - 1, end: start - 1, original }]
    return split({ kind: 'modified', start, end, original }, change.innerChanges, lines)
  })
}

/**
 * A block that grew: some of its lines are new. A line changed and a function
 * written under it are one block to a diff, as git shows them too, and all of
 * it would be marked changed. The lines changed are as many as the block
 * replaced, together, where most of the old text still is; the others are new.
 */
function split(change: Change, inner: readonly InnerChange[] | undefined, lines: string[]): Change[] {
  const n = change.original.length
  const m = change.end - change.start + 1
  if (!inner || m <= n) return [change]
  // How much of each line is text it had before.
  const kept: number[] = []
  for (let line = change.start; line <= change.end; line++) {
    const length = lines[line - 1].length
    let changed = 0
    for (const { modifiedRange: r } of inner) {
      if (line < r.startLineNumber || line > r.endLineNumber) continue
      const from = line === r.startLineNumber ? r.startColumn : 1
      const to = line === r.endLineNumber ? r.endColumn : length + 1
      changed += Math.max(0, Math.min(to, length + 1) - from)
    }
    kept.push(Math.max(0, length - changed))
  }
  let best = -1
  let bestKept = 0
  let sum = kept.slice(0, n).reduce((a, b) => a + b, 0)
  for (let at = 0; at + n <= m; at++) {
    if (at > 0) sum += kept[at + n - 1] - kept[at - 1]
    if (sum > bestKept) {
      best = at
      bestKept = sum
    }
  }
  // Nothing of the old text anywhere: a block rewritten, all of it changed.
  if (best < 0) return [change]
  const from = change.start + best
  const to = from + n - 1
  const parts: Change[] = []
  if (from > change.start) parts.push({ kind: 'added', start: change.start, end: from - 1, original: [] })
  parts.push({ kind: 'modified', start: from, end: to, original: change.original })
  if (to < change.end) parts.push({ kind: 'added', start: to + 1, end: change.end, original: [] })
  return parts
}

interface InnerChange {
  readonly modifiedRange: { startLineNumber: number; startColumn: number; endLineNumber: number; endColumn: number }
}

export function splitLines(text: string): string[] {
  return text.split(/\r\n|\r|\n/)
}
