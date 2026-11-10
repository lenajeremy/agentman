{
  const PICK = "claude:2f6a0b5c-7d9e-4f1a-8b3c-6e7f8a9b0c1d";
  window.__sent = [];
  window.__asking = true;
  const q = { id: "q-typed", prompt: "Which approach should I take?", options: [{ key: "1", label: "The first" }, { key: "2", label: "The second" }], custom: true };
  const patch = (s) => (s?.id === PICK ? { ...s, question: window.__asking ? q : undefined, state: window.__asking ? "waiting_input" : "idle" } : s);
  const rewrite = (text) => text.split("\n").map((line) => {
    if (!line) return line;
    const env = JSON.parse(line); const p = env.payload;
    if (p?.sessions) p.sessions = p.sessions.map(patch);
    if (p?.session) p.session = patch(p.session);
    return JSON.stringify(env);
  }).join("\n");
  const W = window.WebSocket;
  const Fake = class extends W {
    set onmessage(handler) {
      this.__h = handler; let buffer = "";
      super.onmessage = (e) => { buffer += String(e.data); const end = buffer.lastIndexOf("\n"); if (end < 0) return; const whole = buffer.slice(0, end); buffer = buffer.slice(end + 1); handler(new MessageEvent("message", { data: rewrite(whole) + "\n" })); };
    }
    get onmessage() { return super.onmessage; }
    reply(payload, replyTo) { this.__h(new MessageEvent("message", { data: JSON.stringify({ v: 2, id: "f" + Math.random(), to: "app", from: "daemon", ...(replyTo ? { replyTo } : {}), payload }) + "\n" })); }
    send(d) {
      const env = JSON.parse(String(d).split("\n")[0]); const t = env.payload?.type;
      if (t === "answer_question" || t === "send_message") {
        window.__sent.push(env.payload);
        setTimeout(() => {
          this.reply({ type: "send_result", sessionId: env.payload.sessionId }, env.id);
          if (t === "answer_question") { window.__asking = false; this.reply({ type: "session_update", session: { ...window.__last, question: undefined, state: "idle" } }); }
        }, 300);
        return;
      }
      return super.send(d);
    }
  };
  const remember = (text) => { for (const line of text.split("\n")) { try { const p = JSON.parse(line).payload; for (const s of p?.sessions ?? (p?.session ? [p.session] : [])) if (s.id === PICK) window.__last = s; } catch {} } };
  window.WebSocket = function (u, p) { if (String(u).includes("/app/terminal")) return new W(u, p); const ws = new Fake(u, p); ws.addEventListener("message", (e) => remember(String(e.data))); return ws; };
  window.WebSocket.OPEN = W.OPEN; window.WebSocket.prototype = W.prototype;
}
