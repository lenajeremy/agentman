import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { Alert, StyleSheet, Text } from "react-native";

import { MotionPressable } from "../../components/MotionPressable";
import { Divider, Field, Problem, Section, Segmented, SettingsPage, TopAction } from "../../components/SettingsKit";
import { useStyles } from "../../lib/appearance";
import { useManager } from "../../lib/manager";
import { font, Palette, radius, size, space, scaled } from "../../lib/theme";

const RULES = [
  { value: "auto", label: "Any agent" },
  { value: "projects", label: "Some folders" },
  { value: "ask", label: "Ask me" },
];

/** A key's name, what it is for, and who gets it. Its value is set on the
 *  Mac. */
export default function KeyEditor() {
  const styles = useStyles(makeStyles);
  const router = useRouter();
  const manager = useManager();
  const params = useLocalSearchParams<{ name?: string }>();
  const existing = params.name ? manager.keys.find((key) => key.name === params.name) : undefined;
  const [name, setName] = useState(existing?.name ?? "");
  const [description, setDescription] = useState(existing?.description ?? "");
  const [rule, setRule] = useState(existing?.rule ?? "auto");
  const [projects, setProjects] = useState((existing?.projects ?? []).join("\n"));
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  useEffect(() => {
    if (!existing) return;
    setName(existing.name);
    setDescription(existing.description ?? "");
    setRule(existing.rule);
    setProjects((existing.projects ?? []).join("\n"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [existing?.name]);

  const folders = projects.split("\n").map((line) => line.trim()).filter(Boolean);
  const save = async () => {
    setSaving(true);
    const result = await manager.saveKeyDetails({
      name: name.trim().toUpperCase(),
      description: description.trim(),
      rule,
      ...(rule === "projects" ? { projects: folders } : {}),
    });
    setSaving(false);
    if (result) setProblem(result);
    else router.back();
  };

  const remove = () => {
    if (!existing) return;
    Alert.alert(`Delete ${existing.name}?`, "Its value is removed from your Mac's Keychain too.", [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: () => {
          void manager.deleteKey(existing.name).then((result) => {
            if (result) setProblem(result);
            else router.back();
          });
        },
      },
    ]);
  };

  return (
    <SettingsPage
      title={existing ? existing.name : "New key"}
      action={<TopAction label={existing ? "Save" : "Add"} onPress={() => void save()}
        disabled={!name.trim() || (rule === "projects" && folders.length === 0)} busy={saving} />}
    >
      <Section label="Name" footer="The environment variable an agent gets: capitals, digits and _, like ELEVENLABS_API_KEY.">
        <Field value={name} onChangeText={setName} placeholder="ELEVENLABS_API_KEY" mono autoCapitalize="characters"
          autoCorrect={false} editable={!existing} />
        <Divider />
        <Field value={description} onChangeText={setDescription} placeholder="What it's for (optional)" />
      </Section>
      <Section label="Who gets it">
        <Segmented options={RULES} value={rule} onChange={setRule} />
        {rule === "projects" ? (
          <>
            <Divider />
            <Field value={projects} onChangeText={setProjects} multiline mono autoCapitalize="none" autoCorrect={false}
              placeholder={"Folders, one per line:\n~/code/app"} />
          </>
        ) : null}
      </Section>
      <Text style={styles.valueNote}>
        {existing?.set
          ? "A value is saved in your Mac's Keychain."
          : "Set its value in Agentman on your Mac: typed here, it would pass through the relay."}
      </Text>
      <Problem text={problem} />
      {existing ? (
        <MotionPressable onPress={remove} style={styles.delete} accessibilityRole="button">
          <Text style={styles.deleteText}>Delete key</Text>
        </MotionPressable>
      ) : null}
    </SettingsPage>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    valueNote: { fontFamily: font.sans, fontSize: size.label, lineHeight: scaled(17), color: c.muted, marginTop: space.md, marginHorizontal: space.xs },
    delete: {
      minHeight: 48,
      marginTop: space.xl,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.errorWash,
    },
    deleteText: { fontFamily: font.sansBold, fontSize: size.body, color: c.errorText },
  });
