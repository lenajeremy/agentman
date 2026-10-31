import Feather from "@expo/vector-icons/Feather";
import {
  AudioQuality,
  IOSOutputFormat,
  type RecordingOptions,
  requestRecordingPermissionsAsync,
  setAudioModeAsync,
  useAudioRecorder,
  useAudioRecorderState,
} from "expo-audio";
import { File } from "expo-file-system";
import { useState } from "react";
import { ActivityIndicator, Platform, StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { newFrameId } from "../lib/id";
import { useStore } from "../lib/store";
import { font, Palette, radius, scaled } from "../lib/theme";
import { transcribe } from "../lib/voice";
import { MotionPressable } from "./MotionPressable";

/** What Celery takes: 16 kHz mono 16-bit WAV. */
const VOICE: RecordingOptions = {
  extension: ".wav",
  sampleRate: 16000,
  numberOfChannels: 1,
  bitRate: 256000,
  android: { extension: ".wav", outputFormat: "default", audioEncoder: "default" },
  ios: {
    extension: ".wav",
    outputFormat: IOSOutputFormat.LINEARPCM,
    audioQuality: AudioQuality.HIGH,
    linearPCMBitDepth: 16,
    linearPCMIsBigEndian: false,
    linearPCMIsFloat: false,
  },
  web: {},
};

/**
 * The mic beside a message box: tap to speak, tap again to have it written
 * out. The Mac has Celery write it, on the user's own sign-in, and the words
 * land in the box to read over before sending. Not on the web, where the
 * browser records in a format Celery doesn't take.
 */
export function VoiceButton({
  disabled,
  onText,
  onProblem,
}: {
  disabled?: boolean;
  onText: (text: string) => void;
  onProblem: (message: string) => void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const store = useStore();
  const recorder = useAudioRecorder(VOICE);
  const state = useAudioRecorderState(recorder, 250);
  const [phase, setPhase] = useState<"idle" | "recording" | "writing">("idle");

  if (Platform.OS === "web") return null;

  async function start() {
    try {
      const permission = await requestRecordingPermissionsAsync();
      if (!permission.granted) {
        onProblem("Allow the microphone for Agentman in Settings to speak your messages.");
        return;
      }
      await setAudioModeAsync({ allowsRecording: true, playsInSilentMode: true });
      await recorder.prepareToRecordAsync();
      recorder.record();
      setPhase("recording");
    } catch (error) {
      onProblem(error instanceof Error ? error.message : "The microphone didn't start.");
    }
  }

  async function finish() {
    setPhase("writing");
    try {
      await recorder.stop();
      await setAudioModeAsync({ allowsRecording: false, playsInSilentMode: true });
      const uri = recorder.uri;
      if (!uri) throw new Error("Nothing was recorded.");
      const file = new File(uri);
      const base64 = await file.base64();
      try {
        file.delete();
      } catch {
        // A recording left in the cache is cleared with it.
      }
      onText(await transcribe(store.ask, base64, newFrameId()));
    } catch (error) {
      onProblem(error instanceof Error ? error.message : "That couldn't be written out.");
    } finally {
      setPhase("idle");
    }
  }

  const seconds = Math.floor((state.durationMillis ?? 0) / 1000);
  return (
    <MotionPressable
      onPress={() => void (phase === "idle" ? start() : phase === "recording" ? finish() : undefined)}
      disabled={disabled || phase === "writing"}
      style={[styles.button, phase === "recording" && styles.recording]}
      pressedScale={0.92}
      hitSlop={6}
      accessibilityRole="button"
      accessibilityLabel={phase === "recording" ? "Stop and write it out" : phase === "writing" ? "Writing it out" : "Speak your message"}
    >
      {phase === "recording" ? (
        <View style={styles.row}>
          <View style={styles.stop} />
          <Text style={styles.time}>{`${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`}</Text>
        </View>
      ) : phase === "writing" ? (
        <ActivityIndicator size="small" color={color.faint} />
      ) : (
        <Feather name="mic" size={19} color={disabled ? color.faint : color.muted} />
      )}
    </MotionPressable>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    button: { minWidth: 34, height: 34, borderRadius: radius.pill, alignItems: "center", justifyContent: "center" },
    recording: { backgroundColor: c.errorText, paddingHorizontal: 12 },
    row: { flexDirection: "row", alignItems: "center", gap: 7 },
    stop: { width: 9, height: 9, borderRadius: 2, backgroundColor: "#FFFFFF" },
    time: { ...font.monoMedium, fontSize: scaled(12), color: "#FFFFFF" },
  });
