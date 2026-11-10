{
  const frames = [
    { type: "manager_missions", missions: [{ id: "m1", goal: "Ship transcript search on every app", state: "running", createdAt: 1, tasks: [
      { id: "t1", title: "Index the transcripts", agent: "claude", state: "done" },
      { id: "t2", title: "Search from the sidebar", agent: "claude", state: "running", sessionId: "claude:6f1c2a9e-3b7d-4c55-9a10-2d8e4f6b7c01" },
      { id: "t3", title: "Phone search page", agent: "codex", state: "planned" },
    ] }] },
    { type: "manager_verification", verifications: [
      { sessionId: "claude:1e5f9a4b-6c8d-4e0f-9a2b-5d6e7f8a9b0c", state: "passed", summary: "All 214 tests pass", updatedAt: 1 },
      { sessionId: "claude:7a1e5c0b-2d4f-4e6a-9b8c-1f2e3d4c5b6a", state: "concerns", summary: "Something looks off", issues: ["The dash rule also changes `--flag` in code spans"], sentBack: true, updatedAt: 1 },
    ] },
  ];
  const W = window.WebSocket;
  window.WebSocket = class extends W {
    constructor(...a) {
      super(...a);
      this.addEventListener("open", () => setTimeout(() => {
        const data = frames.map((payload, i) => JSON.stringify({ v: 2, id: "fake-" + i, to: "app", from: "daemon", payload })).join("\n") + "\n";
        this.onmessage?.(new MessageEvent("message", { data }));
      }, 2500));
    }
  };
}
