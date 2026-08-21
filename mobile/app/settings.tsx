import Feather from "@expo/vector-icons/Feather";
import { useRouter } from "expo-router";
import { Alert, Platform, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Appear } from "../components/Appear";
import { ContentColumn } from "../components/ContentColumn";
import { MotionPressable } from "../components/MotionPressable";
import { useStore } from "../lib/store";
import { ago, color, font, radius, size, space } from "../lib/theme";

export default function Settings() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();

  const unpair = () => {
    const perform = () => {
      void store.signOut().then(() => router.replace("/pair"));
    };
    if (Platform.OS === "web") {
      if (globalThis.confirm("Unpair this phone from your Mac?")) perform();
      return;
    }
    Alert.alert(
      "Unpair this phone?",
      "You’ll need to run `am pair` on your Mac to reconnect.",
      [
        { text: "Cancel", style: "cancel" },
        { text: "Unpair", style: "destructive", onPress: perform },
      ],
    );
  };

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <ContentColumn style={styles.header}>
        <MotionPressable
          onPress={() => router.back()}
          hitSlop={12}
          pressedScale={0.92}
          style={styles.back}
          accessibilityRole="button"
          accessibilityLabel="Back"
        >
          <Feather name="chevron-left" size={22} color={color.text} />
        </MotionPressable>
        <View>
          <Text style={styles.eyebrow}>Agentman</Text>
          <Text style={styles.title}>Settings</Text>
        </View>
      </ContentColumn>

      <ScrollView contentContainerStyle={{ paddingBottom: insets.bottom + space.xl }}>
        <ContentColumn style={styles.content}>
          <Appear>
            <Text style={styles.sectionLabel}>Connection</Text>
            <View style={styles.card}>
              <Row label="Relay" value={store.credentials?.relayUrl ?? "—"} mono />
              <View style={styles.divider} />
              <Row
                label="Your Mac"
                value={
                  store.daemonOnline
                    ? "Online and receiving updates"
                    : store.lastSeenAt
                      ? `Offline · last seen ${ago(store.lastSeenAt)} ago`
                      : "Offline"
                }
                tint={store.daemonOnline ? color.ok : color.muted}
                status={store.daemonOnline ? "online" : "offline"}
              />
            </View>
          </Appear>

          <Appear delay={45}>
            <Text style={styles.sectionLabel}>Privacy</Text>
            <View style={styles.privacyCard}>
              <View style={styles.privacyHeader}>
                <View style={styles.privacyIcon}>
                  <View style={styles.lockBody} />
                  <View style={styles.lockLoop} />
                </View>
                <View style={styles.privacyCopy}>
                  <Text style={styles.privacyTitle}>Your Mac stays the source of truth</Text>
                  <Text style={styles.body}>
                    Transcripts are not persisted by the relay. Live traffic does pass
                    through it without end-to-end encryption, so use an operator you trust
                    or self-host one.
                  </Text>
                </View>
              </View>
            </View>
          </Appear>

          <Appear delay={90}>
            <MotionPressable
              onPress={unpair}
              style={styles.unpair}
              accessibilityRole="button"
            >
              <Text style={styles.unpairText}>Unpair this phone</Text>
            </MotionPressable>
            <Text style={styles.unpairHint}>
              Removes the relay credentials stored securely on this device.
            </Text>
          </Appear>
        </ContentColumn>
      </ScrollView>
    </View>
  );
}

function Row({
  label,
  value,
  mono,
  status,
  tint,
}: {
  label: string;
  value: string;
  mono?: boolean;
  status?: "online" | "offline";
  tint?: string;
}) {
  return (
    <View style={styles.row}>
      <Text style={styles.rowLabel}>{label}</Text>
      <View style={styles.valueWrap}>
        {status ? (
          <View
            style={[
              styles.statusDot,
              { backgroundColor: status === "online" ? color.ok : color.faint },
            ]}
          />
        ) : null}
        <Text
          style={[
            styles.rowValue,
            mono && { fontFamily: font.mono },
            tint ? { color: tint } : null,
          ]}
          numberOfLines={2}
        >
          {value}
        </Text>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: color.ink },
  header: {
    flexDirection: "row",
    alignItems: "center",
    gap: space.sm,
    paddingHorizontal: space.lg,
    paddingVertical: space.lg,
  },
  back: {
    width: 44,
    height: 44,
    borderRadius: radius.md,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.surface,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  eyebrow: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.3,
    textTransform: "uppercase",
    color: color.faint,
  },
  title: { fontFamily: font.sansBold, fontSize: size.heading, color: color.text },

  content: { paddingHorizontal: space.lg, gap: space.sm },
  sectionLabel: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.4,
    textTransform: "uppercase",
    color: color.faint,
    marginLeft: space.xs,
    marginTop: space.lg,
    marginBottom: space.sm,
  },
  card: {
    backgroundColor: color.surface,
    borderRadius: radius.lg,
    paddingHorizontal: space.lg,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  body: { fontFamily: font.sans, fontSize: size.caption, color: color.muted, lineHeight: 19 },

  row: {
    minHeight: 58,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: space.md,
    paddingVertical: space.md,
  },
  rowLabel: { fontFamily: font.sans, fontSize: size.caption, color: color.muted },
  valueWrap: {
    flex: 1,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "flex-end",
    gap: space.sm,
  },
  statusDot: { width: 7, height: 7, borderRadius: 4 },
  rowValue: { flexShrink: 1, textAlign: "right", fontFamily: font.sans, fontSize: size.caption, color: color.text },
  divider: { height: StyleSheet.hairlineWidth, backgroundColor: color.line },

  privacyCard: {
    backgroundColor: color.surface,
    borderRadius: radius.lg,
    padding: space.lg,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  privacyHeader: { flexDirection: "row", alignItems: "flex-start", gap: space.md },
  privacyIcon: {
    width: 36,
    height: 36,
    borderRadius: radius.md,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.okWash,
  },
  lockBody: { width: 11, height: 8, borderRadius: 2, backgroundColor: color.ok, marginTop: 6 },
  lockLoop: {
    position: "absolute",
    width: 9,
    height: 9,
    top: 8,
    borderWidth: 1.5,
    borderColor: color.ok,
    borderRadius: 5,
  },
  privacyCopy: { flex: 1, gap: space.xs },
  privacyTitle: { fontFamily: font.sansMedium, fontSize: size.body, color: color.text },

  unpair: {
    minHeight: 50,
    borderRadius: radius.lg,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.errorWash,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: "#563039",
    marginTop: space.lg,
  },
  unpairText: { fontFamily: font.sansMedium, fontSize: size.body, color: color.error },
  unpairHint: {
    fontFamily: font.sans,
    fontSize: size.label,
    color: color.faint,
    textAlign: "center",
    lineHeight: 16,
    marginTop: space.sm,
    paddingHorizontal: space.lg,
  },
});
