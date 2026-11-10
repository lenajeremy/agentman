// Speaking a message instead of typing it (Design/VoiceRecorder.swift): the
// microphone records 16 kHz mono WAV, the daemon has Celery write it out,
// and the words land in the box to read over before sending.

import { daemon, type AppRequest } from "../daemon";

/** Ten minutes at most: Celery takes about twelve. */
const MAX_SECONDS = 600;
const RATE = 16000;

const DENIED = "Agentman can't use the microphone. Allow it in System Settings › Privacy & Security › Microphone.";

// Hands each block of samples to the page, as they come.
const WORKLET = `class Tap extends AudioWorkletProcessor {
  process(inputs) { const ch = inputs[0] && inputs[0][0]; if (ch) this.port.postMessage(ch.slice(0)); return true; }
}
registerProcessor("agentman-tap", Tap);`;

export class Recording {
  private chunks: Float32Array[] = [];
  private samples = 0;
  private stream?: MediaStream;
  private context?: AudioContext;
  private node?: AudioWorkletNode;
  /** How loud it is now, 0 to 1. */
  level = 0;
  onLevel?: (level: number) => void;

  /** Starts listening, asking for the microphone the first time; why it
   *  couldn't, if it couldn't. */
  async start(): Promise<string | null> {
    try {
      this.stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
    } catch (err) {
      const name = err instanceof DOMException ? err.name : "";
      return name === "NotAllowedError" || name === "SecurityError" ? DENIED : "The microphone didn't start. Try again.";
    }
    try {
      this.context = new AudioContext({ sampleRate: RATE });
      const module = URL.createObjectURL(new Blob([WORKLET], { type: "text/javascript" }));
      await this.context.audioWorklet.addModule(module);
      URL.revokeObjectURL(module);
      const source = this.context.createMediaStreamSource(this.stream);
      this.node = new AudioWorkletNode(this.context, "agentman-tap");
      this.node.port.onmessage = (e: MessageEvent<Float32Array>) => this.take(e.data);
      source.connect(this.node);
      return null;
    } catch (err) {
      this.stop();
      return `The microphone didn't start: ${err instanceof Error ? err.message : String(err)}`;
    }
  }

  private take(block: Float32Array) {
    if (this.samples >= MAX_SECONDS * RATE) return;
    this.chunks.push(block);
    this.samples += block.length;
    let sum = 0;
    for (const v of block) sum += v * v;
    const rms = Math.sqrt(sum / Math.max(block.length, 1));
    // -50 dB is silence enough; 0 dB is as loud as it gets.
    const db = 20 * Math.log10(Math.max(rms, 1e-6));
    this.level = Math.max(0, Math.min(1, (db + 50) / 50));
    this.onLevel?.(this.level);
  }

  private stop() {
    this.node?.disconnect();
    this.stream?.getTracks().forEach((t) => t.stop());
    void this.context?.close().catch(() => {});
    this.node = undefined;
    this.stream = undefined;
    this.context = undefined;
    this.level = 0;
  }

  /** Stops listening and forgets what was heard. */
  cancel() {
    this.stop();
    this.chunks = [];
    this.samples = 0;
  }

  /** Stops listening and returns the recording as WAV, base64; null when
   *  nothing was heard. */
  finish(): string | null {
    this.stop();
    if (!this.samples) return null;
    const pcm = new DataView(new ArrayBuffer(44 + this.samples * 2));
    const text = (at: number, s: string) => [...s].forEach((c, i) => pcm.setUint8(at + i, c.charCodeAt(0)));
    text(0, "RIFF");
    pcm.setUint32(4, 36 + this.samples * 2, true);
    text(8, "WAVE");
    text(12, "fmt ");
    pcm.setUint32(16, 16, true);
    pcm.setUint16(20, 1, true);
    pcm.setUint16(22, 1, true);
    pcm.setUint32(24, RATE, true);
    pcm.setUint32(28, RATE * 2, true);
    pcm.setUint16(32, 2, true);
    pcm.setUint16(34, 16, true);
    text(36, "data");
    pcm.setUint32(40, this.samples * 2, true);
    let at = 44;
    for (const chunk of this.chunks) {
      for (const v of chunk) {
        pcm.setInt16(at, Math.max(-1, Math.min(1, v)) * 0x7fff, true);
        at += 2;
      }
    }
    this.chunks = [];
    this.samples = 0;
    const bytes = new Uint8Array(pcm.buffer);
    let binary = "";
    for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
    return btoa(binary);
  }
}

/** What a recording says, written out by Celery through the daemon, sent in
 *  slices the relay would carry from a phone too. */
export async function transcribe(wav: string): Promise<string> {
  const size = 96_000;
  const parts = Math.max(1, Math.ceil(wav.length / size));
  const id = crypto.randomUUID();
  for (let part = 0; part < parts; part += 1) {
    const reply = await daemon.request(
      { type: "transcribe", voice: { id, part, parts, mime: "audio/wav", data: wav.slice(part * size, (part + 1) * size) } } as AppRequest,
      // The last part waits for Celery.
      part === parts - 1 ? 150_000 : 20_000,
    );
    if (reply.type === "error") throw new Error((reply.error ?? "That couldn't be written out.").replace(/^daemon: /, ""));
    if (reply.transcript?.done) return reply.transcript.text ?? "";
  }
  throw new Error("The recording didn't come back written out. Try again.");
}

/** The words of a voice prompt added to what is already in a box. */
export function addSpoken(draft: string, spoken: string): string {
  const trimmed = draft.trim();
  return trimmed ? `${trimmed} ${spoken}` : spoken;
}

/** "1:05". */
export function elapsed(seconds: number): string {
  const whole = Math.max(0, Math.floor(seconds));
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
}
