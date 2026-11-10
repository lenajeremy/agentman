{
  const now = Date.now(), day = 86400000;
  const list = [
    { id: "dev-1", name: "Ada's iPhone", model: "iPhone 17 Pro", platform: "ios", os: "26.1", firstSeen: now - 9 * day, lastSeen: now, connected: true },
    { id: "dev-2", model: "iPad Air", platform: "ios", firstSeen: now - 30 * day, lastSeen: now - 3 * day },
    { id: "dev-3", platform: "android", firstSeen: now - 2 * day, lastSeen: now - 5 * 60000 },
  ];
  window.__pairCodes = 0;
  const W = window.WebSocket;
  const Fake = class extends W {
    set onmessage(handler) { this.__handler = handler; super.onmessage = handler; }
    get onmessage() { return super.onmessage; }
    reply(payload, replyTo) {
      this.__handler(new MessageEvent("message", { data: JSON.stringify({ v: 2, id: "fake-" + Math.random(), to: "app", from: "daemon", ...(replyTo ? { replyTo } : {}), payload }) + "\n" }));
    }
    send(data) {
      const env = JSON.parse(String(data).split("\n")[0]);
      const t = env.payload?.type;
      if (t === "devices") { setTimeout(() => this.reply({ type: "devices", devices: list }, env.id), 50); return; }
      if (t === "pair_code") {
        const n = ++window.__pairCodes;
        setTimeout(() => this.reply({ type: "pair_code", pairCode: { code: n === 1 ? "0123456789" : "9876543210", url: "agentman://pair?token=fake-token-" + n + "-abcdefghijklmnopqrstuvwxyz", expiresAt: Date.now() + (window.__codeLife ?? 60000) } }, env.id), 200);
        return;
      }
      return super.send(data);
    }
  };
  window.__connectPhone = () => {
    const sock = window.__sock;
    sock.reply({ type: "devices", devices: [...list, { id: "dev-4", name: "Grace's Pixel", model: "Pixel 9", platform: "android", firstSeen: Date.now(), lastSeen: Date.now(), connected: true }] });
  };
  window.WebSocket = function (url, protocols) {
    if (String(url).includes("/app/terminal")) return new W(url, protocols);
    return (window.__sock = new Fake(url, protocols));
  };
  window.WebSocket.OPEN = W.OPEN; window.WebSocket.CLOSED = W.CLOSED; window.WebSocket.CONNECTING = W.CONNECTING; window.WebSocket.prototype = W.prototype;
}
