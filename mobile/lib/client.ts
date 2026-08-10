import AsyncStorage from "@react-native-async-storage/async-storage";
import {
  Control,
  DaemonEvent,
  Envelope,
  PROTOCOL_VERSION,
  Request,
  newFrameId,
} from "./protocol";

const STORAGE_KEY = "agentman.credentials";

export interface Credentials {
  relayUrl: string;
  token: string;
}

export async function loadCredentials(): Promise<Credentials | null> {
  const raw = await AsyncStorage.getItem(STORAGE_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Credentials;
    return parsed.relayUrl && parsed.token ? parsed : null;
  } catch {
    return null;
  }
}

export async function saveCredentials(creds: Credentials): Promise<void> {
  await AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(creds));
}

export async function clearCredentials(): Promise<void> {
  await AsyncStorage.removeItem(STORAGE_KEY);
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

  constructor(creds: Credentials, handlers: ClientHandlers) {
    this.creds = creds;
    this.handlers = handlers;
  }

  connect(): void {
    if (this.closed) return;
    this.handlers.onConnectionChange("connecting", false);

    const url = `${normalizeWs(this.creds.relayUrl)}/ws/app?token=${encodeURIComponent(this.creds.token)}`;
    const ws = new WebSocket(url);
    this.ws = ws;

    ws.onopen = () => {
      this.attempt = 0;
      // Re-establish streaming for whatever screen is open: the relay holds no
      // state, so a reconnect starts from nothing on its side.
      for (const sessionId of this.subscriptions) {
        this.send({ type: "subscribe", sessionId });
      }
      this.send({ type: "list_sessions" });
    };

    ws.onmessage = (event) => {
      let envelope: Envelope;
      try {
        envelope = JSON.parse(String(event.data)) as Envelope;
      } catch {
        return;
      }
      if (envelope.to === "relay") {
        const control = envelope.payload as Control;
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
      this.handlers.onEvent(envelope.payload as DaemonEvent, envelope.replyTo);
    };

    ws.onerror = () => {
      // Surfaced through onclose; a socket error on mobile is routine.
    };

    ws.onclose = () => {
      this.ws = null;
      if (this.closed) return;
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
    this.attempt = 0;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.connect();
  }

  send(request: Request): string | null {
    const id = newFrameId();
    const envelope: Envelope = {
      v: PROTOCOL_VERSION,
      id,
      to: "daemon",
      payload: request,
    };
    if (this.ws?.readyState !== WebSocket.OPEN) return null;
    this.ws.send(JSON.stringify(envelope));
    return id;
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
  }
}

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
