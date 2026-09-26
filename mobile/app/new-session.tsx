import Feather from "@expo/vector-icons/Feather";
import { useRouter } from "expo-router";
import { useEffect, useState } from "react";
import {
  ActivityIndicator,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { AgentIcon } from "../components/AgentIcon";
import { ContentColumn } from "../components/ContentColumn";
import { MotionPressable } from "../components/MotionPressable";
import { useStyles, useTheme } from "../lib/appearance";
import { useStore } from "../lib/store";
import { font, Palette, radius, size, space } from "../lib/theme";

type LaunchKind = "claude" | "codex" | "cursor-cli" | "opencode";
const agents: { kind: LaunchKind; label: string }[] = [
  { kind: "claude", label: "Claude Code" },
  { kind: "codex", label: "Codex" },
  { kind: "cursor-cli", label: "Cursor CLI" },
  { kind: "opencode", label: "OpenCode" },
];

export default function NewSession() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [kind, setKind] = useState<LaunchKind>("claude");
  const [path, setPath] = useState("");
  const [directories, setDirectories] = useState<string[]>([]);
  const [loadingDirectories, setLoadingDirectories] = useState(false);
  const [prompt, setPrompt] = useState("Wait for my next instruction.");
  const [starting, setStarting] = useState(false);
  const [launchedId, setLaunchedId] = useState<string | null>(null);
  const [slow, setSlow] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!store.daemonOnline) return;
    let live = true;
    setLoadingDirectories(true);
    setError("");
    void store.listDirectories(path)
      .then((names) => { if (live) setDirectories(names); })
      .catch((err: Error) => { if (live) setError(err.message); })
      .finally(() => { if (live) setLoadingDirectories(false); });
    return () => { live = false; };
    // Only a path or connection change should re-read; store methods change
    // identity as session state updates while this screen is open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, store.daemonOnline]);

  useEffect(() => {
    if (!launchedId) return;
    if (store.sessions.some((session) => session.id === launchedId)) {
      router.replace(`/session/${encodeURIComponent(launchedId)}`);
    }
  }, [launchedId, store.sessions, router]);

  useEffect(() => {
    if (!launchedId) return;
    const timer = setTimeout(() => setSlow(true), 20_000);
    return () => clearTimeout(timer);
  }, [launchedId]);

  const up = () => setPath((current) => current.split("/").slice(0, -1).join("/"));
  const start = async () => {
    if (!path || !prompt.trim() || starting || !store.daemonOnline) return;
    setError("");
    setStarting(true);
    try {
      const id = await store.startSession(kind, path, prompt.trim());
      setLaunchedId(id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start the agent.");
      setStarting(false);
    }
  };

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <ScrollView contentContainerStyle={{ paddingBottom: insets.bottom + space.xxl }}>
        <ContentColumn style={styles.content}>
          <MotionPressable onPress={() => router.back()} style={styles.back}
            accessibilityRole="button" accessibilityLabel="Back" hitSlop={12}>
            <Feather name="chevron-left" size={21} color={color.text} />
          </MotionPressable>
          <Text style={styles.title} accessibilityRole="header">New session</Text>
          <Text style={styles.subtitle}>Start an agent on your Mac.</Text>

          <Text style={styles.label}>Agent</Text>
          <View style={styles.agentGrid}>
            {agents.map((agent) => (
              <MotionPressable key={agent.kind} onPress={() => setKind(agent.kind)}
                style={[styles.agentOption, kind === agent.kind && styles.selected]}
                accessibilityRole="button" accessibilityState={{ selected: kind === agent.kind }}>
                <AgentIcon kind={agent.kind} size={23} />
                <Text style={styles.agentName}>{agent.label}</Text>
                {kind === agent.kind ? <Feather name="check" size={17} color={color.working} /> : null}
              </MotionPressable>
            ))}
          </View>

          <Text style={styles.label}>Folder on your Mac</Text>
          <View style={styles.pathBar}>
            {path ? <MotionPressable onPress={up} hitSlop={10} style={styles.up}
              accessibilityRole="button" accessibilityLabel="Parent folder">
              <Feather name="arrow-left" size={17} color={color.text} />
            </MotionPressable> : null}
            <Text style={styles.path} numberOfLines={2}>~{path ? `/${path}` : ""}</Text>
          </View>
          <View style={styles.folderList}>
            {loadingDirectories ? <ActivityIndicator style={styles.spinner} color={color.working} /> : null}
            {!loadingDirectories && directories.length === 0 ? (
              <Text style={styles.empty}>No folders here</Text>
            ) : null}
            {!loadingDirectories && directories.map((name) => (
              <MotionPressable key={name} onPress={() => setPath(path ? `${path}/${name}` : name)}
                style={styles.folder} accessibilityRole="button" accessibilityLabel={`Open ${name}`}>
                <Feather name="folder" size={18} color={color.muted} />
                <Text style={styles.folderName} numberOfLines={1}>{name}</Text>
                <Feather name="chevron-right" size={17} color={color.faint} />
              </MotionPressable>
            ))}
          </View>

          <Text style={styles.label}>First message</Text>
          <TextInput value={prompt} onChangeText={setPrompt} multiline
            style={styles.prompt} placeholder="What should the agent do?"
            placeholderTextColor={color.faint} textAlignVertical="top"
            accessibilityLabel="First message" />
          <Text style={styles.hint}>This message starts the agent’s conversation.</Text>

          {error ? <Text style={styles.error} accessibilityRole="alert">{error}</Text> : null}
          {launchedId ? (
            <View style={styles.status}>
              <ActivityIndicator color={color.working} />
              <Text style={styles.statusText}>
                {slow ? "Started on your Mac. If it stays here, check for a sign-in or trust prompt." :
                  "Started on your Mac. Waiting for the session…"}
              </Text>
            </View>
          ) : (
            <MotionPressable onPress={() => void start()}
              style={[styles.start, (!path || !prompt.trim() || starting || !store.daemonOnline) && styles.disabled]}
              disabled={!path || !prompt.trim() || starting || !store.daemonOnline}
              accessibilityRole="button" accessibilityLabel="Start session">
              {starting ? <ActivityIndicator color={color.onInverse} /> :
                <Text style={styles.startText}>Start session</Text>}
            </MotionPressable>
          )}
        </ContentColumn>
      </ScrollView>
    </View>
  );
}

const makeStyles = (c: Palette) => StyleSheet.create({
  page: { flex: 1, backgroundColor: c.paper },
  content: { paddingHorizontal: space.lg },
  back: { marginTop: space.md, width: 40, height: 40, borderRadius: radius.pill,
    backgroundColor: c.surface, borderWidth: 1, borderColor: c.line,
    alignItems: "center", justifyContent: "center" },
  title: { marginTop: space.lg, color: c.text, fontFamily: font.sansBold,
    fontSize: size.display, letterSpacing: -1.2 },
  subtitle: { marginTop: space.xs, color: c.muted, fontFamily: font.sans,
    fontSize: size.body },
  label: { marginTop: space.xl, marginBottom: space.sm, color: c.text,
    fontFamily: font.sansBold, fontSize: size.label },
  agentGrid: { gap: space.sm },
  agentOption: { minHeight: 54, flexDirection: "row", alignItems: "center", gap: space.md,
    paddingHorizontal: space.md, borderRadius: radius.lg, backgroundColor: c.surface,
    borderWidth: 1, borderColor: c.line },
  selected: { borderColor: c.working },
  agentName: { flex: 1, fontFamily: font.sansMedium, fontSize: size.body, color: c.text },
  pathBar: { minHeight: 48, flexDirection: "row", alignItems: "center", gap: space.sm,
    paddingHorizontal: space.md, borderRadius: radius.lg, backgroundColor: c.fill },
  up: { padding: 6 },
  path: { flex: 1, fontFamily: font.mono, fontSize: size.label, color: c.text },
  folderList: { marginTop: space.sm, borderRadius: radius.lg, backgroundColor: c.surface,
    borderWidth: 1, borderColor: c.line, overflow: "hidden" },
  folder: { minHeight: 48, flexDirection: "row", alignItems: "center", gap: space.sm,
    paddingHorizontal: space.md, borderBottomWidth: StyleSheet.hairlineWidth, borderColor: c.line },
  folderName: { flex: 1, fontFamily: font.sans, fontSize: size.body, color: c.text },
  empty: { padding: space.md, fontFamily: font.sans, color: c.muted },
  spinner: { margin: space.lg },
  prompt: { minHeight: 110, padding: space.md, borderRadius: radius.lg,
    borderWidth: 1, borderColor: c.line, backgroundColor: c.surface,
    fontFamily: font.sans, fontSize: size.body, color: c.text },
  hint: { marginTop: space.xs, fontFamily: font.sans, fontSize: size.caption, color: c.muted },
  error: { marginTop: space.md, fontFamily: font.sans, fontSize: size.label, color: c.errorText },
  status: { marginTop: space.lg, flexDirection: "row", alignItems: "center", gap: space.md },
  statusText: { flex: 1, fontFamily: font.sans, fontSize: size.label, color: c.muted },
  start: { marginTop: space.xl, minHeight: 52, borderRadius: radius.pill,
    backgroundColor: c.inverse, alignItems: "center", justifyContent: "center" },
  disabled: { opacity: 0.45 },
  startText: { color: c.onInverse, fontFamily: font.sansBold, fontSize: size.body },
});
