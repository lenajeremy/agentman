import assert from "node:assert/strict";
import test from "node:test";

import { decodeControl, decodeDaemonEvent, decodeEnvelope } from "./protocol.ts";

test("accepts a valid daemon event envelope", () => {
  const raw = JSON.stringify({
    v: 1,
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

test("rejects malformed websocket envelopes and payloads", () => {
  assert.equal(decodeEnvelope("not json"), null);
  assert.equal(decodeEnvelope(JSON.stringify({ v: 99, id: "x", to: "app", payload: {} })), null);
  assert.equal(decodeEnvelope(JSON.stringify({ v: 1, id: "x", to: "app", payload: null })), null);

  assert.equal(decodeControl(null), null);
  assert.equal(decodeControl({ type: "hello", daemonOnline: "yes" }), null);
  assert.equal(decodeDaemonEvent({ type: "sessions", sessions: "not an array" }), null);
  assert.equal(decodeDaemonEvent({
    type: "messages",
    sessionId: "s",
    messages: [{ id: "m", sessionId: "s", role: "root", ts: 1 }],
  }), null);
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
      prompt: "Which targets?", multiple: true,
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
