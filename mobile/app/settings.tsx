import Feather from "@expo/vector-icons/Feather";
import { useRouter } from "expo-router";
import { useEffect } from "react";
import { Linking, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Appear } from "../components/Appear";
import { BrandMark } from "../components/BrandMark";
import { ContentColumn } from "../components/ContentColumn";
import { DevicePicker } from "../components/DevicePicker";
import { MotionPressable } from "../components/MotionPressable";
import { LinkRow } from "../components/SettingsKit";
import { useStyles, useTheme } from "../lib/appearance";
import { AppearancePreference } from "../lib/appearance-policy";
import type { Look } from "../lib/theme";
import { useManager } from "../lib/manager";
import { modelName } from "../lib/manager-models";
import { NotifyPrefs } from "../lib/notification-prefs";
import { PRIVACY_POLICY_URL } from "../lib/privacy";
import { pushFailureReason } from "../lib/push";
import { hostName, useFleet } from "../lib/fleet";
import { useStore } from "../lib/store";
import { appSummary, macSummary } from "../lib/updates";
import { useUpdates } from "../lib/updates-context";
import { ago, font, Palette, radius, size, space, scaled } from "../lib/theme";
import { edged, eyebrow, screenTitle } from "../components/Brand";

const APPEARANCES: { value: AppearancePreference; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "Match phone" },
];

/** Today's look, or the website's while it is being refined. */
const LOOKS: { value: Look; label: string }[] = [
  { value: "classic", label: "Classic" },
  { value: "site", label: "New" },
];

export default function Settings() {
  const store = useStore();
  const activeManager = useManager();
  const fleet = useFleet();
  const updates = useUpdates();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color, preference, setPreference, look, setLook } = useTheme();

  const multiDevice = fleet.hosts.length > 1;
  const managerHosts = fleet.hosts.filter((host) => host.manager.supported);
  const managerHost =
    managerHosts.find((host) => host.id === fleet.active) ??
    managerHosts[0];
  const manager = managerHost?.manager ?? activeManager;
  const managerHostIndex = managerHost ? fleet.hosts.findIndex((host) => host.id === managerHost.id) : -1;
  const managerDeviceName =
    multiDevice && managerHost && managerHostIndex >= 0 ? hostName(managerHost, managerHostIndex) : null;

  // The counts beside Notes and Keys for whichever device's Agentman is shown.
  const { loadNotes, loadKeys, supported: managerSupported } = manager;
  useEffect(() => {
    if (!managerSupported) return;
    void loadNotes();
    void loadKeys();
  }, [managerSupported, loadNotes, loadKeys, managerHost?.id]);

  const openAgentmanSetting = (path: string) => {
    if (managerHost && managerHost.id !== fleet.active) fleet.select(managerHost.id);
    router.push(path as never);
  };

  const pushActiveCount = fleet.hosts.filter((host) => host.store.pushActive).length;
  const pushSummary = multiDevice
    ? pushActiveCount === fleet.hosts.length
      ? "On — your paired computers can reach you when the app is closed"
      : pushActiveCount > 0
        ? `On for ${pushActiveCount} of ${fleet.hosts.length} devices`
        : pushFailureReason() || "Unavailable in this build"
    : store.pushActive
      ? "On — your Mac can reach you when the app is closed"
      : pushFailureReason() || "Unavailable in this build";

  const alertSummary = multiDevice ? fleetNotifySummary(fleet.hosts.map((host) => host.store.notifyPrefs)) : notifySummary(store.notifyPrefs);

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
              <LinkRow icon="monitor" label="Devices" value={String(fleet.hosts.length)} onPress={() => router.push("/devices")} />
              {multiDevice ? (
                fleet.hosts.map((host, index) => (
                  <View key={host.id}>
                    <View style={styles.divider} />
                    <Row
                      label={hostName(host, index)}
                      value={
                        host.store.daemonOnline
                          ? "Online"
                          : host.store.lastSeenAt
                            ? `Offline · last seen ${ago(host.store.lastSeenAt)} ago`
                            : "Offline"
                      }
                      status={host.store.daemonOnline ? "online" : "offline"}
                    />
                  </View>
                ))
              ) : (
                <>
                  <View style={styles.divider} />
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
                </>
              )}
              <View style={styles.divider} />
              {/* Push either works or it does not, and when it does not the
                  app has the reason. Withholding it turns a one-line fix into
                  a round of guessing against a ten-minute build. */}
              {/* Untinted on purpose: `ok` is for dots, icons and counts
                  beside a label, never for two lines of prose. The dot on
                  "Your Mac" above is what green is for on this screen. */}
              <Row
                label="Background alerts"
                value={pushSummary}
              />
            </View>
          </Appear>

          <Appear delay={15}>
            <Text style={styles.sectionLabel}>Notifications</Text>
            <MotionPressable
              onPress={() => router.push("/notifications")}
              style={[styles.card, styles.linkRow]}
              pressedScale={0.99}
              accessibilityRole="button"
              accessibilityLabel={`Notifications: ${alertSummary}`}
            >
              <Text style={styles.rowLabel}>Alerts</Text>
              <View style={styles.valueWrap}>
                <Text style={styles.rowValue} numberOfLines={1}>
                  {alertSummary}
                </Text>
                <Feather name="chevron-right" size={16} color={color.faint} />
              </View>
            </MotionPressable>
          </Appear>

          <Appear delay={16}>
            <Text style={styles.sectionLabel}>Usage</Text>
            <View style={styles.card}>
              <LinkRow icon="bar-chart-2" label="What the agents spent" onPress={() => router.push("/usage")} />
            </View>
          </Appear>

          {managerSupported ? (
            <Appear delay={18}>
              <Text style={styles.sectionLabel}>
                {managerHosts.length === 1 && managerDeviceName ? `Agentman · ${managerDeviceName}` : "Agentman"}
              </Text>
              {managerHosts.length > 1 && managerHost ? (
                <DevicePicker
                  hosts={managerHosts}
                  value={managerHost.id}
                  onChange={(id) => fleet.select(id)}
                  compact
                />
              ) : null}
              <View style={styles.card}>
                <LinkRow icon="cpu" label="Models" value={manager.settings ? modelName(manager.settings.chatModel) : undefined}
                  onPress={() => openAgentmanSetting("/agentman-settings/models")} />
                <View style={styles.divider} />
                <LinkRow icon="message-circle" label="Personality" onPress={() => openAgentmanSetting("/agentman-settings/personality")} />
                <View style={styles.divider} />
                <LinkRow icon="shield" label="Permissions"
                  value={manager.settings ? (manager.settings.autoApprove ? "Approves low-risk" : "Asks every time") : undefined}
                  onPress={() => openAgentmanSetting("/agentman-settings/permissions")} />
                <View style={styles.divider} />
                <LinkRow icon="book-open" label="Notes" value={manager.notes.length ? String(manager.notes.length) : undefined}
                  onPress={() => openAgentmanSetting("/agentman-settings/notes")} />
                <View style={styles.divider} />
                <LinkRow icon="key" label="Keys" value={manager.keys.length ? String(manager.keys.length) : undefined}
                  onPress={() => openAgentmanSetting("/agentman-settings/keys")} />
              </View>
            </Appear>
          ) : null}

          <Appear delay={22}>
            <Text style={styles.sectionLabel}>Versions</Text>
            <View style={styles.card}>
              <VersionRow
                label="This app"
                value={appSummary(updates.appVersion, updates.appBuild, updates.app)}
                behind={Boolean(updates.app)}
                onPress={() => router.push("/update?kind=app")}
              />
              {multiDevice ? (
                fleet.hosts.map((host, index) => (
                  <View key={host.id}>
                    <View style={styles.divider} />
                    <VersionRow
                      label={hostName(host, index)}
                      value={macSummary(host.store.daemonInfo)}
                      behind={Boolean(updates.macFor(host.id))}
                      onPress={() => router.push(`/update?kind=mac&device=${encodeURIComponent(host.id)}`)}
                    />
                  </View>
                ))
              ) : (
                <>
                  <View style={styles.divider} />
                  <VersionRow
                    label="Your Mac"
                    value={macSummary(updates.daemonInfo)}
                    behind={Boolean(updates.mac)}
                    onPress={() => router.push("/update?kind=mac")}
                  />
                </>
              )}
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
            <Text style={styles.sectionLabel}>Theme</Text>
            <View style={styles.segmented} accessibilityRole="radiogroup">
              {LOOKS.map((option) => {
                const active = option.value === look;
                return (
                  <MotionPressable
                    key={option.value}
                    onPress={() => setLook(option.value)}
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
            <Text style={styles.themeNote}>New is the website's look, still being designed.</Text>
          </Appear>

          <Appear delay={60}>
            <Text style={styles.sectionLabel}>Privacy</Text>
            <View style={[styles.card, styles.privacyCard]}>
              <View style={styles.privacyIcon}>
                <Feather name="lock" size={16} color={color.muted} />
              </View>
              <View style={styles.privacyCopy}>
                <Text style={styles.privacyTitle}>Your Mac keeps your history</Text>
                <Text style={styles.body}>
                  The relay can read live traffic, but does not store transcripts.
                </Text>
                <MotionPressable
                  onPress={() => void Linking.openURL(PRIVACY_POLICY_URL)}
                  style={styles.privacyLink}
                  hitSlop={10}
                  accessibilityRole="link"
                  accessibilityLabel="Read Agentman privacy policy"
                >
                  <Text style={styles.privacyLinkText}>Privacy policy</Text>
                </MotionPressable>
              </View>
            </View>
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

/** The current choice in a few words, for the row that opens it. */
function notifySummary(prefs: NotifyPrefs): string {
  if (prefs.needsYou && prefs.finished) return "Needs you and finished";
  if (prefs.needsYou) return "Only when needed";
  if (prefs.finished) return "Only when finished";
  return "Off";
}

function fleetNotifySummary(list: readonly NotifyPrefs[]): string {
  if (list.length === 0) return "Off";
  const first = notifySummary(list[0]);
  return list.every((item) => notifySummary(item) === first) ? first : "Custom by device";
}

/** A version, which opens what is new when there is something newer. */
function VersionRow({
  label,
  value,
  behind,
  onPress,
}: {
  label: string;
  value: string;
  behind: boolean;
  onPress(): void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  if (!behind) return <Row label={label} value={value} />;
  return (
    <MotionPressable
      onPress={onPress}
      style={styles.row}
      pressedScale={0.99}
      accessibilityRole="button"
      accessibilityLabel={`${label}: ${value}`}
    >
      <Text style={styles.rowLabel}>{label}</Text>
      <View style={styles.valueWrap}>
        <Text style={[styles.rowValue, { color: color.workingText }]} numberOfLines={2}>
          {value}
        </Text>
        <Feather name="chevron-right" size={16} color={color.faint} />
      </View>
    </MotionPressable>
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
            mono && { ...font.mono },
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

const makeSiteStyles = (c: Palette) =>
  StyleSheet.create({
    themeNote: { ...font.sans, fontSize: size.label, color: c.faint, marginTop: space.sm, marginLeft: space.xxs },
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
      ...edged(c, 2),
    },
    title: {
      ...screenTitle(c),
      marginTop: space.lg,
      marginBottom: space.xs,
    },

    sectionLabel: {
      ...eyebrow(c),
      marginLeft: space.xxs,
      marginTop: space.xl,
      marginBottom: space.sm,
    },
    card: {
      backgroundColor: c.surface,
      borderRadius: radius.xl,
      paddingHorizontal: space.lg,
      ...edged(c, 3),
    },
    body: { ...font.sans, fontSize: size.caption, color: c.muted, lineHeight: scaled(19) },

    row: {
      minHeight: 56,
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "space-between",
      gap: space.md,
      paddingVertical: space.md,
    },
    rowLabel: { ...font.sansMedium, fontSize: size.body, color: c.text },
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
      ...font.sans,
      fontSize: size.caption,
      color: c.muted,
    },
    divider: { height: StyleSheet.hairlineWidth, backgroundColor: c.line },
    linkRow: {
      minHeight: 56,
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "space-between",
      gap: space.md,
    },

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
    segmentLabel: { ...font.sansMedium, fontSize: size.caption, color: c.muted },
    segmentLabelActive: { ...font.sansBold, color: c.text },

    privacyCard: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: space.md,
      paddingVertical: space.md,
    },
    privacyIcon: {
      width: 36,
      height: 36,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
      // Neutral: `ok` means connected, delivered, added. A lock is none of
      // those, and the system keeps that colour small and literal.
      backgroundColor: c.fill,
    },
    privacyCopy: { flex: 1, gap: space.xs },
    privacyTitle: { ...font.sansBold, fontSize: size.body, color: c.text },
    privacyLink: { alignSelf: "flex-start", justifyContent: "center", minHeight: 24, marginTop: space.xs },
    // Cobalt, the brand and the colour of anything tappable. Green said
    // "connected", which is not what a policy link means.
    privacyLinkText: { ...font.sansMedium, fontSize: size.label, color: c.workingText },

    signature: {
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "center",
      gap: space.sm,
      marginTop: space.xxl,
      opacity: 0.55,
    },
    signatureText: { ...font.sansBold, fontSize: size.caption, color: c.text },
  });

const makeClassicStyles = (c: Palette) =>
  StyleSheet.create({
    themeNote: { ...font.sans, fontSize: size.label, color: c.faint, marginTop: space.sm, marginLeft: space.xxs },
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
      ...font.sansBold,
      fontSize: size.display,
      letterSpacing: -1.2,
      color: c.text,
      marginTop: space.lg,
      marginBottom: space.xs,
    },

    sectionLabel: {
      ...font.sansBold,
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
    body: { ...font.sans, fontSize: size.caption, color: c.muted, lineHeight: scaled(19) },

    row: {
      minHeight: 56,
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "space-between",
      gap: space.md,
      paddingVertical: space.md,
    },
    rowLabel: { ...font.sansMedium, fontSize: size.body, color: c.text },
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
      ...font.sans,
      fontSize: size.caption,
      color: c.muted,
    },
    divider: { height: StyleSheet.hairlineWidth, backgroundColor: c.line },
    linkRow: {
      minHeight: 56,
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "space-between",
      gap: space.md,
    },

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
    segmentLabel: { ...font.sansMedium, fontSize: size.caption, color: c.muted },
    segmentLabelActive: { ...font.sansBold, color: c.text },

    privacyCard: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: space.md,
      paddingVertical: space.md,
    },
    privacyIcon: {
      width: 36,
      height: 36,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
      // Neutral: `ok` means connected, delivered, added. A lock is none of
      // those, and the system keeps that colour small and literal.
      backgroundColor: c.fill,
    },
    privacyCopy: { flex: 1, gap: space.xs },
    privacyTitle: { ...font.sansBold, fontSize: size.body, color: c.text },
    privacyLink: { alignSelf: "flex-start", justifyContent: "center", minHeight: 24, marginTop: space.xs },
    // Cobalt, the brand and the colour of anything tappable. Green said
    // "connected", which is not what a policy link means.
    privacyLinkText: { ...font.sansMedium, fontSize: size.label, color: c.workingText },

    signature: {
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "center",
      gap: space.sm,
      marginTop: space.xxl,
      opacity: 0.55,
    },
    signatureText: { ...font.sansBold, fontSize: size.caption, color: c.text },
  });

// Today's styles for the Classic theme, the website's look's above them
// (see lib/theme: a palette's look). Keys only the new look has keep its
// values, for what only the new look shows.
type Styles = ReturnType<typeof makeSiteStyles> & ReturnType<typeof makeClassicStyles>;
const makeStyles = (c: Palette): Styles =>
  (c.look === "site" ? makeSiteStyles(c) : { ...makeSiteStyles(c), ...makeClassicStyles(c) }) as Styles;

