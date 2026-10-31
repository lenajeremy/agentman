import Feather from "@expo/vector-icons/Feather";
import { useState } from "react";
import { StyleSheet, Text, View } from "react-native";

import { MotionPressable } from "../../components/MotionPressable";
import { Divider, Problem, Section, Segmented, SettingsPage, SwitchRow } from "../../components/SettingsKit";
import { useStyles, useTheme } from "../../lib/appearance";
import { useManager } from "../../lib/manager";
import { MANAGER_MODELS } from "../../lib/manager-models";
import type { ManagerSettings } from "../../lib/protocol";
import { font, Palette, size, space } from "../../lib/theme";

const EFFORTS = [
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium" },
  { value: "high", label: "High" },
  { value: "xhigh", label: "Higher" },
  { value: "max", label: "Max" },
];

/** The models Agentman runs on, how hard it thinks, and its briefs. Each
 *  choice is saved as it is made. */
export default function ModelsSettings() {
  const manager = useManager();
  const settings = manager.settings;
  const [problem, setProblem] = useState<string | null>(null);
  const save = (change: Partial<ManagerSettings>) => {
    if (!settings) return;
    void manager.saveSettings({ ...settings, ...change }).then(setProblem);
  };
  return (
    <SettingsPage title="Models" intro="What Agentman runs on. Each engine needs its API key, set in Agentman on your Mac.">
      {settings ? (
        <>
          <ModelList label="Conversation" footer="Answers you and acts on your agents." value={settings.chatModel}
            onChange={(chatModel) => save({ chatModel })} />
          <ModelList label="Briefs" footer="Summarises each agent after its turns." value={settings.briefModel}
            onChange={(briefModel) => save({ briefModel })} />
          <Section label="Effort" footer="How hard the conversation model thinks.">
            <Segmented options={EFFORTS} value={settings.effort} onChange={(effort) => save({ effort })} />
          </Section>
          <Section label="Briefs">
            <SwitchRow label="Brief me on each agent" detail="A short summary after an agent's turns."
              value={settings.briefs} onChange={(briefs) => save({ briefs })} />
          </Section>
          <Problem text={problem} />
        </>
      ) : (
        <Problem text="Agentman's settings haven't arrived from your Mac yet." />
      )}
    </SettingsPage>
  );
}

function ModelList({ label, footer, value, onChange }: {
  label: string;
  footer: string;
  value: string;
  onChange(model: string): void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const settings = useManager().settings;
  return (
    <Section label={label} footer={footer}>
      {MANAGER_MODELS.map((model, index) => {
        const keyed = model.provider === "gemini" ? settings?.geminiKeySet : (settings?.anthropicKeySet ?? settings?.keySet);
        return (
          <View key={model.id}>
            {index > 0 ? <Divider /> : null}
            <MotionPressable onPress={() => onChange(model.id)} style={styles.choice} pressedScale={0.99}
              accessibilityRole="radio" accessibilityState={{ selected: value === model.id }}>
              <View style={styles.words}>
                <Text style={styles.name}>{model.name}</Text>
                {keyed === false ? <Text style={styles.noKey}>No {model.provider === "gemini" ? "Gemini" : "Anthropic"} key on your Mac</Text> : null}
              </View>
              {value === model.id ? <Feather name="check" size={18} color={color.working} /> : null}
            </MotionPressable>
          </View>
        );
      })}
    </Section>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    choice: { minHeight: 52, flexDirection: "row", alignItems: "center", gap: space.md, paddingVertical: space.sm },
    words: { flex: 1, gap: 2 },
    name: { ...font.sansMedium, fontSize: size.body, color: c.text },
    noKey: { ...font.sans, fontSize: size.label, color: c.faint },
  });
