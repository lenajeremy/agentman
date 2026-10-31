import Feather from "@expo/vector-icons/Feather";
import { useFocusEffect, useRouter } from "expo-router";
import { useCallback } from "react";
import { StyleSheet, Text, View } from "react-native";

import { MotionPressable } from "../../components/MotionPressable";
import { Divider, Section, SettingsPage, TopAction } from "../../components/SettingsKit";
import { useStyles, useTheme } from "../../lib/appearance";
import { useManager } from "../../lib/manager";
import { font, Palette, size, space, scaled } from "../../lib/theme";

/** The user's notes for their agents: how they like things done, facts about
 *  their projects. Shared ones any agent can read through Agentman; private
 *  ones only Agentman reads. */
export default function NotesSettings() {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const router = useRouter();
  const manager = useManager();
  const { loadNotes } = manager;
  useFocusEffect(useCallback(() => {
    void loadNotes();
  }, [loadNotes]));
  return (
    <SettingsPage
      title="Notes"
      intro="How you like things done, facts about your projects. Shared notes any agent can read through Agentman; private ones only Agentman reads."
      action={<TopAction label="New note" onPress={() => router.push("/agentman-settings/note")} />}
    >
      <Section>
        {manager.notes.length === 0 ? (
          <View style={styles.empty}>
            <Feather name="book-open" size={18} color={color.faint} />
            <Text style={styles.emptyText}>No notes yet. Notes you write, or save from Agentman's suggestions, show here.</Text>
          </View>
        ) : null}
        {manager.notes.map((note, index) => (
          <View key={`${note.tier}/${note.name}`}>
            {index > 0 ? <Divider /> : null}
            <MotionPressable
              onPress={() => router.push(`/agentman-settings/note?name=${encodeURIComponent(note.name)}&tier=${encodeURIComponent(note.tier)}`)}
              style={styles.note}
              pressedScale={0.99}
              accessibilityRole="button"
            >
              <View style={styles.words}>
                <Text style={styles.title} numberOfLines={2}>{note.title}</Text>
                <View style={styles.meta}>
                  <Feather name={note.tier === "shared" ? "users" : "lock"} size={11} color={color.muted} />
                  <Text style={styles.metaText}>
                    {note.tier === "shared" ? "Shared" : "Private"}{note.always ? " · In project instructions" : ""}
                  </Text>
                </View>
              </View>
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
    empty: { alignItems: "center", gap: space.sm, paddingVertical: space.xl, paddingHorizontal: space.md },
    emptyText: { ...font.sans, fontSize: size.caption, lineHeight: scaled(19), color: c.muted, textAlign: "center" },
    note: { flexDirection: "row", alignItems: "center", gap: space.md, paddingVertical: space.md },
    words: { flex: 1, gap: 4 },
    title: { ...font.sansMedium, fontSize: size.body, color: c.text },
    meta: { flexDirection: "row", alignItems: "center", gap: 5 },
    metaText: { ...font.sans, fontSize: size.label, color: c.muted },
  });
