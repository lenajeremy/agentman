import Feather from "@expo/vector-icons/Feather";
import * as Clipboard from "expo-clipboard";
import * as Haptics from "expo-haptics";
import { useLocalSearchParams, useRouter } from "expo-router";
import { ComponentProps, useEffect, useRef, useState } from "react";
import { Linking, Platform, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { ContentColumn } from "../components/ContentColumn";
import { MotionPressable } from "../components/MotionPressable";
import { useStyles, useTheme } from "../lib/appearance";
import { font, Palette, radius, size, space } from "../lib/theme";
import { behindText, PromptKind, versionLabel } from "../lib/updates";
import { useUpdates } from "../lib/updates-context";

type FeatherName = ComponentProps<typeof Feather>["name"];

interface Section {
  key: string;
  heading: string;
  changes: string[];
  url?: string;
}

/**
 * A newer version of the app, or of agentman on the Mac, and what it brings.
 *
 * Shown once per version from the board, and again from Settings whenever
 * someone wants it. Either way it only informs: the app updates through
 * TestFlight, and the Mac through a command run on the Mac.
 */
export default function UpdateSheet() {
  const params = useLocalSearchParams<{ kind?: string }>();
  const kind: PromptKind = params.kind === "mac" ? "mac" : "app";
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const updates = useUpdates();
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Seen is enough: someone who closed it has been told, and Settings keeps
  // the offer for later.
  const { markPrompted } = updates;
  useEffect(() => {
    markPrompted(kind);
  }, [kind, markPrompted]);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);

  const close = () => (router.canGoBack() ? router.back() : router.replace("/"));

  let icon: FeatherName;
  let title: string;
  let lede: string;
  let sections: Section[] = [];
  let primary: { label: string; icon: FeatherName; onPress(): void } | null = null;
  const app = updates.app;
  const mac = updates.mac;

  if (kind === "app" && app) {
    icon = "smartphone";
    title = "A new version of Agentman";
    lede = `Version ${versionLabel(app.latest.version, app.latest.build)} is ready in TestFlight. ` +
      `You have ${versionLabel(updates.appVersion, app.current)}.`;
    sections = app.missing.map((build) => ({
      key: String(build.build),
      heading: `${versionLabel(build.version, build.build)} · ${shortDate(build.date)}`,
      changes: build.changes,
    }));
    primary = {
      label: "Open TestFlight",
      icon: "download",
      onPress: () => {
        void Linking.openURL(app.url).catch(() => {});
        close();
      },
    };
  } else if (kind === "mac" && mac) {
    icon = "monitor";
    title = `Your Mac is ${behindText(mac.behind)}`;
    lede = `agentman ${mac.latest} is out. Your Mac runs ${mac.current}.`;
    sections = mac.releases.map((release) => ({
      key: release.version,
      heading: release.date ? `${release.version} · ${shortDate(release.date)}` : release.version,
      changes: release.changes ?? [],
      url: release.url,
    }));
    primary = {
      label: copied ? "Copied" : "Copy upgrade command",
      icon: copied ? "check" : "copy",
      onPress: () => {
        void Clipboard.setStringAsync(mac.upgrade);
        if (Platform.OS !== "web") void Haptics.selectionAsync().catch(() => {});
        setCopied(true);
        if (timer.current) clearTimeout(timer.current);
        timer.current = setTimeout(() => setCopied(false), 1600);
      },
    };
  } else {
    icon = "check-circle";
    title = "You’re up to date";
    lede = kind === "app"
      ? "This is the newest build of Agentman."
      : "Your Mac runs the newest agentman.";
  }

  return (
    <View style={styles.page}>
      <ScrollView contentContainerStyle={{ paddingBottom: space.xl }}>
        <ContentColumn style={styles.content}>
          <View style={styles.grabber} />
          <View style={styles.iconTile}>
            <Feather name={icon} size={22} color={color.text} />
          </View>
          <Text style={styles.title} accessibilityRole="header">
            {title}
          </Text>
          <Text style={styles.lede}>{lede}</Text>

          {sections.length > 0 ? <Text style={styles.sectionLabel}>What’s new</Text> : null}
          {sections.map((section) => (
            <View key={section.key} style={styles.card}>
              <Text style={styles.cardHeading}>{section.heading}</Text>
              {section.changes.length > 0 ? (
                section.changes.map((change, index) => (
                  <View key={index} style={styles.change}>
                    <View style={styles.bullet} />
                    <Text style={styles.changeText}>{change}</Text>
                  </View>
                ))
              ) : section.url ? (
                <MotionPressable
                  onPress={() => void Linking.openURL(section.url!).catch(() => {})}
                  hitSlop={10}
                  style={styles.link}
                  accessibilityRole="link"
                >
                  <Text style={styles.linkText}>Read the release notes</Text>
                </MotionPressable>
              ) : (
                <Text style={styles.changeText}>Fixes and improvements.</Text>
              )}
            </View>
          ))}

          {kind === "mac" && mac ? (
            <>
              <Text style={styles.sectionLabel}>Upgrade on your Mac</Text>
              <View style={styles.command}>
                <Text style={styles.commandText} selectable>
                  {mac.upgrade}
                </Text>
              </View>
              <Text style={styles.footnote}>
                Then restart am serve, so the new version takes over.
              </Text>
              <MotionPressable
                onPress={() => void Linking.openURL(mac.changelog).catch(() => {})}
                hitSlop={10}
                style={styles.link}
                accessibilityRole="link"
              >
                <Text style={styles.linkText}>Every release’s notes</Text>
              </MotionPressable>
            </>
          ) : null}
        </ContentColumn>
      </ScrollView>

      <ContentColumn style={[styles.footer, { paddingBottom: Math.max(insets.bottom, space.lg) }]}>
        {primary ? (
          <MotionPressable
            onPress={primary.onPress}
            style={styles.primaryButton}
            accessibilityRole="button"
          >
            <Feather name={primary.icon} size={18} color={color.onInverse} />
            <Text style={styles.primaryButtonText}>{primary.label}</Text>
          </MotionPressable>
        ) : null}
        <MotionPressable onPress={close} style={styles.textButton} accessibilityRole="button">
          <Text style={styles.textButtonLabel}>{primary ? "Not now" : "Close"}</Text>
        </MotionPressable>
      </ContentColumn>
    </View>
  );
}

function shortDate(ms: number): string {
  return new Date(ms).toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    content: { paddingHorizontal: space.lg },
    grabber: {
      alignSelf: "center",
      width: 36,
      height: 5,
      borderRadius: radius.pill,
      backgroundColor: c.fillStrong,
      marginTop: space.sm,
    },
    iconTile: {
      width: 48,
      height: 48,
      borderRadius: radius.lg,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
      marginTop: space.xl,
    },
    title: {
      fontFamily: font.sansBold,
      fontSize: size.heading + 4,
      letterSpacing: -0.6,
      color: c.text,
      marginTop: space.lg,
    },
    lede: {
      fontFamily: font.sans,
      fontSize: size.body,
      lineHeight: 22,
      color: c.muted,
      marginTop: space.xs,
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
      borderWidth: 1,
      borderColor: c.line,
      padding: space.lg,
      gap: space.sm,
      marginBottom: space.sm,
    },
    cardHeading: { fontFamily: font.sansBold, fontSize: size.body, color: c.text },
    change: { flexDirection: "row", gap: space.sm },
    bullet: {
      width: 5,
      height: 5,
      borderRadius: radius.pill,
      backgroundColor: c.faint,
      marginTop: 8,
    },
    changeText: { flex: 1, fontFamily: font.sans, fontSize: size.caption + 1, lineHeight: 20, color: c.textSecondary },
    command: {
      backgroundColor: c.fill,
      borderRadius: radius.md,
      paddingHorizontal: space.md,
      paddingVertical: space.md,
    },
    commandText: { fontFamily: font.mono, fontSize: size.caption, lineHeight: 19, color: c.text },
    footnote: {
      fontFamily: font.sans,
      fontSize: size.label,
      lineHeight: 16,
      color: c.faint,
      marginTop: space.sm,
      marginHorizontal: space.xxs,
    },
    link: { alignSelf: "flex-start", minHeight: 24, justifyContent: "center", marginTop: space.sm },
    linkText: { fontFamily: font.sansMedium, fontSize: size.label + 1, color: c.workingText },
    footer: {
      paddingHorizontal: space.lg,
      paddingTop: space.md,
      gap: space.xs,
      borderTopWidth: StyleSheet.hairlineWidth,
      borderTopColor: c.line,
      backgroundColor: c.paper,
    },
    primaryButton: {
      minHeight: 56,
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "center",
      gap: 10,
      borderRadius: radius.pill,
      backgroundColor: c.inverse,
      paddingHorizontal: space.xl,
    },
    primaryButtonText: { fontFamily: font.sansBold, fontSize: 16, color: c.onInverse },
    textButton: { minHeight: 44, alignItems: "center", justifyContent: "center" },
    textButtonLabel: { fontFamily: font.sansMedium, fontSize: size.body, color: c.textSecondary },
  });
