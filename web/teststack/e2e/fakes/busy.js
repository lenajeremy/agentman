{
  const DEMO = "claude:6f1c2a9e-3b7d-4c55-9a10-2d8e4f6b7c01";
  const patch = (s) => (s?.id === DEMO ? { ...s, state: "busy" } : s);
  const rewrite = (text) => text.split("\n").map((line) => {
    if (!line) return line;
    const env = JSON.parse(line);
    const p = env.payload;
    if (p?.sessions) p.sessions = p.sessions.map(patch);
    if (p?.session) p.session = patch(p.session);
    return JSON.stringify(env);
  }).join("\n");
  const W = window.WebSocket;
  const Fake = class extends W {
    set onmessage(handler) {
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
  };
  window.WebSocket = function (url, protocols) {
    return String(url).includes("/app/terminal") ? new W(url, protocols) : new Fake(url, protocols);
  };
  window.WebSocket.OPEN = W.OPEN; window.WebSocket.CLOSED = W.CLOSED; window.WebSocket.CONNECTING = W.CONNECTING; window.WebSocket.prototype = W.prototype;
}
