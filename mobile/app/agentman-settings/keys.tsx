import Feather from "@expo/vector-icons/Feather";
import { useFocusEffect, useRouter } from "expo-router";
import { useCallback } from "react";
import { StyleSheet, Text, View } from "react-native";

import { MotionPressable } from "../../components/MotionPressable";
import { Divider, Section, SettingsPage, TopAction } from "../../components/SettingsKit";
import { useStyles, useTheme } from "../../lib/appearance";
import { useManager } from "../../lib/manager";
import type { Key } from "../../lib/protocol";
import { font, Palette, size, space } from "../../lib/theme";

export function ruleSummary(key: Key): string {
  switch (key.rule) {
    case "auto": return "Any agent that needs it";
    case "projects": return key.projects?.length ? `Agents in ${key.projects.length === 1 ? "1 folder" : `${key.projects.length} folders`}` : "Agents in listed folders";
    default: return "Asks you first";
  }
}

/** The keys Agentman hands to agents that need them, by name. Their values
 *  stay in the Mac's Keychain and never come to the phone. */
export default function KeysSettings() {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const router = useRouter();
  const manager = useManager();
  const { loadKeys } = manager;
  useFocusEffect(useCallback(() => {
    void loadKeys();
  }, [loadKeys]));
  return (
    <SettingsPage
      title="Keys"
      intro="API keys Agentman gives to agents that ask for them, as the environment variable each is named for."
      action={<TopAction label="New key" onPress={() => router.push("/agentman-settings/key")} />}
    >
      <Section footer="A key's value is set in Agentman on your Mac, and never leaves it: typed here, it would pass through the relay.">
        {manager.keys.length === 0 ? <Text style={styles.empty}>No keys yet.</Text> : null}
        {manager.keys.map((key, index) => (
          <View key={key.name}>
            {index > 0 ? <Divider /> : null}
            <MotionPressable onPress={() => router.push(`/agentman-settings/key?name=${encodeURIComponent(key.name)}`)}
              style={styles.key} pressedScale={0.99} accessibilityRole="button">
              <View style={styles.words}>
                <Text style={styles.name} numberOfLines={1}>{key.name}</Text>
                <Text style={styles.meta} numberOfLines={1}>
                  {key.set ? ruleSummary(key) : "No value yet · set it on your Mac"}
                </Text>
              </View>
              <View style={[styles.dot, { backgroundColor: key.set ? color.ok : color.faint }]} />
              <Feather name="chevron-right" size={16} color={color.faint} />
            </MotionPressable>
          </View>
        ))}
      </Section>
    </SettingsPage>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    empty: { fontFamily: font.sans, fontSize: size.caption, color: c.faint, paddingVertical: space.lg },
    key: { flexDirection: "row", alignItems: "center", gap: space.md, paddingVertical: space.md },
    words: { flex: 1, gap: 3 },
    name: { fontFamily: font.mono, fontSize: size.caption, color: c.text },
    meta: { fontFamily: font.sans, fontSize: size.label, color: c.muted },
    dot: { width: 7, height: 7, borderRadius: 4 },
  });
