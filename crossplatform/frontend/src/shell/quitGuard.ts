// Unsaved work the app asks about before quitting (AgentmanApp.swift's
// applicationShouldTerminate). Editor windows register what is unsaved; the
// shell area shows the Mac's alert. Owned by the shell area.

export interface Unsaved {
  /** The file's name, for "Save the changes to <name> before quitting?". */
  name: string;
  path: string;
  /** Saves it; true when it worked. */
  save: () => Promise<boolean>;
}

const sources = new Map<string, () => Unsaved[]>();

/** Registers a window's unsaved files; returns how to unregister. */
export function registerUnsaved(id: string, unsaved: () => Unsaved[]): () => void {
  sources.set(id, unsaved);
  return () => sources.delete(id);
}

export function unsavedFiles(): Unsaved[] {
  return [...sources.values()].flatMap((list) => list());
}
