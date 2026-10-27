// The editor page inside each Agentman editor window: a row of Monaco editors,
// one unless the window is split, and one model per open tab, shared by the
// editors that show it. Swift owns the files, the tabs and saving; this page
// holds the live text and tells Swift every edit, so Swift's copy is always the
// one that gets saved.
//
// Swift calls the functions on window.agentman; this page answers through
// webkit.messageHandlers.editor.

import * as monaco from 'monaco-core'
import * as lsp from './lsp'
import * as syntax from './syntax'
import * as vim from './vim'

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

/** Colours from the app's palette, for the appearance in use: the editor's,
 * and the code's by role. */
interface Theme {
  dark: boolean
  colors: Record<string, string>
  syntax: syntax.Syntax
}

interface Open {
  model: monaco.editor.ITextModel
  gen: number
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

syntax.install((message) => post({ type: 'log', message }))
vim.setup({ post })

const docs = new Map<string, Open>()
/** Set while Swift replaces a text, so the change isn't sent back to it. */
let quiet = false

const font = '"Google Sans Code", ui-monospace, Menlo, monospace'
let fontSize = 13

const options: monaco.editor.IStandaloneEditorConstructionOptions = {
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
  'semanticHighlighting.enabled': true,
  inlayHints: { enabled: 'offUnlessPressed' },
  theme: 'vs',
}

// MARK: - Splits

/** One editor of the row: the folder's files side by side, when split. */
interface Pane {
  element: HTMLElement
  name: HTMLElement
  editor: monaco.editor.IStandaloneCodeEditor
  /** The file it shows, by Swift's id. */
  id: string | null
  /** Where each file it has shown was left in it. */
  views: Map<string, monaco.editor.ICodeEditorViewState | null>
}

const row = document.getElementById('editor')!
const panes: Pane[] = []
/** Which split has the keyboard. */
let focused = 0

function makePane(): Pane {
  const element = document.createElement('div')
  element.className = 'pane'
  const title = document.createElement('div')
  title.className = 'pane-title'
  const name = document.createElement('span')
  const close = document.createElement('button')
  close.className = 'pane-close'
  close.textContent = '×'
  close.title = 'Close this split'
  title.append(name, close)
  const body = document.createElement('div')
  body.className = 'pane-body'
  const empty = document.createElement('div')
  empty.className = 'pane-empty'
  empty.textContent = 'Open a file here: ⌘P, or one from the tree'
  element.append(title, body, empty)
  row.appendChild(element)
  const created = monaco.editor.create(body, { ...options, fontSize })
  const pane: Pane = { element, name, editor: created, id: null, views: new Map() }
  created.onDidFocusEditorText(() => focusPane(panes.indexOf(pane)))
  created.onDidChangeCursorPosition(() => {
    if (created === editor) reportCursor()
  })
  created.onContextMenu((e) => contextMenu(pane, e))
  title.addEventListener('mousedown', (e) => {
    e.preventDefault()
    if (e.target === close) {
      post({ type: 'closePane', index: panes.indexOf(pane) })
    } else {
      focusPane(panes.indexOf(pane))
      created.focus()
    }
  })
  empty.addEventListener('mousedown', () => focusPane(panes.indexOf(pane)))
  if (vim.isOn()) vim.setVim(created, true, element)
  return pane
}

panes.push(makePane())
/** The editor with the keyboard, and the file in it: the split in front. */
let editor = panes[0].editor
let active: string | null = null

/** Shows a file in a split, where that split left it, or nothing. */
function showIn(pane: Pane, id: string | null) {
  const next = id ? docs.get(id) : undefined
  const shown = next ? id : null
  if (pane.id === shown && pane.editor.getModel() === (next?.model ?? null)) return
  if (pane.id) pane.views.set(pane.id, pane.editor.saveViewState())
  pane.id = shown
  pane.editor.setModel(next?.model ?? null)
  // A file from outside the folder is shown, not edited.
  pane.editor.updateOptions({ readOnly: next?.readOnly ?? false, domReadOnly: next?.readOnly ?? false })
  const view = shown ? pane.views.get(shown) : undefined
  if (view) pane.editor.restoreViewState(view)
  pane.name.textContent = next ? next.model.uri.path.split('/').pop() ?? '' : ''
  pane.element.classList.toggle('empty', !next)
  if (pane === panes[focused]) {
    active = shown
    if (next) revealPending(next.model)
  }
}

/** Gives a split the keyboard's attention: the file in it is the one in front. */
function focusPane(index: number) {
  if (index < 0 || index >= panes.length) return
  const changed = index !== focused
  focused = index
  editor = panes[index].editor
  active = panes[index].id
  panes.forEach((pane, at) => pane.element.classList.toggle('focused', at === index))
  if (changed) {
    post({ type: 'pane', index })
    if (vim.isOn()) post({ type: 'vim', mode: vim.modeOf(editor) })
  }
  const open = active ? docs.get(active) : undefined
  if (open) {
    revealPending(open.model)
    reportCursor()
  }
}

/** Where the splits are, so Swift can lay a split's own views over it. */
function reportPanes() {
  post({ type: 'panes', frames: panes.map((pane) => ({ x: pane.element.offsetLeft, width: pane.element.offsetWidth })) })
}
new ResizeObserver(reportPanes).observe(row)

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
// is revealed when it comes to the front. Into the file in front there is
// nothing to open: the editor moves there itself. Asked to open it, Swift
// found it already in front, and nothing moved.
monaco.editor.registerEditorOpener({
  openCodeEditor(_source, resource, selectionOrPosition) {
    if (resource.scheme !== 'file') return false
    if (selectionOrPosition) {
      const range = 'startLineNumber' in selectionOrPosition
        ? selectionOrPosition
        : monaco.Range.fromPositions(selectionOrPosition)
      lsp.reveal.set(resource.toString(), range)
    }
    const model = editor.getModel()
    if (model?.uri.toString() === resource.toString()) {
      revealPending(model)
      return true
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
  if (e.shiftKey && !e.altKey) return ['e', 'f', 'v'].includes(key)
  return ['s', 'p', 'k', 'n', 'o', 'w', 'l', ',', '=', '+', '-', '0', '\\'].includes(key)
}

/** Set after Ctrl-W in Vim's normal mode: the next key says where to go. */
let windowKey = false

/** Vim's Ctrl-W h and l: the file tree on the left, the editor on the right. */
function vimWindowKey(e: KeyboardEvent): boolean {
  if (!vim.isOn() || vim.modeOf(editor) === 'insert' || (e.target as HTMLElement | null)?.closest?.('.vim')) return false
  // Ctrl-B: to the tree, and back from there; Vim's own (a page up) gives way.
  if (e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'b') {
    windowKey = false
    post({ type: 'command', name: 'tree' })
    return true
  }
  if (windowKey) {
    windowKey = false
    if (['Shift', 'Control', 'Alt', 'Meta'].includes(e.key)) {
      windowKey = true
      return false
    }
    // h and l: the split beside, or the tree past the first; v splits.
    if (e.key === 'h' || e.key === 'H' || e.key === 'ArrowLeft') {
      if (focused > 0) agentman.focusSplit(focused - 1)
      else post({ type: 'command', name: 'tree' })
    } else if ((e.key === 'l' || e.key === 'L' || e.key === 'ArrowRight') && focused < panes.length - 1) {
      agentman.focusSplit(focused + 1)
    } else if (e.key === 'v') {
      post({ type: 'command', name: 'split' })
    } else if (e.key === 'c' || e.key === 'q') {
      post({ type: 'closePane', index: focused })
    }
    return true
  }
  if (e.ctrlKey && !e.metaKey && !e.altKey && e.key.toLowerCase() === 'w') {
    windowKey = true
    return true
  }
  return false
}

window.addEventListener(
  'keydown',
  (e) => {
    if (vimWindowKey(e)) {
      e.stopImmediatePropagation()
      e.preventDefault()
    } else if (isAppKey(e)) {
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

function contextMenu(pane: Pane, e: monaco.editor.IEditorMouseEvent) {
  focusPane(panes.indexOf(pane))
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
}

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
  configure(settings: { theme?: Theme; fontSize?: number; vim?: boolean }) {
    if (settings.theme) {
      const { dark, colors } = settings.theme
      monaco.editor.defineTheme('agentman', {
        base: dark ? 'vs-dark' : 'vs',
        inherit: true,
        rules: syntax.rules(settings.theme.syntax),
        colors,
      })
      monaco.editor.setTheme('agentman')
      document.body.style.background = colors['editor.background'] ?? ''
      // The Vim command line, in the editor's colours.
      document.body.style.setProperty('--widget', colors['editorWidget.background'] ?? '')
      document.body.style.setProperty('--widget-border', colors['editorWidget.border'] ?? '')
      document.body.style.setProperty('--text', colors['editor.foreground'] ?? '')
      document.body.style.setProperty('--faint', colors['editorLineNumber.foreground'] ?? '')
    }
    if (settings.fontSize) {
      fontSize = settings.fontSize
      for (const pane of panes) pane.editor.updateOptions({ fontSize })
      document.body.style.setProperty('--font-size', `${settings.fontSize}px`)
    }
    if (settings.vim !== undefined) vim.setOn(settings.vim, panes)
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
    const model = monaco.editor.createModel(doc.text, syntax.languageFor(uri), uri)
    model.updateOptions({
      tabSize: doc.tabSize,
      indentSize: doc.tabSize,
      insertSpaces: doc.insertSpaces,
      // Brackets in the text's colour: calmer, and the colours stay for meaning.
      bracketColorizationOptions: { enabled: false, independentColorPoolPerBracketType: false },
    })
    const open: Open = { model, gen: doc.gen, readOnly: !!doc.readOnly }
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
    for (const pane of panes) {
      if (pane.id === id) showIn(pane, null)
      pane.views.delete(id)
    }
    lsp.detach(open.model)
    open.model.dispose()
    docs.delete(id)
  },

  /** A file renamed in the tree: the same text under its new name. */
  rename(id: string, uri: string) {
    const open = docs.get(id)
    if (!open) return
    const showing = panes.filter((pane) => pane.id === id)
    const views = new Map(showing.map((pane) => [pane, pane.editor.saveViewState()]))
    const text = open.model.getValue()
    const options = open.model.getOptions()
    agentman.open({ id, uri, text, tabSize: options.tabSize, insertSpaces: options.insertSpaces, gen: open.gen, readOnly: open.readOnly })
    for (const [pane, view] of views) {
      pane.views.set(id, view)
      showIn(pane, id)
    }
  },

  /** The text, replaced from outside: a reload from disk or a revert. */
  setText(id: string, text: string, gen: number): number {
    const open = docs.get(id)
    if (!open) return -1
    open.gen = gen
    const views = panes.filter((pane) => pane.id === id).map((pane) => [pane, pane.editor.saveViewState()] as const)
    quiet = true
    try {
      open.model.pushEOL(eol(text))
      replace(open.model, text)
    } finally {
      quiet = false
    }
    for (const [pane, view] of views) if (view) pane.editor.restoreViewState(view)
    return open.model.getValueLength()
  },

  setOptions(id: string, tabSize: number, insertSpaces: boolean) {
    docs.get(id)?.model.updateOptions({ tabSize, indentSize: tabSize, insertSpaces })
  },

  /** Brings a tab's model to the front of the split with the keyboard,
   * where that split left it. */
  show(id: string | null, focus = true) {
    showIn(panes[focused], id)
    const open = id ? docs.get(id) : undefined
    if (open) {
      revealPending(open.model)
      if (focus) editor.focus()
      reportCursor()
    }
  },

  /** The splits, left to right, each with the file it shows, and the one
   * with the keyboard: one split, or the window split in two or more. */
  layout(ids: (string | null)[], front: number) {
    const count = Math.max(ids.length, 1)
    while (panes.length < count) panes.push(makePane())
    while (panes.length > count) {
      const pane = panes.pop()!
      vim.setVim(pane.editor, false, pane.element)
      pane.editor.dispose()
      pane.element.remove()
    }
    row.classList.toggle('split', panes.length > 1)
    focused = Math.min(focused, panes.length - 1)
    panes.forEach((pane, index) => showIn(pane, ids[index] ?? null))
    focusPane(Math.min(Math.max(front, 0), panes.length - 1))
    reportPanes()
  },

  /** Gives a split the keyboard. */
  focusSplit(index: number) {
    focusPane(index)
    editor.focus()
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

  /** Shows a place in the file in front, as Go to Definition does, with
   * length characters from it selected, as a search's match is. */
  reveal(id: string, line: number, column: number, length = 0) {
    if (active !== id) return
    editor.setSelection(new monaco.Selection(line, column, line, column + length))
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
