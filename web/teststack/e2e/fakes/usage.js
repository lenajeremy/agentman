{
  const day = 86400000;
  const start = new Date(); start.setHours(0, 0, 0, 0);
  const buckets = [];
  for (let i = 29; i >= 0; i--) {
    const c = 0.4 + Math.abs(Math.sin(i / 3)) * 2.2, x = 0.1 + Math.abs(Math.cos(i / 4)) * 0.8;
    buckets.push({ start: start.getTime() - i * day, cost: { claude: c, codex: x }, tokens: { claude: Math.round(c * 1.6e6), codex: Math.round(x * 1.1e6) } });
  }
  const usage = {
    days: 30, unit: "day", sessions: 42,
    tokens: { input: 1210000, cacheRead: 48200000, cacheWrite: 3100000, output: 910000 },
    cost: { input: 3.63, cacheRead: 14.46, cacheWrite: 11.62, output: 13.65 },
    cacheSavings: 130.14,
    agents: [
      { kind: "claude", sessions: 30, cost: 33.1, tokens: { input: 900000, cacheRead: 40100000, cacheWrite: 2700000, output: 700000 } },
      { kind: "codex", sessions: 11, cost: 10.26, tokens: { input: 310000, cacheRead: 8100000, cacheWrite: 400000, output: 210000 } },
      { kind: "kiro", sessions: 1, cost: 0, credits: 6.57, tokens: { input: 0, cacheRead: 0, cacheWrite: 0, output: 0 } },
    ],
    buckets,
    models: [
      { model: "claude-opus-5-5", kind: "claude", priced: true, cost: 27.4, tokens: { input: 700000, cacheRead: 32000000, cacheWrite: 2200000, output: 560000 }, fast: { cost: 3.1, tokens: { input: 1, cacheRead: 0, cacheWrite: 0, output: 1 } } },
      { model: "gpt-6-codex", kind: "codex", priced: true, cost: 10.26, tokens: { input: 310000, cacheRead: 8100000, cacheWrite: 400000, output: 210000 } },
      { model: "claude-haiku-5-5", kind: "claude", priced: true, cost: 5.7, tokens: { input: 200000, cacheRead: 8100000, cacheWrite: 500000, output: 140000 } },
    ],
  };
  const now = Date.now();
  const limits = { agents: [
    { kind: "claude", plan: "Max", checkedAt: now - 3 * 60000, windows: [
      { id: "s", kind: "session", label: "Session", used: 62, resetsAt: now + 2 * 3600000 + 12 * 60000, minutes: 300 },
      { id: "w", kind: "weekly", label: "Weekly", used: 35, resetsAt: now + 3 * day + 4 * 3600000, minutes: 10080 },
    ] },
    { kind: "codex", plan: "Plus", checkedAt: now - 25 * 60000, lastTurn: true, windows: [
      { id: "s", kind: "session", label: "Session", used: 100, resetsAt: now + 40 * 60000, minutes: 300 },
    ] },
  ] };
  const W = window.WebSocket;
  window.WebSocket = class extends W {
    send(data) {
      const env = JSON.parse(String(data).split("\n")[0]);
      const type = env.payload?.type;
      if (type === "usage" || type === "limits") {
        const payload = type === "usage" ? { type, usage: { ...usage, days: env.payload.days } } : { type, limits };
        setTimeout(() => this.onmessage?.(new MessageEvent("message", { data: JSON.stringify({ v: 2, id: "fake-" + env.id, to: "app", from: "daemon", replyTo: env.id, payload }) + "\n" })), 20);
        return;
      }
      return super.send(data);
    }
  };
}
