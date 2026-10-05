import type { Artifact } from "./protocol";
import type { FeatherName } from "./tool-icon";

/**
 * What an artifact is for, as a mark and a word. Kinds come from the agent and
 * the list is open-ended, so anything this app does not know reads as a file
 * rather than as nothing.
 */
export function artifactKind(kind: string): { icon: FeatherName; label: string } {
  switch (kind) {
    case "plan":
      return { icon: "map", label: "Plan" };
    case "task":
      return { icon: "check-square", label: "Task list" };
    case "walkthrough":
      return { icon: "book-open", label: "Walkthrough" };
    case "spec":
      return { icon: "clipboard", label: "Spec" };
    case "image":
      return { icon: "image", label: "Image" };
    case "video":
      return { icon: "film", label: "Recording" };
    default:
      return { icon: "file-text", label: "File" };
  }
}

/** The agent's own heading when it gave one, otherwise the file's name. */
export function artifactTitle(artifact: Pick<Artifact, "name" | "title">): string {
  return artifact.title?.trim() || artifact.name;
}

/**
 * Waiting on you first, then newest. A plan the agent is blocked on is the one
 * reason to open this list from a notification, so it is never below the fold.
 */
export function sortArtifacts(artifacts: readonly Artifact[]): Artifact[] {
  return [...artifacts].sort((a, b) => {
    if (Boolean(a.review) !== Boolean(b.review)) return a.review ? -1 : 1;
    if (a.updatedAt !== b.updatedAt) return b.updatedAt - a.updatedAt;
    return a.name.localeCompare(b.name);
  });
}

/** Markdown is rendered as a document; any other text is shown as source. */
export function isMarkdown(name: string, mime?: string): boolean {
  return mime === "text/markdown" || /\.(md|markdown)$/i.test(name);
}

/**
 * The header button's spoken name. The badge on it is a number, and a number
 * with no noun says nothing to someone who cannot see it.
 */
export function artifactsButtonLabel(total: number, toReview: number): string {
  const count = `${total} artifact${total === 1 ? "" : "s"}`;
  return toReview > 0 ? `${count}, ${toReview} waiting for your review` : count;
}
