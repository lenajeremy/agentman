import AsyncStorage from "@react-native-async-storage/async-storage";
import * as SecureStore from "expo-secure-store";
import { Platform } from "react-native";
import {
  Control,
  DaemonEvent,
  Envelope,
  PROTOCOL_VERSION,
  Request,
  decodeControl,
  decodeDaemonEvent,
  decodeEnvelope,
} from "./protocol";
import { newFrameId } from "./id";
import { normalizeCredentials, normalizeRelayUrl } from "./pairing";

const STORAGE_KEY = "agentman.credentials";
const PAIR_TIMEOUT_MS = 15_000;
const CONNECT_TIMEOUT_MS = 15_000;
const ACTION_TIMEOUT_MS = 15_000;
const SECURE_OPTIONS: SecureStore.SecureStoreOptions = {
  keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
};

export interface Credentials {
  relayUrl: string;
  token: string;
}

export async function loadCredentials(): Promise<Credentials | null> {
	if (Platform.OS === "web") {
		return parseCredentials(await AsyncStorage.getItem(STORAGE_KEY));
	}

	const secured = parseCredentials(await SecureStore.getItemAsync(STORAGE_KEY));
	if (secured) {
		// A previous migration may have crashed after the protected write but
		// before deleting the plaintext copy. Retry that cleanup on every launch.
		await AsyncStorage.removeItem(STORAGE_KEY).catch(() => {});
		return secured;
	}

	// One-time migration from releases that kept the bearer in AsyncStorage.
	// Write the protected copy first; a crash between the operations leaves a
	// duplicate to clean up, never a lost credential.
	const legacy = parseCredentials(await AsyncStorage.getItem(STORAGE_KEY));
	if (!legacy) return null;
	await SecureStore.setItemAsync(STORAGE_KEY, JSON.stringify(legacy), SECURE_OPTIONS);
	await AsyncStorage.removeItem(STORAGE_KEY);
	return legacy;
}

export async function saveCredentials(creds: Credentials): Promise<void> {
	const normalized = normalizeCredentials(creds);
	if (!normalized) throw new Error("Refusing to store invalid relay credentials.");
	const raw = JSON.stringify(normalized);
	if (Platform.OS === "web") {
		await AsyncStorage.setItem(STORAGE_KEY, raw);
		return;
	}
	await SecureStore.setItemAsync(STORAGE_KEY, raw, SECURE_OPTIONS);
	await AsyncStorage.removeItem(STORAGE_KEY);
}

export async function clearCredentials(): Promise<void> {
	if (Platform.OS !== "web") {
		await SecureStore.deleteItemAsync(STORAGE_KEY);
	}
	await AsyncStorage.removeItem(STORAGE_KEY);
}

function parseCredentials(raw: string | null): Credentials | null {
	if (!raw) return null;
	try {
		return normalizeCredentials(JSON.parse(raw));
	} catch {
		return null;
	}
}

/** Exchange a scanned pairing token for a device token. */
export async function pairWithToken(
  relayUrl: string,
  token: string,
): Promise<Credentials> {
  const payload = normalizeCredentials({ relayUrl, token });
  if (!payload) throw new Error("This pairing link is invalid. Generate a fresh one with `am pair`.");
  // Same endpoint: the relay tells a scanned token from a typed code by its
  // shape, so the client cannot pick which path it gets.
  return pair(payload.relayUrl, payload.token);
}

/** Exchange a pairing code for a long-lived device token. */
export async function pair(
  relayUrl: string,
  code: string,
): Promise<Credentials> {
  const base = normalizeRelayUrl(relayUrl);
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), PAIR_TIMEOUT_MS);
  try {
    const response = await fetch(`${base}/pair`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code: code.replace(/\s/g, ""), deviceId: "phone" }),
      signal: controller.signal,
    });

    if (!response.ok) {
      const body = (await response.json().catch(() => ({}))) as { error?: string };
      throw new Error(body.error ?? "That code did not work. Generate a new one with `am pair`.");
    }
    const body = (await response.json()) as { token?: string };
    const creds = normalizeCredentials({ relayUrl: base, token: body.token });
    if (!creds) throw new Error("The relay did not return a valid token.");

    await saveCredentials(creds);
    return creds;
  } catch (error) {
    if (controller.signal.aborted) {
      throw new Error("Pairing timed out. Check the relay address and try again.");
    }
    throw error;
  } finally {
    clearTimeout(timeout);
  }
}

export type ConnectionState =
  | "connecting"
  | "online"
  | "offline"
  | "incompatible"
  | "unpaired";

export interface ClientHandlers {
  onEvent(event: DaemonEvent, replyTo?: string): void;
  onControl(control: Control, replyTo?: string): void;
  onConnectionChange(state: ConnectionState, daemonOnline: boolean): void;
}

/**
 * Live connection to the daemon, by way of the relay.
 *
 * Reconnects on its own, because a phone loses its connection constantly —
 * screen lock, cell handover, a lift. None of that is worth an error message,
 * so the UI only ever shows two things: whether the Mac is reachable, and how
 * long it has been gone.
 */
export class Client {
  private ws: WebSocket | null = null;
  private creds: Credentials;
  private handlers: ClientHandlers;
  private closed = false;
  /**
   * The relay refused this build's protocol version. Reconnecting cannot fix
   * that — only a newer app can — so retrying stops and the explanation stays
   * on screen instead of being overwritten by the close that follows it.
   */
  private incompatible = false;
  private attempt = 0;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private connectTimer: ReturnType<typeof setTimeout> | null = null;
  /** Sessions we want streamed, re-sent after every reconnect. */
  private subscriptions = new Set<string>();
  /**
   * Read requests survive a routine socket handover. A history request sent
   * just before the connection closes is safe to repeat because messages are
   * merged by stable id; losing it would leave the session spinner stuck.
   */
  private replayable = new Map<string, Request>();
  /** Non-idempotent commands cannot be replayed safely. Track them only so a
   * disconnect can turn an endless "sending" state into an honest unknown
   * outcome that asks the user to inspect the session before retrying. */
  private inFlightActions = new Map<string, Request>();
  private actionTimers = new Map<string, ReturnType<typeof setTimeout>>();

  constructor(creds: Credentials, handlers: ClientHandlers) {
    this.creds = creds;
    this.handlers = handlers;
  }

  connect(): void {
	if (this.closed || this.incompatible) return;
	if (
	  this.ws?.readyState === WebSocket.CONNECTING ||
	  this.ws?.readyState === WebSocket.OPEN
	) {
	  return;
	}
	if (this.retryTimer) clearTimeout(this.retryTimer);
	this.retryTimer = null;
	this.handlers.onConnectionChange("connecting", false);

	// React Native can authenticate the upgrade with a header, keeping the
	// year-long device token out of proxy access logs. Browsers cannot set
	// websocket headers, so web retains the query fallback supported by relay.
	let ws: WebSocket;
	try {
	  const endpoint = `${normalizeRelayUrl(this.creds.relayUrl).replace(/^http/, "ws")}/ws/app`;
	  ws = Platform.OS === "web"
	    ? new WebSocket(`${endpoint}?token=${encodeURIComponent(this.creds.token)}`)
	    : new NativeWebSocket(endpoint, null, {
	        headers: { Authorization: `Bearer ${this.creds.token}` },
	      });
	} catch {
	  this.handlers.onConnectionChange("offline", false);
	  this.scheduleRetry();
	  return;
	}
	this.ws = ws;
	this.clearConnectTimer();
	this.connectTimer = setTimeout(() => {
	  if (this.ws !== ws || this.closed || ws.readyState !== WebSocket.CONNECTING) return;
	  // Some mobile network failures never produce onerror/onclose. Detach first
	  // so the eventual close event from this stale socket cannot schedule a
	  // second retry or tear down its replacement.
	  this.ws = null;
	  this.clearConnectTimer();
	  try {
	    ws.close();
	  } catch {
	    // A half-created native socket can reject close; it is already detached.
	  }
	  this.handlers.onConnectionChange("offline", false);
	  this.scheduleRetry();
	}, CONNECT_TIMEOUT_MS);

	ws.onopen = () => {
	  if (this.ws !== ws || this.closed) return;
      this.clearConnectTimer();
      this.attempt = 0;
      // Re-establish streaming for whatever screen is open: the relay holds no
      // state, so a reconnect starts from nothing on its side.
	  this.sendSubscriptions();
	  this.resendReplayable();
	  this.write(newFrameId(), { type: "list_sessions" });
    };

	ws.onmessage = (event) => {
	  if (this.ws !== ws || this.closed) return;
	  const envelope = decodeEnvelope(String(event.data));
	  if (!envelope) return;
	  if (envelope.to === "relay") {
		const control = decodeControl(envelope.payload);
		if (!control) return;
		if (control.type === "error" && control.message === "unsupported protocol version") {
		  this.incompatible = true;
		  this.handlers.onConnectionChange("incompatible", false);
		}
		if (envelope.replyTo && control.type === "error") {
		  // Relay protocol/address errors are permanent for this request. Keeping
		  // them replayable would retry forever and leave a stale correlation id.
		  this.replayable.delete(envelope.replyTo);
		  this.failAction(envelope.replyTo, control.message ?? "The relay rejected this request.");
		} else if (envelope.replyTo && control.type === "daemon_offline") {
		  // Reads are safe to replay when the Mac returns; actions are not.
		  this.failAction(envelope.replyTo, control.message ?? "The daemon is offline.");
		}
        if (control.type === "hello") {
          this.handlers.onConnectionChange("online", control.daemonOnline ?? false);
        } else if (control.type === "daemon_online") {
          this.handlers.onConnectionChange("online", true);
		  // The relay rejected every command while the daemon was absent; unlike
		  // a socket reconnect there is no onopen event to restore these.
		  this.sendSubscriptions();
		  this.resendReplayable();
		  this.write(newFrameId(), { type: "list_sessions" });
        } else if (control.type === "daemon_offline") {
          this.handlers.onConnectionChange("online", false);
        }
        this.handlers.onControl(control, envelope.replyTo);
        return;
	  }
	  if (envelope.to === "app") {
		const daemonEvent = decodeDaemonEvent(envelope.payload);
		if (!daemonEvent) return;
		if (envelope.replyTo) {
		  if (daemonEvent.type === "page" || daemonEvent.type === "error") {
			this.replayable.delete(envelope.replyTo);
		  }
		  if (daemonEvent.type === "send_result") this.finishAction(envelope.replyTo);
		}
	    this.handlers.onEvent(daemonEvent, envelope.replyTo);
	  }
    };

    ws.onerror = () => {
      // Surfaced through onclose; a socket error on mobile is routine.
    };

	ws.onclose = () => {
	  // An older socket can close after poke() has already installed a fresh
	  // one. It must not null out that replacement or schedule a second retry.
	  if (this.ws !== ws) return;
	  this.clearConnectTimer();
	  this.ws = null;
      if (this.closed) return;
	  for (const id of Array.from(this.inFlightActions.keys())) {
		this.failAction(id,
		  "Connection closed before the daemon confirmed this action. Check the session before retrying.");
	  }
      // The relay already said why it hung up. Leave that on screen rather than
      // replacing it with "offline" and retrying against the same wall.
      if (this.incompatible) return;
      this.handlers.onConnectionChange("offline", false);
      this.scheduleRetry();
    };
  }

  private scheduleRetry(): void {
    if (this.closed || this.incompatible) return;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    // Back off, but stay responsive: someone staring at the screen waiting for
    // it to come back should not wait 30s for the next try.
    const delay = Math.min(1000 * 2 ** this.attempt, 15000);
    this.attempt += 1;
    this.retryTimer = setTimeout(() => this.connect(), delay);
  }

  private clearConnectTimer(): void {
    if (!this.connectTimer) return;
    clearTimeout(this.connectTimer);
    this.connectTimer = null;
  }

  private resendReplayable(): void {
    if (this.ws?.readyState !== WebSocket.OPEN) return;
    for (const [id, request] of this.replayable) this.write(id, request);
  }

  private sendSubscriptions(): void {
    if (this.ws?.readyState !== WebSocket.OPEN) return;
    for (const sessionId of this.subscriptions) {
      this.write(newFrameId(), { type: "subscribe", sessionId });
    }
  }

  /** Reconnect immediately, e.g. when the app returns to the foreground. */
  poke(): void {
	if (this.ws?.readyState === WebSocket.OPEN) {
      this.resendReplayable();
      this.send({ type: "list_sessions" });
	  return;
	}
	if (this.ws?.readyState === WebSocket.CONNECTING) return;
    this.attempt = 0;
    if (this.retryTimer) clearTimeout(this.retryTimer);
	this.retryTimer = null;
    this.connect();
  }

  send(request: Request): string | null {
    const id = newFrameId();
	if (request.type === "fetch_messages") {
	  // Queue while offline and replay after a disconnect. Bound the queue so a
	  // broken caller cannot retain arbitrary request state forever.
	  if (this.replayable.size >= 64) return null;
	  this.replayable.set(id, request);
	  if (this.ws?.readyState === WebSocket.OPEN) this.write(id, request);
	  return id;
	}
	if (this.ws?.readyState !== WebSocket.OPEN) return null;
	if (request.clientId && isAction(request)) {
	  this.inFlightActions.set(id, request);
	  this.actionTimers.set(id, setTimeout(() => {
		this.failAction(
		  id,
		  "The daemon did not confirm this action in time. Check the session before retrying.",
		);
	  }, ACTION_TIMEOUT_MS));
	}
	if (!this.write(id, request)) {
	  this.finishAction(id);
	  return null;
	}
	return id;
  }

  private write(id: string, request: Request): boolean {
    const envelope: Envelope = {
      v: PROTOCOL_VERSION,
      id,
      to: "daemon",
      payload: request,
    };
	if (this.ws?.readyState !== WebSocket.OPEN) return false;
	try {
      this.ws.send(JSON.stringify(envelope));
	  return true;
	} catch {
	  return false;
	}
  }

  private failAction(id: string, message: string): void {
	const request = this.inFlightActions.get(id);
	if (!request) return;
	this.finishAction(id);
	this.handlers.onEvent({
	  type: "send_result",
	  sessionId: request.sessionId,
	  clientId: request.clientId,
	  status: "failed",
	  error: message,
	}, id);
  }

  private finishAction(id: string): void {
	this.inFlightActions.delete(id);
	const timer = this.actionTimers.get(id);
	if (timer) clearTimeout(timer);
	this.actionTimers.delete(id);
  }

  subscribe(sessionId: string): void {
    this.subscriptions.add(sessionId);
    this.send({ type: "subscribe", sessionId });
  }

  unsubscribe(sessionId: string): void {
    this.subscriptions.delete(sessionId);
    this.send({ type: "unsubscribe", sessionId });
  }

  /** Drop every retained client-side reference after the daemon removes a session. */
  forgetSession(sessionId: string): void {
    this.subscriptions.delete(sessionId);
    for (const [id, request] of this.replayable) {
      if (request.sessionId === sessionId) this.replayable.delete(id);
    }
    this.send({ type: "unsubscribe", sessionId });
  }

  close(): void {
    this.closed = true;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.retryTimer = null;
    this.clearConnectTimer();
    this.ws?.close();
    this.ws = null;
	this.replayable.clear();
	this.inFlightActions.clear();
	for (const timer of this.actionTimers.values()) clearTimeout(timer);
	this.actionTimers.clear();
  }
}

function isAction(request: Request): boolean {
  return request.type === "send_message" || request.type === "interrupt" ||
    request.type === "answer_question";
}

// React Native supports upgrade headers in the third constructor argument,
// while the DOM-flavoured TypeScript declaration bundled with Expo exposes
// only the browser's two-argument signature.
const NativeWebSocket = WebSocket as unknown as {
	new (
		url: string,
		protocols: string | string[] | null,
		options: { headers: Record<string, string> },
	): WebSocket;
};
