import Feather from "@expo/vector-icons/Feather";
import { useRouter } from "expo-router";
import { useState } from "react";
import { Alert, Platform, StyleSheet, Text, TextInput, View } from "react-native";

import { edged } from "../components/Brand";
import { MotionPressable } from "../components/MotionPressable";
import { Divider, Problem, Section, SettingsPage, TopAction } from "../components/SettingsKit";
import { useStyles, useTheme } from "../lib/appearance";
import { hostName, useFleet, type Host } from "../lib/fleet";
import { font, Palette, radius, size, space } from "../lib/theme";

export default function Devices() {
  const fleet = useFleet();
  const router = useRouter();
  const [busy, setBusy] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);

  function startRename(host: Host) {
    setError(null);
    setEditing(host.id);
    setDraft(host.alias ?? host.daemonName ?? "");
  }

  function cancelRename() {
    setEditing(null);
    setDraft("");
    setError(null);
  }

  async function saveRename(host: Host, nextValue: string) {
    setBusy(host.id);
    setError(null);
    try {
      const cleaned = nextValue.trim();
      // Storing the computer's own hostname as an alias would freeze it if the
      // computer is renamed later; clear the alias instead.
      const value = cleaned && cleaned === host.daemonName ? "" : cleaned;
      await fleet.rename(host.id, value);
      setEditing(null);
      setDraft("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not rename this device.");
    } finally {
      setBusy(null);
    }
  }

  function confirmRemoval(id: string, name: string) {
    const remove = async () => {
      setBusy(id);
      setError(null);
      try {
        const host = fleet.hosts.find((item) => item.id === id);
        if (!host) return;
        await host.store.signOut();
        if (editing === id) {
          setEditing(null);
          setDraft("");
        }
        if (fleet.hosts.length === 1) {
          if (router.canDismiss()) router.dismissAll();
          router.replace("/pair");
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : "Could not unpair this device.");
      } finally {
        setBusy(null);
      }
    };
    const title = `Unpair ${name}?`;
    if (Platform.OS === "web") {
      if (globalThis.confirm(title)) void remove();
    } else {
      Alert.alert(title, "Pair again to access its agents from this phone.", [
        { text: "Cancel", style: "cancel" },
        { text: "Unpair", style: "destructive", onPress: () => void remove() },
      ]);
    }
  }

  return (
    <SettingsPage
      title="Devices"
      intro="Rename paired computers so they are easy to tell apart across your agents."
      action={<TopAction label="Add device" onPress={() => router.push("/pair")} />}
    >
      <Section label="Paired computers">
        {fleet.hosts.map((host, index) => {
          const name = hostName(host, index);
          const isEditing = editing === host.id;
          return (
            <DeviceRow
              key={host.id}
              host={host}
              name={name}
              divider={index > 0}
              disabled={busy !== null}
              editing={isEditing}
              draft={isEditing ? draft : ""}
              onDraftChange={setDraft}
              onEdit={() => startRename(host)}
              onCancel={cancelRename}
              onSave={() => void saveRename(host, draft)}
              onReset={() => void saveRename(host, "")}
              onUnpair={() => confirmRemoval(host.id, name)}
            />
          );
        })}
      </Section>
      <Problem text={error} />
    </SettingsPage>
  );
}

function DeviceRow({
  host,
  name,
  divider,
  disabled,
  editing,
  draft,
  onDraftChange,
  onEdit,
  onCancel,
  onSave,
  onReset,
  onUnpair,
}: {
  host: Host;
  name: string;
  divider: boolean;
  disabled: boolean;
  editing: boolean;
  draft: string;
  onDraftChange(text: string): void;
  onEdit(): void;
  onCancel(): void;
  onSave(): void;
  onReset(): void;
  onUnpair(): void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [focused, setFocused] = useState(false);
  const online = host.store.daemonOnline;
  const agentCount = host.store.liveSessions.length;
  const showHostname = Boolean(host.alias && host.daemonName && host.alias !== host.daemonName);
  const statusLine = online
    ? agentCount > 0
      ? `Online · ${agentCount} ${agentCount === 1 ? "agent" : "agents"}`
      : "Online"
    : "Offline";

  return (
    <>
      {divider && <Divider />}
      <View style={styles.rowWrap}>
        <View style={styles.row}>
          <View style={[styles.icon, online && styles.iconOnline]}>
            <Feather
              name="monitor"
              size={19}
              color={online ? color.text : color.muted}
            />
          </View>
          <View style={styles.words}>
            <Text numberOfLines={2} style={styles.name}>
              {name}
            </Text>
            <View style={styles.status}>
              <View style={[styles.dot, { backgroundColor: online ? color.ok : color.faint }]} />
              <Text style={styles.caption} numberOfLines={1}>
                {statusLine}
              </Text>
            </View>
            {showHostname ? (
              <Text style={styles.hostname} numberOfLines={1}>
                {host.daemonName}
              </Text>
            ) : null}
          </View>
          <View style={styles.actions}>
            <MotionPressable
              accessibilityRole="button"
              accessibilityLabel={`Rename ${name}`}
              disabled={disabled}
              onPress={editing ? onCancel : onEdit}
              pressedScale={0.94}
              style={[styles.iconAction, editing && styles.iconActionActive]}
            >
              <Feather
                name={editing ? "x" : "edit-2"}
                size={15}
                color={editing ? color.workingText : color.textSecondary}
              />
            </MotionPressable>
            <MotionPressable
              accessibilityRole="button"
              accessibilityLabel={`Unpair ${name}`}
              disabled={disabled}
              onPress={onUnpair}
              pressedScale={0.95}
              style={styles.unpairButton}
            >
              <Text style={styles.unpairLabel}>Unpair</Text>
            </MotionPressable>
          </View>
        </View>

        {editing ? (
          <View style={styles.editor}>
            <TextInput
              value={draft}
              onChangeText={onDraftChange}
              onFocus={() => setFocused(true)}
              onBlur={() => setFocused(false)}
              onSubmitEditing={onSave}
              placeholder={host.daemonName || "Device name"}
              placeholderTextColor={color.faint}
              maxLength={64}
              autoFocus
              returnKeyType="done"
              accessibilityLabel={`Name for ${name}`}
              style={[styles.input, focused && styles.inputFocused]}
            />
            <View style={styles.editorFooter}>
              {host.alias && host.daemonName ? (
                <MotionPressable
                  onPress={onReset}
                  disabled={disabled}
                  hitSlop={8}
                  style={styles.resetButton}
                  accessibilityRole="button"
                  accessibilityLabel={`Reset name to ${host.daemonName}`}
                >
                  <Text style={styles.resetLabel} numberOfLines={1}>
                    Use {host.daemonName}
                  </Text>
                </MotionPressable>
              ) : (
                <View style={styles.editorSpacer} />
              )}
              <View style={styles.editorButtons}>
                <MotionPressable
                  onPress={onCancel}
                  disabled={disabled}
                  pressedScale={0.96}
                  style={[styles.button, styles.buttonSecondary]}
                  accessibilityRole="button"
                >
                  <Text style={styles.buttonLabel}>Cancel</Text>
                </MotionPressable>
                <MotionPressable
                  onPress={onSave}
                  disabled={disabled}
                  pressedScale={0.96}
                  style={[styles.button, styles.buttonPrimary, disabled && styles.buttonDisabled]}
                  accessibilityRole="button"
                >
                  <Text style={[styles.buttonLabel, styles.buttonLabelPrimary]}>Save</Text>
                </MotionPressable>
              </View>
            </View>
          </View>
        ) : null}
      </View>
    </>
  );
}

const makeSiteStyles = (c: Palette) =>
  StyleSheet.create({
    rowWrap: { paddingVertical: space.sm },
    row: { flexDirection: "row", alignItems: "center", gap: space.md, minHeight: 58 },
    icon: {
      width: 40,
      height: 40,
      borderRadius: radius.md,
      backgroundColor: c.fill,
      alignItems: "center",
      justifyContent: "center",
      flexShrink: 0,
    },
    iconOnline: { backgroundColor: c.cream, ...edged(c) },
    words: { flex: 1, minWidth: 0, gap: 3 },
    name: { ...font.sansBold, fontSize: size.body, color: c.text },
    status: { flexDirection: "row", alignItems: "center", gap: 6 },
    dot: { height: 6, width: 6, borderRadius: 3, flexShrink: 0 },
    caption: { flexShrink: 1, ...font.sans, fontSize: size.caption, color: c.muted },
    hostname: { ...font.mono, fontSize: size.label, color: c.faint },
    actions: { flexDirection: "row", alignItems: "center", gap: space.xs, flexShrink: 0 },
    iconAction: {
      width: 36,
      height: 36,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      ...edged(c),
      alignItems: "center",
      justifyContent: "center",
    },
    iconActionActive: { backgroundColor: c.cream },
    unpairButton: {
      minHeight: 36,
      paddingHorizontal: space.sm,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
    },
    unpairLabel: { ...font.sansMedium, fontSize: size.caption, color: c.muted },

    editor: {
      marginTop: space.sm,
      paddingTop: space.sm,
      gap: space.sm,
    },
    input: {
      minHeight: 44,
      paddingHorizontal: space.md,
      paddingVertical: 10,
      borderRadius: radius.md,
      backgroundColor: c.paper,
      ...edged(c),
      ...font.sans,
      fontSize: size.body,
      color: c.text,
    },
    inputFocused: { borderColor: c.working },
    editorFooter: {
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "space-between",
      gap: space.sm,
    },
    editorSpacer: { flex: 1 },
    resetButton: {
      flexShrink: 1,
      minHeight: 36,
      justifyContent: "center",
      paddingRight: space.xs,
    },
    resetLabel: { ...font.sansMedium, fontSize: size.caption, color: c.muted },
    editorButtons: { flexDirection: "row", alignItems: "center", gap: space.sm, flexShrink: 0 },
    button: {
      minHeight: 36,
      minWidth: 74,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
    },
    buttonPrimary: { backgroundColor: c.inverse },
    buttonSecondary: { backgroundColor: c.surface, ...edged(c) },
    buttonDisabled: { opacity: 0.4 },
    buttonLabel: { ...font.sansBold, fontSize: size.caption, color: c.text },
    buttonLabelPrimary: { color: c.onInverse },
  });

const makeClassicStyles = (c: Palette) =>
  StyleSheet.create({
    rowWrap: { paddingVertical: space.sm },
    row: { flexDirection: "row", alignItems: "center", gap: space.md, minHeight: 58 },
    icon: {
      width: 40,
      height: 40,
      borderRadius: radius.md,
      backgroundColor: c.fill,
      alignItems: "center",
      justifyContent: "center",
      flexShrink: 0,
    },
    iconOnline: { backgroundColor: c.fill },
    words: { flex: 1, minWidth: 0, gap: 3 },
    name: { ...font.sansBold, fontSize: size.body, color: c.text },
    status: { flexDirection: "row", alignItems: "center", gap: 6 },
    dot: { height: 6, width: 6, borderRadius: 3, flexShrink: 0 },
    caption: { flexShrink: 1, ...font.sans, fontSize: size.caption, color: c.muted },
    hostname: { ...font.mono, fontSize: size.label, color: c.faint },
    actions: { flexDirection: "row", alignItems: "center", gap: space.xs, flexShrink: 0 },
    iconAction: {
      width: 36,
      height: 36,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
      alignItems: "center",
      justifyContent: "center",
    },
    iconActionActive: { backgroundColor: c.workingWash },
    unpairButton: {
      minHeight: 36,
      paddingHorizontal: space.sm,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
    },
    unpairLabel: { ...font.sansMedium, fontSize: size.caption, color: c.muted },

    editor: {
      marginTop: space.sm,
      paddingTop: space.sm,
      gap: space.sm,
    },
    input: {
      minHeight: 44,
      paddingHorizontal: space.md,
      paddingVertical: 10,
      borderRadius: radius.md,
      backgroundColor: c.fill,
      borderWidth: 1,
      borderColor: "transparent",
      ...font.sans,
      fontSize: size.body,
      color: c.text,
    },
    inputFocused: { borderColor: c.working, backgroundColor: c.paper },
    editorFooter: {
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "space-between",
      gap: space.sm,
    },
    editorSpacer: { flex: 1 },
    resetButton: {
      flexShrink: 1,
      minHeight: 36,
      justifyContent: "center",
      paddingRight: space.xs,
    },
    resetLabel: { ...font.sansMedium, fontSize: size.caption, color: c.muted },
    editorButtons: { flexDirection: "row", alignItems: "center", gap: space.sm, flexShrink: 0 },
    button: {
      minHeight: 36,
      minWidth: 74,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
    },
    buttonPrimary: { backgroundColor: c.inverse },
    buttonSecondary: { backgroundColor: c.surface, borderWidth: 1, borderColor: c.fillStrong },
    buttonDisabled: { opacity: 0.4 },
    buttonLabel: { ...font.sansBold, fontSize: size.caption, color: c.text },
    buttonLabelPrimary: { color: c.onInverse },
  });

type Styles = ReturnType<typeof makeSiteStyles> & ReturnType<typeof makeClassicStyles>;
const makeStyles = (c: Palette): Styles =>
  (c.look === "site" ? makeSiteStyles(c) : { ...makeSiteStyles(c), ...makeClassicStyles(c) }) as Styles;
