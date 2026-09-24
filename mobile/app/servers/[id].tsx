import Feather from "@expo/vector-icons/Feather";
import { useLocalSearchParams, useRouter } from "expo-router";
import * as WebBrowser from "expo-web-browser";
import { useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Platform,
  ScrollView,
  Share,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { AgentIcon } from "../../components/AgentIcon";
import { Appear } from "../../components/Appear";
import { ContentColumn } from "../../components/ContentColumn";
import { MotionPressable } from "../../components/MotionPressable";
import { useStyles, useTheme } from "../../lib/appearance";
import { Server } from "../../lib/protocol";
import { useStore } from "../../lib/store";
import { agentLabel, font, Palette, radius, shortPath, size, space } from "../../lib/theme";

/**
 * The web servers one agent has started.
 *
 * The daemon finds them on its own — no command to run, nothing to set up —
 * so this page fills in as the agent works. Tapping a server asks the Mac to
 * share it through the relay, then opens the link in an in-app browser.
 */
export default function Servers() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const sessionId = decodeURIComponent(id ?? "");
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const session = store.sessions.find((candidate) => candidate.id === sessionId);
  const servers = session?.servers ?? [];

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
              accessibilityLabel="Back to session"
            >
              <Feather name="chevron-left" size={20} color={color.text} />
            </MotionPressable>
          </View>

          <Text style={styles.title} accessibilityRole="header">
            Servers
          </Text>
          {session ? (
            <View style={styles.sessionLine}>
              <AgentIcon kind={session.kind} size={16} />
              <Text style={styles.sessionName} numberOfLines={1}>
                {session.name} · {shortPath(session.cwd)}
              </Text>
            </View>
          ) : null}

          {!store.daemonOnline ? (
            <View style={styles.offline}>
              <Feather name="wifi-off" size={13} color={color.errorText} />
              <Text style={styles.offlineText}>
                Your Mac is offline. Servers open again once it reconnects.
              </Text>
            </View>
          ) : null}

          {servers.length === 0 ? (
            <Appear>
              <View style={styles.empty}>
                <View style={styles.emptyIcon}>
                  <Feather name="server" size={20} color={color.muted} />
                </View>
                <Text style={styles.emptyTitle}>No servers yet</Text>
                <Text style={styles.emptyBody}>
                  When {session ? agentLabel(session.kind).name : "the agent"} starts a web
                  server — a dev server, a preview, an API — it shows up here within a few
                  seconds.
                </Text>
              </View>
            </Appear>
          ) : (
            <View style={styles.list}>
              {servers.map((server, index) => (
                <Appear key={server.port} delay={index * 30}>
                  <ServerCard sessionId={sessionId} server={server} />
                </Appear>
              ))}
            </View>
          )}

          {servers.length > 0 ? (
            <Text style={styles.footnote}>
              Opening a server creates a link only you know. Anyone you send it to can
              view the server until you stop sharing or the server stops.
            </Text>
          ) : null}
        </ContentColumn>
      </ScrollView>
    </View>
  );
}

function ServerCard({ sessionId, server }: { sessionId: string; server: Server }) {
  const store = useStore();
  const styles = useStyles(makeStyles);
  const { color, scheme } = useTheme();
  const [opening, setOpening] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const shared = Boolean(server.link);
  const name = server.title || `localhost:${server.port}`;

  const view = async (link: string) => {
    await WebBrowser.openBrowserAsync(link, {
      // The in-app browser keeps the user in Agentman: Done returns to this
      // page rather than leaving them in Safari.
      controlsColor: color.working,
      toolbarColor: color.surface,
      dismissButtonStyle: "done",
      presentationStyle: WebBrowser.WebBrowserPresentationStyle.PAGE_SHEET,
      enableBarCollapsing: true,
    }).catch(() => {});
  };

  const open = async () => {
    setError(null);
    if (server.link) {
      await view(server.link);
      return;
    }
    setOpening(true);
    try {
      const link = await store.openServer(sessionId, server.port);
      await view(link);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setOpening(false);
    }
  };

  const share = () => {
    if (!server.link) return;
    void Share.share(
      Platform.OS === "ios" ? { url: server.link } : { message: server.link },
    ).catch(() => {});
  };

  const stop = () => {
    const perform = () => store.closeServer(sessionId, server.port);
    if (Platform.OS === "web") {
      if (globalThis.confirm("Stop sharing this server? Its link stops working.")) perform();
      return;
    }
    Alert.alert("Stop sharing?", "The link stops working for everyone who has it.", [
      { text: "Keep sharing", style: "cancel" },
      { text: "Stop sharing", style: "destructive", onPress: perform },
    ]);
  };

  return (
    <View style={styles.card}>
      <MotionPressable
        onPress={open}
        disabled={opening || !store.daemonOnline}
        style={styles.cardMain}
        pressedScale={0.985}
        accessibilityRole="button"
        accessibilityLabel={`${name}, port ${server.port}. ${shared ? "Shared. " : ""}Open`}
        accessibilityState={{ busy: opening, disabled: !store.daemonOnline }}
      >
        <View style={[styles.serverIcon, shared && { backgroundColor: color.okWash }]}>
          <Feather name="globe" size={18} color={shared ? color.ok : color.muted} />
        </View>
        <View style={styles.cardBody}>
          <Text style={styles.serverName} numberOfLines={1}>
            {name}
          </Text>
          <Text style={styles.serverMeta} numberOfLines={1}>
            :{server.port}
            {server.command ? ` · ${server.command}` : ""}
            {shared ? " · shared" : ""}
          </Text>
        </View>
        {opening ? (
          <ActivityIndicator size="small" color={color.text} />
        ) : (
          <View style={[styles.openPill, !store.daemonOnline && styles.openPillDisabled]}>
            <Text style={styles.openLabel}>Open</Text>
            <Feather name="arrow-up-right" size={14} color={color.onInverse} />
          </View>
        )}
      </MotionPressable>

      {error ? (
        <View style={styles.error}>
          <Feather name="alert-circle" size={13} color={color.errorText} />
          <Text style={styles.errorText}>{error}</Text>
        </View>
      ) : null}

      {shared ? (
        <View style={styles.linkRow}>
          <Text style={styles.link} numberOfLines={1} selectable>
            {server.link}
          </Text>
          <MotionPressable
            onPress={share}
            hitSlop={8}
            pressedScale={0.94}
            style={styles.linkAction}
            accessibilityRole="button"
            accessibilityLabel="Share link"
          >
            <Feather name="share" size={15} color={color.text} />
          </MotionPressable>
          <MotionPressable
            onPress={stop}
            hitSlop={8}
            pressedScale={0.94}
            style={styles.linkAction}
            accessibilityRole="button"
            accessibilityLabel="Stop sharing"
          >
            <Feather name="x-circle" size={15} color={scheme === "dark" ? color.errorText : color.error} />
          </MotionPressable>
        </View>
      ) : null}
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
    },
    sessionLine: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      marginTop: space.xs,
      marginBottom: space.lg,
    },
    sessionName: { flex: 1, fontFamily: font.mono, fontSize: 12, color: c.muted },

    offline: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      padding: space.md,
      borderRadius: radius.md,
      backgroundColor: c.errorWash,
      marginBottom: space.md,
    },
    offlineText: { flex: 1, fontFamily: font.sans, fontSize: size.caption, color: c.errorText },

    list: { gap: space.sm },
    card: {
      backgroundColor: c.surface,
      borderRadius: radius.xl,
      borderWidth: 1,
      borderColor: c.line,
      overflow: "hidden",
    },
    cardMain: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      paddingVertical: 13,
      paddingHorizontal: 14,
    },
    serverIcon: {
      width: 40,
      height: 40,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
    },
    cardBody: { flex: 1, gap: 3 },
    serverName: { fontFamily: font.sansMedium, fontSize: size.body, color: c.text },
    serverMeta: { fontFamily: font.mono, fontSize: 12, color: c.muted },
    openPill: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.xs,
      paddingVertical: 7,
      paddingHorizontal: 12,
      borderRadius: radius.pill,
      backgroundColor: c.inverse,
    },
    openPillDisabled: { opacity: 0.35 },
    openLabel: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.onInverse },

    error: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: space.sm,
      marginHorizontal: 14,
      marginBottom: space.md,
      padding: space.sm,
      borderRadius: radius.sm,
      backgroundColor: c.errorWash,
    },
    errorText: { flex: 1, fontFamily: font.sans, fontSize: size.caption, color: c.errorText },

    linkRow: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingVertical: space.sm,
      paddingLeft: 14,
      paddingRight: space.sm,
      borderTopWidth: 1,
      borderTopColor: c.line,
      backgroundColor: c.fill,
    },
    link: { flex: 1, fontFamily: font.mono, fontSize: 12, color: c.textSecondary },
    linkAction: {
      width: 32,
      height: 32,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
    },

    empty: {
      alignItems: "center",
      paddingVertical: space.xxl,
      paddingHorizontal: space.xl,
      gap: space.sm,
    },
    emptyIcon: {
      width: 48,
      height: 48,
      borderRadius: radius.lg,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
      marginBottom: space.xs,
    },
    emptyTitle: { fontFamily: font.sansBold, fontSize: size.title, color: c.text },
    emptyBody: {
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 19,
      color: c.muted,
      textAlign: "center",
    },
    footnote: {
      fontFamily: font.sans,
      fontSize: size.label,
      lineHeight: 17,
      color: c.faint,
      marginTop: space.lg,
      marginHorizontal: space.xxs,
    },
  });
