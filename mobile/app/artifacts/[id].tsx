import Feather from "@expo/vector-icons/Feather";
import { useFocusEffect, useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useMemo, useState } from "react";
import { ActivityIndicator, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { ContentColumn } from "../../components/ContentColumn";
import { MotionPressable } from "../../components/MotionPressable";
import { useStyles, useTheme } from "../../lib/appearance";
import { artifactKind, artifactTitle, sortArtifacts } from "../../lib/artifacts";
import { Artifact } from "../../lib/protocol";
import { useStore } from "../../lib/store";
import { agentLabel, ago, font, Palette, radius, size, space } from "../../lib/theme";

/**
 * The documents one agent wrote for you apart from the conversation: an
 * implementation plan, a task list, a walkthrough, a screenshot.
 *
 * A plan is what you approve away from the desk, so the ones waiting on you
 * sort to the top with a pill that says so, and opening one leads to the
 * buttons that answer it.
 */
export default function ArtifactsScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const sessionId = decodeURIComponent(String(id));
  const store = useStore();
  const session = store.findSession(sessionId);
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [artifacts, setArtifacts] = useState<Artifact[] | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);

  // Asked again on every return to this screen and whenever the session's
  // own counts move: coming back from approving a plan, or the agent writing
  // a new one, should show without a pull.
  const counts = `${session?.artifacts ?? 0}:${session?.artifactsToReview ?? 0}`;
  useFocusEffect(
    useCallback(() => {
      let current = true;
      setLoading(true);
      void store
        .listArtifacts(sessionId)
        .then((list) => {
          if (!current) return;
          setArtifacts(list);
          setError("");
        })
        .catch((reason) => {
          if (current) setError(reason instanceof Error ? reason.message : String(reason));
        })
        .finally(() => {
          if (current) setLoading(false);
        });
      return () => {
        current = false;
      };
      // Keyed to the route, a refresh and the counts — not to the store's
      // identity, which changes with every session update.
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [sessionId, refresh, counts]),
  );

  const sorted = useMemo(() => sortArtifacts(artifacts ?? []), [artifacts]);
  const waiting = sorted.filter((artifact) => artifact.review).length;
  const agent = session ? agentLabel(session.kind).name : "The agent";
  const project = session?.cwd.split("/").at(-1) || sessionId;

  const open = (artifact: Artifact) =>
    router.push(
      `/artifacts/view?session=${encodeURIComponent(sessionId)}&name=${encodeURIComponent(artifact.name)}`,
    );

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <ContentColumn style={styles.header}>
        <MotionPressable
          onPress={() => router.back()}
          style={styles.iconButton}
          accessibilityRole="button"
          accessibilityLabel="Back to session"
        >
          <Feather name="chevron-left" size={20} color={color.text} />
        </MotionPressable>
        <View style={styles.heading}>
          <Text style={styles.title} numberOfLines={1} accessibilityRole="header">
            Artifacts
          </Text>
          <Text style={styles.subtitle} numberOfLines={1}>
            {project}
            {sorted.length > 0 ? ` · ${sorted.length}` : ""}
            {waiting > 0 ? ` · ${waiting} to review` : ""}
          </Text>
        </View>
        <MotionPressable
          onPress={() => setRefresh((value) => value + 1)}
          style={styles.iconButton}
          accessibilityRole="button"
          accessibilityLabel="Refresh artifacts"
        >
          {loading && artifacts !== null ? (
            <ActivityIndicator size="small" color={color.muted} />
          ) : (
            <Feather name="refresh-cw" size={17} color={color.text} />
          )}
        </MotionPressable>
      </ContentColumn>

      <ScrollView contentContainerStyle={{ paddingBottom: insets.bottom + space.xl }}>
        <ContentColumn style={styles.content}>
          {loading && artifacts === null ? (
            <ActivityIndicator style={styles.center} color={color.muted} />
          ) : null}
          {error ? <Text style={styles.error}>{error}</Text> : null}

          {artifacts !== null && sorted.length === 0 && !error ? (
            <View style={styles.emptyWrap}>
              <Feather name="file-text" size={22} color={color.faint} />
              <Text style={styles.empty}>{agent} has not written any artifacts here yet.</Text>
            </View>
          ) : null}

          {sorted.length > 0 ? (
            <View style={styles.list}>
              {sorted.map((artifact, index) => (
                <ArtifactRow
                  key={artifact.name}
                  artifact={artifact}
                  divided={index > 0}
                  onPress={() => open(artifact)}
                />
              ))}
            </View>
          ) : null}

          {sorted.length > 0 ? (
            <Text style={styles.note}>
              Documents {agent} wrote for you. One marked for review waits until you approve
              it or ask for changes.
            </Text>
          ) : null}
        </ContentColumn>
      </ScrollView>
    </View>
  );
}

function ArtifactRow({
  artifact,
  divided,
  onPress,
}: {
  artifact: Artifact;
  divided: boolean;
  onPress(): void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const kind = artifactKind(artifact.kind);
  const title = artifactTitle(artifact);
  const when = ago(artifact.updatedAt);

  return (
    <MotionPressable
      onPress={onPress}
      style={[styles.row, divided && styles.rowDivided]}
      pressedScale={0.995}
      accessibilityRole="button"
      accessibilityLabel={`${title}, ${kind.label}${artifact.review ? ", needs your review" : ""}`}
    >
      <View style={[styles.kindTile, artifact.review && styles.kindTileReview]}>
        <Feather
          name={kind.icon}
          size={15}
          color={artifact.review ? color.needsYouText : color.muted}
        />
      </View>
      <View style={styles.rowBody}>
        <View style={styles.rowTop}>
          <Text style={styles.rowTitle} numberOfLines={1}>
            {title}
          </Text>
          {when ? <Text style={styles.rowTime}>{when}</Text> : null}
        </View>
        {artifact.summary ? (
          <Text style={styles.rowSummary} numberOfLines={2}>
            {artifact.summary}
          </Text>
        ) : null}
        <View style={styles.rowMeta}>
          {artifact.review ? (
            <View style={styles.reviewPill}>
              <View style={styles.reviewDot} />
              <Text style={styles.reviewLabel}>Needs review</Text>
            </View>
          ) : null}
          {/* The heading says what it is about; the file name is what the
              agent and the Mac call it, so it stays findable from either. */}
          <Text style={styles.rowName} numberOfLines={1}>
            {title === artifact.name ? kind.label : artifact.name}
          </Text>
        </View>
      </View>
      <Feather name="chevron-right" size={14} color={color.faint} />
    </MotionPressable>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    header: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingHorizontal: space.lg,
      paddingTop: space.sm,
      paddingBottom: space.md,
    },
    heading: { flex: 1, minWidth: 0 },
    title: {
      color: c.text,
      fontFamily: font.sansBold,
      fontSize: size.title,
      letterSpacing: -0.3,
    },
    subtitle: { color: c.muted, fontFamily: font.mono, fontSize: 11, marginTop: 1 },
    iconButton: {
      width: 36,
      height: 36,
      alignItems: "center",
      justifyContent: "center",
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },

    content: { paddingHorizontal: space.lg, paddingTop: space.xs },
    // One surface with hairlines, as the workspace lists are: these are rows
    // of one list, not cards of their own.
    list: {
      borderWidth: 1,
      borderColor: c.line,
      borderRadius: radius.lg,
      backgroundColor: c.surface,
      overflow: "hidden",
    },
    row: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      minHeight: 64,
      paddingHorizontal: space.md,
      paddingVertical: space.md,
    },
    rowDivided: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: c.line },
    kindTile: {
      width: 32,
      height: 32,
      borderRadius: radius.sm,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
      alignSelf: "flex-start",
    },
    kindTileReview: { backgroundColor: c.needsYouWash },
    rowBody: { flex: 1, minWidth: 0, gap: 3 },
    rowTop: { flexDirection: "row", alignItems: "baseline", gap: space.sm },
    rowTitle: {
      flex: 1,
      color: c.text,
      fontFamily: font.sansMedium,
      fontSize: size.body,
    },
    rowTime: { color: c.faint, fontFamily: font.mono, fontSize: 11 },
    rowSummary: {
      color: c.muted,
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 18,
    },
    rowMeta: { flexDirection: "row", alignItems: "center", gap: space.sm, marginTop: 1 },
    rowName: { flexShrink: 1, color: c.faint, fontFamily: font.mono, fontSize: 11 },
    // The same pill a question wears: the agent is waiting on you.
    reviewPill: {
      flexDirection: "row",
      alignItems: "center",
      gap: 5,
      height: 20,
      paddingHorizontal: space.sm,
      borderRadius: radius.pill,
      backgroundColor: c.needsYouWash,
    },
    reviewDot: { width: 6, height: 6, borderRadius: 3, backgroundColor: c.needsYou },
    reviewLabel: { color: c.needsYouText, fontFamily: font.sansBold, fontSize: 11 },

    emptyWrap: { alignItems: "center", gap: space.sm, paddingVertical: space.xxl },
    empty: {
      color: c.muted,
      fontFamily: font.sans,
      fontSize: size.caption,
      textAlign: "center",
    },
    error: {
      color: c.errorText,
      fontFamily: font.sans,
      fontSize: size.body,
      paddingVertical: space.md,
    },
    center: { marginTop: space.xl },
    note: {
      color: c.faint,
      fontFamily: font.sans,
      fontSize: 11.5,
      marginTop: space.md,
      lineHeight: 16,
    },
  });
