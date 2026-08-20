import assert from "node:assert/strict";
import test from "node:test";

import {
  DEFAULT_RELAY,
  normalizeCredentials,
  normalizeRelayUrl,
  parsePairingPayload,
} from "./pairing.ts";

test("hostnames default to HTTPS and websocket schemes canonicalize", () => {
  assert.equal(normalizeRelayUrl("relay.example.com/"), "https://relay.example.com");
  assert.equal(normalizeRelayUrl("wss://relay.example.com/base/"), "https://relay.example.com/base");
});

test("cleartext is limited to actual loopback hosts", () => {
  assert.equal(normalizeRelayUrl("http://localhost:8787"), "http://localhost:8787");
  assert.equal(normalizeRelayUrl("ws://127.8.9.10:8787"), "http://127.8.9.10:8787");
  assert.equal(normalizeRelayUrl("http://[::1]:8787"), "http://[::1]:8787");
  assert.throws(() => normalizeRelayUrl("http://relay.example.com"), /HTTPS is required/);
  assert.throws(() => normalizeRelayUrl("http://192.168.1.10:8787"), /HTTPS is required/);
});

test("relay bases reject credential and routing confusion", () => {
  assert.throws(() => normalizeRelayUrl("https://user:pass@relay.example.com"), /cannot contain/);
  assert.throws(() => normalizeRelayUrl("https://relay.example.com?next=evil"), /cannot contain/);
  assert.throws(() => normalizeRelayUrl("ftp://relay.example.com"), /must use HTTPS/);
});

test("pairing payload matches the exact deep-link route", () => {
  assert.deepEqual(parsePairingPayload("agentman://pair?token=once"), {
    relayUrl: DEFAULT_RELAY,
    token: "once",
  });
  assert.deepEqual(
    parsePairingPayload(
      "agentman://pair?relay=http%3A%2F%2Flocalhost%3A8787&token=local",
    ),
    { relayUrl: "http://localhost:8787", token: "local" },
  );
  assert.equal(parsePairingPayload("agentman://pairing?token=once"), null);
  assert.equal(
    parsePairingPayload("agentman://pair?relay=http%3A%2F%2Fexample.com&token=once"),
    null,
  );
});

test("stored credentials are normalized and insecure legacy values are rejected", () => {
  assert.deepEqual(normalizeCredentials({ relayUrl: "relay.example.com/", token: "device" }), {
    relayUrl: "https://relay.example.com",
    token: "device",
  });
  assert.equal(
    normalizeCredentials({ relayUrl: "http://relay.example.com", token: "device" }),
    null,
  );
  assert.equal(normalizeCredentials({ relayUrl: "https://relay.example.com", token: "" }), null);
  assert.equal(
    normalizeCredentials({ relayUrl: "https://relay.example.com", token: "bad\r\nheader" }),
    null,
  );
  assert.equal(parsePairingPayload("agentman://pair?token=bad%0Aheader"), null);
});
