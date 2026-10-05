import assert from "node:assert/strict";
import test from "node:test";

import {
  PROTOCOL_VERSION,
  decodeControl,
  decodeDaemonEvent,
  decodeEnvelope,
} from "./protocol.ts";

test("accepts a valid daemon event envelope", () => {
  const raw = JSON.stringify({
    v: PROTOCOL_VERSION,
    id: "frame-1",
    replyTo: "request-1",
    to: "app",
    payload: {
      type: "sessions",
      sessions: [{
        id: "opencode:ses_1",
        kind: "opencode",
        nativeId: "ses_1",
        name: "agentman",
        cwd: "/work/agentman",
        state: "busy",
        inject: "api",
        startedAt: 1,
        lastActivityAt: 2,
      }],
    },
  });
  const envelope = decodeEnvelope(raw);
  assert.ok(envelope);
  assert.ok(decodeDaemonEvent(envelope.payload));
});

test("accepts a read-only Cursor session and tools with unknown outcomes", () => {
  const session = {
    id: "cursor:session-1", kind: "cursor", nativeId: "session-1",
    name: "Investigate build", cwd: "/work/app", state: "waiting_input",
    inject: "none", startedAt: 1, lastActivityAt: 2,
  };
  const update = decodeDaemonEvent({ type: "session_update", session });
  assert.ok(update);
  assert.equal(update.type, "session_update");
  assert.equal(update.session.inject, "none");

  const messages = decodeDaemonEvent({
    type: "messages", sessionId: session.id,
    messages: [{
      id: "o42:tool1", sessionId: session.id, role: "tool", ts: 2,
      tool: { name: "Shell", summary: "pwd" },
    }],
  });
  assert.ok(messages);
  assert.equal(messages.type, "messages");
  assert.equal(messages.messages[0].tool?.status, undefined);
});

test("accepts bounded workspace views and rejects unsafe image payloads", () => {
  assert.ok(decodeDaemonEvent({ type: "workspace", workspace: {
    kind: "directory", sessionId: "codex:one", entries: [{ name: "app", directory: true }],
  } }));
  assert.ok(decodeDaemonEvent({ type: "workspace", workspace: {
    kind: "file", sessionId: "codex:one", path: "icon.png", mime: "image/png", image: "aGVsbG8=",
  } }));
  assert.equal(decodeDaemonEvent({ type: "workspace", workspace: {
    kind: "file", sessionId: "codex:one", mime: "text/html", image: "PHNjcmlwdD4=",
  } }), null);
  assert.equal(decodeDaemonEvent({ type: "workspace", workspace: {
    kind: "changes", sessionId: "codex:one", changes: [{ path: "a", status: 4 }],
  } }), null);
});

test("accepts launch replies and bounds directory listings", () => {
  assert.ok(decodeDaemonEvent({ type: "directories", path: "Desktop",
    directories: ["agentman", "project"] }));
  assert.ok(decodeDaemonEvent({ type: "directories", path: "Desktop/empty" }));
  assert.ok(decodeDaemonEvent({ type: "session_started", sessionId: "claude:abc" }));
  assert.equal(decodeDaemonEvent({ type: "session_started", sessionId: "" }), null);
  assert.equal(decodeDaemonEvent({ type: "directories", directories: [4] }), null);
  assert.equal(decodeDaemonEvent({ type: "directories",
    directories: Array.from({ length: 201 }, (_, index) => `dir-${index}`) }), null);
});

test("rejects malformed websocket envelopes and payloads", () => {
  assert.equal(decodeEnvelope("not json"), null);
  assert.equal(decodeEnvelope(JSON.stringify({
    v: PROTOCOL_VERSION + 1, id: "x", to: "app", payload: {},
  })), null);
  assert.equal(decodeEnvelope(JSON.stringify({
    v: PROTOCOL_VERSION, id: "x", to: "app", payload: null,
  })), null);

  assert.equal(decodeControl(null), null);
  assert.equal(decodeControl({ type: "hello", daemonOnline: "yes" }), null);
  assert.equal(decodeDaemonEvent({ type: "sessions", sessions: "not an array" }), null);
  assert.equal(decodeDaemonEvent({
    type: "messages",
    sessionId: "s",
    messages: [{ id: "m", sessionId: "s", role: "root", ts: 1 }],
  }), null);

  const oldVersionError = decodeEnvelope(JSON.stringify({
    v: PROTOCOL_VERSION - 1,
    id: "upgrade-error",
    replyTo: "list-1",
    to: "relay",
    payload: { type: "error", message: "unsupported protocol version" },
  }));
  assert.ok(oldVersionError, "version negotiation error must remain readable");
  assert.equal(oldVersionError.v, PROTOCOL_VERSION - 1);
});

test("rejects nested fields that could crash rendering", () => {
  assert.equal(decodeDaemonEvent({
    type: "page",
    page: {
      sessionId: "s",
      messages: [{ id: "m", sessionId: "s", role: "assistant", ts: 1, text: { unsafe: true } }],
      hasMore: false,
    },
  }), null);
  assert.equal(decodeDaemonEvent({
    type: "session_update",
    session: {
      id: "s", kind: "claude", nativeId: "n", name: "name", cwd: "/tmp",
      state: "idle", inject: "tmux", startedAt: 1, lastActivityAt: 2,
      question: { prompt: "approve?", options: [{ key: 1, label: "yes" }] },
    },
  }), null);

  const questionSession = {
    id: "s", kind: "claude", nativeId: "n", name: "name", cwd: "/tmp",
    state: "waiting_input", inject: "tmux", startedAt: 1, lastActivityAt: 2,
    question: {
      id: "question-1", prompt: "Which targets?", multiple: true,
      options: [{
        key: "1", label: "API", description: "HTTP service",
        preview: "curl http://localhost", checked: true,
      }],
    },
  };
  assert.ok(decodeDaemonEvent({ type: "session_update", session: questionSession }));
  assert.equal(decodeDaemonEvent({
    type: "session_update",
    session: {
      ...questionSession,
      question: { ...questionSession.question, id: "x".repeat(257) },
    },
  }), null);
  assert.equal(decodeDaemonEvent({
    type: "session_update",
    session: {
      ...questionSession,
      question: { ...questionSession.question, options: [{ key: "1", label: "API", checked: "yes" }] },
    },
  }), null);
  assert.equal(decodeDaemonEvent({
    type: "session_update",
    session: {
      ...questionSession,
      question: {
        ...questionSession.question,
        options: [{ key: "1", label: "API", description: { unsafe: true } }],
      },
    },
  }), null);
  assert.equal(decodeDaemonEvent({
    type: "session_update",
    session: {
      ...questionSession,
      question: {
        ...questionSession.question,
        options: [{ key: "1", label: "API", preview: { unsafe: true } }],
      },
    },
  }), null);
});

const baseSession = {
  id: "claude:s", kind: "claude", nativeId: "s", name: "app", cwd: "/work/app",
  state: "busy", inject: "tmux", startedAt: 1, lastActivityAt: 2,
};

test("accepts sessions with servers and rejects unsafe preview links", () => {
  const withServers = {
    ...baseSession,
    servers: [
      { port: 5173, command: "node", title: "Vite App" },
      { port: 3000, link: "https://abc.agentman.online" },
    ],
  };
  assert.ok(decodeDaemonEvent({ type: "session_update", session: withServers }));

  for (const link of ["javascript:alert(1)", "agentman://pair?token=x", "https://a b"]) {
    assert.equal(decodeDaemonEvent({
      type: "session_update",
      session: { ...baseSession, servers: [{ port: 3000, link }] },
    }), null, link);
  }
  assert.equal(decodeDaemonEvent({
    type: "session_update",
    session: { ...baseSession, servers: [{ port: 70000 }] },
  }), null);
});

test("decodes server_opened only with a port and an http(s) link", () => {
  assert.ok(decodeDaemonEvent({
    type: "server_opened", sessionId: "claude:s", port: 5173, link: "https://x.agentman.online",
  }));
  assert.equal(decodeDaemonEvent({
    type: "server_opened", sessionId: "claude:s", port: 5173, link: "javascript:alert(1)",
  }), null);
  assert.equal(decodeDaemonEvent({ type: "server_opened", sessionId: "claude:s", port: 5173 }), null);
});

test("an agent this app does not know yet still loads", () => {
  // A newer daemon adding Gemini must not blank the whole session list.
  assert.ok(decodeDaemonEvent({
    type: "sessions", sessions: [baseSession, { ...baseSession, id: "gemini:g", kind: "gemini" }],
  }));
});

test("an older Mac's empty session lists are read as empty, not dropped", () => {
  // Daemons before the fix left an empty list out entirely. Dropping that
  // frame kept every ended session on the board.
  const board = decodeDaemonEvent({ type: "sessions" });
  assert.ok(board);
  assert.equal(board.type, "sessions");
  assert.deepEqual(board.sessions, []);

  const folder = decodeDaemonEvent({ type: "directory_sessions", path: "/work/empty" });
  assert.ok(folder);
  assert.equal(folder.type, "directory_sessions");
  assert.deepEqual(folder.sessions, []);
});

test("a question whose options were withdrawn keeps the rest of the board", () => {
  // An older Mac sends options:null for a question it can only show, not let
  // the phone answer. Rejecting it took the whole session list with it.
  const sessions = [
    {
      id: "opencode:ses_1", kind: "opencode", nativeId: "ses_1", name: "n", cwd: "/tmp",
      state: "waiting_input", inject: "api", startedAt: 1, lastActivityAt: 2,
      question: {
        id: "question-q1-0", prompt: "Pick one",
        detail: "Answer it in the terminal.", options: null,
      },
    },
    {
      id: "claude:abc", kind: "claude", nativeId: "abc", name: "other", cwd: "/tmp",
      state: "idle", inject: "tmux", startedAt: 1, lastActivityAt: 2,
    },
  ];
  const board = decodeDaemonEvent({ type: "sessions", sessions });
  assert.ok(board);
  assert.equal(board.type, "sessions");
  assert.equal(board.sessions.length, 2);
  assert.deepEqual(board.sessions[0].question?.options, []);
});

test("accepts a session's mode, context and artifact counts, and bounds them", () => {
  const status = { ...baseSession, mode: "plan", contextPercent: 42, artifacts: 3, artifactsToReview: 1 };
  const update = decodeDaemonEvent({ type: "session_update", session: status });
  assert.ok(update);
  assert.equal(update.session?.mode, "plan");
  assert.equal(update.session?.contextPercent, 42);

  for (const bad of [
    { mode: 7 },
    { mode: "x".repeat(257) },
    { contextPercent: 101 },
    { contextPercent: -1 },
    { contextPercent: 4.5 },
    { contextPercent: "42" },
    { artifacts: -1 },
    { artifacts: 1.5 },
    { artifactsToReview: "1" },
  ]) {
    assert.equal(decodeDaemonEvent({ type: "session_update", session: { ...baseSession, ...bad } }), null,
      JSON.stringify(bad));
  }
});

const plan = {
  name: "implementation_plan.md", kind: "plan", title: "Implementation plan",
  summary: "Three steps", updatedAt: 1_790_000_000_000, size: 2048, mime: "text/markdown", review: true,
};

test("decodes an artifact list, including an empty or missing one", () => {
  const listed = decodeDaemonEvent({ type: "artifacts", sessionId: "antigravity:c1", artifacts: [plan] });
  assert.ok(listed);
  assert.equal(listed.artifacts?.[0].name, "implementation_plan.md");
  assert.ok(decodeDaemonEvent({ type: "artifacts", sessionId: "antigravity:c1", artifacts: [] }));
  assert.ok(decodeDaemonEvent({ type: "artifacts", sessionId: "antigravity:c1" }));
  // An agent this app has never heard of may invent a kind; it still lists.
  assert.ok(decodeDaemonEvent({
    type: "artifacts", sessionId: "antigravity:c1", artifacts: [{ ...plan, kind: "recording" }],
  }));
  assert.equal(decodeDaemonEvent({ type: "artifacts", artifacts: [plan] }), null);
  assert.equal(decodeDaemonEvent({ type: "artifacts", sessionId: "antigravity:c1", artifacts: plan }), null);
});

test("refuses an artifact whose name could leave its folder", () => {
  for (const name of [
    "../secrets.md", "plans/implementation_plan.md", "/etc/passwd", "..", "a..b.md",
    "plan\\..\\x.md", "", "x".repeat(256), "plan\u0000.md", "plan\n.md",
  ]) {
    assert.equal(decodeDaemonEvent({
      type: "artifacts", sessionId: "antigravity:c1", artifacts: [{ ...plan, name }],
    }), null, JSON.stringify(name));
  }
});

test("refuses malformed artifact fields and oversized lists", () => {
  for (const bad of [
    { kind: "" }, { kind: 3 }, { kind: "k".repeat(65) },
    { title: "t".repeat(2049) }, { summary: { text: "x" } },
    { updatedAt: "yesterday" }, { updatedAt: -1 }, { size: -1 }, { size: Infinity },
    { mime: "m".repeat(129) }, { review: "yes" },
  ]) {
    assert.equal(decodeDaemonEvent({
      type: "artifacts", sessionId: "antigravity:c1", artifacts: [{ ...plan, ...bad }],
    }), null, JSON.stringify(bad));
  }
  assert.equal(decodeDaemonEvent({
    type: "artifacts", sessionId: "antigravity:c1",
    artifacts: Array.from({ length: 501 }, (_, index) => ({ ...plan, name: `plan-${index}.md` })),
  }), null);
});

test("accepts an artifact read answered as a workspace view", () => {
  assert.ok(decodeDaemonEvent({ type: "workspace", workspace: {
    kind: "artifact", sessionId: "antigravity:c1", path: "implementation_plan.md",
    mime: "text/markdown", text: "# Plan",
  } }));
  assert.ok(decodeDaemonEvent({ type: "workspace", workspace: {
    kind: "artifact", sessionId: "antigravity:c1", path: "screenshot.png", mime: "image/png", image: "aGVsbG8=",
  } }));
  assert.equal(decodeDaemonEvent({ type: "workspace", workspace: {
    kind: "artifact", sessionId: "antigravity:c1", mime: "text/html", image: "PHNjcmlwdD4=",
  } }), null);
});
