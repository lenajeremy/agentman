// The app's connection to am serve, cut without stopping am serve:
// window.__net = "hang" makes new connections never open (connecting),
// "down" makes them fail at once (offline), "" lets them through again;
// window.__cut() closes the one open now. Load after other fakes.
{
  const Prev = window.WebSocket;
  const open = [];
  window.__net = window.__net ?? "";
  window.__cut = () => { for (const ws of open.splice(0)) ws.close(); };
  window.WebSocket = function (url, protocols) {
    if (String(url).includes("/app/terminal")) return new Prev(url, protocols);
    if (window.__net === "hang") return { readyState: 0, send() {}, close() {} };
    if (window.__net === "down") return new Prev("ws://127.0.0.1:9/", protocols);
    const ws = new Prev(url, protocols);
    open.push(ws);
    return ws;
  };
  window.WebSocket.OPEN = Prev.OPEN; window.WebSocket.CLOSED = Prev.CLOSED; window.WebSocket.CONNECTING = Prev.CONNECTING; window.WebSocket.prototype = Prev.prototype;
}
