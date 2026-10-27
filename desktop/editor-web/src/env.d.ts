// The Monaco build without its own language services (see build.mjs).
declare module 'monaco-core' {
  export * from 'monaco-editor/editor/editor.api'
}

// Monaco's own settings for a language (brackets, comments), borrowed by the
// languages it doesn't know.
declare module 'monaco-editor/esm/vs/languages/definitions/*' {
  export const conf: import('monaco-editor').languages.LanguageConfiguration
}

// The line diff VS Code shows changes with, from inside Monaco (gitdiff.ts).
declare module 'monaco-editor/editor/common/diff/linesDiffComputers' {
  interface LineRange {
    readonly startLineNumber: number
    readonly endLineNumberExclusive: number
    readonly isEmpty: boolean
  }
  interface LinesDiff {
    readonly changes: readonly {
      readonly original: LineRange
      readonly modified: LineRange
      /** Where, within the lines, the characters changed. */
      readonly innerChanges?: readonly {
        readonly modifiedRange: { startLineNumber: number; startColumn: number; endLineNumber: number; endColumn: number }
      }[]
    }[]
    readonly hitTimeout: boolean
  }
  interface LinesDiffComputer {
    computeDiff(
      original: string[],
      modified: string[],
      options: { ignoreTrimWhitespace: boolean; maxComputationTimeMs: number; computeMoves: boolean },
    ): LinesDiff
  }
  export const linesDiffComputers: { getDefault(): LinesDiffComputer; getLegacy(): LinesDiffComputer }
}
