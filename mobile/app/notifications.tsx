import Feather from "@expo/vector-icons/Feather";
import * as Notifications from "expo-notifications";
import { useRouter } from "expo-router";
import { ComponentProps, useCallback, useEffect, useState } from "react";
import { AppState, Linking, Platform, ScrollView, StyleSheet, Switch, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Appear } from "../components/Appear";
import { ContentColumn } from "../components/ContentColumn";
import { MotionPressable } from "../components/MotionPressable";
import { useStyles, useTheme } from "../lib/appearance";
import { AlertKind } from "../lib/notification-prefs";
import { isPushActive, pushFailureReason } from "../lib/push";
import { useStore } from "../lib/store";
import { font, Palette, radius, size, space } from "../lib/theme";

type FeatherName = ComponentProps<typeof Feather>["name"];

/**
 * Which alerts reach this phone.
 *
 * Two kinds, because they ask different things of you. "Needs you" is an
 * agent stopped until you answer; "Finished" is one done and waiting to be
 * looked at. Neither is ever shown while the app is open — that rule is not a
 * setting, because there is no reason to want a banner over the screen you
 * are already reading.
 */
export default function NotificationSettings() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const prefs = store.notifyPrefs;

  // iOS can switch every alert off for the app, which no choice here can
  // override. Read again whenever the app comes back, since that is where
  // someone who just changed it in Settings returns to.
  const [allowed, setAllowed] = useState<boolean | null>(null);
  const readPermission = useCallback(() => {
    if (Platform.OS === "web") return;
    void Notifications.getPermissionsAsync()
      .then((permission) => setAllowed(permission.granted))
      .catch(() => setAllowed(null));
  }, []);
  useEffect(() => {
    readPermission();
    const subscription = AppState.addEventListener("change", (state) => {
      if (state === "active") readPermission();
    });
    return () => subscription.remove();
  }, [readPermission]);

  const set = (kind: AlertKind, value: boolean) => store.setNotifyPrefs({ ...prefs, [kind]: value });

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
            Notifications
          </Text>
          <Text style={styles.intro}>
            Agentman only notifies you when the app is closed. While it’s open, you’re already
            looking.
          </Text>

          {allowed === false ? (
            <Appear>
              <View style={[styles.card, styles.blocked]}>
                <Feather name="bell-off" size={16} color={color.needsYouText} />
                <View style={styles.blockedCopy}>
                  <Text style={styles.blockedTitle}>Notifications are off in iOS Settings</Text>
                  <Text style={styles.body}>Nothing below can reach you until they are on.</Text>
                  <MotionPressable
                    onPress={() => void Linking.openSettings()}
                    hitSlop={10}
                    style={styles.link}
                    accessibilityRole="link"
                  >
                    <Text style={styles.linkText}>Open Settings</Text>
                  </MotionPressable>
                </View>
              </View>
            </Appear>
          ) : null}

          <Appear delay={allowed === false ? 30 : 0}>
            <Text style={styles.sectionLabel}>Notify me when</Text>
            <View style={styles.card}>
              <ToggleRow
                icon="alert-circle"
                tint={color.needsYou}
                label="An agent needs you"
                detail="It’s waiting on an approval or a question, and can’t go on without you."
                value={prefs.needsYou}
                onChange={(value) => set("needsYou", value)}
              />
              <View style={styles.divider} />
              <ToggleRow
                icon="check-circle"
                tint={color.working}
                label="An agent finishes"
                detail="It finished a turn you weren’t watching. Quick turns and ones you were following stay quiet."
                value={prefs.finished}
                onChange={(value) => set("finished", value)}
              />
            </View>
            <Text style={styles.footnote}>
              {isPushActive()
                ? "Your Mac sends these, so they reach you even when the app has been closed for a while."
                : pushFailureReason() ||
                  "Without push, these only arrive while the app is still running in the background."}
            </Text>
          </Appear>
        </ContentColumn>
      </ScrollView>
    </View>
  );
}

function ToggleRow({
  icon,
  tint,
  label,
  detail,
  value,
  onChange,
}: {
  icon: FeatherName;
  tint: string;
  label: string;
  detail: string;
  value: boolean;
  onChange(value: boolean): void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.toggleRow}>
      <View style={styles.toggleIcon}>
        <Feather name={icon} size={16} color={tint} />
      </View>
      <View style={styles.toggleCopy}>
        <Text style={styles.toggleLabel}>{label}</Text>
        <Text style={styles.body}>{detail}</Text>
      </View>
      <Switch
        value={value}
        onValueChange={onChange}
        trackColor={{ true: color.working, false: color.fillStrong }}
        thumbColor={color.onAccent}
        ios_backgroundColor={color.fillStrong}
        accessibilityLabel={label}
      />
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
    intro: { fontFamily: font.sans, fontSize: size.body, lineHeight: 22, color: c.muted },
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
    divider: { height: StyleSheet.hairlineWidth, backgroundColor: c.line },
    body: { fontFamily: font.sans, fontSize: size.caption, color: c.muted, lineHeight: 19 },
    toggleRow: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      paddingVertical: space.md,
    },
    toggleIcon: {
      width: 36,
      height: 36,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
    },
    toggleCopy: { flex: 1, gap: 2 },
    toggleLabel: { fontFamily: font.sansMedium, fontSize: size.body, color: c.text },
    footnote: {
      fontFamily: font.sans,
      fontSize: size.label,
      lineHeight: 16,
      color: c.faint,
      marginTop: space.sm,
      marginHorizontal: space.xxs,
    },
    blocked: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: space.md,
      paddingVertical: space.md,
      marginTop: space.lg,
      backgroundColor: c.needsYouWash,
      borderColor: c.needsYouEdge,
    },
    blockedCopy: { flex: 1, gap: space.xs },
    blockedTitle: { fontFamily: font.sansBold, fontSize: size.body, color: c.text },
    link: { alignSelf: "flex-start", minHeight: 24, justifyContent: "center", marginTop: space.xs },
    linkText: { fontFamily: font.sansMedium, fontSize: size.label, color: c.workingText },
  });
