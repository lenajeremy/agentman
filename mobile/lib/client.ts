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
  newFrameId,
} from "./protocol";

const STORAGE_KEY = "agentman.credentials";
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
	if (secured) return secured;

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
	const raw = JSON.stringify(creds);
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
		const parsed = JSON.parse(raw) as Partial<Credentials>;
		return typeof parsed.relayUrl === "string" && parsed.relayUrl.length > 0 &&
			typeof parsed.token === "string" && parsed.token.length > 0
			? { relayUrl: parsed.relayUrl, token: parsed.token }
			: null;
	} catch {
		return null;
	}
}

/** Exchange a scanned pairing token for a device token. */
export async function pairWithToken(
  relayUrl: string,
  token: string,
): Promise<Credentials> {
  // Same endpoint: the relay tells a scanned token from a typed code by its
  // shape, so the client cannot pick which path it gets.
  return pair(relayUrl, token);
}

/** Exchange a pairing code for a long-lived device token. */
export async function pair(
  relayUrl: string,
  code: string,
): Promise<Credentials> {
  const base = normalizeHttp(relayUrl);
  const response = await fetch(`${base}/pair`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code: code.replace(/\s/g, ""), deviceId: "phone" }),
  });

  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? "That code did not work. Generate a new one with `am pair`.");
  }
  const body = (await response.json()) as { token?: string };
  if (!body.token) throw new Error("The relay did not return a token.");

  const creds = { relayUrl: base, token: body.token };
  await saveCredentials(creds);
  return creds;
}

export type ConnectionState = "connecting" | "online" | "offline" | "unpaired";

export interface ClientHandlers {
  onEvent(event: DaemonEvent, replyTo?: string): void;
  onControl(control: Control): void;
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
  private attempt = 0;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
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

  constructor(creds: Credentials, handlers: ClientHandlers) {
    this.creds = creds;
    this.handlers = handlers;
  }

  connect(): void {
	if (this.closed) return;
	if (
	  this.ws?.readyState === WebSocket.CONNECTING ||
	  this.ws?.readyState === WebSocket.OPEN
	) {
	  return;
	}
	this.handlers.onConnectionChange("connecting", false);

	const endpoint = `${normalizeWs(this.creds.relayUrl)}/ws/app`;
	// React Native can authenticate the upgrade with a header, keeping the
	// year-long device token out of proxy access logs. Browsers cannot set
	// websocket headers, so web retains the query fallback supported by relay.
	const ws = Platform.OS === "web"
	  ? new WebSocket(`${endpoint}?token=${encodeURIComponent(this.creds.token)}`)
	  : new NativeWebSocket(endpoint, null, {
	      headers: { Authorization: `Bearer ${this.creds.token}` },
	    });
	this.ws = ws;

	ws.onopen = () => {
	  if (this.ws !== ws || this.closed) return;
      this.attempt = 0;
      // Re-establish streaming for whatever screen is open: the relay holds no
      // state, so a reconnect starts from nothing on its side.
      for (const sessionId of this.subscriptions) {
		this.write(newFrameId(), { type: "subscribe", sessionId });
      }
	  for (const [id, request] of this.replayable) {
		this.write(id, request);
	  }
	  this.write(newFrameId(), { type: "list_sessions" });
    };

	ws.onmessage = (event) => {
	  if (this.ws !== ws || this.closed) return;
	  const envelope = decodeEnvelope(String(event.data));
	  if (!envelope) return;
	  if (envelope.to === "relay") {
		const control = decodeControl(envelope.payload);
		if (!control) return;
		if (envelope.replyTo && (control.type === "error" || control.type === "daemon_offline")) {
		  this.failAction(envelope.replyTo, control.message ?? "The daemon is offline.");
		}
        if (control.type === "hello") {
          this.handlers.onConnectionChange("online", control.daemonOnline ?? false);
        } else if (control.type === "daemon_online") {
          this.handlers.onConnectionChange("online", true);
        } else if (control.type === "daemon_offline") {
          this.handlers.onConnectionChange("online", false);
        }
        this.handlers.onControl(control);
        return;
	  }
	  if (envelope.to === "app") {
		const daemonEvent = decodeDaemonEvent(envelope.payload);
		if (!daemonEvent) return;
		if (envelope.replyTo) {
		  this.replayable.delete(envelope.replyTo);
		  this.inFlightActions.delete(envelope.replyTo);
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
	  this.ws = null;
      if (this.closed) return;
	  for (const id of this.inFlightActions.keys()) {
		this.failAction(id,
		  "Connection closed before the daemon confirmed this action. Check the session before retrying.");
	  }
      this.handlers.onConnectionChange("offline", false);
      this.scheduleRetry();
    };
  }

  private scheduleRetry(): void {
    if (this.retryTimer) clearTimeout(this.retryTimer);
    // Back off, but stay responsive: someone staring at the screen waiting for
    // it to come back should not wait 30s for the next try.
    const delay = Math.min(1000 * 2 ** this.attempt, 15000);
    this.attempt += 1;
    this.retryTimer = setTimeout(() => this.connect(), delay);
  }

  /** Reconnect immediately, e.g. when the app returns to the foreground. */
  poke(): void {
	if (this.ws?.readyState === WebSocket.OPEN) {
      this.send({ type: "list_sessions" });
	  return;
	}
	if (this.ws?.readyState === WebSocket.CONNECTING) return;
    this.attempt = 0;
    if (this.retryTimer) clearTimeout(this.retryTimer);
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
	if (request.clientId && isAction(request)) this.inFlightActions.set(id, request);
	if (!this.write(id, request)) {
	  this.inFlightActions.delete(id);
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
	this.inFlightActions.delete(id);
	this.handlers.onEvent({
	  type: "send_result",
	  sessionId: request.sessionId,
	  clientId: request.clientId,
	  status: "failed",
	  error: message,
	}, id);
  }

  subscribe(sessionId: string): void {
    this.subscriptions.add(sessionId);
    this.send({ type: "subscribe", sessionId });
  }

  unsubscribe(sessionId: string): void {
    this.subscriptions.delete(sessionId);
    this.send({ type: "unsubscribe", sessionId });
  }

  close(): void {
    this.closed = true;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.ws?.close();
    this.ws = null;
	this.replayable.clear();
	this.inFlightActions.clear();
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

function normalizeHttp(raw: string): string {
  const trimmed = raw.trim().replace(/\/+$/, "");
  if (/^https?:\/\//.test(trimmed)) return trimmed;
  if (/^wss:\/\//.test(trimmed)) return trimmed.replace(/^wss:/, "https:");
  if (/^ws:\/\//.test(trimmed)) return trimmed.replace(/^ws:/, "http:");
  // Bare host: assume TLS, since a hosted relay always has it and a LAN
  // address is the exception the user can type in full.
  return `https://${trimmed}`;
}

function normalizeWs(raw: string): string {
  const http = normalizeHttp(raw);
  return http.replace(/^http/, "ws");
}
