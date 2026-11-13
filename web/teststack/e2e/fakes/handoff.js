{
  const DEMO = "claude:6f1c2a9e-3b7d-4c55-9a10-2d8e4f6b7c01", BUGS = "claude:0b9d4e21-7c3a-4f6e-8d12-5a6b7c8d9e10";
  window.__handoffs = [];
  let moved = false;
  const patch = (s) => {
    if (s?.id === DEMO && !moved) return { ...s, inject: "api", handoff: "terminal" };
    if (s?.id === BUGS) return { ...s, inject: "none", handoff: "agentman", state: "idle" };
    return s;
  };
  let lastDemo = null;
  const rewrite = (text) => text.split("\n").map((line) => {
    if (!line) return line;
    const env = JSON.parse(line);
    const p = env.payload;
    if (p?.sessions) p.sessions = p.sessions.map((s) => (s.id === DEMO ? (lastDemo = s) : s)).map(patch);
    if (p?.session) { if (p.session.id === DEMO) lastDemo = p.session; p.session = patch(p.session); }
    return JSON.stringify(env);
  }).join("\n");
  const W = window.WebSocket;
  const Fake = class extends W {
    set onmessage(handler) {
      this.__handler = handler;
      let buffer = "";
      super.onmessage = (e) => {
        buffer += String(e.data);
        const end = buffer.lastIndexOf("\n");
        if (end < 0) return;
        const whole = buffer.slice(0, end);
        buffer = buffer.slice(end + 1);
        handler(new MessageEvent("message", { data: rewrite(whole) + "\n" }));
      };
    }
    get onmessage() { return super.onmessage; }
    send(data) {
      const env = JSON.parse(String(data).split("\n")[0]);
      if (env.payload?.type === "handoff_session") {
        window.__handoffs.push(env.payload.sessionId);
        const reply = (payload, replyTo) => this.__handler(new MessageEvent("message", { data: JSON.stringify({ v: 2, id: "fake-" + Math.random(), to: "app", from: "daemon", ...(replyTo ? { replyTo } : {}), payload }) + "\n" }));
        setTimeout(() => {
          if (window.__refuse) { reply({ type: "error", error: "daemon: it's in the middle of a turn; try again when it stops" }, env.id); return; }
          reply({ type: "session_update", sessionId: env.payload.sessionId }, env.id);
          if (env.payload.sessionId === DEMO) {
            moved = true;
            setTimeout(() => { window.__arrived = { had: !!lastDemo, id: lastDemo?.id }; reply({ type: "session_update", session: { ...lastDemo, inject: "tmux", handoff: "agentman" } }); }, 600);
          }
        }, 300);
        return;
      }
      return super.send(data);
    }
  };
  // Only the app's own connection: a terminal's carries its output, not JSON.
  window.WebSocket = function (url, protocols) {
    return String(url).includes("/app/terminal") ? new W(url, protocols) : new Fake(url, protocols);
  };
  window.WebSocket.OPEN = W.OPEN; window.WebSocket.CLOSED = W.CLOSED; window.WebSocket.CONNECTING = W.CONNECTING; window.WebSocket.prototype = W.prototype;
}
