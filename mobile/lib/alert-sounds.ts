// Agentman's own alert sounds: the ping and the chime from the launch video.
//
// The files live in assets/sounds and the expo-notifications plugin copies
// them into the app (see app.json). iOS only plays a custom sound that ships
// inside the app, and when it does, it plays it for a push that arrives while
// the app is closed too. The daemon names the same files in the pushes it
// sends (internal/push), so an alert sounds the same whichever side raised it.
export const ALERT_SOUND_NEEDS_YOU = "agentman-needs-you.wav";
export const ALERT_SOUND_FINISHED = "agentman-finished.wav";
