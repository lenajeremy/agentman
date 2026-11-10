// Agentman without a key, though the test daemon has a fake one in its
// environment: its settings and status say none is set. Set before the
// page's scripts: window.__gemini = true for Gemini as its provider.
// Nothing here saves a key: on a Mac that's the login Keychain.
{
  const patch = (p) => {
    if (p?.managerSettings) p.managerSettings = { ...p.managerSettings, keySet: false, ...(window.__gemini ? { provider: "gemini" } : {}) };
    if (p?.managerStatus) p.managerStatus = { ...p.managerStatus, keySet: false, state: "needs_key" };
    return p;
  };
  const rewrite = (text) => text.split("\n").map((line) => {
    if (!line) return line;
    const env = JSON.parse(line);
    env.payload = patch(env.payload);
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
    send(data) {
      // Never a key saved from a test page.
      if (String(data).includes('"key_set"')) { window.__keySetTried = true; return; }
      return super.send(data);
    }
  };
  window.WebSocket = function (url, protocols) {
    return String(url).includes("/app/terminal") ? new W(url, protocols) : new Fake(url, protocols);
  };
  window.WebSocket.OPEN = W.OPEN; window.WebSocket.CLOSED = W.CLOSED; window.WebSocket.CONNECTING = W.CONNECTING; window.WebSocket.prototype = W.prototype;
}
