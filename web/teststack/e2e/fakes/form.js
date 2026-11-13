// pick asks window.__q (set before this runs), a question of any shape: a
// form of several, a review, ticks. What the page asks of it is recorded in
// window.__sent; am serve's side is played here: move_question shows the
// step asked for (as window.__moved(step) makes it, else the same question
// on that step), close_question and answer_question take the question away.
{
  const PICK = "claude:2f6a0b5c-7d9e-4f1a-8b3c-6e7f8a9b0c1d";
  window.__sent = [];
  const patch = (s) => (s?.id === PICK ? { ...s, question: window.__q ?? undefined, state: window.__q ? "waiting_input" : "idle" } : s);
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
      const env = JSON.parse(String(d).split("\n")[0]); const p = env.payload; const t = p?.type;
      if (p?.sessionId === PICK && (t === "answer_question" || t === "move_question" || t === "close_question" || t === "send_message")) {
        window.__sent.push(p);
        setTimeout(() => {
          if (window.__refuse) {
            this.reply({ type: "send_result", sessionId: p.sessionId, status: "failed", error: window.__refuse }, env.id);
            return;
          }
          this.reply({ type: "send_result", sessionId: p.sessionId }, env.id);
          if (t === "move_question") {
            const q = window.__q;
            window.__q = window.__moved ? window.__moved(p.step) : { ...q, id: `${q.id.split("@")[0]}@${p.step}`, step: p.step };
          } else if (t !== "send_message") {
            window.__q = null;
          }
          // pick as last seen, with the question now asked; never a
          // session it hasn't seen.
          if (window.__last) this.reply({ type: "session_update", session: patch(window.__last) });
        }, window.__delay ?? 300);
        return;
      }
      return super.send(d);
    }
  };
  // Whole lines only: a frame can split one across two messages.
  const remember = (text) => { for (const line of text.split("\n")) { try { const p = JSON.parse(line).payload; for (const s of p?.sessions ?? (p?.session ? [p.session] : [])) if (s.id === PICK) window.__last = s; } catch {} } };
  window.WebSocket = function (u, p) {
    if (String(u).includes("/app/terminal")) return new W(u, p);
    const ws = new Fake(u, p);
    let buffer = "";
    ws.addEventListener("message", (e) => { buffer += String(e.data); const end = buffer.lastIndexOf("\n"); if (end < 0) return; remember(buffer.slice(0, end)); buffer = buffer.slice(end + 1); });
    return ws;
  };
  window.WebSocket.OPEN = W.OPEN; window.WebSocket.prototype = W.prototype;
}
