/**
 * The models Agentman can run on, as the Mac offers them: Claude's and
 * Gemini's, each needing its engine's key on the Mac.
 */
export interface ManagerModel {
  id: string;
  name: string;
  provider: "anthropic" | "gemini";
}

export const MANAGER_MODELS: ManagerModel[] = [
  { id: "claude-opus-5-5", name: "Claude Opus 5.5", provider: "anthropic" },
  { id: "claude-sonnet-5-5", name: "Claude Sonnet 5.5", provider: "anthropic" },
  { id: "claude-haiku-5-5", name: "Claude Haiku 5.5", provider: "anthropic" },
  { id: "claude-fable-5-1", name: "Claude Fable 5.1", provider: "anthropic" },
  { id: "gemini-4-argon", name: "Gemini 4 Argon", provider: "gemini" },
  { id: "gemini-3.8-flash", name: "Gemini 3.8 Flash", provider: "gemini" },
  { id: "gemini-3.1-pro-preview", name: "Gemini 3.1 Pro", provider: "gemini" },
  { id: "gemini-3.5-flash-lite", name: "Gemini 3.5 Flash-Lite", provider: "gemini" },
];

/** A model's name for a button, or its id when this app doesn't know it. */
export function modelName(id: string): string {
  return MANAGER_MODELS.find((model) => model.id === id)?.name ?? id;
}
