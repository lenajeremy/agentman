import Feather from "@expo/vector-icons/Feather";
import { useIsFocused, useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState, type ComponentRef, type ReactNode } from "react";
import {
  Keyboard,
  KeyboardAvoidingView,
  Platform,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  useWindowDimensions,
  View,
} from "react-native";
import Animated, { FadeIn, FadeOut, LinearTransition, useReducedMotion } from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { AgentmanChat } from "../components/AgentmanChat";
import { AskBar } from "../components/AskBar";
import { BoardActions, BoardCard, BoardMissionRow, BoardOfferCard, type ViewRef } from "../components/BoardCard";
import { BoardSheet, boardSheetTop } from "../components/BoardSheet";
import { ContentColumn } from "../components/ContentColumn";
import { Loader } from "../components/Loader";
import { MotionPressable } from "../components/MotionPressable";
import { Popover, type PopoverItem } from "../components/Popover";
import { AGENT_COLOR_CHOICES, agentCardTint } from "../lib/agent-colors";
import { useStyles, useTheme } from "../lib/appearance";
import { boardBrief, boardSections, type BoardColumn } from "../lib/board";
import { useBoardPrefs } from "../lib/board-prefs";
import { folderContains, folderLabel } from "../lib/folders";
import { useManager } from "../lib/manager";
import { missionOf } from "../lib/manager-feed";
import type { Session } from "../lib/protocol";
import { useStore } from "../lib/store";
import { font, Palette, radius, scaled, size, space } from "../lib/theme";

type Anchor = { x: number; y: number; width: number; height: number };

/** Measures the control that opened a menu, for hanging the menu off it. */
function measure(view: ViewRef | null, then: (anchor: Anchor) => void) {
  view?.measureInWindow((x, y, width, height) => then({ x, y, width, height }));
}

/** Re-renders on a timer, so "2m" becomes "3m" by itself. */
function useTicking(everyMs: number) {
  const [, setTick] = useState(0);
  useEffect(() => {
    const timer = setInterval(() => setTick((value) => value + 1), everyMs);
    return () => clearInterval(timer);
  }, [everyMs]);
}

/**
 * Agentman's screen: every agent as a card, stacked by what it wants from
 * the user, with the brief as the title and a box to ask Agentman anything
 * at the foot. Agents move between the sections by themselves as their states
 * change. The conversation with Agentman opens over it as a sheet.
 */
export default function AgentmanBoard() {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const store = useStore();
  const manager = useManager();
  const prefs = useBoardPrefs();
  const window = useWindowDimensions();
  const reduceMotion = useReducedMotion();
  const focused = useIsFocused();
  const { chat } = useLocalSearchParams<{ chat?: string }>();
  const [chatOpen, setChatOpen] = useState(chat === "1");
  const [refreshing, setRefreshing] = useState(false);
  const [menu, setMenu] = useState<{ anchor: Anchor; items: PopoverItem[]; title?: string } | null>(null);
  useTicking(30_000);

  const closeChat = useCallback(() => setChatOpen(false), []);
  const openChat = useCallback(() => setChatOpen(true), []);

  // One object for the Board's life, reading the latest store: a card
  // redraws when its own agent changes, not when any agent does.
  const latest = useRef({ store, manager, router, prefs });
  latest.current = { store, manager, router, prefs };
  const actions = useMemo<BoardActions>(() => ({
    open: (id) => latest.current.router.push(`/session/${encodeURIComponent(id)}`),
    openChanges: (id) => latest.current.router.push(`/workspace/${encodeURIComponent(id)}?tab=changes`),
    openServers: (id) => latest.current.router.push(`/servers/${encodeURIComponent(id)}`),
    openDocuments: (id) => latest.current.router.push(`/artifacts/${encodeURIComponent(id)}`),
    answer: (id, answer) => latest.current.store.answerQuestion(id, answer),
    replyToCard: (card, answer) => latest.current.manager.reply(card, answer),
    dismissCard: (card) => latest.current.manager.dismiss(card),
    send: (id, text) => void latest.current.store.sendMessage(id, text),
    follow: (id) => {
      latest.current.store.openSession(id);
      return () => latest.current.store.closeSession(id);
    },
    changedFiles: async (id) => {
      const result = await latest.current.store.workspace(id, "list_changes");
      return result.changes?.length ?? 0;
    },
    menu: (session, anchor) => measure(anchor, (position) => {
      const items: PopoverItem[] = [
        {
          key: "open",
          label: "Open conversation",
          icon: "message-square",
          onPress: () => latest.current.router.push(`/session/${encodeURIComponent(session.id)}`),
        },
      ];
      // Hiding an agent that needs the user is how it sits untouched for an hour.
      if (!session.question && session.state !== "waiting_input") {
        items.push({
          key: "hide",
          label: "Hide from the Board",
          icon: "eye-off",
          onPress: () => latest.current.prefs.hide(session.id),
        });
      }
      setMenu({ anchor: position, items });
    }),
  }), []);

  const { folderFilter, folderRoot, sessions, dismissals } = store;
  const waiting = useMemo(
    () => new Set(manager.openCards.filter((card) => card.kind === "question" || card.kind === "needs_you").map((card) => card.sessionId)),
    [manager.openCards],
  );
  const sections = useMemo(
    () => boardSections(sessions, {
      hidden: prefs.hidden,
      waiting,
      dismissals,
      inFolder: folderFilter ? (cwd) => folderContains(folderFilter, folderRoot, cwd) : undefined,
    }),
    [sessions, prefs.hidden, waiting, dismissals, folderFilter, folderRoot],
  );
  const { needsYou, working, idle, hidden } = sections;
  const connected = store.connection === "online" && store.daemonOnline;
  const brief = boardBrief(sections, connected);
  const showsStart = connected && needsYou.length + working.length + idle.length + hidden.length === 0;

  // Open cards the Board has no place for: a key an agent asked for, a note
  // Agentman suggests, a question whose agent has gone. They stay in the
  // conversation, and the Board says so.
  const shownAgents = useMemo(() => new Set(needsYou.map((session) => session.id)), [needsYou]);
  const moreCards = manager.openCards.filter((card) =>
    card.kind !== "offer" && !shownAgents.has(card.sessionId)).length;
  const offers = manager.offers;
  const missions = manager.runningMissions;

  // A reply box or an answer's note low on the Board, kept clear of the
  // keyboard and the Ask box as the keyboard comes up.
  const askBarHeight = useRef(0);
  const scrollOffset = useRef(0);
  const chatIsOpen = useRef(chatOpen);
  chatIsOpen.current = chatOpen;
  useEffect(() => {
    const subscription = Keyboard.addListener("keyboardDidShow", (event) => {
      const field = TextInput.State.currentlyFocusedInput?.() as ViewRef | null;
      if (!field || chatIsOpen.current) return;
      const clear = event.endCoordinates.screenY - askBarHeight.current - space.md;
      field.measureInWindow((_x, y, _width, height) => {
        const over = y + height - clear;
        if (over > 0) scrollRef.current?.scrollTo({ y: scrollOffset.current + over, animated: true });
      });
    });
    return () => subscription.remove();
  }, []);

  // Where each section's heading sits, for its chip to jump to.
  const scrollRef = useRef<ComponentRef<typeof ScrollView>>(null);
  const chipsHeight = useRef(0);
  const headings = useRef<Partial<Record<BoardColumn, ViewRef | null>>>({});
  // Where it is now, not where it was first laid out: cards above it grow
  // as messages arrive.
  const jumpTo = (column: BoardColumn) => measure(headings.current[column] ?? null, ({ y }) => {
    const target = scrollOffset.current + y - insets.top - chipsHeight.current - space.sm;
    scrollRef.current?.scrollTo({ y: Math.max(0, target), animated: !reduceMotion });
  });

  const colorAnchor = useRef<ViewRef>(null);
  const hiddenAnchor = useRef<ViewRef>(null);
  const openColorMenu = () => measure(colorAnchor.current, (anchor) => setMenu({
    anchor,
    title: "Color agents by",
    items: AGENT_COLOR_CHOICES.map((choice) => ({
      key: choice.value,
      label: choice.label,
      checked: prefs.colorBy === choice.value,
      onPress: () => prefs.setColorBy(choice.value),
    })),
  }));
  const openHiddenMenu = () => measure(hiddenAnchor.current, (anchor) => setMenu({
    anchor,
    title: "They come back by themselves when they need you.",
    items: [
      ...hidden.map((session) => ({
        key: session.id,
        label: session.name,
        onPress: () => prefs.show(session.id),
      })),
      { key: "__all", label: "Show all", icon: "eye" as const, onPress: prefs.showAll },
    ],
  }));

  const tintFor = (session: Session) => {
    const hue = prefs.colorOf(session);
    return hue ? agentCardTint(hue, color.surface) : undefined;
  };

  const renderCard = (session: Session, column: BoardColumn) => (
    <Animated.View
      key={session.id}
      layout={reduceMotion ? undefined : LinearTransition.duration(240)}
      entering={reduceMotion ? undefined : FadeIn.duration(180)}
      exiting={reduceMotion ? undefined : FadeOut.duration(140)}
    >
      <BoardCard
        session={session}
        column={column}
        messages={store.messages[session.id]}
        brief={manager.briefs[session.id]}
        check={manager.verifications[session.id]}
        goal={missionOf(missions, session.id)?.goal}
        card={manager.openCards.find((card) =>
          card.sessionId === session.id && (card.kind === "question" || card.kind === "needs_you"))}
        answerAction={store.actions.find((action) => action.sessionId === session.id && action.kind === "answer")}
        sendFailed={store.pending.some((item) => item.sessionId === session.id && item.status === "failed")}
        online={store.daemonOnline}
        focused={focused}
        tint={tintFor(session)}
        actions={actions}
      />
    </Animated.View>
  );

  const heading = (column: BoardColumn, title: string, count: number, trailing?: ReactNode) => (
    <Animated.View
      key={`heading-${column}`}
      layout={reduceMotion ? undefined : LinearTransition.duration(240)}
    >
      <View
        ref={(view) => {
          headings.current[column] = view;
        }}
        collapsable={false}
        style={styles.heading}
      >
      <View style={styles.headingGlyph}>
        {column === "needsYou" && count > 0 ? (
          <Feather name="alert-circle" size={14} color={color.needsYou} />
        ) : column === "working" && count > 0 ? (
          <Loader size={12} />
        ) : (
          <Feather name="circle" size={14} color={color.faint} />
        )}
      </View>
      <Text style={styles.headingTitle} accessibilityRole="header">{title}</Text>
      <Text style={styles.headingCount}>{count}</Text>
      <View style={styles.headingGap} />
      {trailing}
      </View>
    </Animated.View>
  );

  const anchorPosition = (anchor: Anchor) => {
    // Near the foot of the screen a menu opens upward, clear of the Ask box.
    if (anchor.y > window.height * 0.58) return { bottom: window.height - anchor.y + space.xs };
    return { top: anchor.y + anchor.height + space.xs - insets.top };
  };

  const needsYouCount = needsYou.length + offers.length;

  return (
    <KeyboardAvoidingView
      style={[styles.page, { paddingTop: insets.top }]}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <ScrollView
        ref={scrollRef}
        style={styles.scroll}
        contentContainerStyle={{ paddingBottom: space.xl }}
        stickyHeaderIndices={[1]}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode="on-drag"
        onScroll={(event) => {
          scrollOffset.current = event.nativeEvent.contentOffset.y;
        }}
        scrollEventThrottle={64}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            tintColor={color.faint}
            onRefresh={() => {
              setRefreshing(true);
              store.refresh();
              void manager.reload();
              setTimeout(() => setRefreshing(false), 600);
            }}
          />
        }
      >
        <ContentColumn style={styles.gutter}>
          <View style={styles.topBar}>
            <MotionPressable onPress={() => router.back()} hitSlop={10} style={styles.back}
              accessibilityRole="button" accessibilityLabel="Back">
              <Feather name="chevron-left" size={26} color={color.text} />
            </MotionPressable>
            <View style={styles.connection}>
              <View style={[styles.connectionDot, { backgroundColor: connected ? color.ok : color.error }]} />
              <Text style={styles.connectionText} numberOfLines={1}>
                {connected ? "Mac connected" : store.connection === "incompatible" ? "Update required" : "Reconnecting"}
              </Text>
            </View>
            <View style={styles.topGap} />
            <View ref={colorAnchor} collapsable={false}>
              <MotionPressable onPress={openColorMenu} hitSlop={6} style={styles.iconButton} pressedScale={0.94}
                accessibilityRole="button" accessibilityLabel="Color agents by">
                <Feather name="droplet" size={17} color={prefs.colorBy === "none" ? color.textSecondary : color.working} />
              </MotionPressable>
            </View>
            {manager.supported ? (
              <MotionPressable onPress={openChat} hitSlop={6} style={styles.iconButton} pressedScale={0.94}
                accessibilityRole="button"
                accessibilityLabel={`Open the conversation with Agentman${manager.unread > 0 ? `. ${manager.unread} new` : ""}`}>
                <Feather name="message-circle" size={18} color={color.textSecondary} />
                {manager.unread > 0 ? <View style={styles.unreadDot} /> : null}
              </MotionPressable>
            ) : null}
            <MotionPressable onPress={() => router.push("/new-session")} disabled={!store.daemonOnline}
              hitSlop={6} style={styles.iconButton} pressedScale={0.94}
              accessibilityRole="button" accessibilityLabel="New agent">
              <Feather name="plus" size={20} color={store.daemonOnline ? color.textSecondary : color.faint} />
            </MotionPressable>
            <MotionPressable onPress={() => router.push("/settings")} hitSlop={6} style={styles.iconButton}
              pressedScale={0.94} accessibilityRole="button" accessibilityLabel="Settings">
              <Feather name="settings" size={17} color={color.textSecondary} />
            </MotionPressable>
          </View>

          <Text style={styles.title} accessibilityRole="header">{brief.title}</Text>
          {brief.detail ? <Text style={styles.detail}>{brief.detail}</Text> : null}
          {showsStart ? (
            <MotionPressable onPress={() => router.push("/new-session")} style={styles.startButton}
              accessibilityRole="button" accessibilityLabel="New agent">
              <Feather name="plus" size={16} color={color.onInverse} />
              <Text style={styles.startText}>New agent</Text>
            </MotionPressable>
          ) : null}
        </ContentColumn>

        <View
          style={styles.chipBand}
          onLayout={(event) => {
            chipsHeight.current = event.nativeEvent.layout.height;
          }}
        >
          {needsYouCount + working.length + idle.length > 0 || folderFilter ? (
          <ContentColumn>
            <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.chips}
              keyboardShouldPersistTaps="handled">
              {folderFilter ? (
                <MotionPressable onPress={() => store.setFolderFilter(null)} style={[styles.chip, styles.folderChip]}
                  pressedScale={0.96} accessibilityRole="button"
                  accessibilityLabel={`Showing ${folderLabel(folderFilter)}. Show every folder`}>
                  <Feather name="folder" size={13} color={color.muted} />
                  <Text style={styles.chipLabel} numberOfLines={1}>{folderLabel(folderFilter)}</Text>
                  <Feather name="x" size={13} color={color.faint} />
                </MotionPressable>
              ) : null}
              <StateChip label="Needs you" count={needsYouCount} tint={color.needsYouText} onPress={() => jumpTo("needsYou")} />
              <StateChip label="Working" count={working.length} tint={color.working} onPress={() => jumpTo("working")} />
              <StateChip label="Idle" count={idle.length} tint={color.muted} onPress={() => jumpTo("idle")} />
            </ScrollView>
          </ContentColumn>
          ) : null}
        </View>

        <ContentColumn style={styles.gutter}>
          <View style={styles.stack}>
            {needsYouCount > 0 || moreCards > 0 ? heading("needsYou", "Needs you", needsYouCount) : null}
            {moreCards > 0 ? (
              <Animated.View key="more-cards" layout={reduceMotion ? undefined : LinearTransition.duration(240)}>
                <MotionPressable onPress={openChat} pressedScale={0.99} style={styles.moreCards}
                  accessibilityRole="button" accessibilityLabel={`${moreCards} more in your conversation with Agentman. Open`}>
                  <Text style={styles.moreCardsText}>
                    {moreCards} more in your conversation with Agentman
                  </Text>
                  <Feather name="chevron-right" size={15} color={color.faint} />
                </MotionPressable>
              </Animated.View>
            ) : null}
            {offers.map((card) => (
              <Animated.View key={`offer-${card.id}`} layout={reduceMotion ? undefined : LinearTransition.duration(240)}>
                <BoardOfferCard card={card} online={store.daemonOnline} actions={actions} />
              </Animated.View>
            ))}
            {needsYou.map((session) => renderCard(session, "needsYou"))}

            {working.length > 0 || missions.length > 0 ? heading("working", "Working", working.length) : null}
            {missions.map((mission) => (
              <Animated.View key={`mission-${mission.id}`} layout={reduceMotion ? undefined : LinearTransition.duration(240)}>
                <BoardMissionRow mission={mission} />
              </Animated.View>
            ))}
            {working.map((session) => renderCard(session, "working"))}

            {idle.length > 0 || hidden.length > 0
              ? heading("idle", "Idle", idle.length, hidden.length > 0 ? (
                  <View ref={hiddenAnchor} collapsable={false}>
                    <MotionPressable onPress={openHiddenMenu} hitSlop={12} style={styles.hiddenControl}
                      accessibilityRole="button" accessibilityLabel={`${hidden.length} hidden. Show them`}>
                      <Feather name="eye-off" size={12} color={color.muted} />
                      <Text style={styles.hiddenText}>{hidden.length} hidden</Text>
                    </MotionPressable>
                  </View>
                ) : null)
              : null}
            {idle.map((session) => renderCard(session, "idle"))}
          </View>
        </ContentColumn>
      </ScrollView>

      {manager.supported ? (
        <View onLayout={(event) => { askBarHeight.current = event.nativeEvent.layout.height; }}>
          <AskBar onOpenConversation={openChat} />
        </View>
      ) : null}

      <BoardSheet open={chatOpen} onClose={closeChat}>
        <AgentmanChat open={chatOpen} onClose={closeChat} keyboardOffset={boardSheetTop(insets.top)} />
      </BoardSheet>

      {menu ? (
        <Popover
          visible
          onDismiss={() => setMenu(null)}
          {...anchorPosition(menu.anchor)}
          align="right"
          title={menu.title}
          items={menu.items}
        />
      ) : null}
    </KeyboardAvoidingView>
  );
}

function StateChip({
  label,
  count,
  tint,
  onPress,
}: {
  label: string;
  count: number;
  tint: string;
  onPress(): void;
}) {
  const styles = useStyles(makeStyles);
  return (
    <MotionPressable
      onPress={onPress}
      disabled={count === 0}
      style={styles.chip}
      pressedScale={0.96}
      accessibilityRole="button"
      accessibilityLabel={`${count} ${label}. Jump to them`}
    >
      <Text style={[styles.chipCount, { color: count > 0 ? tint : styles.chipLabel.color }]}>{count}</Text>
      <Text style={styles.chipLabel}>{label}</Text>
    </MotionPressable>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    scroll: { flex: 1 },
    gutter: { paddingHorizontal: space.lg },
    topBar: { flexDirection: "row", alignItems: "center", gap: 6, paddingTop: space.md },
    back: { width: 26, height: 38, alignItems: "center", justifyContent: "center", marginLeft: -4 },
    connection: {
      flexShrink: 1,
      flexDirection: "row",
      alignItems: "center",
      gap: 7,
      height: 32,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      borderWidth: 1,
      borderColor: c.line,
      backgroundColor: c.surface,
    },
    connectionDot: { width: 7, height: 7, borderRadius: 4 },
    connectionText: { flexShrink: 1, ...font.sansMedium, fontSize: size.label, color: c.textSecondary },
    topGap: { flex: 1 },
    iconButton: {
      width: 38,
      height: 38,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      borderWidth: 1,
      borderColor: c.line,
      backgroundColor: c.surface,
    },
    unreadDot: {
      position: "absolute",
      top: 7,
      right: 8,
      width: 7,
      height: 7,
      borderRadius: 4,
      backgroundColor: c.needsYou,
      borderWidth: 1,
      borderColor: c.surface,
    },
    title: {
      ...font.sansBold,
      fontSize: scaled(25),
      lineHeight: scaled(31),
      letterSpacing: -0.3,
      color: c.text,
      marginTop: space.lg,
    },
    detail: { ...font.sans, fontSize: size.caption, lineHeight: scaled(19), color: c.muted, marginTop: space.xs },
    startButton: {
      flexDirection: "row",
      alignItems: "center",
      alignSelf: "flex-start",
      gap: 6,
      minHeight: 44,
      paddingHorizontal: space.lg,
      marginTop: space.lg,
      borderRadius: radius.pill,
      backgroundColor: c.inverse,
    },
    startText: { ...font.sansMedium, fontSize: size.caption, color: c.onInverse },

    chipBand: { backgroundColor: c.paper, paddingTop: space.lg, paddingBottom: space.md },
    chips: { flexDirection: "row", gap: space.sm, paddingHorizontal: space.lg },
    chip: {
      flexDirection: "row",
      alignItems: "center",
      gap: 6,
      minHeight: 36,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      borderWidth: 1,
      borderColor: c.line,
      backgroundColor: c.surface,
    },
    folderChip: { maxWidth: 190 },
    chipCount: { ...font.sansBold, fontSize: size.caption, fontVariant: ["tabular-nums"] },
    chipLabel: { flexShrink: 1, ...font.sans, fontSize: size.caption, color: c.muted },

    stack: { gap: 10, paddingTop: space.xs },
    heading: { flexDirection: "row", alignItems: "center", gap: 7, minHeight: 30, marginTop: space.sm, paddingHorizontal: 2 },
    headingGlyph: { width: 14, height: 14, alignItems: "center", justifyContent: "center" },
    headingTitle: { ...font.sansMedium, fontSize: size.caption, color: c.text },
    headingCount: { ...font.sans, fontSize: size.caption, color: c.faint, fontVariant: ["tabular-nums"] },
    headingGap: { flex: 1 },
    hiddenControl: { flexDirection: "row", alignItems: "center", gap: 5, minHeight: 30 },
    hiddenText: { ...font.sans, fontSize: size.label, color: c.muted },

    moreCards: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      minHeight: 44,
      paddingHorizontal: 14,
      borderRadius: radius.lg,
      borderWidth: 1,
      borderColor: c.line,
      backgroundColor: c.surface,
    },
    moreCardsText: { flex: 1, ...font.sans, fontSize: size.caption, color: c.textSecondary },
  });
