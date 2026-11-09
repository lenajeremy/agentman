import { useEffect, useState } from "react";
import { StyleSheet, Text, View } from "react-native";

import { useStyles } from "../lib/appearance";
import { font, Palette, size, space } from "../lib/theme";
import { workDuration } from "../lib/toolgroups";
import { Thinking } from "./Thinking";

/** How long the agent took, between its work and what comes next. */
export function WorkedLine({ seconds }: { seconds: number }) {
  const styles = useStyles(makeStyles);
  const text = `Worked for ${workDuration(seconds)}`;
  return (
    <View style={styles.row} accessible accessibilityLabel={text}>
      <Text style={styles.label}>{text}</Text>
      <View style={styles.rule} />
    </View>
  );
}

/** The foot of the feed while the agent works, counting from its turn's
 *  start when that is known. */
export function WorkingFor({ since }: { since: number | null }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (since === null) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [since]);
  if (since === null) return <Thinking />;
  return <Thinking label={`Working for ${workDuration(Math.max(0, Math.floor((now - since) / 1000)))}`} />;
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    row: { flexDirection: "row", alignItems: "center", gap: space.sm },
    label: { ...font.sans, fontSize: size.caption, color: c.muted },
    rule: { flex: 1, height: StyleSheet.hairlineWidth, backgroundColor: c.line },
  });
