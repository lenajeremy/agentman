/**
 * Whether the Mac is sending this phone's alerts by push.
 *
 * Once it is, the app stops scheduling its own, so one finished turn does not
 * ring twice. That switch used to flip the moment the app asked, before the
 * Mac had kept the token, and also when the Mac was offline and never saw the
 * request at all. The phone then got no alerts from either side.
 *
 * Now a request is accepted only once it has gone unanswered by an error for
 * a short while (the Mac sends nothing back on success), and refused by any
 * error about it. Only the latest request counts.
 */
export interface PushRegistration {
  /** The request waiting to be accepted or refused. */
  pending: string | null;
  /** Whether the Mac is known to be sending alerts by push. */
  active: boolean;
}

export const initialPushRegistration: PushRegistration = { pending: null, active: false };

/** How long a request must go unrefused to count as accepted. */
export const PUSH_ACCEPT_MS = 5_000;

/** A registration request went out, or could not (id null). */
export function pushRegistrationSent(state: PushRegistration, id: string | null): PushRegistration {
  if (!id) return state;
  return { pending: id, active: state.active };
}

/** The request went unrefused for PUSH_ACCEPT_MS. */
export function pushRegistrationAccepted(state: PushRegistration, id: string): PushRegistration {
  if (state.pending !== id) return state;
  return { pending: null, active: true };
}

/** The Mac, or the relay on its behalf, answered the request with an error. */
export function pushRegistrationRejected(state: PushRegistration, id: string): PushRegistration {
  if (state.pending !== id) return state;
  return { pending: null, active: false };
}
