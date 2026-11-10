// The mic beside Agentman's box (Design/VoiceRecorder.swift's VoiceButton):
// tap to speak, tap again to have it written out, Escape to throw it away.
// Records 16 kHz mono WAV, as Celery takes it, and has am serve write it
// out (the transcribe request, in parts).

import { useEffect, useRef, useState } from "react";
import { Mic } from "lucide-react";
import { daemon, type AppEvent, type AppRequest } from "../daemon";
import { Icon } from "../ui";

type State = { kind: "idle" } | { kind: "recording"; since: number } | { kind: "writing" };

const DENIED = "Agentman can't use the microphone. Allow it in System Settings › Privacy & Security › Microphone.";

class Recorder {
  private stream?: MediaStream;
  private context?: AudioContext;
  private node?: ScriptProcessorNode;
  private chunks: Float32Array[] = [];
  level = 0;
  onLevel?: (level: number) => void;

  async start(): Promise<string | undefined> {
    try {
      this.stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch {
      return DENIED;
    }
    this.context = new AudioContext();
    const source = this.context.createMediaStreamSource(this.stream);
    this.node = this.context.createScriptProcessor(4096, 1, 1);
    this.chunks = [];
    this.node.onaudioprocess = (e) => {
      const data = new Float32Array(e.inputBuffer.getChannelData(0));
      this.chunks.push(data);
      let sum = 0;
      for (const v of data) sum += v * v;
      this.level = Math.min(1, Math.sqrt(sum / data.length) * 4);
      this.onLevel?.(this.level);
    };
    source.connect(this.node);
    this.node.connect(this.context.destination);
    return undefined;
  }

  private release() {
    this.node?.disconnect();
    this.stream?.getTracks().forEach((t) => t.stop());
    void this.context?.close();
  }

  cancel() {
    this.release();
    this.chunks = [];
  }

  /** The recording as 16 kHz mono WAV, or null if nothing was heard. */
  async finish(): Promise<Uint8Array | null> {
    const rate = this.context?.sampleRate ?? 48000;
    this.release();
    const length = this.chunks.reduce((n, c) => n + c.length, 0);
    if (!length) return null;
    const all = new Float32Array(length);
    let at = 0;
    for (const c of this.chunks) {
      all.set(c, at);
      at += c.length;
    }
    this.chunks = [];
    const offline = new OfflineAudioContext(1, Math.ceil((length * 16000) / rate), 16000);
    const buffer = offline.createBuffer(1, length, rate);
    buffer.copyToChannel(all, 0);
    const source = offline.createBufferSource();
    source.buffer = buffer;
    source.connect(offline.destination);
    source.start();
    const samples = (await offline.startRendering()).getChannelData(0);
    const wav = new DataView(new ArrayBuffer(44 + samples.length * 2));
    const text = (o: number, s: string) => [...s].forEach((ch, i) => wav.setUint8(o + i, ch.charCodeAt(0)));
    text(0, "RIFF");
    wav.setUint32(4, 36 + samples.length * 2, true);
    text(8, "WAVE");
    text(12, "fmt ");
    wav.setUint32(16, 16, true);
    wav.setUint16(20, 1, true);
    wav.setUint16(22, 1, true);
    wav.setUint32(24, 16000, true);
    wav.setUint32(28, 32000, true);
    wav.setUint16(32, 2, true);
    wav.setUint16(34, 16, true);
    text(36, "data");
    wav.setUint32(40, samples.length * 2, true);
    samples.forEach((v, i) => wav.setInt16(44 + i * 2, Math.max(-1, Math.min(1, v)) * 0x7fff, true));
    return new Uint8Array(wav.buffer);
  }
}

/** Sends a request and waits for its answer, as long as `seconds`. */
function ask(req: AppRequest, seconds: number): Promise<AppEvent> {
  return new Promise((resolve, reject) => {
    const id = daemon.send(req);
    if (!id) return reject(new Error("not connected to am serve"));
    const timer = window.setTimeout(() => {
      off();
      reject(new Error("That took too long. Try again."));
    }, seconds * 1000);
    const off = daemon.on((event, replyTo) => {
      if (replyTo !== id) return;
      window.clearTimeout(timer);
      off();
      resolve(event);
    });
  });
}

/** The words in a recording, written out by Celery through am serve. */
async function transcribe(wav: Uint8Array): Promise<{ text?: string; problem?: string }> {
  let binary = "";
  for (let i = 0; i < wav.length; i += 0x8000) binary += String.fromCharCode(...wav.subarray(i, i + 0x8000));
  const encoded = btoa(binary);
  const size = 96_000;
  const parts = Math.max(1, Math.ceil(encoded.length / size));
  const id = crypto.randomUUID();
  for (let part = 0; part < parts; part++) {
    const voice = { id, part, parts, mime: "audio/wav", data: encoded.slice(part * size, (part + 1) * size) };
    try {
      // The last part waits for Celery.
      const reply = await ask({ type: "transcribe", voice } as unknown as AppRequest, part === parts - 1 ? 150 : 20);
      if (reply.type === "error") return { problem: reply.error ?? "That couldn't be written out." };
      const transcript = reply.transcript as { done?: boolean; text?: string } | undefined;
      if (transcript?.done) return { text: transcript.text ?? "" };
    } catch (err) {
      return { problem: err instanceof Error ? err.message : String(err) };
    }
  }
  return { problem: "The recording didn't come back written out. Try again." };
}

function elapsed(seconds: number): string {
  const whole = Math.max(0, Math.floor(seconds));
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
}

export function VoiceButton({ size, enabled = true, onText, onProblem }: { size: number; enabled?: boolean; onText: (text: string) => void; onProblem: (problem: string) => void }) {
  const [state, setState] = useState<State>({ kind: "idle" });
  const [level, setLevel] = useState(0);
  const [, tick] = useState(0);
  const recorder = useRef<Recorder | null>(null);

  useEffect(() => {
    if (state.kind !== "recording") return;
    const timer = window.setInterval(() => tick((n) => n + 1), 1000);
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== "Escape") return;
      recorder.current?.cancel();
      setState({ kind: "idle" });
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("keydown", onKey);
    };
  }, [state.kind]);
  useEffect(() => () => recorder.current?.cancel(), []);

  const tap = async () => {
    if (state.kind === "idle") {
      const r = new Recorder();
      r.onLevel = setLevel;
      recorder.current = r;
      const problem = await r.start();
      if (problem) return onProblem(problem);
      setState({ kind: "recording", since: Date.now() });
    } else if (state.kind === "recording") {
      const wav = await recorder.current?.finish();
      if (!wav) {
        setState({ kind: "idle" });
        return onProblem("Nothing was recorded.");
      }
      setState({ kind: "writing" });
      const result = await transcribe(wav);
      if (result.text !== undefined) onText(result.text);
      else if (result.problem) onProblem(result.problem);
      setState({ kind: "idle" });
    }
  };

  const help = state.kind === "idle" ? "Speak your message (Celery writes it out)" : state.kind === "recording" ? "Stop and write it out (Escape throws it away)" : "Writing it out…";
  return (
    <button
      className={`am-voice ${state.kind}`}
      style={state.kind === "recording" ? { height: size } : { width: size, height: size }}
      disabled={!enabled || state.kind === "writing"}
      title={help}
      aria-label={help}
      onClick={() => void tap()}
    >
      {state.kind === "idle" && <Icon icon={Mic} sf="mic" size={13} weight="semibold" />}
      {state.kind === "recording" && (
        <>
          <span className="am-voice-halo" style={{ transform: `scale(${1 + level * 0.25})` }} />
          <span className="am-voice-stop" />
          <span className="am-voice-time mono">{elapsed((Date.now() - state.since) / 1000)}</span>
        </>
      )}
      {state.kind === "writing" && <span className="am-spinner" />}
    </button>
  );
}
