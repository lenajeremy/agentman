import Feather from "@expo/vector-icons/Feather";
import { useFocusEffect, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Image,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { AgentIcon } from "../components/AgentIcon";
import { AttachmentStrip } from "../components/AttachmentStrip";
import { BrandMark } from "../components/BrandMark";
import { ManagerCardView } from "../components/ManagerCards";
import { Markdown } from "../components/Markdown";
import { MotionPressable } from "../components/MotionPressable";
import { Popover, type PopoverItem } from "../components/Popover";
import { SwipeToDismiss } from "../components/SwipeToDismiss";
import { KEEP_READING, ScrollToLatest, SCROLL_AWAY } from "../components/ScrollToLatest";
import { SeenImage } from "../components/SeenImage";
import { Thinking } from "../components/Thinking";
import { useStyles, useTheme } from "../lib/appearance";
import { useManager } from "../lib/manager";
import { chatItems, type ChatItem } from "../lib/manager-feed";
import { MANAGER_MODELS, modelName } from "../lib/manager-models";
import type { ManagerEntry, ManagerSchedule } from "../lib/protocol";
import { useStore } from "../lib/store";
import { font, Palette, radius, size, space } from "../lib/theme";
import { useAttachments } from "../lib/use-attachments";
import { chooseImageSource } from "../lib/image-source-sheet";
import { addPastedImagesListener, pasteTakesImages } from "../modules/image-paste";

/**
 * The conversation with Agentman: the one place to ask what every agent is
 * doing, read what happened, and answer what needs you. The same
 * conversation the Mac shows.
 */
export default function AgentmanScreen() {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const store = useStore();
  const manager = useManager();
  const images = useAttachments(store.credentials);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [schedulesOpen, setSchedulesOpen] = useState(false);
  const [modelMenu, setModelMenu] = useState(false);
  const listRef = useRef<FlatList<ChatItem>>(null);
  const [scrolledAway, setScrolledAway] = useState(false);
  const [composerHeight, setComposerHeight] = useState(0);
  // Paste in the box takes an image while it is focused, as in an agent's
  // thread: a paste goes to whichever box has focus.
  const [inputFocused, setInputFocused] = useState(false);
  const { addPasted } = images;
  useEffect(() => {
    if (!inputFocused) return;
    const subscription = addPastedImagesListener((pasted) => void addPasted(pasted));
    return () => subscription?.remove();
  }, [inputFocused, addPasted]);
  const settings = manager.settings;
  const modelItems: PopoverItem[] = MANAGER_MODELS.map((model) => {
    const keyed = model.provider === "gemini" ? settings?.geminiKeySet : (settings?.anthropicKeySet ?? settings?.keySet);
    return {
      key: model.id,
      label: model.name,
      checked: settings?.chatModel === model.id,
      disabled: keyed === false,
      detail: keyed === false ? "No key" : undefined,
      onPress: () => {
        void manager.setChatModel(model.id).then((problem) => problem && images.setError(problem));
      },
    };
  });

  // Newest first, for a list that opens on the latest and stays there.
  const rows = useMemo(() => chatItems(manager.entries).reverse(), [manager.entries]);

  // Whatever arrives while the conversation is open has been seen.
  const [focused, setFocused] = useState(false);
  useFocusEffect(useCallback(() => {
    setFocused(true);
    return () => setFocused(false);
  }, []));
  const { markRead, entries } = manager;
  useEffect(() => {
    if (focused) markRead();
  }, [focused, entries, markRead]);

  const reachable = store.connection === "online" && store.daemonOnline;
  const confirmNewChat = () => {
    Alert.alert(
      "Start a new chat?",
      "This conversation is set aside, and Agentman keeps what matters from it in its private notes. Cards still waiting stay.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "New chat",
          onPress: () => {
            void manager.reset().then((problem) => problem && images.setError(problem));
          },
        },
      ],
    );
  };
  const canSend = reachable && !manager.needsKey && !sending &&
    (draft.trim().length > 0 || images.attachments.length > 0);

  const send = async () => {
    if (!canSend) return;
    setSending(true);
    // Sending always comes back to the latest, wherever the reader was.
    listRef.current?.scrollToOffset({ offset: 0, animated: true });
    try {
      const uploadIds = await images.upload();
      if (await manager.send(draft.trim(), uploadIds)) {
        setDraft("");
        images.clear();
      }
    } catch (reason) {
      images.setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setSending(false);
    }
  };

  return (
    <KeyboardAvoidingView
      style={[styles.page, { paddingTop: insets.top }]}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <View style={styles.header}>
        <MotionPressable onPress={() => router.back()} hitSlop={12} style={styles.back}
          accessibilityRole="button" accessibilityLabel="Back">
          <Feather name="chevron-left" size={26} color={color.text} />
        </MotionPressable>
        <BrandMark size={34} ring={color.paper} />
        <View style={styles.headerBody}>
          <Text style={styles.title}>Agentman</Text>
          <Text style={styles.subtitle} numberOfLines={1}>
            {!reachable ? "Waiting for your Mac" : manager.thinking ? "Thinking…" : "Manages your agents"}
          </Text>
        </View>
        {manager.entries.length > 0 ? (
          <MotionPressable onPress={confirmNewChat} style={styles.headerIcon} hitSlop={8}
            accessibilityRole="button" accessibilityLabel="New chat">
            <Feather name="edit" size={17} color={color.textSecondary} />
          </MotionPressable>
        ) : null}
        {manager.schedules.length > 0 ? (
          <MotionPressable onPress={() => setSchedulesOpen((open) => !open)} style={styles.headerButton}
            accessibilityRole="button" accessibilityLabel={`${manager.schedules.length} scheduled`}>
            <Feather name="clock" size={15} color={color.textSecondary} />
            <Text style={styles.headerButtonText}>{manager.schedules.length}</Text>
          </MotionPressable>
        ) : null}
      </View>

      {schedulesOpen ? <Schedules onClose={() => setSchedulesOpen(false)} /> : null}

      <View style={styles.feed}>
      <FlatList
        ref={listRef}
        style={styles.feed}
        data={rows}
        inverted
        maintainVisibleContentPosition={KEEP_READING}
        onScroll={(event) => {
          const away = event.nativeEvent.contentOffset.y > SCROLL_AWAY;
          if (away !== scrolledAway) setScrolledAway(away);
        }}
        scrollEventThrottle={64}
        keyExtractor={(item) => item.key}
        renderItem={({ item }) => <Row item={item} />}
        contentContainerStyle={styles.list}
        keyboardDismissMode="interactive"
        keyboardShouldPersistTaps="handled"
        onEndReached={() => void manager.loadOlder()}
        onEndReachedThreshold={0.4}
        ListHeaderComponent={
          manager.thinking && manager.entries.at(-1)?.streaming !== true ? (
            <View style={styles.thinking}><Thinking label="Thinking" /></View>
          ) : null
        }
        ListEmptyComponent={
          <View style={styles.empty}>
            {manager.loaded || !reachable ? (
              <>
                <BrandMark size={44} ring={color.paper} />
                <Text style={styles.emptyTitle}>Ask Agentman about your agents</Text>
                <Text style={styles.emptyText}>
                  What everyone is doing, what changed, what needs you. It reads them so you don't have to.
                </Text>
              </>
            ) : (
              <ActivityIndicator color={color.faint} />
            )}
          </View>
        }
      />
      <ScrollToLatest
        visible={scrolledAway}
        onPress={() => listRef.current?.scrollToOffset({ offset: 0, animated: true })}
      />
      </View>

      <View
        style={[styles.composer, { paddingBottom: Math.max(insets.bottom, space.sm) }]}
        onLayout={(event) => setComposerHeight(event.nativeEvent.layout.height)}
      >
        {manager.problem ? <Text style={styles.problem}>{manager.problem}</Text> : null}
        {images.error ? <Text style={styles.problem}>{images.error}</Text> : null}
        {manager.needsKey ? (
          <View style={styles.notice}>
            <Feather name="key" size={14} color={color.muted} />
            <View style={styles.noticeBody}>
              <Text style={styles.noticeText}>
                {settings?.provider === "gemini" ? "This model runs on your Gemini API key" : "This model runs on your Anthropic API key"}.
                Add it in Agentman on your Mac; it never leaves the Mac.
              </Text>
              {settings ? (
                <MotionPressable onPress={() => setModelMenu(true)} hitSlop={8} accessibilityRole="button">
                  <Text style={styles.noticeAction}>Switch model</Text>
                </MotionPressable>
              ) : null}
            </View>
          </View>
        ) : (
          <View style={[styles.field, images.attachments.length > 0 && styles.fieldWithImages]}>
            <AttachmentStrip attachments={images.attachments} onRemove={images.remove} />
            {/* Where the box's own Paste can take an image (iOS and the web),
              that is how a copied screenshot goes in; elsewhere a long press
              pastes it. */}
            <Pressable
              onLongPress={
                pasteTakesImages
                  ? undefined
                  : () => {
                      if (images.clipboardReady && images.canAdd) void images.paste();
                    }
              }
              delayLongPress={400}
              accessibilityLabel={images.clipboardReady && !pasteTakesImages ? "Hold to paste the image on the clipboard" : undefined}
            >
              <TextInput
                style={styles.input}
                value={draft}
                onChangeText={setDraft}
                onFocus={() => setInputFocused(true)}
                onBlur={() => setInputFocused(false)}
                placeholder={reachable ? "Ask Agentman about your agents" : "Waiting for your Mac…"}
                placeholderTextColor={color.faint}
                multiline
                editable={reachable}
              />
            </Pressable>
            <View style={styles.toolbar}>
              <MotionPressable
                onPress={() =>
                  chooseImageSource(images.clipboardReady, (source) =>
                    void (source === "paste" ? images.paste() : images.pick()),
                  )
                }
                style={styles.attach}
                disabled={!reachable || !images.canAdd || images.busy}
                accessibilityRole="button"
                accessibilityLabel="Add an image"
              >
                {images.busy ? (
                  <ActivityIndicator size="small" color={color.faint} />
                ) : (
                  <Feather name="plus" size={20} color={reachable ? color.textSecondary : color.faint} />
                )}
              </MotionPressable>
              <View style={styles.toolbarGap} />
              {settings ? (
                <MotionPressable onPress={() => setModelMenu(true)} style={styles.modelButton} hitSlop={6}
                  accessibilityRole="button" accessibilityLabel={`Model ${modelName(settings.chatModel)}. Switch model`}>
                  <Text style={styles.modelText} numberOfLines={1}>{modelName(settings.chatModel)}</Text>
                  <Feather name="chevron-down" size={13} color={color.muted} />
                </MotionPressable>
              ) : null}
              {manager.thinking ? (
                <MotionPressable onPress={manager.stop} style={styles.send}
                  accessibilityRole="button" accessibilityLabel="Stop Agentman">
                  <Feather name="square" size={13} color={color.onAccent} />
                </MotionPressable>
              ) : (
                <MotionPressable onPress={() => void send()} disabled={!canSend}
                  style={[styles.send, !canSend && styles.sendDisabled]}
                  accessibilityRole="button" accessibilityLabel="Send to Agentman">
                  {sending ? (
                    <ActivityIndicator size="small" color={color.onAccent} />
                  ) : (
                    <Feather name="arrow-up" size={18} color={canSend ? color.onAccent : color.faint} />
                  )}
                </MotionPressable>
              )}
            </View>
          </View>
        )}
      </View>
      {modelMenu ? (
        <Popover
          visible
          onDismiss={() => setModelMenu(false)}
          bottom={composerHeight + space.xs}
          align="right"
          title="Agentman runs on"
          items={modelItems}
        />
      ) : null}
    </KeyboardAvoidingView>
  );
}

function Row({ item }: { item: ChatItem }) {
  if (item.kind === "activities") return <ActivityRun entries={item.entries} />;
  if (item.kind === "image") return <ImageActivity entry={item.entry} />;
  return <EntryRow entry={item.entry} />;
}

function EntryRow({ entry }: { entry: ManagerEntry }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  switch (entry.role) {
    case "user":
      return (
        <View style={styles.userRow}>
          {entry.images && entry.images.length > 0 ? <KeptImages names={entry.images} /> : null}
          {entry.text ? (
            <View style={styles.bubble}>
              <Text style={styles.bubbleText} selectable>{entry.text}</Text>
            </View>
          ) : null}
        </View>
      );
    case "assistant":
      return entry.text ? <Markdown>{entry.text}</Markdown> : <Thinking label="Writing" />;
    case "brief":
    case "agent":
      return <BriefCard entry={entry} />;
    case "card":
      return entry.card ? <ManagerCardView card={entry.card} /> : null;
    case "scheduled":
      return (
        <View style={styles.quietLine}>
          <Feather name="clock" size={12} color={color.faint} />
          <Text style={styles.quietText}>{entry.text}</Text>
        </View>
      );
    case "compacted":
      return (
        <View style={styles.divider} accessibilityLabel={entry.text}>
          <View style={styles.dividerLine} />
          <Text style={styles.dividerText}>{entry.text}</Text>
          <View style={styles.dividerLine} />
        </View>
      );
    case "error":
      return (
        <View style={styles.quietLine}>
          <Feather name="alert-circle" size={12} color={color.errorText} />
          <Text style={[styles.quietText, { color: color.errorText }]}>{entry.text}</Text>
        </View>
      );
    default:
      return <Text style={styles.quietText}>{entry.text}</Text>;
  }
}

/** An icon for what Agentman did. */
function activityIcon(tool?: string): keyof typeof Feather.glyphMap {
  switch (tool) {
    case "send_to_agent": return "send";
    case "answer_question": return "message-square";
    case "approve": return "shield";
    case "interrupt": return "stop-circle";
    case "dismiss_card": return "x-circle";
    case "give_key": return "key";
    case "search_knowledge":
    case "read_knowledge":
    case "save_knowledge": return "book-open";
    case "update_instructions": return "file-text";
    case "view_image": return "image";
    default: return "eye";
  }
}

/** Agentman's tool calls, in the same soft card as an agent's: one line for
 *  one thing it did, a line that opens for several. */
function ActivityRun({ entries }: { entries: ManagerEntry[] }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [open, setOpen] = useState(false);
  const last = entries[entries.length - 1];
  const step = (entry: ManagerEntry) => (
    <View key={entry.id} style={styles.activityRow}>
      <Feather name={activityIcon(entry.tool)} size={12} color={color.faint} />
      <Text style={styles.activityText} numberOfLines={2}>{entry.text}</Text>
    </View>
  );
  if (entries.length === 1) return <View style={styles.toolCard}>{step(last)}</View>;
  return (
    <View style={styles.toolCard}>
      <MotionPressable onPress={() => setOpen((value) => !value)} style={styles.activityRow} pressedScale={0.995}
        accessibilityRole="button" accessibilityState={{ expanded: open }}>
        <Feather name={activityIcon(last.tool)} size={12} color={color.muted} />
        <Text style={[styles.activityText, styles.activityHead]} numberOfLines={1}>{last.text}</Text>
        <Text style={styles.activityMore}>and {entries.length - 1} more</Text>
        <Feather name={open ? "chevron-down" : "chevron-right"} size={14} color={color.faint} />
      </MotionPressable>
      {open ? <View style={styles.activitySteps}>{entries.map(step)}</View> : null}
    </View>
  );
}

/** An image Agentman looked at: the step in its card, the picture under it. */
function ImageActivity({ entry }: { entry: ManagerEntry }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  if (!entry.sessionId || !entry.path) return null;
  return (
    <View style={styles.imageActivity}>
      <View style={styles.toolCard}>
        <View style={styles.activityRow}>
          <Feather name="image" size={12} color={color.faint} />
          <Text style={styles.activityText} numberOfLines={1}>{entry.text}</Text>
        </View>
      </View>
      <SeenImage sessionId={entry.sessionId} target={{ path: entry.path, request: "read_seen_file" }} />
    </View>
  );
}

/** News from one agent, as Agentman put it, with the way to that agent. */
function BriefCard({ entry }: { entry: ManagerEntry }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const store = useStore();
  const manager = useManager();
  const router = useRouter();
  const session = entry.sessionId ? store.findSession(entry.sessionId) : undefined;
  const brief = entry.sessionId ? manager.briefs[entry.sessionId] : undefined;
  const name = session?.name ?? brief?.name ?? "An agent";
  return (
    <SwipeToDismiss onDismiss={() => void manager.close(entry)} accessibilityLabel={`News about ${name}`}>
      <MotionPressable
        onPress={() => session && router.push(`/session/${encodeURIComponent(session.id)}`)}
        disabled={!session}
        style={styles.brief}
        pressedScale={0.99}
        accessibilityRole={session ? "button" : "text"}
      >
        <View style={styles.briefHead}>
          <AgentIcon kind={session?.kind ?? ""} size={20} />
          <Text style={styles.briefName} numberOfLines={1}>{name}</Text>
          {session ? <Feather name="chevron-right" size={16} color={color.faint} /> : null}
          {/* Closes it here and on the Mac; so does a swipe. */}
          <MotionPressable onPress={() => void manager.close(entry)} hitSlop={10} style={styles.briefClose}
            accessibilityRole="button" accessibilityLabel={`Close the news about ${name}`}>
            <Feather name="x" size={16} color={color.muted} />
          </MotionPressable>
        </View>
        <Text style={styles.briefText} selectable>{entry.text}</Text>
        {brief?.needsYou ? <Text style={styles.briefNeeds}>{brief.needsYou}</Text> : null}
      </MotionPressable>
    </SwipeToDismiss>
  );
}

/** Pictures the user sent Agentman, read back from the Mac. */
function KeptImages({ names }: { names: string[] }) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.keptRow}>
      {names.map((name) => <KeptImage key={name} name={name} />)}
    </View>
  );
}

function KeptImage({ name }: { name: string }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const manager = useManager();
  const router = useRouter();
  const [uri, setUri] = useState<string | null>(null);
  const [gone, setGone] = useState(false);
  useEffect(() => {
    let current = true;
    void manager.keptImage(name).then((found) => {
      if (!current) return;
      if (found) setUri(found);
      else setGone(true);
    });
    return () => {
      current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name]);
  if (gone) {
    return (
      <View style={[styles.kept, styles.keptGone]}>
        <Feather name="image" size={16} color={color.muted} />
        <Text style={styles.keptGoneText}>Sent image</Text>
      </View>
    );
  }
  return (
    <MotionPressable
      onPress={() => router.push(`/agentman-image?name=${encodeURIComponent(name)}`)}
      disabled={!uri}
      style={styles.kept}
      accessibilityRole="imagebutton"
      accessibilityLabel="Sent image. Open full screen"
    >
      {uri ? <Image source={{ uri }} style={styles.keptImage} resizeMode="cover" /> : null}
    </MotionPressable>
  );
}

/** What Agentman has scheduled for itself, each one cancellable. */
function Schedules({ onClose }: { onClose(): void }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const manager = useManager();
  const [problem, setProblem] = useState<string | null>(null);
  const describe = (schedule: ManagerSchedule) => {
    const when = new Date(schedule.due).toLocaleString(undefined, {
      weekday: "short", hour: "numeric", minute: "2-digit",
    });
    if (schedule.kind === "watch") return `Watching ${schedule.agentName ?? "an agent"} · until ${when}`;
    return schedule.every ? `Every ${Math.round(schedule.every / 60)} min · next ${when}` : when;
  };
  return (
    <View style={styles.schedules}>
      <View style={styles.schedulesHead}>
        <Text style={styles.schedulesTitle}>Scheduled</Text>
        <MotionPressable onPress={onClose} hitSlop={10} accessibilityRole="button" accessibilityLabel="Close">
          <Feather name="x" size={16} color={color.muted} />
        </MotionPressable>
      </View>
      {manager.schedules.map((schedule) => (
        <View key={schedule.id} style={styles.scheduleRow}>
          <Feather name={schedule.kind === "watch" ? "eye" : "clock"} size={13} color={color.muted} />
          <View style={{ flex: 1 }}>
            <Text style={styles.scheduleText} numberOfLines={2}>{schedule.text}</Text>
            <Text style={styles.scheduleWhen}>{describe(schedule)}</Text>
          </View>
          <MotionPressable
            onPress={() => void manager.cancelSchedule(schedule).then(setProblem)}
            hitSlop={8}
            accessibilityRole="button"
            accessibilityLabel="Cancel"
          >
            <Text style={styles.cancel}>Cancel</Text>
          </MotionPressable>
        </View>
      ))}
      {problem ? <Text style={styles.problem}>{problem}</Text> : null}
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    header: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingHorizontal: space.md,
      paddingTop: space.xs,
      paddingBottom: space.sm,
    },
    back: { width: 30, height: 38, alignItems: "center", justifyContent: "center" },
    headerBody: { flex: 1, marginLeft: 2 },
    title: { fontFamily: font.sansMedium, fontSize: size.body, color: c.text },
    subtitle: { fontFamily: font.sans, fontSize: size.label, color: c.muted },
    headerButton: {
      flexDirection: "row",
      alignItems: "center",
      gap: 5,
      height: 32,
      paddingHorizontal: 11,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    headerButtonText: { fontFamily: font.sansMedium, fontSize: size.label, color: c.textSecondary },
    headerIcon: { width: 36, height: 36, borderRadius: radius.pill, alignItems: "center", justifyContent: "center", backgroundColor: c.fill },
    divider: { flexDirection: "row", alignItems: "center", gap: space.sm },
    dividerLine: { flex: 1, height: StyleSheet.hairlineWidth, backgroundColor: c.line },
    dividerText: { fontFamily: font.sans, fontSize: size.label, color: c.faint },
    feed: { flex: 1 },
    list: { paddingHorizontal: space.lg, paddingVertical: space.lg, gap: space.lg },
    thinking: { paddingVertical: space.xs },
    empty: { alignItems: "center", gap: space.sm, paddingHorizontal: space.xl, paddingVertical: space.xxxl },
    emptyTitle: { fontFamily: font.sansMedium, fontSize: size.body, color: c.text, marginTop: space.sm },
    emptyText: { fontFamily: font.sans, fontSize: size.caption, color: c.muted, textAlign: "center", lineHeight: 19 },

    userRow: { alignItems: "flex-end", gap: space.sm, paddingLeft: space.xxl },
    bubble: {
      backgroundColor: c.fill,
      borderRadius: radius.lg,
      paddingHorizontal: 14,
      paddingVertical: 10,
    },
    bubbleText: { fontFamily: font.sans, fontSize: size.reading, lineHeight: 23, color: c.text },
    keptRow: { flexDirection: "row", flexWrap: "wrap", justifyContent: "flex-end", gap: space.sm },
    kept: {
      width: 110,
      height: 110,
      borderRadius: radius.lg,
      overflow: "hidden",
      backgroundColor: c.fill,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.line,
    },
    keptImage: { width: "100%", height: "100%" },
    keptGone: { alignItems: "center", justifyContent: "center", gap: space.xs },
    keptGoneText: { fontFamily: font.sans, fontSize: size.caption, color: c.muted },

    toolCard: {
      paddingHorizontal: 10,
      paddingVertical: 6,
      borderRadius: radius.md,
      borderWidth: 1,
      borderColor: c.toolCardEdge,
      backgroundColor: c.toolCard,
    },
    activityRow: { flexDirection: "row", alignItems: "center", gap: 8, minHeight: 22 },
    activityText: { flex: 1, fontFamily: font.sans, fontSize: size.caption, color: c.textSecondary },
    activityHead: { color: c.muted },
    activityMore: { fontFamily: font.sans, fontSize: size.caption, color: c.faint },
    activitySteps: { gap: 6, paddingTop: 6, paddingLeft: space.lg },
    imageActivity: { gap: space.sm },

    brief: {
      gap: 6,
      padding: 14,
      borderRadius: radius.lg,
      borderWidth: 1,
      borderColor: c.line,
      backgroundColor: c.surface,
    },
    briefHead: { flexDirection: "row", alignItems: "center", gap: space.sm },
    briefName: { flex: 1, fontFamily: font.sansMedium, fontSize: size.body, color: c.text },
    briefClose: { width: 24, height: 24, marginRight: -4, alignItems: "center", justifyContent: "center" },
    briefText: { fontFamily: font.sans, fontSize: size.reading, lineHeight: 23, color: c.textSecondary },
    briefNeeds: { fontFamily: font.sans, fontSize: size.caption, color: c.needsYouText },

    quietLine: { flexDirection: "row", alignItems: "center", gap: 6 },
    quietText: { flex: 1, fontFamily: font.sans, fontSize: size.caption, color: c.faint },

    schedules: {
      marginHorizontal: space.lg,
      marginBottom: space.sm,
      padding: space.md,
      gap: space.sm,
      borderRadius: radius.lg,
      borderWidth: 1,
      borderColor: c.line,
      backgroundColor: c.surface,
    },
    schedulesHead: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
    schedulesTitle: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.muted },
    scheduleRow: { flexDirection: "row", alignItems: "center", gap: space.sm },
    scheduleText: { fontFamily: font.sans, fontSize: size.caption, color: c.text },
    scheduleWhen: { fontFamily: font.sans, fontSize: size.label, color: c.faint },
    cancel: { fontFamily: font.sansMedium, fontSize: size.label, color: c.errorText },

    composer: { paddingHorizontal: space.lg, paddingTop: space.sm, gap: space.xs, backgroundColor: c.paper },
    problem: { fontFamily: font.sans, fontSize: size.label, color: c.errorText, paddingHorizontal: space.xs },
    notice: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: space.sm,
      padding: space.md,
      borderRadius: radius.lg,
      backgroundColor: c.fill,
    },
    noticeBody: { flex: 1, gap: space.xs },
    noticeText: { fontFamily: font.sans, fontSize: size.caption, lineHeight: 19, color: c.textSecondary },
    noticeAction: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.working },
    field: {
      backgroundColor: c.surface,
      borderRadius: radius.xxl,
      borderWidth: 1,
      borderColor: c.line,
      paddingHorizontal: 6,
      paddingTop: 4,
      paddingBottom: 6,
    },
    fieldWithImages: { borderRadius: radius.lg, paddingTop: space.sm, paddingLeft: space.sm },
    input: {
      minHeight: 38,
      maxHeight: 120,
      paddingHorizontal: 10,
      paddingTop: space.sm,
      paddingBottom: space.xs,
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.text,
    },
    toolbar: { flexDirection: "row", alignItems: "center", gap: space.xs },
    modelButton: {
      flexDirection: "row",
      alignItems: "center",
      gap: 3,
      height: 32,
      maxWidth: 200,
      paddingHorizontal: 10,
      borderRadius: radius.pill,
    },
    modelText: { flexShrink: 1, fontFamily: font.sansMedium, fontSize: 13, color: c.muted },
    toolbarGap: { flex: 1 },
    attach: { width: 34, height: 34, borderRadius: radius.md, alignItems: "center", justifyContent: "center" },
    send: {
      width: 36,
      height: 36,
      borderRadius: radius.pill,
      backgroundColor: c.working,
      alignItems: "center",
      justifyContent: "center",
    },
    sendDisabled: { backgroundColor: c.fill },
  });
