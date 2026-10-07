import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { ALERT_SOUND_FINISHED, ALERT_SOUND_NEEDS_YOU } from "./alert-sounds.ts";

// A push names its sound by file name, and iOS plays the default sound,
// silently, when the app holds no file of that name. So a typo on either side
// would cost the custom sound without any error. These hold the three places
// that name the files to each other: the app, the build config and the daemon.
const sounds = [ALERT_SOUND_NEEDS_YOU, ALERT_SOUND_FINISHED];

test("the build bundles every alert sound the app plays", () => {
  const app = JSON.parse(readFileSync(new URL("../app.json", import.meta.url), "utf8"));
  const plugin = app.expo.plugins.find((p: unknown) => Array.isArray(p) && p[0] === "expo-notifications");
  assert.ok(plugin, "app.json does not configure the expo-notifications plugin");
  assert.deepEqual(plugin[1].sounds, sounds.map((name) => `./assets/sounds/${name}`));
});

test("each sound is one iOS will play: a WAV of plain PCM, under 30 seconds", () => {
  for (const name of sounds) {
    const wav = readFileSync(new URL(`../assets/sounds/${name}`, import.meta.url));
    assert.equal(wav.toString("ascii", 0, 4), "RIFF", `${name} is not a WAV`);
    assert.equal(wav.toString("ascii", 8, 12), "WAVE", `${name} is not a WAV`);
    assert.equal(wav.toString("ascii", 12, 16), "fmt ", `${name} has no format chunk first`);
    assert.equal(wav.readUInt16LE(20), 1, `${name} is not linear PCM`);
    const byteRate = wav.readUInt32LE(28);
    const seconds = (wav.length - 44) / byteRate;
    assert.ok(seconds > 0 && seconds < 30, `${name} lasts ${seconds}s; iOS plays the default sound past 30s`);
  }
});

test("the daemon's pushes name the same files", () => {
  const push = readFileSync(new URL("../../internal/push/push.go", import.meta.url), "utf8");
  assert.match(push, new RegExp(`SoundNeedsYou = "${ALERT_SOUND_NEEDS_YOU}"`));
  assert.match(push, new RegExp(`SoundFinished = "${ALERT_SOUND_FINISHED}"`));
});
