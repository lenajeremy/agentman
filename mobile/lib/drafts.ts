import AsyncStorage from "@react-native-async-storage/async-storage";

import { createDraftPersistence } from "./draft-policy";

/**
 * Unsent message drafts, kept per relay account and session on the device.
 *
 * Someone types a long instruction on a phone, gets interrupted, and comes
 * back — losing that text is a small betrayal, and it is entirely avoidable.
 * Drafts live only on the device: they are not sent to the daemon or the
 * relay, because an unsent message is not something the agent should see and
 * not something a relay has any business holding.
 */
const persistence = createDraftPersistence(AsyncStorage);

export const loadDraft = persistence.load;
export const saveDraft = persistence.save;
export const clearDraft = persistence.clear;
