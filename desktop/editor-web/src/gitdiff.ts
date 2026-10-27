// What changed since git last had the file, beside the line numbers, as
// VS Code and Cursor show it: a green bar by lines that are new, an amber
// one by lines that changed, and a red mark where lines were taken out. A
// click on one shows what git has there, with Revert; Alt-F5 and Vim's ]c
// and [c go from one change to the next.
//
// Swift sends the file as git's index has it (what `git diff` compares
// with), or nothing for a file git doesn't follow. The bars follow every
// edit, a moment after the typing stops, and every change an agent makes.

import * as monaco from 'monaco-core'
import { type Change, type Kind, lineChanges, splitLines } from './linediff'

export type { Change, Kind }

// MARK: - Bars

interface State {
  base: string[] | null
  changes: Change[]
  decorations: string[]
  timer?: ReturnType<typeof setTimeout>
  listener: monaco.IDisposable
}

const states = new Map<monaco.editor.ITextModel, State>()

/** The bars' colours, from the theme, as the file tree marks files. */
export function setColors(theme: Record<string, string>) {
  for (const kind of ['added', 'modified', 'deleted']) {
    const color = theme[`editorGutter.${kind}Background`]
    if (color) document.body.style.setProperty(`--git-${kind}`, color)
  }
}

/** Follows a file's edits, once it has a base to compare them with. */
export function setBase(model: monaco.editor.ITextModel, text: string | null) {
  let state = states.get(model)
  if (!state) {
    state = { base: null, changes: [], decorations: [], listener: model.onDidChangeContent(() => schedule(model)) }
    states.set(model, state)
  }
  state.base = text === null ? null : text === '' ? [] : splitLines(text)
  update(model)
}

export function detach(model: monaco.editor.ITextModel) {
  const state = states.get(model)
  if (!state) return
  clearTimeout(state.timer)
  state.listener.dispose()
  states.delete(model)
}

export function changesOf(model: monaco.editor.ITextModel | null): Change[] {
  return (model && states.get(model)?.changes) ?? []
}

function schedule(model: monaco.editor.ITextModel) {
  const state = states.get(model)
  if (!state || state.base === null) return
  clearTimeout(state.timer)
  state.timer = setTimeout(() => update(model), 250)
}

function update(model: monaco.editor.ITextModel) {
  const state = states.get(model)
  if (!state || model.isDisposed()) return
  clearTimeout(state.timer)
  state.changes = state.base === null ? [] : lineChanges(state.base, model.getLinesContent())
  paint(model)
  for (const peek of peeks.values()) if (peek.editor.getModel() === model) peek.refresh()
}

function paint(model: monaco.editor.ITextModel) {
  const state = states.get(model)
  if (!state || model.isDisposed()) return
  const decorations = state.changes.map((change): monaco.editor.IModelDeltaDecoration => {
    const top = change.kind === 'deleted' && change.start === 0
    const line = Math.max(change.start, 1)
    return {
      range: new monaco.Range(line, 1, change.kind === 'deleted' ? line : change.end, 1),
      options: {
        isWholeLine: true,
        linesDecorationsClassName: `git-bar git-${change.kind}${top ? ' git-top' : ''}`,
      },
    }
  })
  state.decorations = model.deltaDecorations(state.decorations, decorations)
}

// MARK: - Showing what git has

/** What git has in place of a change, below it in the editor, with Revert. */
class Peek {
  private zone: string | null = null
  private node = document.createElement('div')

  constructor(
    readonly editor: monaco.editor.IStandaloneCodeEditor,
    private change: Change,
  ) {
    this.node.className = 'git-peek'
    // The editor would take a click here for its text.
    this.node.addEventListener('mousedown', (e) => {
      const action = (e.target as HTMLElement).closest<HTMLElement>('[data-git]')?.dataset.git
      e.preventDefault()
      e.stopPropagation()
      if (action === 'revert') this.revert()
      else if (action === 'close') close(this.editor)
      else if (action === 'next' || action === 'previous') go(this.editor, action === 'next' ? 1 : -1, true)
    })
    this.render()
  }

  get line() {
    return this.change.start
  }

  /** Follows the change through edits; gone, the peek goes too. */
  refresh() {
    const changes = changesOf(this.editor.getModel())
    const same = changes.find((c) => c.kind === this.change.kind && c.start === this.change.start)
      ?? changes.find((c) => c.start <= this.change.start && this.change.start <= Math.max(c.end, c.start))
    if (!same) {
      close(this.editor)
      return
    }
    this.change = same
    this.render()
  }

  private render() {
    const { change, editor } = this
    const model = editor.getModel()
    if (!model) return
    const all = changesOf(model)
    const index = all.findIndex((c) => c.start === change.start && c.kind === change.kind)
    const font = editor.getOption(monaco.editor.EditorOption.fontInfo)
    const lineHeight = editor.getOption(monaco.editor.EditorOption.lineHeight)
    this.node.style.setProperty('--peek-font', font.fontFamily)
    this.node.style.setProperty('--peek-size', `${font.fontSize}px`)
    this.node.style.setProperty('--peek-line', `${lineHeight}px`)
    this.node.style.setProperty('--peek-tab', String(model.getOptions().tabSize))

    const header = document.createElement('div')
    header.className = 'git-peek-header'
    const title = document.createElement('span')
    const count = (n: number) => `${n} line${n === 1 ? '' : 's'}`
    title.textContent = change.kind === 'added'
      ? `${count(change.end - change.start + 1)} added`
      : change.kind === 'deleted'
        ? `${count(change.original.length)} removed`
        : `${count(change.end - change.start + 1)} changed`
    const place = document.createElement('span')
    place.className = 'git-peek-place'
    place.textContent = `${index + 1} of ${all.length}`
    header.append(title, place, button('previous', '↑', 'Previous Change'), button('next', '↓', 'Next Change'),
      button('revert', 'Revert', 'Put back what git has'), button('close', '×', 'Close'))

    const body = document.createElement('div')
    body.className = 'git-peek-lines'
    for (const text of change.original) {
      const line = document.createElement('div')
      line.className = 'git-peek-line'
      line.textContent = text || ' '
      body.appendChild(line)
    }
    this.node.replaceChildren(header, ...(change.original.length ? [body] : []))
    void monaco.editor.colorize(change.original.join('\n'), model.getLanguageId(), { tabSize: model.getOptions().tabSize })
      .then((html) => {
        if (!this.node.contains(body)) return
        const lines = html.split('<br/>')
        body.querySelectorAll('.git-peek-line').forEach((line, at) => {
          if (lines[at]) line.innerHTML = lines[at]
        })
      })
      .catch(() => {})

    const shown = Math.min(change.original.length, 12)
    const height = 28 + (shown ? shown * lineHeight + 8 : 0)
    body.style.maxHeight = `${12 * lineHeight}px`
    editor.changeViewZones((zones) => {
      if (this.zone) zones.removeZone(this.zone)
      this.zone = zones.addZone({ afterLineNumber: change.kind === 'deleted' ? change.start : change.end, heightInPx: height, domNode: this.node })
    })
  }

  revert() {
    const model = this.editor.getModel()
    if (!model) return
    const { change } = this
    const eol = model.getEOL()
    const text = change.original.join(eol)
    let edit: monaco.editor.IIdentifiedSingleEditOperation
    if (change.kind === 'deleted') {
      edit = change.start === 0
        ? { range: new monaco.Range(1, 1, 1, 1), text: text + eol }
        : { range: rangeAtEnd(model, change.start), text: eol + text }
    } else if (change.kind === 'added') {
      edit = change.end < model.getLineCount()
        ? { range: new monaco.Range(change.start, 1, change.end + 1, 1), text: '' }
        : change.start > 1
          ? { range: new monaco.Range(change.start - 1, model.getLineMaxColumn(change.start - 1), change.end, model.getLineMaxColumn(change.end)), text: '' }
          : { range: model.getFullModelRange(), text: '' }
    } else {
      edit = { range: new monaco.Range(change.start, 1, change.end, model.getLineMaxColumn(change.end)), text }
    }
    close(this.editor)
    this.editor.pushUndoStop()
    this.editor.executeEdits('git', [edit])
    this.editor.pushUndoStop()
    this.editor.setPosition({ lineNumber: Math.max(change.start, 1), column: 1 })
    update(model)
    this.editor.focus()
  }

  dispose() {
    if (this.zone) this.editor.changeViewZones((zones) => zones.removeZone(this.zone!))
    this.zone = null
  }
}

function rangeAtEnd(model: monaco.editor.ITextModel, line: number) {
  const column = model.getLineMaxColumn(line)
  return new monaco.Range(line, column, line, column)
}

function button(action: string, label: string, title: string) {
  const element = document.createElement('button')
  element.dataset.git = action
  element.textContent = label
  element.title = title
  return element
}

const peeks = new Map<monaco.editor.IStandaloneCodeEditor, Peek>()

function close(editor: monaco.editor.IStandaloneCodeEditor) {
  peeks.get(editor)?.dispose()
  peeks.delete(editor)
}

function show(editor: monaco.editor.IStandaloneCodeEditor, change: Change) {
  close(editor)
  peeks.set(editor, new Peek(editor, change))
}

/** The change at a line: its lines, or a deletion's mark on the line above. */
function changeAt(model: monaco.editor.ITextModel, line: number): Change | undefined {
  return changesOf(model).find((c) => (c.kind === 'deleted' ? Math.max(c.start, 1) === line : c.start <= line && line <= c.end))
}

/** Moves to the next change, or the one before, round the file; with a peek
 * open, it shows that one instead. */
function go(editor: monaco.editor.IStandaloneCodeEditor, direction: 1 | -1, peek = peeks.has(editor)) {
  const model = editor.getModel()
  const changes = changesOf(model)
  if (!model || !changes.length) return
  const line = peeks.get(editor)?.line ?? editor.getPosition()?.lineNumber ?? 1
  const starts = changes.map((c) => Math.max(c.start, 1))
  let index = direction === 1 ? starts.findIndex((s) => s > line) : -1
  if (direction === -1) for (let at = starts.length - 1; at >= 0 && index < 0; at--) if (starts[at] < line) index = at
  if (index < 0) index = direction === 1 ? 0 : changes.length - 1
  const target = changes[index]
  const at = Math.max(target.start, 1)
  editor.setPosition({ lineNumber: at, column: 1 })
  editor.revealLineInCenterIfOutsideViewport(at)
  if (peek) show(editor, target)
}

/** Clicks on the bars, and the keys, for one editor. */
export function install(editor: monaco.editor.IStandaloneCodeEditor) {
  editor.onMouseDown((e) => {
    if (e.target.type !== monaco.editor.MouseTargetType.GUTTER_LINE_DECORATIONS) return
    if (!e.target.element?.classList.contains('git-bar')) return
    const model = editor.getModel()
    const line = e.target.position?.lineNumber
    const change = model && line ? changeAt(model, line) : undefined
    if (!change) return
    if (peeks.get(editor)?.line === change.start) close(editor)
    else show(editor, change)
  })
  editor.onDidChangeModel(() => close(editor))
  editor.onKeyDown((e) => {
    if (e.keyCode === monaco.KeyCode.Escape && peeks.has(editor)) close(editor)
  })
  editor.onDidDispose(() => close(editor))
  editor.addAction({
    id: 'agentman.git.next',
    label: 'Go to Next Change',
    keybindings: [monaco.KeyMod.Alt | monaco.KeyCode.F5],
    run: () => go(editor, 1),
  })
  editor.addAction({
    id: 'agentman.git.previous',
    label: 'Go to Previous Change',
    keybindings: [monaco.KeyMod.Alt | monaco.KeyMod.Shift | monaco.KeyCode.F5],
    run: () => go(editor, -1),
  })
  editor.addAction({
    id: 'agentman.git.show',
    label: 'Show Change',
    keybindings: [monaco.KeyMod.Alt | monaco.KeyCode.F3],
    run: () => {
      const model = editor.getModel()
      const change = model ? changeAt(model, editor.getPosition()?.lineNumber ?? 1) : undefined
      if (change) show(editor, change)
      else go(editor, 1, true)
    },
  })
}
