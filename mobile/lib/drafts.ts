import AsyncStorage from "@react-native-async-storage/async-storage";

/**
 * Unsent message drafts, kept per session on the device.
 *
 * Someone types a long instruction on a phone, gets interrupted, and comes
 * back — losing that text is a small betrayal, and it is entirely avoidable.
 * Drafts live only on the device: they are not sent to the daemon or the
 * relay, because an unsent message is not something the agent should see and
 * not something a relay has any business holding.
 */
const PREFIX = "agentman.draft.";

export async function loadDraft(sessionId: string): Promise<string> {
  try {
    return (await AsyncStorage.getItem(PREFIX + sessionId)) ?? "";
  } catch {
    return "";
  }
}

export async function saveDraft(sessionId: string, text: string): Promise<void> {
  try {
    if (text.trim() === "") {
      await AsyncStorage.removeItem(PREFIX + sessionId);
      return;
    }
    await AsyncStorage.setItem(PREFIX + sessionId, text);
  } catch {
    // A draft that fails to persist is not worth surfacing an error for.
  }
}

export async function clearDraft(sessionId: string): Promise<void> {
  try {
    await AsyncStorage.removeItem(PREFIX + sessionId);
  } catch {
    // Ignored for the same reason.
  }
}
