/**
 * Which icon a filename gets.
 *
 * Kept apart from the icon data and the component so the rules can be tested
 * on Node: the interesting part is not drawing an icon, it is deciding that
 * ".test.mts" is TypeScript, that "Dockerfile" has no extension at all, and
 * that an unknown type gets a document rather than nothing.
 */

/** Whole filenames that identify themselves without an extension. */
const byName: Record<string, string> = {
  dockerfile: "docker",
  "docker-compose.yml": "docker",
  "docker-compose.yaml": "docker",
  "docker-compose.override.yml": "docker",
  "docker-compose.override.yaml": "docker",
  makefile: "settings",
  ".gitignore": "git",
  ".gitattributes": "git",
  ".gitmodules": "git",
  "go.mod": "go",
  "go.sum": "go",
  "package.json": "json",
  "package-lock.json": "lock",
  "yarn.lock": "lock",
  "podfile.lock": "lock",
  "cargo.lock": "lock",
  license: "document",
  "license.md": "document",
};

const byExtension: Record<string, string> = {
  ts: "typescript",
  mts: "typescript",
  cts: "typescript",
  "d.ts": "typescript",
  tsx: "react",
  jsx: "react",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  go: "go",
  py: "python",
  rs: "rust",
  rb: "ruby",
  swift: "swift",
  java: "java",
  kt: "kotlin",
  kts: "kotlin",
  c: "c",
  h: "c",
  cpp: "cpp",
  cc: "cpp",
  hpp: "cpp",
  cs: "csharp",
  php: "php",
  html: "html",
  htm: "html",
  css: "css",
  scss: "sass",
  sass: "sass",
  json: "json",
  jsonc: "json",
  yml: "yaml",
  yaml: "yaml",
  toml: "settings",
  ini: "settings",
  conf: "settings",
  plist: "settings",
  md: "markdown",
  mdx: "markdown",
  png: "image",
  jpg: "image",
  jpeg: "image",
  gif: "image",
  webp: "image",
  svg: "image",
  ico: "image",
  avif: "image",
  bmp: "image",
  mp4: "video",
  mov: "video",
  webm: "video",
  mp3: "audio",
  wav: "audio",
  m4a: "audio",
  pdf: "pdf",
  zip: "zip",
  gz: "zip",
  tar: "zip",
  tgz: "zip",
  sh: "console",
  bash: "console",
  zsh: "console",
  fish: "console",
  sql: "database",
  db: "database",
  sqlite: "database",
  ttf: "font",
  otf: "font",
  woff: "font",
  woff2: "font",
  lock: "lock",
  env: "settings",
  xml: "html",
};

/**
 * The icon name for a file, or for a directory.
 *
 * Longest extension first, so "app.d.ts" is TypeScript rather than whatever
 * ".ts" alone would give, and "styles.module.css" stays CSS.
 */
export function fileIconName(name: string, directory = false): string {
  if (directory) return "folder-base";

  // Change rows pass a workspace-relative path; name rules must still see the
  // final component (for example, mobile/package.json or build/Dockerfile).
  const lower = (name.split(/[\\/]/).pop() ?? "").toLowerCase();
  if (Object.prototype.hasOwnProperty.call(byName, lower)) return byName[lower];
  if (lower.startsWith(".env.")) return "settings";
  if (lower.startsWith("dockerfile.")) return "docker";

  // A dotfile with nothing after the dot is a name, not an extension:
  // ".gitignore" is matched above, ".prettierrc" falls through to settings.
  if (lower.startsWith(".") && !lower.slice(1).includes(".")) {
    return "settings";
  }

  const parts = lower.split(".");
  for (let i = 1; i < parts.length; i++) {
    const candidate = parts.slice(i).join(".");
    if (Object.prototype.hasOwnProperty.call(byExtension, candidate)) return byExtension[candidate];
  }
  return "document";
}
