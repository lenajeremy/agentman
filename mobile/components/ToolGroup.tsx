import Feather from "@expo/vector-icons/Feather";
import { useEffect, useState } from "react";
import { ScrollView, StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import type { Message } from "../lib/protocol";
import { readImage } from "../lib/imagepath";
import { useStore } from "../lib/store";
import { font, Palette, radius, size, space, scaled } from "../lib/theme";
import { runCounts, runFinished, toolRunKey } from "../lib/toolgroups";
import { MotionPressable } from "./MotionPressable";
import { SeenImage } from "./SeenImage";
import { ToolRow } from "./ToolRow";

/** Titles already written, so scrolling back does not ask again. */
const titles = new Map<string, string>();

/**
 * A run of tool calls as one line saying what was done, opened to the steps
 * one by one. Once the run has finished, Agentman on the Mac writes that
 * line; until then, or without it, the steps' own words make it.
 *
 * A run of calls that each read a picture has the pictures side by side
 * under it, outside the pill, as a single one has its picture.
 */
export function ToolGroup({
  calls,
  said,
  cwd,
  pictures = false,
}: {
  calls: Message[];
  said: string;
  cwd: string;
  pictures?: boolean;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const store = useStore();
  const [open, setOpen] = useState(false);
  const key = toolRunKey(calls);
  const finished = runFinished(calls);
  const [title, setTitle] = useState(titles.get(key) ?? "");

  const online = store.connection === "online";
  useEffect(() => {
    if (!finished || !online || titles.has(key)) return;
    let current = true;
    let retry: ReturnType<typeof setTimeout> | undefined;
    const steps = calls.flatMap((call) => (call.tool ? [call.tool] : []));
    // A Mac that is restarting, or a moment without signal, should not leave
    // the run untitled for good: ask again a little later, a few times.
    const ask = (attempt: number) => {
      void store
        .titleTools(calls[0].sessionId, said, steps)
        .then((written) => {
          titles.set(key, written);
          if (current) setTitle(written);
        })
        .catch(() => {
          if (current && attempt < 3) retry = setTimeout(() => ask(attempt + 1), 4000 * 2 ** attempt);
        });
    };
    ask(0);
    return () => {
      current = false;
      if (retry) clearTimeout(retry);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, finished, online]);

  const running = calls.find((call) => call.tool?.status === "running");
  const failures = calls.filter((call) => call.tool?.status === "error").length;
  const line = running
    ? `${running.tool?.title || "Working"}…`
    : title || runCounts(calls);

  const group = (
    <View style={[styles.group, open && styles.groupOpen]}>
      <MotionPressable
        onPress={() => setOpen((value) => !value)}
        style={styles.header}
        pressedScale={0.995}
        accessibilityRole="button"
        accessibilityLabel={`${line}. ${calls.length} steps`}
        accessibilityState={{ expanded: open }}
      >
        <View style={[styles.tile, running && styles.tileRunning]}>
          <Feather name="layers" size={12} color={running ? color.working : color.muted} />
        </View>
        {/* One line while it is a pill; all of it once opened. */}
        <Text style={styles.line} numberOfLines={open ? undefined : 1}>
          {line}
          {failures > 0 ? <Text style={styles.failed}>{` · ${failures} failed`}</Text> : null}
        </Text>
        <Feather name={open ? "chevron-down" : "chevron-right"} size={14} color={color.faint} style={styles.chevron} />
      </MotionPressable>
      {open ? (
        <View style={styles.steps}>
          {calls.map((call) => (
            <ToolRow key={call.id} message={call} cwd={cwd} />
          ))}
        </View>
      ) : null}
    </View>
  );
  if (!pictures) return group;
  const read = calls.flatMap((call) => {
    const target = call.tool ? readImage(call.tool.name, call.tool.summary ?? "", cwd) : null;
    return target ? [{ id: call.id, target }] : [];
  });
  return (
    <View style={styles.withPictures}>
      {group}
      <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.pictures}>
        {read.map(({ id, target }) => (
          <SeenImage key={id} sessionId={calls[0].sessionId} target={target} style={styles.picture} />
        ))}
      </ScrollView>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    // The same soft grey pill as a single step: it holds its words, and
    // widens to the column when opened to show the steps.
    group: {
      alignSelf: "flex-start",
      maxWidth: "100%",
      marginVertical: -4,
      paddingLeft: 4,
      paddingRight: 10,
      paddingVertical: 1,
      borderRadius: 16,
      backgroundColor: c.toolCard,
    },
    groupOpen: { alignSelf: "stretch", paddingBottom: space.sm },
    // As a single picture sits under its step: a little in from the pill's
    // edge, and the row scrolls sideways when the pictures run past it.
    withPictures: { gap: space.sm },
    pictures: { gap: space.sm, paddingLeft: 12, paddingRight: space.lg },
    picture: { marginLeft: 0 },
    // As a single call's row: even room around the words, the tile and the
    // chevron level with the first line.
    header: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: 8,
      paddingVertical: 5,
      paddingHorizontal: space.xs,
      borderRadius: radius.sm,
    },
    chevron: { marginTop: 3, marginLeft: -4 },
    tile: {
      width: 22,
      height: 20,
      borderRadius: 7,
      alignItems: "center",
      justifyContent: "center",
    },
    tileRunning: { backgroundColor: c.workingWash },
    line: { flexShrink: 1, fontFamily: font.sans, fontSize: size.caption, color: c.textSecondary, lineHeight: scaled(20) },
    failed: { color: c.errorText },
    // Opened, the steps hang from a thin rule under the run's icon, as a
    // list of what it was made of.
    steps: {
      marginLeft: space.xs + 10,
      paddingLeft: space.sm,
      paddingTop: 2,
      paddingBottom: 2,
      gap: 2,
      borderLeftWidth: 1,
      borderLeftColor: c.line,
    },
  });
