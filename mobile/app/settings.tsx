import { useRouter } from "expo-router";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useStore } from "../lib/store";
import { ago, color, font, radius, size, space } from "../lib/theme";

export default function Settings() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <View style={styles.header}>
        <Pressable onPress={() => router.back()} hitSlop={12}>
          <Text style={styles.backGlyph}>‹</Text>
        </Pressable>
        <Text style={styles.title}>Settings</Text>
      </View>

      <ScrollView contentContainerStyle={styles.content}>
        <View style={styles.card}>
          <Text style={styles.cardLabel}>Connection</Text>
          <Row label="Relay" value={store.credentials?.relayUrl ?? "—"} mono />
          <Row
            label="Your Mac"
            value={
              store.daemonOnline
                ? "Online"
                : store.lastSeenAt
                  ? `Offline · last seen ${ago(store.lastSeenAt)} ago`
                  : "Offline"
            }
            tint={store.daemonOnline ? color.ok : color.muted}
          />
        </View>

        <View style={styles.card}>
          <Text style={styles.cardLabel}>What the relay keeps</Text>
          <Text style={styles.body}>
            Nothing. Your transcripts stay on your Mac and stream to this phone only while
            you are looking at them. The relay matches the two connections and forgets
            everything else.
          </Text>
        </View>

        <Pressable
          onPress={() => {
            void store.signOut().then(() => router.replace("/pair"));
          }}
          style={({ pressed }) => [styles.unpair, pressed && { opacity: 0.7 }]}
        >
          <Text style={styles.unpairText}>Unpair this phone</Text>
        </Pressable>
      </ScrollView>
    </View>
  );
}

function Row({
  label,
  value,
  mono,
  tint,
}: {
  label: string;
  value: string;
  mono?: boolean;
  tint?: string;
}) {
  return (
    <View style={styles.row}>
      <Text style={styles.rowLabel}>{label}</Text>
      <Text
        style={[
          styles.rowValue,
          mono && { fontFamily: font.mono },
          tint ? { color: tint } : null,
        ]}
        numberOfLines={1}
      >
        {value}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: color.ink },
  header: {
    flexDirection: "row",
    alignItems: "center",
    gap: space.sm,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
  },
  backGlyph: { color: color.muted, fontSize: 30, lineHeight: 32, marginTop: -4 },
  title: { fontFamily: font.sansBold, fontSize: size.title, color: color.text },

  content: { padding: space.lg, gap: space.lg },
  card: {
    backgroundColor: color.surface,
    borderRadius: radius.md,
    padding: space.md,
    gap: space.sm,
  },
  cardLabel: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.4,
    textTransform: "uppercase",
    color: color.faint,
  },
  body: { fontFamily: font.sans, fontSize: size.caption, color: color.muted, lineHeight: 19 },

  row: { flexDirection: "row", justifyContent: "space-between", gap: space.md },
  rowLabel: { fontFamily: font.sans, fontSize: size.caption, color: color.muted },
  rowValue: { flex: 1, textAlign: "right", fontFamily: font.sans, fontSize: size.caption, color: color.text },

  unpair: {
    borderRadius: radius.md,
    paddingVertical: space.md,
    alignItems: "center",
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  unpairText: { fontFamily: font.sansMedium, fontSize: size.body, color: color.error },
});
