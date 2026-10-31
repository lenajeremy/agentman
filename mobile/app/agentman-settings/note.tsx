import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { Alert, StyleSheet, Text } from "react-native";

import { MotionPressable } from "../../components/MotionPressable";
import { Divider, Field, Problem, Section, Segmented, SettingsPage, SwitchRow, TopAction } from "../../components/SettingsKit";
import { useStyles } from "../../lib/appearance";
import { useManager } from "../../lib/manager";
import type { Note } from "../../lib/protocol";
import { font, Palette, radius, size, space } from "../../lib/theme";

/** One note, new or being changed: its title, who reads it, and its words. */
export default function NoteEditor() {
  const styles = useStyles(makeStyles);
  const router = useRouter();
  const manager = useManager();
  const params = useLocalSearchParams<{ name?: string; tier?: string }>();
  const [original, setOriginal] = useState<Note | null>(null);
  const [title, setTitle] = useState("");
  const [tier, setTier] = useState("shared");
  const [always, setAlways] = useState(false);
  const [body, setBody] = useState("");
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  useEffect(() => {
    if (!params.name || !params.tier) return;
    void manager.readNote(params.name, params.tier).then((note) => {
      if (!note) {
        setProblem("This note couldn't be read from your Mac.");
        return;
      }
      setOriginal(note);
      setTitle(note.title);
      setTier(note.tier);
      setAlways(note.always === true);
      setBody(note.body ?? "");
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params.name, params.tier]);

  const save = async () => {
    setSaving(true);
    const result = await manager.writeNote({
      name: original?.name ?? "",
      tier,
      title: title.trim(),
      always: tier === "shared" && always,
      body,
      updatedAt: 0,
    });
    setSaving(false);
    if (typeof result === "string") setProblem(result);
    else router.back();
  };

  const remove = () => {
    if (!original) return;
    Alert.alert("Delete this note?", original.title, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: () => {
          void manager.deleteNote(original).then((result) => {
            if (result) setProblem(result);
            else router.back();
          });
        },
      },
    ]);
  };

  return (
    <SettingsPage
      title={original ? "Note" : "New note"}
      action={<TopAction label={original ? "Save" : "Add"} onPress={() => void save()} disabled={!title.trim()} busy={saving} />}
    >
      <Section>
        <Field value={title} onChangeText={setTitle} placeholder="Title" style={styles.title} />
      </Section>
      <Section label="Who reads it">
        <Segmented options={[{ value: "shared", label: "Shared with agents" }, { value: "private", label: "Only Agentman" }]}
          value={tier} onChange={setTier} />
        {tier === "shared" ? (
          <>
            <Divider />
            <SwitchRow label="In project instructions"
              detail="Agentman writes it into CLAUDE.md or AGENTS.md when you ask it to update an agent's instructions."
              value={always} onChange={setAlways} />
          </>
        ) : null}
      </Section>
      <Section label="The note">
        <Field value={body} onChangeText={setBody} multiline placeholder="How you like things done, or what agents should know." />
      </Section>
      <Problem text={problem} />
      {original ? (
        <MotionPressable onPress={remove} style={styles.delete} accessibilityRole="button">
          <Text style={styles.deleteText}>Delete note</Text>
        </MotionPressable>
      ) : null}
    </SettingsPage>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    title: { ...font.sansMedium, fontSize: size.body },
    delete: {
      minHeight: 48,
      marginTop: space.xl,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.errorWash,
    },
    deleteText: { ...font.sansBold, fontSize: size.body, color: c.errorText },
  });
