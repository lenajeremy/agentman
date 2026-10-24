import Feather from "@expo/vector-icons/Feather";
import { useEffect, useState } from "react";
import { StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import type { Message } from "../lib/protocol";
import { useStore } from "../lib/store";
import { font, Palette, radius, size, space } from "../lib/theme";
import { runFinished, runFromSteps, toolRunKey } from "../lib/toolgroups";
import { MotionPressable } from "./MotionPressable";
import { ToolRow } from "./ToolRow";

/** Titles already written, so scrolling back does not ask again. */
const titles = new Map<string, string>();

/**
 * A run of tool calls as one line saying what was done, opened to the steps
 * one by one. Once the run has finished, Agentman on the Mac writes that
 * line; until then, or without it, the steps' own words make it.
 */
export function ToolGroup({ calls, said, cwd }: { calls: Message[]; said: string; cwd: string }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const store = useStore();
  const [open, setOpen] = useState(false);
  const key = toolRunKey(calls);
  const finished = runFinished(calls);
  const [title, setTitle] = useState(titles.get(key) ?? "");

  useEffect(() => {
    if (!finished || titles.has(key)) return;
    let current = true;
    const steps = calls.flatMap((call) => (call.tool ? [call.tool] : []));
    void store
      .titleTools(calls[0].sessionId, said, steps)
      .then((written) => {
        titles.set(key, written);
        if (current) setTitle(written);
      })
      .catch(() => {});
    return () => {
      current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, finished]);

  const running = calls.find((call) => call.tool?.status === "running");
  const failures = calls.filter((call) => call.tool?.status === "error").length;
  const line = running
    ? `${running.tool?.title || "Working"}…`
    : title || runFromSteps(calls) || `${calls.length} steps`;

  return (
    <View style={styles.group}>
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
        <Text style={styles.line} numberOfLines={2}>
          {line}
          {failures > 0 ? <Text style={styles.failed}>{` · ${failures} failed`}</Text> : null}
        </Text>
        <Feather name={open ? "chevron-down" : "chevron-right"} size={14} color={color.faint} />
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
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    group: { marginHorizontal: -space.xs },
    header: {
      flexDirection: "row",
      alignItems: "center",
      gap: 10,
      minHeight: 32,
      borderRadius: radius.sm,
      paddingHorizontal: space.xs,
    },
    tile: {
      width: 22,
      height: 22,
      borderRadius: 7,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
    },
    tileRunning: { backgroundColor: c.workingWash },
    line: { flex: 1, fontFamily: font.sans, fontSize: size.label, color: c.text, lineHeight: 19 },
    failed: { color: c.errorText },
    steps: { paddingLeft: space.lg, paddingTop: 2, gap: 2 },
  });
