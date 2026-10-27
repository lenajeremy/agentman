// Vim in the editor, when the app's setting says so: monaco-vim's motions,
// operators, registers, visual modes, search and `:` commands. The mode is
// shown in the app's own status bar; the `:` and `/` prompts open in a strip
// at the foot of the editor, as Vim's command line does. Saving and closing
// with `:w` and `:q` are the app's, so they behave as ⌘S and ⌘W do.

import type * as monaco from 'monaco-core'
import { StatusBar, VimMode, initVimMode, type VimAdapterInstance } from 'monaco-vim'

/** What the page around Vim does for it. */
export interface Host {
  post(message: Record<string, unknown>): void
}

let host: Host
let adapter: VimAdapterInstance | null = null
let strip: HTMLElement | null = null
let observer: MutationObserver | null = null
/** The mode, as the status bar says it. */
export let mode = 'normal'

/** Vim's names for its modes, as Vim shows them. */
function label(event: { mode: string; subMode?: string }): string {
  if (event.mode === 'visual') return event.subMode === 'linewise' ? 'visual line' : event.subMode === 'blockwise' ? 'visual block' : 'visual'
  return event.mode
}

/** The status bar monaco-vim draws into, keeping only its command line:
 * the mode and the keys typed so far go to the app's status bar. */
class CommandLine extends StatusBar {
  setMode(event: { mode: string; subMode?: string }) {
    mode = label(event)
    host.post({ type: 'vim', mode })
  }

  setKeyBuffer(keys: string) {
    host.post({ type: 'vimKeys', keys })
  }
}

type Ex = (cm: unknown, params: { args?: string[]; argString?: string }) => void

/** monaco-vim's stand-in for CodeMirror, as an action is handed it. */
type Adapter = { editor: monaco.editor.ICodeEditor }

/** `:` commands that are the app's: saving, closing tabs, opening files.
 * And the language server's, on the keys Vim's own LSP setups give them: K
 * shows what it says about the symbol (its type, signature and
 * documentation), the hover a pointer resting on it opens; gd goes to its
 * definition, in another file if need be; gr lists its references. */
function defineCommands() {
  const vim = (VimMode as unknown as {
    Vim: {
      defineEx(name: string, prefix: string, run: Ex): void
      defineAction(name: string, run: (cm: Adapter) => void): void
      mapCommand(keys: string, type: string, name: string, args: object, extra: object): void
    }
  }).Vim
  const language: [keys: string, action: string, command: string][] = [
    ['K', 'showHover', 'editor.action.showHover'],
    ['gd', 'goToDefinition', 'editor.action.revealDefinition'],
    ['gr', 'goToReferences', 'editor.action.goToReferences'],
  ]
  for (const [keys, action, command] of language) {
    vim.defineAction(action, (cm) => cm.editor.trigger('vim', command, {}))
    vim.mapCommand(keys, 'action', action, {}, { context: 'normal' })
  }
  const commands = (VimMode as unknown as { commands: Record<string, (cm: unknown) => void> }).commands
  commands.save = () => host.post({ type: 'command', name: 'save' })
  vim.defineEx('quit', 'q', (_cm, params) => host.post({ type: 'command', name: params.argString?.trim() === '!' ? 'discard' : 'close' }))
  vim.defineEx('wq', 'wq', () => host.post({ type: 'command', name: 'saveClose' }))
  vim.defineEx('xit', 'x', () => host.post({ type: 'command', name: 'saveClose' }))
  vim.defineEx('wall', 'wa', () => host.post({ type: 'command', name: 'saveAll' }))
  vim.defineEx('qall', 'qa', () => host.post({ type: 'command', name: 'closeAll' }))
  vim.defineEx('wqall', 'wqa', () => host.post({ type: 'command', name: 'saveCloseAll' }))
  vim.defineEx('edit', 'e', (_cm, params) => {
    const path = params.argString?.trim()
    host.post(path ? { type: 'command', name: 'open', path } : { type: 'command', name: 'goToFile' })
  })
  vim.defineEx('Explore', 'Ex', () => host.post({ type: 'command', name: 'tree' }))
}

let defined = false

/** Yanks reach the Mac's clipboard too, as with `set clipboard=unnamed`. */
function shareYanks() {
  const vim = (VimMode as unknown as { Vim: { getRegisterController(): { pushText: (...args: unknown[]) => void } } }).Vim
  const registers = vim.getRegisterController()
  const push = registers.pushText
  if ((push as { shared?: boolean }).shared) return
  const shared = function (this: unknown, name: unknown, operator: unknown, text: unknown, ...rest: unknown[]) {
    push.call(this, name, operator, text, ...rest)
    if (operator === 'yank' && name !== '_' && typeof text === 'string') host.post({ type: 'clipboard', text })
  }
  ;(shared as { shared?: boolean }).shared = true
  registers.pushText = shared
}

export function setup(page: Host) {
  host = page
}

/** Turns Vim on or off for the editor. */
export function setVim(editor: monaco.editor.IStandaloneCodeEditor, on: boolean) {
  if (on === !!adapter) return
  if (!on) {
    adapter?.dispose()
    adapter = null
    observer?.disconnect()
    strip?.remove()
    strip = null
    mode = 'normal'
    host.post({ type: 'vim', mode: null })
    editor.updateOptions({ cursorStyle: 'line' })
    return
  }
  if (!defined) {
    defineCommands()
    defined = true
  }
  strip = document.createElement('div')
  strip.id = 'vim'
  document.body.appendChild(strip)
  // The strip shows only while it has something to say: a prompt, or a
  // message such as "E492: Not an editor command".
  observer = new MutationObserver(() => {
    strip?.classList.toggle('shown', !!(strip.querySelector('input') || strip.textContent?.trim()))
  })
  observer.observe(strip, { childList: true, subtree: true, characterData: true })
  adapter = initVimMode(editor, strip, CommandLine as unknown as typeof StatusBar)
  shareYanks()
  mode = 'normal'
  host.post({ type: 'vim', mode })
}

export const isOn = () => !!adapter
