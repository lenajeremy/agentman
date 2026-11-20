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
import type { Worktree } from "../lib/protocol";
import { hostName, useFleet } from "../lib/fleet";
import { useStore } from "../lib/store";
import { font, Palette, radius, size, space } from "../lib/theme";
import { edged, eyebrow, hardShadow, screenTitle } from "../components/Brand";

type LaunchKind = "claude" | "codex" | "cursor-cli" | "opencode" | "kiro" | "antigravity" | "muse";
const agents: { kind: LaunchKind; label: string }[] = [
  { kind: "claude", label: "Claude Code" },
  { kind: "codex", label: "Codex" },
  { kind: "cursor-cli", label: "Cursor CLI" },
  { kind: "opencode", label: "OpenCode" },
  { kind: "kiro", label: "Kiro CLI" },
  { kind: "antigravity", label: "Antigravity" },
  { kind: "muse", label: "Muse" },
];

export default function NewSession() {
  const activeStore = useStore();
  const fleet = useFleet();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [hostId, setHostId] = useState(
    () =>
      fleet.filter ??
      fleet.hosts.find((host) => host.id === fleet.active && host.store.daemonOnline)?.id ??
      fleet.hosts.find((host) => host.store.daemonOnline)?.id ??
      fleet.active,
  );
  const targetIndex = Math.max(0, fleet.hosts.findIndex((host) => host.id === hostId));
  const targetHost = fleet.hosts[targetIndex] ?? fleet.hosts[0];
  const store = targetHost?.store ?? activeStore;
  const targetId = targetHost?.id ?? fleet.active;
  const targetName = targetHost
    ? hostName(targetHost, targetIndex)
    : store.daemonInfo?.deviceName || "this computer";

  const [kind, setKind] = useState<LaunchKind>("claude");
  const [path, setPath] = useState("");
  const [directories, setDirectories] = useState<string[]>([]);
  const [loadingDirectories, setLoadingDirectories] = useState(false);
  const [prompt, setPrompt] = useState("Wait for my next instruction.");
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState("");
  // Naming a new folder happens inline rather than in a system prompt:
  // Alert.prompt is iOS-only, and a row that expands keeps the list as the
  // one place folders are chosen.
  const [naming, setNaming] = useState(false);
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);

  const selectHost = (nextId: string) => {
    if (nextId === hostId) return;
    setHostId(nextId);
    setPath("");
    setDirectories([]);
    setTrees([]);
    setDestination("");
    setBranchName("");
    setNaming(false);
    setNewName("");
    setError("");
  };

  useEffect(() => {
    if (!store.daemonOnline) {
      setDirectories([]);
      setLoadingDirectories(false);
      return;
    }
    let live = true;
    setLoadingDirectories(true);
    setError("");
    void store.listDirectories(path)
      .then((listing) => { if (live) setDirectories(listing.names); })
      .catch((err: Error) => { if (live) setError(err.message); })
      .finally(() => { if (live) setLoadingDirectories(false); });
    return () => { live = false; };
    // Only a path, host, or connection change should re-read; store methods
    // change identity as session state updates while this screen is open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, targetId, store.daemonOnline]);

  const [trees, setTrees] = useState<Worktree[]>([]);
  const [destination, setDestination] = useState("");
  const [branchName, setBranchName] = useState("");
  useEffect(() => {
    let current = true;
    setTrees([]);
    setDestination("");
    setBranchName("");
    if (path && store.daemonOnline) {
      void store.worktrees(path).then(rows => { if (current) setTrees(rows); }).catch(() => {});
    }
    return () => { current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, targetId, store.daemonOnline, store.credentials]);

  const up = () => setPath((current) => current.split("/").slice(0, -1).join("/"));

  const createFolder = async () => {
    const name = newName.trim();
    if (!name || creating) return;
    setError("");
    setCreating(true);
    try {
      const listing = await store.createDirectory(path ? `${path}/${name}` : name);
      setDirectories(listing.names);
      setNaming(false);
      setNewName("");
      // Straight into it: naming a folder is how you say where the agent
      // should work, so stopping at its parent would be half the action.
      setPath((current) => (current ? `${current}/${name}` : name));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create that folder.");
    } finally {
      setCreating(false);
    }
  };
  const start = async () => {
    if (!path || !prompt.trim() || (destination === ":new" && !branchName.trim()) || starting || !store.daemonOnline) return;
    setError("");
    setStarting(true);
    try {
      let target = destination || path;
      if (destination === ":new") {
        const [tree] = await store.worktrees(path, branchName.trim());
        if (!tree?.launchPath) throw new Error("Update Agentman on this computer to launch in a worktree.");
        target = tree.launchPath;
        setTrees(rows => [...rows, tree]);
        setDestination(target);
      }
      if (targetId) fleet.select(targetId);
      const id = await store.startSession(kind, target, prompt.trim());
      router.replace({ pathname: "/session/[id]", params: { id, device: targetId } });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start the agent.");
      setStarting(false);
    }
  };

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <ScrollView contentContainerStyle={{ paddingBottom: insets.bottom + space.xxl }}>
        <View pointerEvents={starting ? "none" : "auto"}><ContentColumn style={styles.content}>
          <MotionPressable onPress={() => router.back()} style={styles.back}
            accessibilityRole="button" accessibilityLabel="Back" hitSlop={12}>
            <Feather name="chevron-left" size={21} color={color.text} />
          </MotionPressable>
          <Text style={styles.title} accessibilityRole="header">New session</Text>
          <Text style={styles.subtitle}>Start an agent on {targetName}.</Text>

          {fleet.hosts.length > 1 ? (
            <>
              <Text style={styles.label}>Computer</Text>
              <View style={styles.agentGrid}>
                {fleet.hosts.map((host, index) => {
                  const name = hostName(host, index);
                  const chosen = host.id === targetId;
                  const online = host.store.daemonOnline;
                  return (
                    <MotionPressable
                      key={host.id}
                      onPress={() => selectHost(host.id)}
                      style={[
                        styles.agentOption,
                        chosen && styles.selected,
                        !online && styles.hostOffline,
                      ]}
                      accessibilityRole="button"
                      accessibilityState={{ selected: chosen, disabled: !online }}
                      accessibilityLabel={`${name}, ${online ? "Online" : "Offline"}`}
                    >
                      <Feather name="monitor" size={19} color={online ? color.text : color.muted} />
                      <View style={styles.hostCopy}>
                        <Text style={styles.agentName} numberOfLines={1}>{name}</Text>
                        <Text style={styles.hostStatus}>{online ? "Online" : "Offline"}</Text>
                      </View>
                      {chosen ? <Feather name="check" size={17} color={color.working} /> : null}
                    </MotionPressable>
                  );
                })}
              </View>
            </>
          ) : null}

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

          <Text style={styles.label}>
            {fleet.hosts.length > 1 ? `Folder on ${targetName}` : "Folder on your Mac"}
          </Text>
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
            {naming ? (
              <View style={styles.folder}>
                <Feather name="folder-plus" size={18} color={color.working} />
                <TextInput value={newName} onChangeText={setNewName} autoFocus
                  style={styles.newFolderInput} placeholder="Folder name"
                  placeholderTextColor={color.faint} autoCapitalize="none"
                  autoCorrect={false} returnKeyType="done"
                  onSubmitEditing={() => void createFolder()}
                  accessibilityLabel="New folder name" />
                {creating ? <ActivityIndicator size="small" color={color.working} /> : (
                  <MotionPressable onPress={() => void createFolder()} hitSlop={10}
                    disabled={!newName.trim()}
                    accessibilityRole="button" accessibilityLabel="Create folder">
                    <Feather name="check" size={18}
                      color={newName.trim() ? color.working : color.faint} />
                  </MotionPressable>
                )}
                <MotionPressable onPress={() => { setNaming(false); setNewName(""); }} hitSlop={10}
                  accessibilityRole="button" accessibilityLabel="Cancel">
                  <Feather name="x" size={17} color={color.muted} />
                </MotionPressable>
              </View>
            ) : (
              <MotionPressable onPress={() => setNaming(true)} style={styles.folder}
                disabled={!store.daemonOnline}
                accessibilityRole="button" accessibilityLabel="New folder">
                <Feather name="folder-plus" size={18} color={color.muted} />
                <Text style={[styles.folderName, styles.newFolder]}>New folder</Text>
              </MotionPressable>
            )}
          </View>

          {trees.length > 0 && <View>
            <Text style={styles.label}>Worktree</Text>
            <View style={styles.folderList}>
              {[{ value: "", label: "Selected folder" },
                ...trees.filter(t => t.launchPath && t.launchPath !== path).map(t => ({
                  value: t.launchPath!, label: t.branch || t.name,
                })),
                ...(trees.some(t => t.launchPath) ? [{ value: ":new", label: "New worktree…" }] : [])
              ].map(option => <MotionPressable key={option.value} style={styles.folder}
                disabled={starting} accessibilityRole="radio" accessibilityState={{ checked: destination === option.value }}
                onPress={() => setDestination(option.value)}>
                <Text style={styles.folderName}>{option.label}</Text>
                {destination === option.value && <Feather name="check" size={18} color={color.working} />}
              </MotionPressable>)}
              {destination === ":new" && <View style={styles.folder}><TextInput style={styles.newFolderInput}
                accessibilityLabel="New worktree branch name" placeholder="Branch name" placeholderTextColor={color.faint}
                value={branchName} onChangeText={setBranchName} autoCapitalize="none" autoCorrect={false} /></View>}
              {!trees.some(t => t.launchPath) && <Text style={styles.hint}>Update Agentman on this computer to choose worktrees.</Text>}
            </View>
          </View>}
          <Text style={styles.label}>First message</Text>
          <TextInput value={prompt} onChangeText={setPrompt} multiline
            style={styles.prompt} placeholder="What should the agent do?"
            placeholderTextColor={color.faint} textAlignVertical="top"
            accessibilityLabel="First message" />
          <Text style={styles.hint}>This message starts the agent’s conversation.</Text>

          {error ? <Text style={styles.error} accessibilityRole="alert">{error}</Text> : null}
          <MotionPressable onPress={() => void start()}
            style={[styles.start, (!path || !prompt.trim() || (destination === ":new" && !branchName.trim()) || starting || !store.daemonOnline) && styles.disabled]}
            disabled={!path || !prompt.trim() || (destination === ":new" && !branchName.trim()) || starting || !store.daemonOnline}
            accessibilityRole="button" accessibilityLabel="Start session">
            {starting ? <ActivityIndicator color={color.onInverse} /> :
              <Text style={styles.startText}>Start session</Text>}
          </MotionPressable>
        </ContentColumn></View>
      </ScrollView>
    </View>
  );
}

const makeSiteStyles = (c: Palette) => StyleSheet.create({
  page: { flex: 1, backgroundColor: c.paper },
  content: { paddingHorizontal: space.lg },
  back: { marginTop: space.md, width: 40, height: 40, borderRadius: radius.pill,
    backgroundColor: c.surface, ...edged(c, 2),
    alignItems: "center", justifyContent: "center" },
  title: { marginTop: space.lg, ...screenTitle(c) },
  subtitle: { marginTop: space.xs, color: c.muted, ...font.sans,
    fontSize: size.body },
  label: { marginTop: space.xl, marginBottom: space.sm, ...eyebrow(c) },
  agentGrid: { gap: space.sm },
  agentOption: { minHeight: 54, flexDirection: "row", alignItems: "center", gap: space.md,
    paddingHorizontal: space.md, borderRadius: radius.lg, backgroundColor: c.surface,
    ...edged(c) },
  // The chosen agent: the site's cream, lifted on its hard shadow.
  selected: { backgroundColor: c.cream, ...edged(c, 3) },
  hostOffline: { opacity: 0.6 },
  hostCopy: { flex: 1, minWidth: 0, gap: 2 },
  hostStatus: { ...font.sans, fontSize: size.label, color: c.muted },
  agentName: { flex: 1, ...font.sansMedium, fontSize: size.body, color: c.text },
  pathBar: { minHeight: 48, flexDirection: "row", alignItems: "center", gap: space.sm,
    paddingHorizontal: space.md, borderRadius: radius.lg, backgroundColor: c.fill },
  up: { padding: 6 },
  path: { flex: 1, ...font.mono, fontSize: size.label, color: c.text },
  folderList: { marginTop: space.sm, borderRadius: radius.lg, backgroundColor: c.surface,
    ...edged(c), overflow: "hidden" },
  folder: { minHeight: 48, flexDirection: "row", alignItems: "center", gap: space.sm,
    paddingHorizontal: space.md, borderBottomWidth: StyleSheet.hairlineWidth, borderColor: c.line },
  folderName: { flex: 1, ...font.sans, fontSize: size.body, color: c.text },
  empty: { padding: space.md, ...font.sans, color: c.muted },
  newFolder: { color: c.muted },
  newFolderInput: { flex: 1, paddingVertical: space.sm, ...font.mono,
    fontSize: size.body, color: c.text },
  spinner: { margin: space.lg },
  prompt: { minHeight: 110, padding: space.md, borderRadius: radius.lg,
    ...edged(c), backgroundColor: c.surface,
    ...font.sans, fontSize: size.body, color: c.text },
  hint: { marginTop: space.xs, ...font.sans, fontSize: size.caption, color: c.muted },
  error: { marginTop: space.md, ...font.sans, fontSize: size.label, color: c.errorText },
  start: { marginTop: space.xl, minHeight: 52, borderRadius: 14,
    backgroundColor: c.inverse,
    ...hardShadow(c.inverseShadow), alignItems: "center", justifyContent: "center" },
  disabled: { opacity: 0.45 },
  startText: { color: c.onInverse, ...font.sansBold, fontSize: size.body },
});

const makeClassicStyles = (c: Palette) => StyleSheet.create({
  page: { flex: 1, backgroundColor: c.paper },
  content: { paddingHorizontal: space.lg },
  back: { marginTop: space.md, width: 40, height: 40, borderRadius: radius.pill,
    backgroundColor: c.surface, borderWidth: 1, borderColor: c.line,
    alignItems: "center", justifyContent: "center" },
  title: { marginTop: space.lg, color: c.text, ...font.sansBold,
    fontSize: size.display, letterSpacing: -1.2 },
  subtitle: { marginTop: space.xs, color: c.muted, ...font.sans,
    fontSize: size.body },
  label: { marginTop: space.xl, marginBottom: space.sm, color: c.text,
    ...font.sansBold, fontSize: size.label },
  agentGrid: { gap: space.sm },
  agentOption: { minHeight: 54, flexDirection: "row", alignItems: "center", gap: space.md,
    paddingHorizontal: space.md, borderRadius: radius.lg, backgroundColor: c.surface,
    borderWidth: 1, borderColor: c.line },
  selected: { borderColor: c.working },
  hostOffline: { opacity: 0.6 },
  hostCopy: { flex: 1, minWidth: 0, gap: 2 },
  hostStatus: { ...font.sans, fontSize: size.label, color: c.muted },
  agentName: { flex: 1, ...font.sansMedium, fontSize: size.body, color: c.text },
  pathBar: { minHeight: 48, flexDirection: "row", alignItems: "center", gap: space.sm,
    paddingHorizontal: space.md, borderRadius: radius.lg, backgroundColor: c.fill },
  up: { padding: 6 },
  path: { flex: 1, ...font.mono, fontSize: size.label, color: c.text },
  folderList: { marginTop: space.sm, borderRadius: radius.lg, backgroundColor: c.surface,
    borderWidth: 1, borderColor: c.line, overflow: "hidden" },
  folder: { minHeight: 48, flexDirection: "row", alignItems: "center", gap: space.sm,
    paddingHorizontal: space.md, borderBottomWidth: StyleSheet.hairlineWidth, borderColor: c.line },
  folderName: { flex: 1, ...font.sans, fontSize: size.body, color: c.text },
  empty: { padding: space.md, ...font.sans, color: c.muted },
  newFolder: { color: c.muted },
  newFolderInput: { flex: 1, paddingVertical: space.sm, ...font.mono,
    fontSize: size.body, color: c.text },
  spinner: { margin: space.lg },
  prompt: { minHeight: 110, padding: space.md, borderRadius: radius.lg,
    borderWidth: 1, borderColor: c.line, backgroundColor: c.surface,
    ...font.sans, fontSize: size.body, color: c.text },
  hint: { marginTop: space.xs, ...font.sans, fontSize: size.caption, color: c.muted },
  error: { marginTop: space.md, ...font.sans, fontSize: size.label, color: c.errorText },
  start: { marginTop: space.xl, minHeight: 52, borderRadius: radius.pill,
    backgroundColor: c.inverse, alignItems: "center", justifyContent: "center" },
  disabled: { opacity: 0.45 },
  startText: { color: c.onInverse, ...font.sansBold, fontSize: size.body },
});

// Today's styles for the Classic theme, the website's look's above them
// (see lib/theme: a palette's look). Keys only the new look has keep its
// values, for what only the new look shows.
type Styles = ReturnType<typeof makeSiteStyles> & ReturnType<typeof makeClassicStyles>;
const makeStyles = (c: Palette): Styles =>
  (c.look === "site" ? makeSiteStyles(c) : { ...makeSiteStyles(c), ...makeClassicStyles(c) }) as Styles;

