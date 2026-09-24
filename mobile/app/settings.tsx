import Feather from "@expo/vector-icons/Feather";
import { useRouter } from "expo-router";
import { Alert, Platform, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Appear } from "../components/Appear";
import { BrandMark } from "../components/BrandMark";
import { ContentColumn } from "../components/ContentColumn";
import { MotionPressable } from "../components/MotionPressable";
import { useStyles, useTheme } from "../lib/appearance";
import { AppearancePreference } from "../lib/appearance-policy";
import { isPushActive, pushFailureReason } from "../lib/push";
import { useStore } from "../lib/store";
import { ago, font, Palette, radius, size, space } from "../lib/theme";

const APPEARANCES: { value: AppearancePreference; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "Match phone" },
];

export default function Settings() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color, preference, setPreference } = useTheme();

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
      <ScrollView contentContainerStyle={{ paddingBottom: insets.bottom + space.xl }}>
        <ContentColumn style={styles.content}>
          <View style={styles.topBar}>
            <MotionPressable
              onPress={() => router.back()}
              hitSlop={12}
              pressedScale={0.92}
              style={styles.iconButton}
              accessibilityRole="button"
              accessibilityLabel="Back"
            >
              <Feather name="chevron-left" size={20} color={color.text} />
            </MotionPressable>
          </View>
          <Text style={styles.title} accessibilityRole="header">
            Settings
          </Text>

          <Appear>
            <Text style={styles.sectionLabel}>Connection</Text>
            <View style={styles.card}>
              <Row label="Relay" value={store.credentials?.relayUrl ?? "—"} mono />
              <View style={styles.divider} />
              <Row
                label="Your Mac"
                value={
                  store.daemonOnline
                    ? "Online"
                    : store.lastSeenAt
                      ? `Offline · last seen ${ago(store.lastSeenAt)} ago`
                      : "Offline"
                }
                status={store.daemonOnline ? "online" : "offline"}
              />
              <View style={styles.divider} />
              {/* Push either works or it does not, and when it does not the
                  app has the reason. Withholding it turns a one-line fix into
                  a round of guessing against a ten-minute build. */}
              <Row
                label="Background alerts"
                value={
                  isPushActive()
                    ? "On — your Mac can reach you when the app is closed"
                    : pushFailureReason() || "Unavailable in this build"
                }
                tint={isPushActive() ? color.ok : undefined}
              />
            </View>
          </Appear>

          <Appear delay={30}>
            <Text style={styles.sectionLabel}>Appearance</Text>
            <View style={styles.segmented} accessibilityRole="radiogroup">
              {APPEARANCES.map((option) => {
                const active = option.value === preference;
                return (
                  <MotionPressable
                    key={option.value}
                    onPress={() => setPreference(option.value)}
                    style={[styles.segment, active && styles.segmentActive]}
                    pressedScale={0.97}
                    accessibilityRole="radio"
                    accessibilityState={{ selected: active }}
                  >
                    <Text style={[styles.segmentLabel, active && styles.segmentLabelActive]}>
                      {option.label}
                    </Text>
                  </MotionPressable>
                );
              })}
            </View>
          </Appear>

          <Appear delay={60}>
            <Text style={styles.sectionLabel}>Privacy</Text>
            <View style={[styles.card, styles.privacyCard]}>
              <View style={styles.privacyIcon}>
                <Feather name="lock" size={16} color={color.ok} />
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

          <View style={styles.signature}>
            <BrandMark size={18} />
            <Text style={styles.signatureText}>agentman</Text>
          </View>
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
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
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

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    content: { paddingHorizontal: space.lg },
    topBar: { flexDirection: "row", paddingTop: space.sm },
    iconButton: {
      width: 40,
      height: 40,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    title: {
      fontFamily: font.sansBold,
      fontSize: size.display,
      letterSpacing: -1.2,
      color: c.text,
      marginTop: space.lg,
      marginBottom: space.xs,
    },

    sectionLabel: {
      fontFamily: font.sansBold,
      fontSize: size.caption,
      color: c.muted,
      marginLeft: space.xxs,
      marginTop: space.xl,
      marginBottom: space.sm,
    },
    card: {
      backgroundColor: c.surface,
      borderRadius: radius.xl,
      paddingHorizontal: space.lg,
      borderWidth: 1,
      borderColor: c.line,
    },
    body: { fontFamily: font.sans, fontSize: size.caption, color: c.muted, lineHeight: 19 },

    row: {
      minHeight: 56,
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "space-between",
      gap: space.md,
      paddingVertical: space.md,
    },
    rowLabel: { fontFamily: font.sansMedium, fontSize: size.body, color: c.text },
    valueWrap: {
      flex: 1,
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "flex-end",
      gap: space.sm,
    },
    statusDot: { width: 7, height: 7, borderRadius: 4 },
    rowValue: {
      flexShrink: 1,
      textAlign: "right",
      fontFamily: font.sans,
      fontSize: size.caption,
      color: c.muted,
    },
    divider: { height: StyleSheet.hairlineWidth, backgroundColor: c.line },

    segmented: {
      flexDirection: "row",
      padding: 4,
      gap: 4,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    segment: {
      flex: 1,
      minHeight: 40,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
    },
    segmentActive: {
      backgroundColor: c.surface,
      shadowColor: c.shadow,
      shadowOpacity: 0.08,
      shadowRadius: 6,
      shadowOffset: { width: 0, height: 2 },
      elevation: 1,
    },
    segmentLabel: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.muted },
    segmentLabelActive: { fontFamily: font.sansBold, color: c.text },

    privacyCard: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: space.md,
      paddingVertical: space.lg,
    },
    privacyIcon: {
      width: 36,
      height: 36,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.okWash,
    },
    privacyCopy: { flex: 1, gap: space.xs },
    privacyTitle: { fontFamily: font.sansBold, fontSize: size.body, color: c.text },

    unpair: {
      minHeight: 52,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.errorWash,
      marginTop: space.xl,
    },
    unpairText: { fontFamily: font.sansBold, fontSize: size.body, color: c.errorText },
    unpairHint: {
      fontFamily: font.sans,
      fontSize: size.label,
      color: c.faint,
      textAlign: "center",
      lineHeight: 16,
      marginTop: space.sm,
      paddingHorizontal: space.lg,
    },
    signature: {
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "center",
      gap: space.sm,
      marginTop: space.xxl,
      opacity: 0.55,
    },
    signatureText: { fontFamily: font.sansBold, fontSize: size.caption, color: c.text },
  });
