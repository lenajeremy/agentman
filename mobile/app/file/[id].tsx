import Feather from "@expo/vector-icons/Feather";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { StatusBar } from "expo-status-bar";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { ContentColumn } from "../../components/ContentColumn";
import { FileBadge } from "../../components/FileBadge";
import { ImageViewer } from "../../components/ImageViewer";
import { MotionPressable } from "../../components/MotionPressable";
import { SyntaxCode } from "../../components/SyntaxCode";
import { useStyles, useTheme } from "../../lib/appearance";
import { diffStat, parseDiff, type DiffRow } from "../../lib/diff";
import { WorkspaceResult } from "../../lib/protocol";
import { useStore } from "../../lib/store";
import { font, Palette, radius, size, space } from "../../lib/theme";

export default function FileScreen() {
  const {
    id,
    path: pathParam,
    tab: tabParam,
    seen,
  } = useLocalSearchParams<{
    id: string;
    path: string;
    tab?: string;
    seen?: string;
  }>();
  // A file outside the session's directory, reachable only because this
  // session's agent opened it. It has no working tree to diff against and no
  // folder above it to go back to, so the screen shows just the picture.
  const seenOnly = seen === "1";
  const sessionId = decodeURIComponent(String(id));
  const filePath = typeof pathParam === "string" ? pathParam : "";
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [tab, setTab] = useState<"code" | "diff">(() =>
    tabParam === "diff" ? "diff" : "code",
  );
  const [result, setResult] = useState<WorkspaceResult | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  // Chrome over a photo starts visible, so it is clear there is a way out, and
  // goes away on a tap once you are looking.
  const [chrome, setChrome] = useState(true);
  // Wrapped by default: panning a long line on a phone loses your place, and
  // the line you were reading is rarely the one you end up looking at.
  const [wrap, setWrap] = useState(true);

  useEffect(() => {
    let current = true;
    setLoading(true);
    setError("");
    setResult(null);
    void store
      .workspace(
        sessionId,
        seenOnly
          ? "read_seen_file"
          : tab === "code"
            ? "read_file"
            : "file_diff",
        filePath,
      )
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, filePath, tab, refresh, seenOnly]);

  const name = filePath.split("/").at(-1) || "File";
  const rows = useMemo(
    () => (result?.diff ? parseDiff(result.diff) : []),
    [result],
  );
  const stat = tab === "diff" && rows.length > 0 ? diffStat(rows) : null;
  // A picture gets the whole display, with the chrome floating over it and out
  // of the way on a tap. Anything else is a document, and documents get a
  // header, tabs and a scrolling body.
  const imageUri =
    result?.image && result.mime
      ? `data:${result.mime};base64,${result.image}`
      : "";
  if (imageUri) {
    return (
      <View style={styles.photoPage}>
        <StatusBar style="light" hidden={!chrome} animated />
        <ImageViewer
          uri={imageUri}
          onTap={() => setChrome((shown) => !shown)}
          onDismiss={() => router.back()}
        />
        {chrome ? (
          <View
            style={[styles.photoBar, { paddingTop: insets.top + space.sm }]}
            pointerEvents="box-none"
          >
            <PhotoScrim />
            <MotionPressable
              onPress={() => router.back()}
              style={styles.photoButton}
              hitSlop={10}
              accessibilityRole="button"
              accessibilityLabel="Close image"
            >
              <Feather name="x" size={20} color="#FFFFFF" />
            </MotionPressable>
            <View style={styles.photoHeading}>
              <Text style={styles.photoTitle} numberOfLines={1}>
                {name}
              </Text>
              <Text
                style={styles.photoPath}
                numberOfLines={1}
                ellipsizeMode="head"
              >
                {filePath}
              </Text>
            </View>
            <MotionPressable
              onPress={() => setRefresh((value) => value + 1)}
              style={styles.photoButton}
              hitSlop={10}
              accessibilityRole="button"
              accessibilityLabel="Reload image"
            >
              <Feather name="refresh-cw" size={17} color="#FFFFFF" />
            </MotionPressable>
          </View>
        ) : null}
      </View>
    );
  }

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <ContentColumn style={styles.header}>
        <MotionPressable
          onPress={() => router.back()}
          style={styles.iconButton}
          accessibilityRole="button"
          accessibilityLabel="Back to files"
        >
          <Feather name="chevron-left" size={20} color={color.text} />
        </MotionPressable>
        <FileBadge name={name} directory={false} />
        <View style={styles.heading}>
          <Text style={styles.title} numberOfLines={1}>
            {name}
          </Text>
          <Text style={styles.path} numberOfLines={1}>
            {filePath}
          </Text>
        </View>
        <MotionPressable
          onPress={() => setRefresh((value) => value + 1)}
          style={styles.iconButton}
          accessibilityRole="button"
          accessibilityLabel="Refresh file"
        >
          <Feather name="refresh-cw" size={17} color={color.text} />
        </MotionPressable>
      </ContentColumn>
      {/* A file outside the working tree has nothing to diff against, so the
          tabs would offer a choice with one broken half. */}
      <ContentColumn style={styles.tabs}>
        {(seenOnly ? [] : (["code", "diff"] as const)).map((value) => (
          <MotionPressable
            key={value}
            onPress={() => setTab(value)}
            style={[styles.tab, tab === value && styles.tabActive]}
            accessibilityRole="tab"
            accessibilityState={{ selected: tab === value }}
          >
            <Text
              style={[styles.tabText, tab === value && styles.tabTextActive]}
            >
              {value === "code" ? "File" : "Diff"}
            </Text>
          </MotionPressable>
        ))}
        <View style={styles.spacer} />
        {!result?.image ? (
          <MotionPressable
            onPress={() => setWrap((value) => !value)}
            style={[styles.wrapToggle, wrap && styles.wrapToggleOn]}
            accessibilityRole="switch"
            accessibilityState={{ checked: wrap }}
            accessibilityLabel="Wrap long lines"
          >
            <Feather
              name="corner-down-left"
              size={13}
              color={wrap ? color.onInverse : color.muted}
            />
            <Text style={[styles.wrapLabel, wrap && styles.wrapLabelOn]}>
              Wrap
            </Text>
          </MotionPressable>
        ) : null}
      </ContentColumn>
      {stat ? (
        <ContentColumn style={styles.statBar}>
          <Text style={styles.statAdd}>+{stat.added}</Text>
          <Text style={styles.statRemove}>−{stat.removed}</Text>
          <Text style={styles.statNote}>vs last commit</Text>
        </ContentColumn>
      ) : null}
      {loading ? (
        <ActivityIndicator style={styles.center} color={color.muted} />
      ) : null}
      {error ? (
        <ContentColumn style={styles.message}>
          <Text style={styles.error}>{error}</Text>
        </ContentColumn>
      ) : null}
      {!loading && !error && result && (
        <ScrollView
          contentContainerStyle={{ paddingBottom: insets.bottom + space.xl }}
        >
          {tab === "code" ? (
            <SyntaxCode
              source={result.text ?? ""}
              filename={name}
              wrap={wrap}
            />
          ) : rows.length > 0 ? (
            <DiffBody rows={rows} wrap={wrap} styles={styles} />
          ) : (
            <ContentColumn style={styles.message}>
              <Text style={styles.empty}>
                No working tree changes for this file.
              </Text>
            </ContentColumn>
          )}
          {result.truncated && (
            <ContentColumn style={styles.message}>
              <Text style={styles.note}>
                Preview truncated. The full file remains on your Mac.
              </Text>
            </ContentColumn>
          )}
        </ScrollView>
      )}
    </View>
  );
}

/**
 * A unified diff with its own line numbers.
 *
 * The marker moves out of the text and into a narrow column, so the code in a
 * changed line starts at the same x as the code around it. With the marker
 * inline every added line is shifted one character and the eye has to correct
 * for it on every row.
 */
/**
 * A fade behind the chrome over a photo.
 *
 * Six bands rather than a real gradient: expo-linear-gradient is a native
 * module, and pulling one in — with the build cycle that costs — to darken a
 * strip is not a trade worth making. The flat wash it replaces lost badly over
 * a light screenshot, where the filename landed in the middle of the picture's
 * own text and neither could be read.
 */
/** Reaches past the bar it sits behind, so the fade lands below the text. */
const scrimFill = {
  position: "absolute",
  top: 0,
  left: 0,
  right: 0,
  bottom: -56,
} as const;

function PhotoScrim() {
  return (
    <View style={scrimFill} pointerEvents="none">
      {[0.92, 0.92, 0.92, 0.9, 0.84, 0.66, 0.4, 0.18, 0.05].map(
        (opacity, index) => (
          <View
            key={index}
            style={{ flex: 1, backgroundColor: `rgba(0, 0, 0, ${opacity})` }}
          />
        ),
      )}
    </View>
  );
}

function DiffBody({
  rows,
  wrap,
  styles,
}: {
  rows: DiffRow[];
  wrap: boolean;
  styles: ReturnType<typeof makeStyles>;
}) {
  const body = (
    <View style={wrap ? styles.diffWrapBody : undefined}>
      {rows.map((row, index) => {
        if (row.kind === "hunk" || row.kind === "meta") {
          return (
            <View key={index} style={styles.hunkRow}>
              <Text style={styles.hunkText} numberOfLines={1}>
                {row.text}
              </Text>
            </View>
          );
        }
        return (
          <View
            key={index}
            style={[
              styles.diffRow,
              row.kind === "add" && styles.addedRow,
              row.kind === "remove" && styles.removedRow,
            ]}
          >
            {/* Both numbers stay visible so a line can be found in either the
                committed file or the working tree. */}
            <Text style={styles.diffNo} selectable={false}>
              {row.oldNumber ?? ""}
            </Text>
            <Text style={styles.diffNo} selectable={false}>
              {row.newNumber ?? ""}
            </Text>
            <Text
              style={[
                styles.diffMark,
                row.kind === "add" && styles.markAdd,
                row.kind === "remove" && styles.markRemove,
              ]}
              selectable={false}
            >
              {row.kind === "add" ? "+" : row.kind === "remove" ? "−" : " "}
            </Text>
            <Text
              selectable
              style={[styles.diffLine, wrap && styles.diffLineWrap]}
            >
              {row.text.length > 0 ? row.text : " "}
            </Text>
          </View>
        );
      })}
    </View>
  );
  return wrap ? (
    <View style={styles.diffFrame}>{body}</View>
  ) : (
    <ScrollView
      horizontal
      style={styles.diffFrame}
      contentContainerStyle={{ minWidth: "100%" }}
    >
      {body}
    </ScrollView>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },

    // A photo's own page. Black regardless of theme: it is what a picture is
    // shown on, and the one ground that tints nothing at the edges.
    photoPage: { flex: 1, backgroundColor: "#000000" },
    photoBar: {
      position: "absolute",
      top: 0,
      left: 0,
      right: 0,
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      paddingHorizontal: space.lg,
      paddingBottom: space.md,
      // Nothing clips here: the scrim deliberately extends past this bar so
      // the fade happens below the text rather than across it.
    },
    photoScrim: {
      position: "absolute",
      top: 0,
      left: 0,
      right: 0,
      bottom: -56,
    },
    photoButton: {
      width: 34,
      height: 34,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: "rgba(255, 255, 255, 0.14)",
    },
    photoHeading: { flex: 1 },
    photoTitle: {
      fontFamily: font.sansMedium,
      fontSize: size.caption,
      color: "#FFFFFF",
    },
    photoPath: {
      fontFamily: font.mono,
      fontSize: 11,
      color: "rgba(255, 255, 255, 0.55)",
      marginTop: 1,
    },
    header: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingHorizontal: space.lg,
      paddingTop: space.sm,
      paddingBottom: space.md,
    },
    heading: { flex: 1 },
    title: { color: c.text, fontFamily: font.sansBold, fontSize: size.title },
    path: { color: c.muted, fontFamily: font.mono, fontSize: 11 },
    iconButton: {
      width: 40,
      height: 40,
      alignItems: "center",
      justifyContent: "center",
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    tabs: {
      flexDirection: "row",
      gap: space.xs,
      paddingHorizontal: space.lg,
      paddingBottom: space.md,
    },
    tab: {
      paddingHorizontal: space.lg,
      paddingVertical: space.sm,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    tabActive: { backgroundColor: c.inverse },
    tabText: {
      color: c.muted,
      fontFamily: font.sansMedium,
      fontSize: size.caption,
    },
    tabTextActive: { color: c.onInverse },
    center: { marginTop: space.xl },
    message: { paddingHorizontal: space.lg, paddingTop: space.md },
    error: { color: c.errorText, fontFamily: font.sans, fontSize: size.body },
    empty: { color: c.muted, fontFamily: font.sans, fontSize: size.body },
    note: { color: c.muted, fontFamily: font.sans, fontSize: size.caption },
    spacer: { flex: 1 },
    wrapToggle: {
      flexDirection: "row",
      alignItems: "center",
      gap: 5,
      paddingHorizontal: space.md,
      paddingVertical: 7,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    wrapToggleOn: { backgroundColor: c.inverse },
    wrapLabel: { color: c.muted, fontFamily: font.sansMedium, fontSize: 12 },
    wrapLabelOn: { color: c.onInverse },

    statBar: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingHorizontal: space.lg,
      paddingBottom: space.sm,
    },
    statAdd: { color: c.ok, fontFamily: font.monoMedium, fontSize: 12 },
    statRemove: {
      color: c.errorText,
      fontFamily: font.monoMedium,
      fontSize: 12,
    },
    statNote: { color: c.faint, fontFamily: font.sans, fontSize: 11.5 },

    diffFrame: {
      backgroundColor: c.surface,
      borderTopWidth: 1,
      borderTopColor: c.line,
    },
    diffWrapBody: { width: "100%" },
    diffRow: {
      flexDirection: "row",
      alignItems: "flex-start",
      paddingRight: space.md,
    },
    // Fixed-width gutters: a number column that resizes per row makes the code
    // column jitter as you scroll.
    diffNo: {
      width: 30,
      textAlign: "right",
      paddingRight: 6,
      fontFamily: font.mono,
      fontSize: 10.5,
      lineHeight: 19,
      color: c.faint,
    },
    diffMark: {
      width: 14,
      textAlign: "center",
      fontFamily: font.monoMedium,
      fontSize: 12,
      lineHeight: 19,
      color: c.faint,
    },
    markAdd: { color: c.ok },
    markRemove: { color: c.errorText },
    diffLine: {
      fontFamily: font.mono,
      fontSize: 12,
      lineHeight: 19,
      color: c.text,
    },
    diffLineWrap: { flex: 1 },
    addedRow: { backgroundColor: c.okWash },
    removedRow: { backgroundColor: c.errorWash },
    hunkRow: {
      paddingHorizontal: space.md,
      paddingVertical: 5,
      backgroundColor: c.workingWash,
      borderTopWidth: StyleSheet.hairlineWidth,
      borderBottomWidth: StyleSheet.hairlineWidth,
      borderColor: c.workingSoft,
    },
    hunkText: { color: c.workingText, fontFamily: font.mono, fontSize: 11 },
  });
