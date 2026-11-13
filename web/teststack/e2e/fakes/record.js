// Notes every request the page sends am serve, in window.__requests, and
// lets each through: for checking what a button asked for.
{
  window.__requests = [];
  const send = WebSocket.prototype.send;
  WebSocket.prototype.send = function (data) {
    try {
      const env = JSON.parse(String(data).split("\n")[0]);
      if (env?.payload?.type) window.__requests.push(env.payload);
    } catch {
      // A terminal's keystrokes, not a request.
    }
    return send.call(this, data);
  };
}
