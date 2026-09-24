/**
 * Which tool targets the app can open as a picture.
 *
 * The daemon only reads inside a session's own directory — os.Root
 * containment, symlinks refused — so a path outside it is not something the
 * phone can ask for, however plausible it looks. Deciding that here keeps a
 * row from offering a button that could only ever fail, and keeps the rule
 * testable on Node rather than discoverable on a phone.
 */

/** The formats readWorkspace will return as base64. */
const IMAGE = /\.(png|jpe?g|gif|webp)$/i;

/**
 * Tools whose argument is a path rather than a program to run.
 *
 * Matched loosely because the names come straight from each CLI — Claude's
 * "Read", OpenCode's "read", Codex's "Edit" — and matched at all because
 * without it a shell command that merely ends in .png ("open shot.png") reads
 * as a file that exists.
 */
const PATH_TOOLS = /(read|write|edit|view|notebook)/i;

/** A path, not a command: one or more plain segments and nothing to execute. */
const PATH_SHAPE = /^\/?[\w.@+-]+(\/[\w.@+-]+)*$/;

/**
 * The tool's target as a path the workspace can read, or "" when it is not an
 * image this session is allowed to open.
 */
export function workspaceImage(name: string, summary: string, cwd: string): string {
  if (!PATH_TOOLS.test(name)) return "";

  const first = (summary.split("\n", 1)[0] ?? "").trim();
  if (!first || !IMAGE.test(first)) return "";

  let rel = first.replace(/^\.\//, "");
  if (first.startsWith("/")) {
    // "~" never reaches here: it fails PATH_SHAPE below, which is right —
    // expanding it would mean guessing a home the daemon may not share.
    const root = cwd.replace(/\/+$/, "");
    if (!root || !first.startsWith(`${root}/`)) return "";
    rel = first.slice(root.length + 1);
  }

  if (!rel || !PATH_SHAPE.test(rel)) return "";
  if (rel.split("/").some((part) => part === "..")) return "";
  return rel;
}
