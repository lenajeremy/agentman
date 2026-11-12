// No agents and nothing said to Agentman yet: every list of sessions comes
// empty, no session arrives on its own, and Agentman's history is empty, so
// the empty sidebar and Agentman's first page show. Nothing sent changes.
{
  const rewrite = (text) => text.split("\n").map((line) => {
    if (!line) return line;
    const env = JSON.parse(line); const p = env.payload;
    if (p?.type === "session_update") return "";
    if (Array.isArray(p?.sessions)) p.sessions = [];
    if (Array.isArray(p?.entries)) { p.entries = []; p.more = false; }
    if (p?.type === "manager_entry") return "";
    return JSON.stringify(env);
  }).filter(Boolean).join("\n");
  const W = window.WebSocket;
  const Fake = class extends W {
    set onmessage(h) { let b = ""; super.onmessage = (e) => { b += String(e.data); const end = b.lastIndexOf("\n"); if (end < 0) return; const whole = rewrite(b.slice(0, end)); b = b.slice(end + 1); if (whole) h(new MessageEvent("message", { data: whole + "\n" })); }; }
    get onmessage() { return super.onmessage; }
  };
  window.WebSocket = function (u, p) { return String(u).includes("/app/terminal") ? new W(u, p) : new Fake(u, p); };
  window.WebSocket.OPEN = W.OPEN; window.WebSocket.prototype = W.prototype;
}
