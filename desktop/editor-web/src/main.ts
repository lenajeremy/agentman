// The editor page inside each Agentman editor window: one Monaco editor, one
// model per open tab. Swift owns the files, the tabs and saving; this page
// holds the live text and tells Swift every edit, so Swift's copy is always the
// one that gets saved.
//
// Swift calls the functions on window.agentman; this page answers through
// webkit.messageHandlers.editor.

import * as monaco from 'monaco-core'
import * as lsp from './lsp'

declare const WORKER_SOURCE: string

/** A file, as Swift opens it. */
interface Doc {
  id: string
  uri: string
  text: string
  tabSize: number
  insertSpaces: boolean
  /** Swift's revision of the text: bumped when the text is replaced from outside. */
  gen: number
  readOnly?: boolean
}

/** Colours from the app's palette, for the appearance in use. */
interface Theme {
  dark: boolean
  colors: Record<string, string>
  tokens: Record<string, string>
}

interface Open {
  model: monaco.editor.ITextModel
  gen: number
  view: monaco.editor.ICodeEditorViewState | null
  readOnly: boolean
}

const swift = (window as unknown as { webkit?: { messageHandlers?: { editor?: { postMessage(body: unknown): void } } } })
  .webkit?.messageHandlers?.editor

function post(message: Record<string, unknown>) {
  swift?.postMessage(message)
}

const asker = (window as unknown as { webkit?: { messageHandlers?: { editorAsk?: { postMessage(body: unknown): Promise<unknown> } } } })
  .webkit?.messageHandlers?.editorAsk

/** Asks Swift something and waits for the answer. */
async function ask<T>(message: Record<string, unknown>): Promise<T | null> {
  try {
    return ((await asker?.postMessage(message)) as T) ?? null
  } catch {
    return null
  }
}

// Monaco's helper worker (diffs, links) starts from a blob: getWorker must
// answer at once, and the source is already in this page.
const workerURL = URL.createObjectURL(new Blob([WORKER_SOURCE], { type: 'text/javascript' }))
;(self as unknown as { MonacoEnvironment: monaco.Environment }).MonacoEnvironment = {
  getWorker: () => new Worker(workerURL),
}

const docs = new Map<string, Open>()
let active: string | null = null
/** Set while Swift replaces a text, so the change isn't sent back to it. */
let quiet = false

const font = '"Google Sans Code", ui-monospace, Menlo, monospace'

const editor = monaco.editor.create(document.getElementById('editor')!, {
  model: null,
  automaticLayout: true,
  fontFamily: font,
  fontSize: 13,
  minimap: { enabled: false },
  scrollBeyondLastLine: false,
  renderLineHighlight: 'line',
  // The app shows its own, native menu.
  contextmenu: false,
  padding: { top: 10, bottom: 10 },
  overviewRulerBorder: false,
  hideCursorInOverviewRuler: true,
  scrollbar: { useShadows: false, verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
  fixedOverflowWidgets: true,
  stickyScroll: { enabled: false },
  guides: { indentation: true, bracketPairs: false },
  showFoldingControls: 'mouseover',
  wordBasedSuggestions: 'currentDocument',
  theme: 'vs',
})

editor.onDidChangeCursorPosition(() => reportCursor())

function tabOf(model: monaco.editor.ITextModel): string | undefined {
  for (const [id, open] of docs) if (open.model === model) return id
  return undefined
}

lsp.setup({
  post,
  read: (path) => ask<string>({ type: 'read', path }),
  open: (path) => post({ type: 'open', path }),
  editElsewhere: async (files) => (await ask<boolean>({ type: 'edit', files })) === true,
  tabOf,
})

// Go to Definition into another file: Swift opens its tab, and the range
// is revealed when it comes to the front.
monaco.editor.registerEditorOpener({
  openCodeEditor(_source, resource, selectionOrPosition) {
    if (resource.scheme !== 'file') return false
    if (selectionOrPosition) {
      const range = 'startLineNumber' in selectionOrPosition
        ? selectionOrPosition
        : monaco.Range.fromPositions(selectionOrPosition)
      lsp.reveal.set(resource.toString(), range)
    }
    post({ type: 'open', path: resource.fsPath })
    return true
  },
})

function reportCursor() {
  const position = editor.getPosition()
  if (!active || !position) return
  post({ type: 'cursor', id: active, line: position.lineNumber, column: position.column })
}

// MARK: - Keys

/**
 * Keys the app answers, wherever the keyboard is. Monaco never sees them, so
 * WebKit hands them back to the menu bar: Save, Go to File, the command
 * palette, the terminal and text size stay the app's, as they are everywhere
 * else in it.
 */
function isAppKey(e: KeyboardEvent): boolean {
  const key = e.key.toLowerCase()
  if (e.ctrlKey && !e.metaKey) return key === '`' || e.code === 'Backquote'
  if (!e.metaKey || e.ctrlKey) return false
  if (e.shiftKey && !e.altKey) return key === 'e'
  return ['s', 'p', 'k', 'n', 'o', 'w', ',', '=', '+', '-', '0'].includes(key)
}

window.addEventListener(
  'keydown',
  (e) => {
    if (isAppKey(e)) {
      e.stopImmediatePropagation()
    } else if (e.key === 'F1') {
      // Monaco's own command list: the app's ⌘K has these commands.
      e.stopImmediatePropagation()
      e.preventDefault()
    }
  },
  true,
)

// MARK: - The menu

/** What the right-click menu offers, in order; a missing title is a divider. */
const menuActions: { id: string; title?: string }[] = [
  { id: 'editor.action.revealDefinition', title: 'Go to Definition' },
  { id: 'editor.action.goToReferences', title: 'Find References' },
  { id: 'editor.action.rename', title: 'Rename Symbol' },
  { id: '-' },
  { id: 'editor.action.changeAll', title: 'Change All Occurrences' },
  { id: 'editor.action.formatDocument', title: 'Format Document' },
  { id: 'editor.action.quickFix', title: 'Quick Fix…' },
  { id: '-' },
  { id: 'cut', title: 'Cut' },
  { id: 'copy', title: 'Copy' },
  { id: 'paste', title: 'Paste' },
  { id: '-' },
  { id: 'editor.action.commentLine', title: 'Comment Lines' },
]

document.addEventListener('contextmenu', (e) => e.preventDefault(), true)

editor.onContextMenu((e) => {
  const model = editor.getModel()
  if (!model || !active) return
  // Right-click outside the selection moves the cursor there first.
  const position = e.target.position
  const selections = editor.getSelections() ?? []
  if (position && !selections.some((s) => s.containsPosition(position))) editor.setPosition(position)
  const items: { id: string; title?: string; enabled?: boolean }[] = []
  for (const item of menuActions) {
    if (item.id === '-') {
      if (items.length && items[items.length - 1].id !== '-') items.push({ id: '-' })
      continue
    }
    if (['cut', 'copy', 'paste'].includes(item.id)) {
      const empty = (editor.getSelection()?.isEmpty() ?? true)
      const readOnly = editor.getOption(monaco.editor.EditorOption.readOnly)
      items.push({ ...item, enabled: item.id === 'copy' ? !empty : !readOnly && (item.id === 'paste' || !empty) })
      continue
    }
    const action = editor.getAction(item.id)
    if (action?.isSupported()) items.push({ ...item, enabled: true })
  }
  while (items.length && items[items.length - 1].id === '-') items.pop()
  post({ type: 'menu', x: e.event.posx, y: e.event.posy, items })
})

// MARK: - Calls from Swift

/** Replaces a model's text with as small an edit as covers the change, so
 * folds, the cursor and the scroll stay put when an agent changes a few lines. */
function replace(model: monaco.editor.ITextModel, text: string) {
  const old = model.getValue()
  if (old === text) return
  let start = 0
  const shorter = Math.min(old.length, text.length)
  while (start < shorter && old.charCodeAt(start) === text.charCodeAt(start)) start++
  let end = 0
  while (end < shorter - start && old.charCodeAt(old.length - 1 - end) === text.charCodeAt(text.length - 1 - end)) end++
  const range = monaco.Range.fromPositions(model.getPositionAt(start), model.getPositionAt(old.length - end))
  model.pushStackElement()
  model.pushEditOperations([], [{ range, text: text.slice(start, text.length - end) }], () => null)
  model.pushStackElement()
}

function eol(text: string) {
  return text.includes('\r\n') ? monaco.editor.EndOfLineSequence.CRLF : monaco.editor.EndOfLineSequence.LF
}

function revealPending(model: monaco.editor.ITextModel) {
  const range = lsp.reveal.get(model.uri.toString())
  if (!range) return
  lsp.reveal.delete(model.uri.toString())
  editor.setSelection(monaco.Range.lift(range).collapseToStart())
  editor.revealRangeInCenter(range, monaco.editor.ScrollType.Immediate)
}

const agentman = {
  configure(settings: { theme?: Theme; fontSize?: number }) {
    if (settings.theme) {
      const { dark, colors, tokens } = settings.theme
      monaco.editor.defineTheme('agentman', {
        base: dark ? 'vs-dark' : 'vs',
        inherit: true,
        rules: Object.entries(tokens).map(([token, foreground]) => ({ token, foreground: foreground.replace('#', '') })),
        colors,
      })
      monaco.editor.setTheme('agentman')
      document.body.style.background = colors['editor.background'] ?? ''
    }
    if (settings.fontSize) editor.updateOptions({ fontSize: settings.fontSize })
  },

  /** Opens a file's model; answers with its length, which is shorter than
   * Swift's when Monaco evened out mixed line endings. */
  open(doc: Doc): number {
    agentman.close(doc.id)
    const uri = monaco.Uri.parse(doc.uri)
    const preview = monaco.editor.getModel(uri)
    if (preview) {
      lsp.forgetPreview(preview)
      preview.dispose()
    }
    const model = monaco.editor.createModel(doc.text, undefined, uri)
    model.updateOptions({
      tabSize: doc.tabSize,
      indentSize: doc.tabSize,
      insertSpaces: doc.insertSpaces,
      // Brackets in the text's colour: calmer, and the colours stay for meaning.
      bracketColorizationOptions: { enabled: false, independentColorPoolPerBracketType: false },
    })
    const open: Open = { model, gen: doc.gen, view: null, readOnly: !!doc.readOnly }
    docs.set(doc.id, open)
    model.onDidChangeContent((e) => {
      if (quiet) return
      // Offsets are into the text before the edit; last first, each one
      // applies to what the one before it left.
      const changes = [...e.changes]
        .sort((a, b) => b.rangeOffset - a.rangeOffset)
        .map((c) => ({ offset: c.rangeOffset, length: c.rangeLength, text: c.text }))
      post({ type: 'changes', id: doc.id, gen: open.gen, changes, length: model.getValueLength() })
    })
    void lsp.attach(model)
    return model.getValueLength()
  },

  close(id: string) {
    const open = docs.get(id)
    if (!open) return
    if (active === id) {
      editor.setModel(null)
      active = null
    }
    lsp.detach(open.model)
    open.model.dispose()
    docs.delete(id)
  },

  /** A file renamed in the tree: the same text under its new name. */
  rename(id: string, uri: string) {
    const open = docs.get(id)
    if (!open) return
    const wasActive = active === id
    const view = wasActive ? editor.saveViewState() : open.view
    const text = open.model.getValue()
    const options = open.model.getOptions()
    agentman.open({ id, uri, text, tabSize: options.tabSize, insertSpaces: options.insertSpaces, gen: open.gen, readOnly: open.readOnly })
    const reopened = docs.get(id)!
    reopened.view = view
    if (wasActive) agentman.show(id, false)
  },

  /** The text, replaced from outside: a reload from disk or a revert. */
  setText(id: string, text: string, gen: number): number {
    const open = docs.get(id)
    if (!open) return -1
    open.gen = gen
    const view = active === id ? editor.saveViewState() : null
    quiet = true
    try {
      open.model.pushEOL(eol(text))
      replace(open.model, text)
    } finally {
      quiet = false
    }
    if (view) editor.restoreViewState(view)
    return open.model.getValueLength()
  },

  setOptions(id: string, tabSize: number, insertSpaces: boolean) {
    docs.get(id)?.model.updateOptions({ tabSize, indentSize: tabSize, insertSpaces })
  },

  /** Brings a tab's model to the front, where it was left. */
  show(id: string | null, focus = true) {
    if (active === id) {
      if (focus && id) editor.focus()
      return
    }
    const current = active ? docs.get(active) : undefined
    if (current) current.view = editor.saveViewState()
    const next = id ? docs.get(id) : undefined
    active = next ? id : null
    editor.setModel(next?.model ?? null)
    // A file from outside the folder is shown, not edited.
    editor.updateOptions({ readOnly: next?.readOnly ?? false, domReadOnly: next?.readOnly ?? false })
    if (next?.view) editor.restoreViewState(next.view)
    if (next) {
      revealPending(next.model)
      if (focus) editor.focus()
      reportCursor()
    }
  },

  /** Where the language servers are, from Swift, once am serve says. */
  connect(url: string, token: string, folder: string) {
    lsp.connect(url, token, folder)
  },

  filesChanged(changes: { path: string; type: number }[]) {
    lsp.filesChanged(changes)
  },

  saved(id: string) {
    const open = docs.get(id)
    if (open) lsp.saved(open.model)
  },

  restartLanguage(id: string) {
    const open = docs.get(id)
    if (open) lsp.restart(open.model)
  },

  focus() {
    editor.focus()
  },

  text(id: string): string | null {
    return docs.get(id)?.model.getValue() ?? null
  },

  /** Shows a range in the file in front, as Go to Definition does. */
  reveal(id: string, line: number, column: number) {
    if (active !== id) return
    editor.setPosition({ lineNumber: line, column })
    editor.revealPositionInCenter({ lineNumber: line, column })
  },

  run(action: string) {
    editor.focus()
    void editor.getAction(action)?.run()
  },
}

;(window as unknown as { agentman: typeof agentman }).agentman = agentman

// Code is measured in its own font, once it has loaded.
document.fonts.load(`13px ${font}`).finally(() => {
  monaco.editor.remeasureFonts()
  post({ type: 'ready' })
})

window.addEventListener('error', (e) => post({ type: 'log', message: String(e.message) }))
window.addEventListener('unhandledrejection', (e) => post({ type: 'log', message: String(e.reason) }))
