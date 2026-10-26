// Between LSP's shapes and Monaco's: positions and ranges, markdown, the
// kinds of things, and edits. Nothing here touches Monaco at runtime, so it
// can be tested without a page (convert.test.ts).

export interface LspPosition {
  line: number
  character: number
}

export interface LspRange {
  start: LspPosition
  end: LspPosition
}

export interface LspTextEdit {
  range: LspRange
  newText: string
}

/** Monaco's shapes, as plain objects. */
export interface Position {
  lineNumber: number
  column: number
}

export interface Range {
  startLineNumber: number
  startColumn: number
  endLineNumber: number
  endColumn: number
}

// Both count columns in UTF-16 units: LSP's from 0, Monaco's from 1.

export function toPosition(p: LspPosition): Position {
  return { lineNumber: p.line + 1, column: p.character + 1 }
}

export function fromPosition(p: Position): LspPosition {
  return { line: p.lineNumber - 1, character: p.column - 1 }
}

export function toRange(r: LspRange): Range {
  return {
    startLineNumber: r.start.line + 1,
    startColumn: r.start.character + 1,
    endLineNumber: r.end.line + 1,
    endColumn: r.end.character + 1,
  }
}

export function fromRange(r: Range): LspRange {
  return {
    start: { line: r.startLineNumber - 1, character: r.startColumn - 1 },
    end: { line: r.endLineNumber - 1, character: r.endColumn - 1 },
  }
}

/** The LSP language id for a file, by its name, or null when no server
 * speaks it. Monaco calls .tsx TypeScript; servers need to be told it's React. */
export function lspLanguage(path: string): string | null {
  const name = path.split('/').pop() ?? ''
  const ext = name.includes('.') ? name.split('.').pop()!.toLowerCase() : ''
  switch (ext) {
    case 'go':
      return 'go'
    case 'ts':
    case 'mts':
    case 'cts':
      return 'typescript'
    case 'tsx':
      return 'typescriptreact'
    case 'js':
    case 'mjs':
    case 'cjs':
      return 'javascript'
    case 'jsx':
      return 'javascriptreact'
    case 'py':
    case 'pyi':
      return 'python'
    case 'rs':
      return 'rust'
    case 'swift':
      return 'swift'
    case 'c':
    case 'h':
      return 'c'
    case 'cc':
    case 'cpp':
    case 'cxx':
    case 'hpp':
    case 'hh':
    case 'hxx':
      return 'cpp'
    case 'm':
      return 'objective-c'
    case 'mm':
      return 'objective-cpp'
    default:
      return null
  }
}

// MARK: - Markdown

type MarkedString = string | { language: string; value: string }
type MarkupContent = { kind: 'markdown' | 'plaintext'; value: string }
export type HoverContents = MarkedString | MarkedString[] | MarkupContent

/** Text that shows as written, not as markdown. */
export function escapeMarkdown(text: string): string {
  return text.replace(/[\\`*_{}[\]()#+\-.!|<>~]/g, '\\$&')
}

export function markdown(contents: HoverContents | null | undefined): { value: string }[] {
  if (!contents) return []
  const list = Array.isArray(contents) ? contents : [contents]
  return list
    .map((item) => {
      if (typeof item === 'string') return { value: item }
      if ('kind' in item) return { value: item.kind === 'markdown' ? item.value : escapeMarkdown(item.value) }
      return { value: '```' + item.language + '\n' + item.value + '\n```' }
    })
    .filter((item) => item.value.trim() !== '')
}

export function documentation(doc: string | MarkupContent | undefined): string | { value: string } | undefined {
  if (doc === undefined || typeof doc === 'string') return doc
  return doc.kind === 'markdown' ? { value: doc.value } : doc.value
}

// MARK: - Kinds

/** Monaco's names for LSP's completion item kinds, 1 to 25. */
export const completionKinds = [
  'Text', 'Method', 'Function', 'Constructor', 'Field', 'Variable', 'Class', 'Interface', 'Module', 'Property',
  'Unit', 'Value', 'Enum', 'Keyword', 'Snippet', 'Color', 'File', 'Reference', 'Folder', 'EnumMember',
  'Constant', 'Struct', 'Event', 'Operator', 'TypeParameter',
] as const

export function completionKind(kind: number | undefined): (typeof completionKinds)[number] {
  return completionKinds[(kind ?? 1) - 1] ?? 'Text'
}

/** LSP's symbol kinds count from 1 (File), Monaco's from 0. */
export function symbolKind(kind: number): number {
  return Math.max(0, kind - 1)
}

/** LSP's highlight kinds, 1 Text, 2 Read, 3 Write; Monaco's from 0. */
export function highlightKind(kind: number | undefined): number {
  return Math.max(0, (kind ?? 1) - 1)
}

/** Monaco's marker severities for LSP's 1 Error … 4 Hint. */
export function severity(level: number | undefined): number {
  return [8, 8, 4, 2, 1][level ?? 1] ?? 8
}

// MARK: - Completion

/** Where a completion goes: the server's range, its insert and replace
 * ranges, or the word being typed. */
export function completionRange(
  edit: { range?: LspRange; insert?: LspRange; replace?: LspRange } | undefined,
  word: { startColumn: number; endColumn: number },
  position: Position,
): Range | { insert: Range; replace: Range } {
  if (edit?.insert && edit.replace) return { insert: toRange(edit.insert), replace: toRange(edit.replace) }
  if (edit?.range) return toRange(edit.range)
  return {
    insert: { startLineNumber: position.lineNumber, startColumn: word.startColumn, endLineNumber: position.lineNumber, endColumn: position.column },
    replace: { startLineNumber: position.lineNumber, startColumn: word.startColumn, endLineNumber: position.lineNumber, endColumn: word.endColumn },
  }
}

// MARK: - Edits

export interface WorkspaceEdit {
  changes?: Record<string, LspTextEdit[]>
  documentChanges?: ({ textDocument: { uri: string; version?: number | null }; edits: LspTextEdit[] } | { kind: string })[]
}

/** A workspace edit's text edits, by file. File creates, renames and
 * deletes aren't offered (the client says so), so they're left out. */
export function editsByFile(edit: WorkspaceEdit | null | undefined): Map<string, LspTextEdit[]> {
  const files = new Map<string, LspTextEdit[]>()
  const add = (uri: string, edits: LspTextEdit[]) => files.set(uri, [...(files.get(uri) ?? []), ...edits])
  for (const change of edit?.documentChanges ?? []) {
    if ('textDocument' in change) add(change.textDocument.uri, change.edits)
  }
  if (!edit?.documentChanges) {
    for (const [uri, edits] of Object.entries(edit?.changes ?? {})) add(uri, edits)
  }
  return files
}

/** A location or link, as a file and range to go to. */
export function target(
  location: { uri?: string; range?: LspRange; targetUri?: string; targetRange?: LspRange; targetSelectionRange?: LspRange; originSelectionRange?: LspRange },
): { uri: string; range: Range; originSelectionRange?: Range; targetSelectionRange?: Range } {
  if (location.targetUri) {
    return {
      uri: location.targetUri,
      range: toRange(location.targetRange ?? location.targetSelectionRange!),
      targetSelectionRange: location.targetSelectionRange ? toRange(location.targetSelectionRange) : undefined,
      originSelectionRange: location.originSelectionRange ? toRange(location.originSelectionRange) : undefined,
    }
  }
  return { uri: location.uri!, range: toRange(location.range!) }
}
