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

/** How the app should ask the daemon for a given image. */
export interface ImageTarget {
  /** A workspace-relative path, or the absolute path the tool named. */
  path: string;
  /**
   * read_file is confined to the session's directory. read_seen_file serves
   * any path the session's agent already opened, which is the only way to
   * reach the temp directory an agent actually writes screenshots into.
   */
  request: "read_file" | "read_seen_file";
}

/**
 * How to open the image a tool named, or null when it named something else.
 *
 * An absolute path is not rejected for being outside the session any more.
 * Agents write screenshots to temp directories, so that rule excluded exactly
 * the files worth looking at. It is safe because the daemon grants nothing on
 * the strength of this path: it serves an absolute path only when its own
 * record of the transcript says that session's agent opened it, and this path
 * came from that same transcript.
 */
export function workspaceImage(name: string, summary: string, cwd: string): ImageTarget | null {
  if (!PATH_TOOLS.test(name)) return null;

  const first = (summary.split("\n", 1)[0] ?? "").trim();
  if (!first || !IMAGE.test(first)) return null;

  if (first.startsWith("/")) {
    if (!PATH_SHAPE.test(first)) return null;
    if (first.split("/").some((part) => part === "..")) return null;
    // Inside the session, the workspace reader is still the better route: it
    // works on a daemon too old to know read_seen_file.
    const root = cwd.replace(/\/+$/, "");
    if (root && first.startsWith(`${root}/`)) {
      return { path: first.slice(root.length + 1), request: "read_file" };
    }
    return { path: first, request: "read_seen_file" };
  }

  const rel = first.replace(/^\.\//, "");
  if (!rel || !PATH_SHAPE.test(rel)) return null;
  if (rel.split("/").some((part) => part === "..")) return null;
  return { path: rel, request: "read_file" };
}
