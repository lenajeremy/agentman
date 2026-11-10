{
  const BUGS = "claude:0b9d4e21-7c3a-4f6e-8d12-5a6b7c8d9e10";
  window.__sentCards = [];
  const card = { id: "c-offer", kind: "offer", state: "open", sessionId: BUGS, agentName: "bugs", title: "bugs has been stuck on the same error for 20 minutes", detail: "It keeps retrying the same failing test. I can tell it to try something else, or stop it and ask what it needs.", questionId: "q-offer", options: [{ key: "nudge", label: "Nudge it" }, { key: "ask", label: "Interrupt and ask" }] };
  const entry = (c) => ({ id: "e-offer", role: "card", text: "", ts: Date.now(), sessionId: BUGS, card: c });
  const W = window.WebSocket;
  const Fake = class extends W {
    set onmessage(h) { this.__h = h; super.onmessage = h; }
    get onmessage() { return super.onmessage; }
    emit(payload, replyTo) { this.__h(new MessageEvent("message", { data: JSON.stringify({ v: 2, id: "f" + Math.random(), to: "app", from: "daemon", ...(replyTo ? { replyTo } : {}), payload }) + "\n" })); }
    send(d) {
      const env = JSON.parse(String(d).split("\n")[0]); const t = env.payload?.type;
      if (t === "manager_card_reply" || t === "manager_card_dismiss") {
        window.__sentCards.push(env.payload);
        setTimeout(() => {
          this.emit({ type: "manager_entry", entry: entry({ ...card, state: t === "manager_card_reply" ? "answered" : "dismissed", outcome: "Told it to try something else" }) }, env.id);
        }, 200);
        return;
      }
      return super.send(d);
    }
  };
  window.WebSocket = function (u, p) {
    if (String(u).includes("/app/terminal")) return new W(u, p);
    const ws = new Fake(u, p);
    ws.addEventListener("open", () => setTimeout(() => ws.emit({ type: "manager_entry", entry: entry(card) }), 1500));
    return ws;
  };
  window.WebSocket.OPEN = W.OPEN; window.WebSocket.prototype = W.prototype;
}
