// The Monaco build without its own language services (see build.mjs).
declare module 'monaco-core' {
  export * from 'monaco-editor/editor/editor.api'
}
