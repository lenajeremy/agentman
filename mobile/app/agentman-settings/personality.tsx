import { useEffect, useState } from "react";
import { StyleSheet, Text } from "react-native";

import { MotionPressable } from "../../components/MotionPressable";
import { Field, Problem, Section, SettingsPage, TopAction } from "../../components/SettingsKit";
import { useStyles } from "../../lib/appearance";
import { useManager } from "../../lib/manager";
import { font, Palette, size, space } from "../../lib/theme";

/** How Agentman talks until the user says otherwise; the daemon's default. */
const DEFAULT_STYLE = "Calm and concise. Lead with what matters, in as few words as it takes. " +
  "No preamble, no filler, no exclamation marks. Name agents by name. " +
  "When nothing needs the user, say so in one line.";

/** How Agentman talks, and anything else it should know. Both go into its
 *  prompt on every message. */
export default function PersonalitySettings() {
  const styles = useStyles(makeStyles);
  const manager = useManager();
  const settings = manager.settings;
  const [style, setStyle] = useState(settings?.style ?? "");
  const [instructions, setInstructions] = useState(settings?.instructions ?? "");
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  useEffect(() => {
    if (!settings) return;
    setStyle(settings.style);
    setInstructions(settings.instructions);
    // Only when the settings first arrive, not while the user types.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settings === null]);
  const changed = settings !== null && (style !== settings.style || instructions !== settings.instructions);
  const save = async () => {
    if (!settings) return;
    setSaving(true);
    setProblem(await manager.saveSettings({ ...settings, style, instructions }));
    setSaving(false);
  };
  return (
    <SettingsPage title="Personality" action={<TopAction label="Save" onPress={() => void save()} disabled={!changed} busy={saving} />}>
      <Section label="How Agentman talks">
        <Field value={style} onChangeText={setStyle} multiline placeholder="Calm and concise…" />
      </Section>
      <MotionPressable onPress={() => setStyle(DEFAULT_STYLE)} style={styles.reset} hitSlop={8} accessibilityRole="button">
        <Text style={styles.resetText}>Use the default style</Text>
      </MotionPressable>
      <Section label="Anything else Agentman should know"
        footer="Notes for your agents belong in Notes; this is for Agentman itself.">
        <Field value={instructions} onChangeText={setInstructions} multiline placeholder="What to watch for, how you like updates…" />
      </Section>
      <Problem text={problem} />
    </SettingsPage>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    reset: { alignSelf: "flex-end", marginTop: space.sm, marginRight: space.xs },
    resetText: { ...font.sansMedium, fontSize: size.label, color: c.workingText },
  });
