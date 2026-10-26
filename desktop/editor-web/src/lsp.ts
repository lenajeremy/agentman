// Language servers for the editor: one WebSocket to `am serve`, which runs
// the servers (internal/lsp), and Monaco's providers on top of it. Each file
// is resolved to a session (a server for a project root); its text is kept
// in step with that server while it is open, and hover, completion, errors,
// definitions and the rest come from it.

import * as monaco from 'monaco-core'
import {
  type HoverContents,
  type LspRange,
  type LspTextEdit,
  type WorkspaceEdit,
  completionKind,
  completionRange,
  documentation,
  editsByFile,
  fromPosition,
  fromRange,
  highlightKind,
  lspLanguage,
  markdown,
  severity,
  symbolKind,
  target,
  toRange,
} from './convert'

/** What the page around the client does for it. */
export interface Host {
  post(message: Record<string, unknown>): void
  /** A file's text, from Swift, for a preview. */
  read(path: string): Promise<string | null>
  /** Opens a file in a tab. */
  open(path: string): void
  /** Applies edits to files that aren't open, by opening them unsaved. */
  editElsewhere(files: { path: string; edits: LspTextEdit[] }[]): Promise<boolean>
  /** The tab a model belongs to, or none for a preview. */
  tabOf(model: monaco.editor.ITextModel): string | undefined
}

interface Status {
  session: string
  server: string
  name: string
  language: string
  root: string
  state: 'starting' | 'ready' | 'restarting' | 'failed' | 'stopped'
  detail?: string
}

interface Frame {
  t: string
  id?: number
  session?: string
  msg?: RpcMessage
  status?: Status | null
  capabilities?: Capabilities
}

interface RpcMessage {
  jsonrpc?: '2.0'
  id?: number | string | null
  method?: string
  params?: any
  result?: any
  error?: { code: number; message: string }
}

type Capabilities = Record<string, any>

const OWNER = 'lsp'

let host: Host
let folder = ''
let socket: WebSocket | null = null
let nextFrame = 1
const resolving = new Map<number, (status: Status | null) => void>()
const sessions = new Map<string, Session>()
/** Each model's session, once resolved; null when no server speaks it. */
const attached = new Map<monaco.editor.ITextModel, Session | null>()
const listening = new Map<monaco.editor.ITextModel, monaco.IDisposable>()
/** Errors and warnings by file, kept for files not open yet. */
const diagnostics = new Map<string, any[]>()
/** Models made to preview another file (a reference, a definition). */
const previews = new Set<monaco.editor.ITextModel>()
const registered = new Set<string>()

export function setup(page: Host) {
  host = page
  monaco.editor.registerCommand('agentman.lsp.command', (_accessor, key: string, command: { command: string; arguments?: any[] }) => {
    void sessions.get(key)?.request('workspace/executeCommand', { command: command.command, arguments: command.arguments })
  })
  monaco.editor.registerCommand('agentman.lsp.edit', (_accessor, edit: WorkspaceEdit) => {
    void applyEdit(edit)
  })
  monaco.editor.registerCommand('agentman.lsp.editThen', async (_accessor, key: string, edit: WorkspaceEdit, command: any) => {
    if (await applyEdit(edit)) void sessions.get(key)?.request('workspace/executeCommand', { command: command.command, arguments: command.arguments })
  })
}

/** Connects to `am serve`; files open already are resolved again. */
export function connect(url: string, token: string, root: string) {
  folder = root
  socket?.close()
  const ws = new WebSocket(`${url}?token=${encodeURIComponent(token)}`)
  socket = ws
  ws.onopen = () => {
    for (const model of [...attached.keys()]) {
      attached.delete(model)
      void attach(model)
    }
  }
  ws.onmessage = (e) => {
    let frame: Frame
    try {
      frame = JSON.parse(String(e.data))
    } catch {
      return
    }
    received(frame)
  }
  ws.onclose = () => {
    if (socket !== ws) return
    socket = null
    for (const session of sessions.values()) session.lost()
    sessions.clear()
    for (const resolve of resolving.values()) resolve(null)
    resolving.clear()
    host.post({ type: 'lspClosed' })
  }
}

function send(frame: Record<string, unknown>) {
  if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(frame))
}

function received(frame: Frame) {
  switch (frame.t) {
    case 'resolved': {
      const resolve = resolving.get(frame.id ?? -1)
      resolving.delete(frame.id ?? -1)
      resolve?.(frame.status ?? null)
      break
    }
    case 'status':
      if (frame.status) sessionFor(frame.status).statusChanged(frame.status, frame.capabilities)
      break
    case 'rpc':
      if (frame.session && frame.msg) sessions.get(frame.session)?.received(frame.msg)
      break
  }
}

function sessionFor(status: Status): Session {
  let session = sessions.get(status.session)
  if (!session) {
    session = new Session(status)
    sessions.set(status.session, session)
  }
  return session
}

/** Tells the servers about files an agent changed on disk. */
export function filesChanged(changes: { path: string; type: number }[]) {
  send({ t: 'files', changes })
}

// MARK: - Files

/** Starts keeping a tab's text in step with its language's server. */
export async function attach(model: monaco.editor.ITextModel) {
  if (attached.has(model) || model.isDisposed()) return
  const language = lspLanguage(model.uri.path)
  attached.set(model, null)
  if (!language || !socket) return
  const id = nextFrame++
  const status = await new Promise<Status | null>((resolve) => {
    resolving.set(id, resolve)
    send({ t: 'resolve', id, file: model.uri.fsPath, folder, language })
  })
  if (!status || model.isDisposed() || !attached.has(model)) return
  const session = sessionFor(status)
  attached.set(model, session)
  session.models.add(model)
  ensureProviders(model.getLanguageId())
  if (!listening.has(model)) {
    listening.set(model, model.onDidChangeContent((e) => attached.get(model)?.edited(model, e)))
  }
  session.open(model)
  session.report(model)
  showDiagnostics(model)
}

/** Stops: the tab closed. */
export function detach(model: monaco.editor.ITextModel) {
  const session = attached.get(model)
  attached.delete(model)
  listening.get(model)?.dispose()
  listening.delete(model)
  session?.close(model)
}

export function saved(model: monaco.editor.ITextModel) {
  attached.get(model)?.saved(model)
}

/** Asks for a server that failed to be started again. */
export function restart(model: monaco.editor.ITextModel) {
  const session = attached.get(model)
  if (session) send({ t: 'restart', session: session.key })
}

function modelFor(uri: string): monaco.editor.ITextModel | null {
  return monaco.editor.getModel(monaco.Uri.parse(uri))
}

function key(uri: string): string {
  return monaco.Uri.parse(uri).toString()
}

// MARK: - A server for a project

class Session {
  key: string
  status: Status
  caps: Capabilities | null = null
  models = new Set<monaco.editor.ITextModel>()
  private opened = new Set<monaco.editor.ITextModel>()
  private pending = new Map<number, { resolve: (v: any) => void; reject: (e: Error) => void }>()
  private next = 1

  constructor(status: Status) {
    this.key = status.session
    this.status = status
  }

  get ready() {
    return this.status.state === 'ready' && this.caps !== null
  }

  statusChanged(status: Status, caps?: Capabilities) {
    const wasReady = this.ready
    this.status = status
    if (status.state === 'ready') {
      if (caps) this.caps = caps
    } else {
      this.caps = null
      this.opened.clear()
      this.failAll(`${status.name} isn't ready`)
    }
    if (this.ready && !wasReady) for (const model of this.models) this.open(model)
    for (const model of this.models) this.report(model)
  }

  /** The connection to am went away. */
  lost() {
    this.caps = null
    this.opened.clear()
    this.failAll('lost am serve')
  }

  private failAll(reason: string) {
    for (const { reject } of this.pending.values()) reject(new Error(reason))
    this.pending.clear()
  }

  /** Tells Swift how the server is doing, for the tab's status bar. */
  report(model: monaco.editor.ITextModel) {
    const id = host.tabOf(model)
    if (!id) return
    const { name, language, state, detail } = this.status
    host.post({ type: 'language', id, name, language, state, detail: detail ?? '' })
  }

  request<T = any>(method: string, params: unknown): Promise<T> {
    if (!this.ready) return Promise.reject(new Error(`${this.status.name} isn't ready`))
    const id = this.next++
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve, reject })
      send({ t: 'rpc', session: this.key, msg: { jsonrpc: '2.0', id, method, params } })
    })
  }

  /** A request Monaco may cancel: the server is told to stop working on it. */
  cancellable<T = any>(method: string, params: unknown, token: monaco.CancellationToken): Promise<T> {
    const id = this.next
    const sub = token.onCancellationRequested(() => this.notify('$/cancelRequest', { id }))
    return this.request<T>(method, params).finally(() => sub.dispose())
  }

  notify(method: string, params: unknown) {
    send({ t: 'rpc', session: this.key, msg: { jsonrpc: '2.0', method, params } })
  }

  private syncKind(): number {
    const sync = this.caps?.textDocumentSync
    if (typeof sync === 'number') return sync
    return sync?.change ?? 2
  }

  open(model: monaco.editor.ITextModel) {
    if (!this.ready || this.opened.has(model) || model.isDisposed()) return
    this.opened.add(model)
    this.notify('textDocument/didOpen', {
      textDocument: { uri: model.uri.toString(), languageId: lspLanguage(model.uri.path), version: model.getVersionId(), text: model.getValue() },
    })
  }

  edited(model: monaco.editor.ITextModel, e: monaco.editor.IModelContentChangedEvent) {
    if (!this.opened.has(model)) return
    const kind = this.syncKind()
    if (kind === 0) return
    const contentChanges =
      kind === 1
        ? [{ text: model.getValue() }]
        : [...e.changes]
            .sort((a, b) => b.rangeOffset - a.rangeOffset)
            .map((c) => ({ range: fromRange(c.range), rangeLength: c.rangeLength, text: c.text }))
    this.notify('textDocument/didChange', { textDocument: { uri: model.uri.toString(), version: model.getVersionId() }, contentChanges })
  }

  saved(model: monaco.editor.ITextModel) {
    if (!this.opened.has(model)) return
    const save = this.caps?.textDocumentSync?.save
    const includeText = typeof save === 'object' && save?.includeText
    this.notify('textDocument/didSave', { textDocument: { uri: model.uri.toString() }, ...(includeText ? { text: model.getValue() } : {}) })
  }

  close(model: monaco.editor.ITextModel) {
    this.models.delete(model)
    if (this.opened.delete(model)) this.notify('textDocument/didClose', { textDocument: { uri: model.uri.toString() } })
    // No file of this project open in the window: the server may go idle.
    if (this.models.size === 0) send({ t: 'release', session: this.key })
  }

  received(msg: RpcMessage) {
    if (msg.method === undefined && msg.id !== undefined && msg.id !== null) {
      const waiting = this.pending.get(Number(msg.id))
      this.pending.delete(Number(msg.id))
      if (!waiting) return
      if (msg.error) waiting.reject(new Error(msg.error.message))
      else waiting.resolve(msg.result)
      return
    }
    if (msg.id !== undefined && msg.id !== null) {
      void this.answer(msg)
      return
    }
    if (msg.method === 'textDocument/publishDiagnostics') {
      const uri = key(msg.params.uri)
      diagnostics.set(uri, msg.params.diagnostics ?? [])
      const model = modelFor(uri)
      if (model) showDiagnostics(model)
    }
  }

  /** What the server asks of the editor. */
  private async answer(msg: RpcMessage) {
    let result: unknown = null
    try {
      switch (msg.method) {
        case 'workspace/applyEdit':
          result = { applied: await applyEdit(msg.params.edit) }
          break
        case 'window/showDocument': {
          const uri = monaco.Uri.parse(msg.params.uri)
          if (uri.scheme === 'file') {
            if (msg.params.selection) reveal.set(uri.toString(), toRange(msg.params.selection))
            host.open(uri.fsPath)
            result = { success: true }
          } else {
            result = { success: false }
          }
          break
        }
      }
    } catch {
      result = null
    }
    send({ t: 'rpc', session: this.key, msg: { jsonrpc: '2.0', id: msg.id, result } })
  }
}

// MARK: - Errors and warnings

function showDiagnostics(model: monaco.editor.ITextModel) {
  const list = diagnostics.get(model.uri.toString()) ?? []
  monaco.editor.setModelMarkers(
    model,
    OWNER,
    list.map((d) => ({
      ...toRange(d.range),
      severity: severity(d.severity),
      message: d.message,
      source: d.source,
      code: d.code === undefined ? undefined : d.codeDescription?.href ? { value: String(d.code), target: monaco.Uri.parse(d.codeDescription.href) } : String(d.code),
      tags: (d.tags ?? []).map((tag: number) => (tag === 1 ? monaco.MarkerTag.Unnecessary : monaco.MarkerTag.Deprecated)),
      relatedInformation: (d.relatedInformation ?? []).map((r: any) => ({ resource: monaco.Uri.parse(r.location.uri), message: r.message, ...toRange(r.location.range) })),
    })),
  )
  const id = host.tabOf(model)
  if (id) {
    const errors = list.filter((d) => (d.severity ?? 1) === 1).length
    const warnings = list.filter((d) => d.severity === 2).length
    host.post({ type: 'problems', id, errors, warnings })
  }
}

// MARK: - Edits

/** Applies a server's edit: in open tabs through Monaco, so it can be
 * undone, and in other files by opening them, unsaved. */
async function applyEdit(edit: WorkspaceEdit): Promise<boolean> {
  const elsewhere: { path: string; edits: LspTextEdit[] }[] = []
  for (const [uri, edits] of editsByFile(edit)) {
    const model = modelFor(uri)
    if (model && host.tabOf(model)) {
      model.pushStackElement()
      model.pushEditOperations([], edits.map((e) => ({ range: toRange(e.range), text: e.newText })), () => null)
      model.pushStackElement()
    } else {
      elsewhere.push({ path: monaco.Uri.parse(uri).fsPath, edits })
    }
  }
  return elsewhere.length === 0 || host.editElsewhere(elsewhere)
}

// MARK: - Going to other files

/** Ranges to reveal once a file opens in its tab. */
export const reveal = new Map<string, monaco.IRange>()

/** Makes models for files a result points into, so Monaco can preview them. */
async function preview(uris: string[]) {
  const missing = [...new Set(uris.map(key))].filter((uri) => !modelFor(uri)).slice(0, 20)
  await Promise.all(
    missing.map(async (uri) => {
      const parsed = monaco.Uri.parse(uri)
      if (parsed.scheme !== 'file') return
      const text = await host.read(parsed.fsPath)
      if (text === null || modelFor(uri)) return
      previews.add(monaco.editor.createModel(text, undefined, parsed))
    }),
  )
}

export function isPreview(model: monaco.editor.ITextModel) {
  return previews.has(model)
}

export function forgetPreview(model: monaco.editor.ITextModel) {
  previews.delete(model)
}

async function locations(result: any): Promise<monaco.languages.Location[] | monaco.languages.LocationLink[]> {
  if (!result) return []
  const list = (Array.isArray(result) ? result : [result]).map(target)
  await preview(list.map((l) => l.uri))
  return list.map((l) => ({ ...l, uri: monaco.Uri.parse(l.uri) }))
}

// MARK: - Monaco's providers

/** The ready session for a model, if its server offers a feature. */
function route(model: monaco.editor.ITextModel, capability: string): Session | null {
  const session = attached.get(model)
  if (!session?.ready || !session.caps?.[capability]) return null
  return session
}

function doc(model: monaco.editor.ITextModel) {
  return { uri: model.uri.toString() }
}

function at(model: monaco.editor.ITextModel, position: monaco.Position) {
  return { textDocument: doc(model), position: fromPosition(position) }
}

const triggers = ['.', ':', '<', '"', "'", '/', '@', '(', '#', '>', '`', '$', '[', '{', '*', '-', '%']

function ensureProviders(language: string) {
  if (registered.has(language)) return
  registered.add(language)
  const L = monaco.languages

  L.registerHoverProvider(language, {
    async provideHover(model, position, token) {
      const session = route(model, 'hoverProvider')
      if (!session) return null
      const hover = await session.cancellable<{ contents: HoverContents; range?: LspRange } | null>('textDocument/hover', at(model, position), token)
      if (!hover) return null
      const contents = markdown(hover.contents)
      return contents.length ? { contents, range: hover.range ? toRange(hover.range) : undefined } : null
    },
  })

  L.registerCompletionItemProvider(language, {
    triggerCharacters: triggers,
    async provideCompletionItems(model, position, context, token) {
      const session = route(model, 'completionProvider')
      if (!session) return { suggestions: [] }
      const serverTriggers: string[] = session.caps!.completionProvider.triggerCharacters ?? []
      if (context.triggerKind === L.CompletionTriggerKind.TriggerCharacter && !serverTriggers.includes(context.triggerCharacter ?? '')) {
        return { suggestions: [] }
      }
      const result = await session.cancellable('textDocument/completion', {
        ...at(model, position),
        context: { triggerKind: context.triggerKind + 1, triggerCharacter: context.triggerCharacter },
      }, token)
      const items: any[] = Array.isArray(result) ? result : (result?.items ?? [])
      const word = model.getWordUntilPosition(position)
      const wordEnd = model.getWordAtPosition(position)?.endColumn ?? position.column
      return {
        incomplete: !Array.isArray(result) && !!result?.isIncomplete,
        suggestions: items.map((item) => suggestion(session, item, completionRange(item.textEdit, { startColumn: word.startColumn, endColumn: wordEnd }, position))),
      }
    },
    async resolveCompletionItem(item, token) {
      const lsp = (item as any).lsp
      const session: Session | undefined = (item as any).session
      if (!lsp || !session?.ready || !session.caps?.completionProvider?.resolveProvider) return item
      const resolved = await session.cancellable('completionItem/resolve', lsp, token)
      return {
        ...item,
        detail: resolved.detail ?? item.detail,
        documentation: documentation(resolved.documentation) ?? item.documentation,
        additionalTextEdits: resolved.additionalTextEdits?.map((e: LspTextEdit) => ({ range: toRange(e.range), text: e.newText })) ?? item.additionalTextEdits,
      }
    },
  })

  L.registerSignatureHelpProvider(language, {
    signatureHelpTriggerCharacters: ['(', ',', '<'],
    signatureHelpRetriggerCharacters: [')'],
    async provideSignatureHelp(model, position, token, context) {
      const session = route(model, 'signatureHelpProvider')
      if (!session) return null
      const help = await session.cancellable('textDocument/signatureHelp', {
        ...at(model, position),
        context: { triggerKind: context.triggerKind, triggerCharacter: context.triggerCharacter, isRetrigger: context.isRetrigger },
      }, token)
      if (!help?.signatures?.length) return null
      return {
        value: {
          activeSignature: help.activeSignature ?? 0,
          activeParameter: help.activeParameter ?? help.signatures[help.activeSignature ?? 0]?.activeParameter ?? 0,
          signatures: help.signatures.map((s: any) => ({
            label: s.label,
            documentation: documentation(s.documentation),
            activeParameter: s.activeParameter,
            parameters: (s.parameters ?? []).map((p: any) => ({ label: p.label, documentation: documentation(p.documentation) })),
          })),
        },
        dispose() {},
      }
    },
  })

  const goTo = (capability: string, method: string) => ({
    async provide(model: monaco.editor.ITextModel, position: monaco.Position, token: monaco.CancellationToken) {
      const session = route(model, capability)
      if (!session) return null
      return locations(await session.cancellable(method, at(model, position), token))
    },
  })
  const definition = goTo('definitionProvider', 'textDocument/definition')
  L.registerDefinitionProvider(language, { provideDefinition: definition.provide })
  const typeDefinition = goTo('typeDefinitionProvider', 'textDocument/typeDefinition')
  L.registerTypeDefinitionProvider(language, { provideTypeDefinition: typeDefinition.provide })
  const implementation = goTo('implementationProvider', 'textDocument/implementation')
  L.registerImplementationProvider(language, { provideImplementation: implementation.provide })

  L.registerReferenceProvider(language, {
    async provideReferences(model, position, context, token) {
      const session = route(model, 'referencesProvider')
      if (!session) return null
      const result = await session.cancellable('textDocument/references', { ...at(model, position), context: { includeDeclaration: context.includeDeclaration } }, token)
      return (await locations(result)) as monaco.languages.Location[]
    },
  })

  L.registerDocumentHighlightProvider(language, {
    async provideDocumentHighlights(model, position, token) {
      const session = route(model, 'documentHighlightProvider')
      if (!session) return null
      const result = await session.cancellable<any[] | null>('textDocument/documentHighlight', at(model, position), token)
      return (result ?? []).map((h) => ({ range: toRange(h.range), kind: highlightKind(h.kind) }))
    },
  })

  L.registerDocumentSymbolProvider(language, {
    async provideDocumentSymbols(model, token) {
      const session = route(model, 'documentSymbolProvider')
      if (!session) return null
      const result = await session.cancellable<any[] | null>('textDocument/documentSymbol', { textDocument: doc(model) }, token)
      const symbol = (s: any): monaco.languages.DocumentSymbol => ({
        name: s.name,
        detail: s.detail ?? '',
        kind: symbolKind(s.kind),
        tags: [],
        range: toRange(s.range ?? s.location.range),
        selectionRange: toRange(s.selectionRange ?? s.range ?? s.location.range),
        children: (s.children ?? []).map(symbol),
      })
      return (result ?? []).map(symbol)
    },
  })

  L.registerRenameProvider(language, {
    async provideRenameEdits(model, position, newName, token) {
      const session = route(model, 'renameProvider')
      if (!session) return null
      const edit = await session.cancellable<WorkspaceEdit | null>('textDocument/rename', { ...at(model, position), newName }, token)
      // Files open in tabs change through Monaco; the rest open unsaved.
      const here: monaco.languages.IWorkspaceTextEdit[] = []
      const elsewhere: { path: string; edits: LspTextEdit[] }[] = []
      for (const [uri, edits] of editsByFile(edit)) {
        const target = modelFor(uri)
        if (target && host.tabOf(target)) {
          for (const e of edits) here.push({ resource: target.uri, textEdit: { range: toRange(e.range), text: e.newText }, versionId: undefined })
        } else {
          elsewhere.push({ path: monaco.Uri.parse(uri).fsPath, edits })
        }
      }
      if (elsewhere.length && !(await host.editElsewhere(elsewhere))) {
        return { edits: [], rejectReason: "Couldn't change every file" }
      }
      return { edits: here }
    },
    async resolveRenameLocation(model, position, token) {
      const session = route(model, 'renameProvider')
      const word = model.getWordAtPosition(position)
      const fallback = word
        ? { range: new monaco.Range(position.lineNumber, word.startColumn, position.lineNumber, word.endColumn), text: word.word }
        : null
      if (!session || typeof session.caps!.renameProvider !== 'object' || !session.caps!.renameProvider.prepareProvider) {
        return fallback ?? { range: new monaco.Range(1, 1, 1, 1), text: '', rejectReason: 'Nothing to rename here' }
      }
      const result = await session.cancellable('textDocument/prepareRename', at(model, position), token)
      if (!result) return { range: new monaco.Range(1, 1, 1, 1), text: '', rejectReason: "This can't be renamed" }
      if (result.defaultBehavior) return fallback!
      const range = toRange(result.range ?? result)
      return { range, text: result.placeholder ?? model.getValueInRange(range) }
    },
  })

  const formatOptions = (options: monaco.languages.FormattingOptions) => ({ tabSize: options.tabSize, insertSpaces: options.insertSpaces })
  L.registerDocumentFormattingEditProvider(language, {
    async provideDocumentFormattingEdits(model, options, token) {
      const session = route(model, 'documentFormattingProvider')
      if (!session) return null
      const edits = await session.cancellable<LspTextEdit[] | null>('textDocument/formatting', { textDocument: doc(model), options: formatOptions(options) }, token)
      return (edits ?? []).map((e) => ({ range: toRange(e.range), text: e.newText }))
    },
  })
  L.registerDocumentRangeFormattingEditProvider(language, {
    async provideDocumentRangeFormattingEdits(model, range, options, token) {
      const session = route(model, 'documentRangeFormattingProvider')
      if (!session) return null
      const edits = await session.cancellable<LspTextEdit[] | null>('textDocument/rangeFormatting', { textDocument: doc(model), range: fromRange(range), options: formatOptions(options) }, token)
      return (edits ?? []).map((e) => ({ range: toRange(e.range), text: e.newText }))
    },
  })

  L.registerCodeActionProvider(language, {
    async provideCodeActions(model, range, context, token) {
      const session = route(model, 'codeActionProvider')
      if (!session) return { actions: [], dispose() {} }
      const markers = new Set(context.markers.map((m) => `${m.startLineNumber}:${m.startColumn}:${m.message}`))
      const related = (diagnostics.get(model.uri.toString()) ?? []).filter((d) => {
        const r = toRange(d.range)
        return markers.has(`${r.startLineNumber}:${r.startColumn}:${d.message}`)
      })
      const result = await session.cancellable<any[] | null>('textDocument/codeAction', {
        textDocument: doc(model),
        range: fromRange(range),
        context: { diagnostics: related, only: context.only ? [context.only] : undefined, triggerKind: context.trigger },
      }, token)
      const actions: monaco.languages.CodeAction[] = (result ?? []).map((a) => {
        // A plain command, or an action with an edit, a command or both.
        if (typeof a.command === 'string') {
          return { title: a.title, command: { id: 'agentman.lsp.command', title: a.title, arguments: [session.key, a] } }
        }
        const action: monaco.languages.CodeAction = { title: a.title, kind: a.kind, isPreferred: a.isPreferred, disabled: a.disabled?.reason }
        // Edits go through applyEdit, which opens files that aren't open.
        if (a.edit && a.command) {
          action.command = { id: 'agentman.lsp.editThen', title: a.title, arguments: [session.key, a.edit, a.command] }
        } else if (a.edit) {
          action.command = { id: 'agentman.lsp.edit', title: a.title, arguments: [a.edit] }
        } else if (a.command) {
          action.command = { id: 'agentman.lsp.command', title: a.title, arguments: [session.key, a.command] }
        }
        return action
      })
      return { actions, dispose() {} }
    },
  })
}

function suggestion(session: Session, item: any, range: ReturnType<typeof completionRange>): monaco.languages.CompletionItem {
  const L = monaco.languages
  const text = item.textEdit?.newText ?? item.insertText ?? item.label
  return {
    label: item.labelDetails ? { label: item.label, detail: item.labelDetails.detail, description: item.labelDetails.description } : item.label,
    kind: L.CompletionItemKind[completionKind(item.kind)],
    tags: item.tags?.includes(1) || item.deprecated ? [L.CompletionItemTag.Deprecated] : [],
    detail: item.detail,
    documentation: documentation(item.documentation),
    sortText: item.sortText,
    filterText: item.filterText,
    preselect: item.preselect,
    insertText: text,
    insertTextRules: item.insertTextFormat === 2 ? L.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
    range,
    commitCharacters: item.commitCharacters,
    additionalTextEdits: item.additionalTextEdits?.map((e: LspTextEdit) => ({ range: toRange(e.range), text: e.newText })),
    command: item.command
      ? ['editor.action.triggerSuggest', 'editor.action.triggerParameterHints'].includes(item.command.command)
        ? { id: item.command.command, title: item.command.title }
        : { id: 'agentman.lsp.command', title: item.command.title, arguments: [session.key, item.command] }
      : undefined,
    // Kept for completionItem/resolve.
    ...({ lsp: item, session } as object),
  }
}
