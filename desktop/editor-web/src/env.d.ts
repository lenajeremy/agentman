// The Monaco build without its own language services (see build.mjs).
declare module 'monaco-core' {
  export * from 'monaco-editor/editor/editor.api'
}

// Monaco's own settings for a language (brackets, comments), borrowed by the
// languages it doesn't know.
declare module 'monaco-editor/esm/vs/languages/definitions/*' {
  export const conf: import('monaco-editor').languages.LanguageConfiguration
}
