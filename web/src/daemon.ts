// The connection to am serve: the phone's protocol over the loopback
// WebSocket am serve offers apps (internal/daemon/appws.go). One envelope per
// line; lines can be split across messages or share one, so the messages are
// read as one stream.

import {
  PROTOCOL_VERSION,
  decodeDaemonEvent,
  decodeEnvelope,
  type DaemonEvent,
  type Request,
} from "@shared/protocol";
import { endpoint } from "./host";

/** Requests only an app on this computer makes, beyond the phone's. */
export type AppRequest = Omit<Request, "type"> & { type: Request["type"] | "lsp_endpoint" | "session_terminal" };

/** An event, with what the decoder doesn't know kept as sent. */
export type AppEvent = DaemonEvent & Record<string, unknown>;

export type Status = "connecting" | "connected" | "offline";

type Listener = (event: AppEvent, replyTo?: string) => void;

const TIMEOUT = 30_000;

export class DaemonClient {
  private ws?: WebSocket;
  private buffer = "";
  private next = 0;
  private pending = new Map<string, { resolve: (e: AppEvent) => void; reject: (e: Error) => void; timer: number }>();
  private listeners = new Set<Listener>();
  private statusListeners = new Set<(s: Status, detail?: string) => void>();
  private backoff = 500;
  private stopped = false;
  status: Status = "connecting";
  detail = "";

  start() {
    this.stopped = false;
    void this.open();
  }

  stop() {
    this.stopped = true;
    this.ws?.close();
  }

  on(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  onStatus(listener: (s: Status, detail?: string) => void): () => void {
    this.statusListeners.add(listener);
    return () => this.statusListeners.delete(listener);
  }

  /** Sends a request without waiting for its answer. */
  send(req: AppRequest): string | null {
    if (this.ws?.readyState !== WebSocket.OPEN) return null;
    const id = `app-${++this.next}`;
    const envelope = { v: PROTOCOL_VERSION, id, to: "daemon", payload: req };
    this.ws.send(JSON.stringify(envelope) + "\n");
    return id;
  }

  /** Sends a request and waits for the event that answers it. */
  request(req: AppRequest, timeout = TIMEOUT): Promise<AppEvent> {
    const id = this.send(req);
    if (!id) return Promise.reject(new Error("not connected to am serve"));
    return new Promise((resolve, reject) => {
      const timer = window.setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`${req.type} timed out`));
      }, timeout);
      this.pending.set(id, { resolve, reject, timer });
    });
  }

  private setStatus(status: Status, detail = "") {
    this.status = status;
    this.detail = detail;
    for (const l of this.statusListeners) l(status, detail);
  }

  private async open() {
    if (this.stopped) return;
    this.setStatus("connecting");
    let url: string;
    try {
      const ep = await endpoint();
      url = `${ep.url}?token=${encodeURIComponent(ep.token)}`;
    } catch (err) {
      this.retry(err instanceof Error ? err.message : String(err));
      return;
    }
    const ws = new WebSocket(url);
    this.ws = ws;
    this.buffer = "";
    ws.onopen = () => {
      this.backoff = 500;
      this.setStatus("connected");
    };
    ws.onmessage = (e) => this.receive(typeof e.data === "string" ? e.data : "");
    ws.onclose = () => {
      if (this.ws !== ws) return;
      for (const [id, p] of this.pending) {
        clearTimeout(p.timer);
        p.reject(new Error("am serve went away"));
        this.pending.delete(id);
      }
      this.retry("am serve went away");
    };
  }

  private retry(detail: string) {
    if (this.stopped) return;
    this.setStatus("offline", detail);
    const wait = this.backoff;
    this.backoff = Math.min(this.backoff * 2, 5000);
    window.setTimeout(() => void this.open(), wait);
  }

  private receive(chunk: string) {
    this.buffer += chunk;
    let newline: number;
    while ((newline = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.slice(0, newline);
      this.buffer = this.buffer.slice(newline + 1);
      if (line) this.frame(line);
    }
  }

  private frame(line: string) {
    const envelope = decodeEnvelope(line);
    if (!envelope) return;
    const raw = envelope.payload as Record<string, unknown>;
    // The phone's decoder checks every event it knows; one only this app
    // asks for (lsp_endpoint, session_terminal) passes as sent.
    const event = (decodeDaemonEvent(structuredClone(raw)) ?? raw) as AppEvent;
    const waiting = envelope.replyTo ? this.pending.get(envelope.replyTo) : undefined;
    if (waiting && envelope.replyTo) {
      this.pending.delete(envelope.replyTo);
      clearTimeout(waiting.timer);
      waiting.resolve(event);
    }
    for (const l of this.listeners) l(event, envelope.replyTo);
  }
}

export const daemon = new DaemonClient();
