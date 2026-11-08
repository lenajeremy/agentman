import Feather from "@expo/vector-icons/Feather";
import { useState } from "react";
import { ActivityIndicator, Alert, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useStyles, useTheme } from "../lib/appearance";
import { useManager } from "../lib/manager";
import { useStore } from "../lib/store";
import { font, Palette, radius, scaled, size, space } from "../lib/theme";
import { addSpoken } from "../lib/voice";
import { BrandMark } from "./BrandMark";
import { MotionPressable } from "./MotionPressable";
import { VoiceButton } from "./VoiceButton";

/**
 * The box for asking Agentman, pinned to the foot of the Board. Sending opens
 * the conversation over the Board, where the answer comes in.
 */
export function AskBar({ onOpenConversation }: { onOpenConversation(): void }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const insets = useSafeAreaInsets();
  const store = useStore();
  const manager = useManager();
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const reachable = store.connection === "online" && store.daemonOnline;
  const typed = draft.trim().length > 0;

  const send = async () => {
    if (!typed || sending || !reachable) return;
    setSending(true);
    onOpenConversation();
    if (await manager.send(draft.trim())) setDraft("");
    setSending(false);
  };

  if (manager.needsKey) {
    return (
      <View style={[styles.bar, { paddingBottom: Math.max(insets.bottom, space.md) }]}>
        <MotionPressable
          onPress={onOpenConversation}
          pressedScale={0.99}
          style={styles.field}
          accessibilityRole="button"
          accessibilityLabel="Agentman needs an API key. Open the conversation."
        >
          <BrandMark size={26} ring={color.surface} />
          <Text style={styles.placeholder} numberOfLines={1}>Agentman needs an API key</Text>
          <Feather name="chevron-right" size={18} color={color.faint} />
        </MotionPressable>
      </View>
    );
  }

  return (
    <View style={[styles.bar, { paddingBottom: Math.max(insets.bottom, space.md) }]}>
      <View style={styles.field}>
        <BrandMark size={26} ring={color.surface} />
        <TextInput
          style={styles.input}
          value={draft}
          onChangeText={setDraft}
          placeholder={reachable ? "Ask Agentman" : "Waiting for your Mac…"}
          placeholderTextColor={color.faint}
          multiline
          editable={reachable}
          accessibilityLabel="Ask Agentman"
        />
        {manager.thinking && !typed ? (
          <MotionPressable
            onPress={manager.stop}
            style={styles.send}
            accessibilityRole="button"
            accessibilityLabel="Stop Agentman"
          >
            <Feather name="square" size={12} color={color.onInverse} />
          </MotionPressable>
        ) : typed ? (
          <MotionPressable
            onPress={() => void send()}
            disabled={!reachable || sending}
            style={styles.send}
            accessibilityRole="button"
            accessibilityLabel="Send to Agentman"
          >
            {sending ? (
              <ActivityIndicator size="small" color={color.onInverse} />
            ) : (
              <Feather name="arrow-up" size={17} color={color.onInverse} />
            )}
          </MotionPressable>
        ) : (
          <VoiceButton
            disabled={!reachable}
            onText={(spoken) => setDraft((current) => addSpoken(current, spoken))}
            onProblem={(message) => Alert.alert("Voice", message)}
          />
        )}
      </View>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    bar: {
      paddingHorizontal: space.lg,
      paddingTop: 10,
      backgroundColor: c.paper,
      borderTopWidth: StyleSheet.hairlineWidth,
      borderTopColor: c.line,
    },
    field: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      minHeight: 52,
      paddingLeft: 12,
      paddingRight: 8,
      paddingVertical: 6,
      borderRadius: 26,
      borderWidth: 1,
      borderColor: c.line,
      backgroundColor: c.surface,
    },
    input: {
      flex: 1,
      minWidth: 0,
      maxHeight: 96,
      paddingVertical: 6,
      ...font.sans,
      fontSize: scaled(15),
      color: c.text,
    },
    placeholder: { flex: 1, ...font.sans, fontSize: size.body, color: c.faint },
    send: {
      width: 36,
      height: 36,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.inverse,
    },
  });
