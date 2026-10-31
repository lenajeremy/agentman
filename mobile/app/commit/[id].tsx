import Feather from "@expo/vector-icons/Feather";
import * as Clipboard from "expo-clipboard";
import * as Haptics from "expo-haptics";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { ChangeList } from "../../components/ChangeList";
import { CommitSize } from "../../components/CommitList";
import { ContentColumn } from "../../components/ContentColumn";
import { MotionPressable } from "../../components/MotionPressable";
import { useStyles, useTheme } from "../../lib/appearance";
import {
  commitDate,
  initials,
  shortHash,
  splitTrailers,
  trailerValue,
  unwrapMessage,
} from "../../lib/commits";
import { WorkspaceResult } from "../../lib/protocol";
import { useStore } from "../../lib/store";
import { font, Palette, radius, size, space, scaled } from "../../lib/theme";

/** The message's line height, and where a long one is cut: six lines is most
 *  messages whole, and leaves the files on the first screen of a long one. */
const BODY_LINE = 22;
const BODY_CUT = BODY_LINE * 6;
/** Cut only when that hides a line and a half or more. "Read more" that
 *  reveals one line is a tap for nothing. */
const BODY_CUT_FROM = BODY_CUT + BODY_LINE * 1.5;
/** Opacity of each band of the fade over a cut message, top to bottom. */
const FADE_BANDS = [0, 0.15, 0.35, 0.6, 0.82, 0.95];

/**
 * One commit: what it says, who wrote it and when, and the files it changed.
 * A file opens its diff within this commit, against the commit's parent.
 */
export default function CommitScreen() {
  const { id, sha } = useLocalSearchParams<{ id: string; sha: string }>();
  const sessionId = decodeURIComponent(String(id));
  const hash = typeof sha === "string" ? sha : "";
  const store = useStore();
  const session = store.findSession(sessionId);
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [result, setResult] = useState<WorkspaceResult | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0);
  const [copied, setCopied] = useState(false);
  // How tall the whole message is at this width, and whether it is shown
  // whole.
  const [bodyHeight, setBodyHeight] = useState(0);
  const [expanded, setExpanded] = useState(false);
  const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (copiedTimer.current) clearTimeout(copiedTimer.current);
    },
    [],
  );

  // A commit never changes, so there is nothing to refresh; a failed load is
  // retried from the error itself.
  useEffect(() => {
    let current = true;
    setLoading(true);
    setError("");
    void store
      .workspace(sessionId, "commit_files", "", undefined, { commit: hash })
      .then((value) => {
        if (current) setResult(value);
      })
      .catch((reason) => {
        if (current)
          setError(reason instanceof Error ? reason.message : String(reason));
      })
      .finally(() => {
        if (current) setLoading(false);
      });
    return () => {
      current = false;
    };
    // The request is keyed to route and retry, not to store's changing snapshot.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, hash, attempt]);

  const copyHash = () => {
    void Clipboard.setStringAsync(hash);
    if (Platform.OS !== "web") void Haptics.selectionAsync().catch(() => {});
    setCopied(true);
    if (copiedTimer.current) clearTimeout(copiedTimer.current);
    copiedTimer.current = setTimeout(() => setCopied(false), 1600);
  };

  const commit = result?.commit;
  const changes = result?.changes ?? [];
  const hidden = result?.hidden ?? 0;
  const message = splitTrailers(commit?.body ?? "");
  const body = unwrapMessage(message.text);
  const cut = bodyHeight > BODY_CUT_FROM;
  const files = changes.length;
  const added = commit?.added ?? 0;
  const removed = commit?.removed ?? 0;

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <ContentColumn style={styles.header}>
        <MotionPressable
          onPress={() => router.back()}
          style={styles.iconButton}
          accessibilityRole="button"
          accessibilityLabel="Back to commits"
        >
          <Feather name="chevron-left" size={20} color={color.text} />
        </MotionPressable>
        <View style={styles.heading}>
          <Text style={styles.title} accessibilityRole="header">
            {commit?.merge ? "Merge commit" : "Commit"}
          </Text>
          {session ? (
            <Text style={styles.subtitle} numberOfLines={1}>
              {session.cwd.split("/").at(-1)}
            </Text>
          ) : null}
        </View>
      </ContentColumn>

      <ScrollView
        contentContainerStyle={{ paddingBottom: insets.bottom + space.xl }}
      >
        <ContentColumn style={styles.content}>
          {loading && !commit ? (
            <ActivityIndicator style={styles.center} color={color.muted} />
          ) : null}
          {error && !loading ? (
            <View style={styles.failure}>
              <Text style={styles.error}>{error}</Text>
              <MotionPressable
                onPress={() => setAttempt((value) => value + 1)}
                style={styles.retry}
                accessibilityRole="button"
              >
                <Feather name="refresh-cw" size={14} color={color.text} />
                <Text style={styles.retryText}>Try again</Text>
              </MotionPressable>
            </View>
          ) : null}

          {commit ? (
            <>
              <Text style={styles.subject} selectable>
                {commit.subject || "(no message)"}
              </Text>

              <View style={styles.authorRow}>
                <View style={styles.monogram}>
                  <Text style={styles.monogramText}>{initials(commit.author)}</Text>
                </View>
                <View style={styles.authorText}>
                  <Text style={styles.author} numberOfLines={1}>
                    {commit.author}
                  </Text>
                  <Text style={styles.when}>{commitDate(commit.authoredAt)}</Text>
                </View>
                {/* The short form is what you read; tapping copies the whole
                    hash, which is what a terminal or a search wants. */}
                <MotionPressable
                  onPress={copyHash}
                  style={[styles.hashChip, copied && styles.hashChipCopied]}
                  pressedScale={0.96}
                  hitSlop={6}
                  accessibilityRole="button"
                  accessibilityLabel={
                    copied
                      ? "Commit hash copied"
                      : `Commit ${shortHash(hash)}. Copy the full hash`
                  }
                >
                  <Feather
                    name={copied ? "check" : "copy"}
                    size={12}
                    color={copied ? color.ok : color.muted}
                  />
                  <Text style={[styles.hash, copied && styles.hashCopied]}>
                    {copied ? "Copied" : shortHash(hash)}
                  </Text>
                </MotionPressable>
              </View>

              {body ? (
                <View style={styles.bodyBlock}>
                  {/* Clipped rather than cut with numberOfLines, which on iOS
                      drops the blank line between paragraphs and runs them
                      together. Clipped, the text floats free of the box: text
                      measured to fit a height is cut the same way, and its
                      height would then say it no longer needed cutting. */}
                  <View style={cut && !expanded ? styles.bodyClip : undefined}>
                    <Text
                      style={[styles.body, cut && !expanded && styles.bodyFloating]}
                      selectable
                      onLayout={(event) => setBodyHeight(event.nativeEvent.layout.height)}
                    >
                      {body}
                    </Text>
                    {cut && !expanded ? (
                      <View style={styles.fade} pointerEvents="none">
                        {FADE_BANDS.map((opacity) => (
                          <View key={opacity} style={[styles.fadeBand, { opacity }]} />
                        ))}
                      </View>
                    ) : null}
                  </View>
                  {cut ? (
                    <MotionPressable
                      onPress={() => setExpanded((value) => !value)}
                      style={styles.readMore}
                      hitSlop={10}
                      accessibilityRole="button"
                      accessibilityState={{ expanded }}
                    >
                      <Text style={styles.readMoreText}>
                        {expanded ? "Show less" : "Read more"}
                      </Text>
                      <Feather
                        name={expanded ? "chevron-up" : "chevron-down"}
                        size={14}
                        color={color.workingText}
                      />
                    </MotionPressable>
                  ) : null}
                </View>
              ) : null}

              {message.trailers.length > 0 ? (
                <View style={styles.trailers}>
                  {message.trailers.map((trailer, index) => (
                    <View
                      key={`${trailer.key}:${index}`}
                      style={[styles.trailer, index > 0 && styles.trailerDivided]}
                    >
                      <Text style={styles.trailerKey}>
                        {trailer.key.replace(/-/g, " ")}
                      </Text>
                      <Text style={styles.trailerValue} selectable>
                        {trailerValue(trailer.value)}
                      </Text>
                    </View>
                  ))}
                </View>
              ) : null}

              <View style={styles.summary}>
                <View style={styles.summaryRow}>
                  <Text style={styles.summaryLabel}>
                    {files === 0
                      ? "No files changed"
                      : `${files}${result?.truncated ? "+" : ""} ${files === 1 ? "file" : "files"} changed`}
                  </Text>
                  <CommitSize commit={commit} large />
                </View>
                {added + removed > 0 ? (
                  <View
                    style={styles.bar}
                    accessible
                    accessibilityLabel={`${added} lines added, ${removed} removed`}
                  >
                    {added > 0 ? (
                      <View style={[styles.barAdded, { flex: added }]} />
                    ) : null}
                    {removed > 0 ? (
                      <View style={[styles.barRemoved, { flex: removed }]} />
                    ) : null}
                  </View>
                ) : null}
                {commit.merge ? (
                  <View style={styles.mergeNote}>
                    <Feather name="git-merge" size={12} color={color.faint} />
                    <Text style={styles.mergeText}>
                      Shown against the branch it merged into.
                    </Text>
                  </View>
                ) : null}
              </View>

              {files > 0 ? (
                <ChangeList
                  changes={changes}
                  onOpen={(change) =>
                    router.push(
                      `/file/${encodeURIComponent(sessionId)}?path=${encodeURIComponent(change.path)}&tab=diff&commit=${encodeURIComponent(hash)}`,
                    )
                  }
                />
              ) : null}
              {result?.truncated ? (
                <Text style={styles.note}>Showing the first files only.</Text>
              ) : null}
              {hidden > 0 ? (
                <Text style={styles.note}>
                  {hidden} {hidden === 1 ? "file" : "files"} kept private and not shown.
                </Text>
              ) : null}
            </>
          ) : null}
        </ContentColumn>
      </ScrollView>
    </View>
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
      paddingBottom: space.sm,
    },
    heading: { flex: 1, minWidth: 0 },
    title: {
      color: c.text,
      ...font.sansBold,
      fontSize: size.title,
      letterSpacing: -0.3,
    },
    subtitle: {
      color: c.muted,
      ...font.mono,
      fontSize: scaled(11),
      marginTop: 1,
    },
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

    content: { paddingHorizontal: space.lg, paddingTop: space.md },
    subject: {
      color: c.text,
      ...font.sansBold,
      fontSize: scaled(20),
      lineHeight: scaled(26),
      letterSpacing: -0.4,
    },

    authorRow: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      marginTop: space.lg,
    },
    // A neutral mark rather than a colour per person: colour in this app
    // says what an agent is doing, and nothing decorative should compete.
    monogram: {
      width: 34,
      height: 34,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
      borderWidth: 1,
      borderColor: c.line,
    },
    monogramText: {
      color: c.muted,
      ...font.sansBold,
      fontSize: scaled(12),
      letterSpacing: 0.3,
    },
    authorText: { flex: 1, minWidth: 0, gap: 1 },
    author: { color: c.text, ...font.sansMedium, fontSize: scaled(14) },
    when: { color: c.muted, ...font.sans, fontSize: scaled(12) },
    hashChip: {
      flexDirection: "row",
      alignItems: "center",
      gap: 6,
      height: 30,
      paddingHorizontal: 10,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    hashChipCopied: { backgroundColor: c.okWash, borderColor: c.okWash },
    hash: { color: c.textSecondary, ...font.monoMedium, fontSize: scaled(12) },
    hashCopied: { color: c.ok, ...font.sansMedium },

    bodyBlock: { marginTop: space.lg },
    body: {
      color: c.textSecondary,
      ...font.sans,
      fontSize: size.body - 0.5,
      lineHeight: BODY_LINE,
    },
    bodyClip: { height: BODY_CUT, overflow: "hidden" },
    bodyFloating: { position: "absolute", top: 0, left: 0, right: 0 },
    // The last two lines fade into the page, which says "there is more"
    // before the button does.
    fade: {
      position: "absolute",
      left: 0,
      right: 0,
      bottom: 0,
      height: BODY_LINE * 2,
    },
    fadeBand: { flex: 1, backgroundColor: c.paper },
    readMore: {
      flexDirection: "row",
      alignItems: "center",
      gap: 3,
      alignSelf: "flex-start",
      marginTop: space.sm,
    },
    readMoreText: {
      color: c.workingText,
      ...font.sansMedium,
      fontSize: size.caption,
    },

    // Trailers are data about the commit, so they read as a small table
    // under the message rather than as one more paragraph of it.
    trailers: {
      marginTop: space.lg,
      borderRadius: radius.md,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
      paddingHorizontal: space.md,
    },
    trailer: { paddingVertical: 10, gap: 3 },
    trailerDivided: {
      borderTopWidth: StyleSheet.hairlineWidth,
      borderTopColor: c.line,
    },
    trailerKey: {
      color: c.faint,
      ...font.sansMedium,
      fontSize: scaled(10.5),
      letterSpacing: 0.6,
      textTransform: "uppercase",
    },
    trailerValue: {
      color: c.textSecondary,
      ...font.sans,
      fontSize: scaled(13.5),
      lineHeight: scaled(19),
    },

    summary: { marginTop: space.xl, marginBottom: space.md, gap: space.sm },
    summaryRow: {
      flexDirection: "row",
      alignItems: "baseline",
      justifyContent: "space-between",
      paddingHorizontal: space.xxs,
    },
    summaryLabel: {
      color: c.muted,
      ...font.sansBold,
      fontSize: size.caption,
    },
    // How the change splits between lines added and lines removed, at a
    // glance: mostly green is new work, mostly red is a clean-up.
    bar: {
      flexDirection: "row",
      height: 4,
      gap: 2,
      borderRadius: radius.pill,
      overflow: "hidden",
    },
    barAdded: { backgroundColor: c.ok, borderRadius: radius.pill },
    barRemoved: { backgroundColor: c.error, borderRadius: radius.pill },
    mergeNote: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: 6,
      paddingHorizontal: space.xxs,
    },
    mergeText: {
      flex: 1,
      color: c.faint,
      ...font.sans,
      fontSize: scaled(11.5),
      lineHeight: scaled(16),
    },

    center: { marginTop: space.xl },
    failure: { alignItems: "flex-start", gap: space.md, paddingVertical: space.md },
    error: { color: c.errorText, ...font.sans, fontSize: size.body },
    retry: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      height: 38,
      paddingHorizontal: space.lg,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    retryText: { color: c.text, ...font.sansMedium, fontSize: size.caption },
    note: {
      color: c.faint,
      ...font.sans,
      fontSize: scaled(11.5),
      marginTop: space.md,
      lineHeight: scaled(16),
    },
  });
