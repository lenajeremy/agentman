import Feather from "@expo/vector-icons/Feather";
import { useRouter } from "expo-router";
import { useEffect, useState } from "react";
import {
  ActivityIndicator,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { ContentColumn } from "../components/ContentColumn";
import { MotionPressable } from "../components/MotionPressable";
import { useStyles, useTheme } from "../lib/appearance";
import { agentCount, folderLabel } from "../lib/folders";
import { Folder } from "../lib/protocol";
import { useStore } from "../lib/store";
import { useDirectoryBrowser } from "../lib/use-directory-browser";
import { font, Palette, radius, size, space } from "../lib/theme";

/**
 * Picking a folder to filter the agent list by.
 *
 * A full screen rather than a sheet, because this is a directory walk and not
 * a short menu. Two ways in, and both are needed:
 *
 *   Recent lists the directories agents actually ran in. It is the only route
 *   to one the walk cannot reach — the walk skips dot-directories and stops
 *   at the Mac user's home, and plenty of real work happens in both.
 *
 *   Browse walks the Mac, reusing the New session browser, so a folder can be
 *   chosen before anything has ever run in it.
 *
 * Tapping a row goes into it; the pinned button chooses the folder you are
 * standing in. Making the row do both would mean guessing which one was meant.
 */
export default function Folders() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const browser = useDirectoryBrowser();
  const [recent, setRecent] = useState<Folder[]>([]);

  const { daemonOnline, listFolders } = store;
  useEffect(() => {
    if (!daemonOnline) return;
    let live = true;
    void listFolders()
      .then((folders) => {
        if (live) setRecent(folders);
      })
      .catch(() => {
        // Recent is a shortcut. If the Mac cannot produce it, the walk below
        // still works, and an error banner over a working screen would be
        // noise.
      });
    return () => {
      live = false;
    };
    // Store methods change identity as sessions update; only reconnecting
    // should re-read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [daemonOnline]);

  const choose = (path: string) => {
    store.setFolderFilter(path);
    router.back();
  };

  const atHome = browser.path === "";
  const parent = browser.path.split("/").slice(0, -1).join("/");

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <ScrollView
        contentContainerStyle={{ paddingBottom: insets.bottom + 120 }}
        keyboardShouldPersistTaps="handled"
      >
        <ContentColumn style={styles.content}>
          <MotionPressable
            onPress={() => router.back()}
            style={styles.back}
            accessibilityRole="button"
            accessibilityLabel="Back"
            hitSlop={12}
          >
            <Feather name="chevron-left" size={21} color={color.text} />
          </MotionPressable>

          <Text style={styles.title} accessibilityRole="header">
            Folders
          </Text>
          <Text style={styles.subtitle}>
            Pick a folder to filter the list. Counts include subfolders.
          </Text>

          {store.folderFilter ? (
            <MotionPressable
              onPress={() => {
                store.setFolderFilter(null);
                router.back();
              }}
              style={styles.clear}
              accessibilityRole="button"
              accessibilityLabel="Show agents from every folder"
            >
              <Feather name="x" size={15} color={color.muted} />
              <Text style={styles.clearLabel}>Show every folder</Text>
            </MotionPressable>
          ) : null}

          {atHome && recent.length > 0 ? (
            <>
              <SectionHeading label="Recent" note={String(recent.length)} />
              <View style={styles.group}>
                {recent.map((folder, index) => (
                  <Row
                    key={folder.path}
                    first={index === 0}
                    mono
                    label={folderLabel(folder.path)}
                    note={countNote(folder.agents, folder.running)}
                    running={folder.running}
                    onPress={() => choose(folder.path)}
                  />
                ))}
              </View>
            </>
          ) : null}

          <SectionHeading
            label="Browse"
            note={atHome ? "~" : `~/${browser.path}`}
          />

          {browser.error ? (
            <Text style={styles.error}>{browser.error}</Text>
          ) : null}

          <View style={styles.group}>
            {!atHome ? (
              <Row
                first
                label={`Up to ${parent === "" ? "Home" : parent.split("/").slice(-1)[0]}`}
                onPress={browser.up}
                icon="corner-left-up"
                muted
              />
            ) : null}
            {browser.folders.map((folder, index) => (
              <Row
                key={folder.name}
                first={atHome && index === 0}
                mono
                label={folder.name}
                note={folder.agents ? countNote(folder.agents, folder.running) : ""}
                running={folder.running}
                onPress={() => browser.down(folder.name)}
              />
            ))}
            {browser.loading && browser.folders.length === 0 ? (
              <View style={styles.loading}>
                <ActivityIndicator color={color.faint} />
              </View>
            ) : null}
            {!browser.loading && browser.folders.length === 0 ? (
              <Text style={styles.empty}>No folders here.</Text>
            ) : null}
          </View>
        </ContentColumn>
      </ScrollView>

      <View style={[styles.bar, { paddingBottom: insets.bottom + space.lg }]}>
        <Text style={styles.barNote} numberOfLines={1}>
          {atHome ? "Home" : `~/${browser.path}`}
        </Text>
        <MotionPressable
          onPress={() => choose(browser.path)}
          style={[styles.use, atHome && styles.useDisabled]}
          disabled={atHome}
          accessibilityRole="button"
          accessibilityState={{ disabled: atHome }}
          accessibilityLabel={
            atHome
              ? "Go into a folder to choose it"
              : `Use ~/${browser.path}`
          }
        >
          <Text style={[styles.useLabel, atHome && styles.useLabelDisabled]}>
            {atHome ? "Go into a folder" : "Use this folder"}
          </Text>
        </MotionPressable>
      </View>
    </View>
  );
}

/** "41 agents · 3 running", or just the count when nothing is live. */
function countNote(agents?: number, running?: number): string {
  if (!agents) return "";
  return running ? `${agentCount(agents)} · ${running} running` : agentCount(agents);
}

function SectionHeading({ label, note }: { label: string; note: string }) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.heading}>
      <Text style={styles.headingLabel}>{label}</Text>
      <Text style={styles.headingNote} numberOfLines={1}>
        {note}
      </Text>
    </View>
  );
}

function Row({
  label,
  note,
  running,
  onPress,
  first,
  mono,
  muted,
  icon = "folder",
}: {
  label: string;
  note?: string;
  running?: number;
  onPress(): void;
  first?: boolean;
  mono?: boolean;
  muted?: boolean;
  icon?: "folder" | "corner-left-up";
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <MotionPressable
      onPress={onPress}
      style={[styles.row, !first && styles.rowDivided]}
      pressedScale={0.99}
      accessibilityRole="button"
      accessibilityLabel={note ? `${label}, ${note}` : label}
    >
      <Feather
        name={icon}
        size={17}
        color={note ? color.muted : color.faint}
      />
      <View style={styles.rowBody}>
        <Text
          style={[
            styles.rowLabel,
            mono && styles.rowLabelMono,
            muted && styles.rowLabelMuted,
          ]}
          numberOfLines={1}
          ellipsizeMode="head"
        >
          {label}
        </Text>
        {note ? (
          <Text style={styles.rowNote} numberOfLines={1}>
            {note}
          </Text>
        ) : null}
      </View>
      {running ? <View style={styles.liveDot} /> : null}
      {icon === "folder" ? (
        <Feather name="chevron-right" size={18} color={color.faint} />
      ) : null}
    </MotionPressable>
  );
}

function makeStyles(c: Palette) {
  return StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    content: { paddingHorizontal: space.lg },
    back: {
      width: 36,
      height: 36,
      marginLeft: -space.sm,
      alignItems: "center",
      justifyContent: "center",
    },
    title: {
      marginTop: space.sm,
      fontFamily: font.sansBold,
      fontSize: size.display,
      letterSpacing: -1.2,
      color: c.text,
    },
    subtitle: {
      marginTop: space.xs,
      fontFamily: font.sans,
      fontSize: size.caption,
      color: c.muted,
    },
    clear: {
      marginTop: space.md,
      alignSelf: "flex-start",
      flexDirection: "row",
      alignItems: "center",
      gap: space.xs,
      minHeight: 36,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    clearLabel: {
      fontFamily: font.sansMedium,
      fontSize: size.caption,
      color: c.muted,
    },
    heading: {
      marginTop: space.xl,
      flexDirection: "row",
      alignItems: "baseline",
      justifyContent: "space-between",
      gap: space.sm,
    },
    headingLabel: {
      fontFamily: font.sansBold,
      fontSize: size.caption,
      color: c.muted,
    },
    headingNote: {
      flexShrink: 1,
      fontFamily: font.mono,
      fontSize: size.label,
      color: c.faint,
    },
    group: {
      marginTop: space.sm,
      backgroundColor: c.surface,
      borderRadius: radius.lg,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.line,
      overflow: "hidden",
    },
    row: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      minHeight: 52,
      paddingHorizontal: space.md,
      paddingVertical: space.sm,
    },
    rowDivided: {
      borderTopWidth: StyleSheet.hairlineWidth,
      borderTopColor: c.line,
    },
    rowBody: { flexGrow: 1, flexShrink: 1, minWidth: 0 },
    rowLabel: { fontFamily: font.sansMedium, fontSize: 14.5, color: c.text },
    rowLabelMono: { fontFamily: font.monoMedium },
    rowLabelMuted: { color: c.textSecondary },
    rowNote: {
      marginTop: space.xxs,
      fontFamily: font.mono,
      fontSize: size.label,
      lineHeight: 18,
      color: c.muted,
    },
    liveDot: {
      width: 6,
      height: 6,
      borderRadius: radius.pill,
      backgroundColor: c.working,
    },
    loading: { paddingVertical: space.xl, alignItems: "center" },
    empty: {
      padding: space.lg,
      fontFamily: font.sans,
      fontSize: size.caption,
      color: c.faint,
    },
    error: {
      marginTop: space.sm,
      fontFamily: font.sans,
      fontSize: size.caption,
      color: c.errorText,
    },
    bar: {
      position: "absolute",
      left: 0,
      right: 0,
      bottom: 0,
      paddingHorizontal: space.lg,
      paddingTop: space.md,
      backgroundColor: c.paper,
      borderTopWidth: StyleSheet.hairlineWidth,
      borderTopColor: c.line,
    },
    barNote: {
      textAlign: "center",
      fontFamily: font.mono,
      fontSize: size.label,
      color: c.muted,
    },
    use: {
      marginTop: space.sm,
      minHeight: 48,
      alignItems: "center",
      justifyContent: "center",
      borderRadius: radius.pill,
      backgroundColor: c.inverse,
    },
    useDisabled: { backgroundColor: c.fill },
    useLabel: {
      fontFamily: font.sansBold,
      fontSize: size.body,
      color: c.onInverse,
    },
    useLabelDisabled: { color: c.faint },
  });
}
