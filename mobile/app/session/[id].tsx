import * as Haptics from "expo-haptics";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Pulse } from "../../components/Pulse";
import { Appear } from "../../components/Appear";
import { clearDraft, loadDraft, saveDraft } from "../../lib/drafts";
import { Markdown } from "../../components/Markdown";
import { QuestionCard } from "../../components/QuestionCard";
import { Thinking } from "../../components/Thinking";
import { ToolRow } from "../../components/ToolRow";
import { Message } from "../../lib/protocol";
import { PendingSend, useStore } from "../../lib/store";
import { color, font, radius, shortPath, size, space, stateStyle } from "../../lib/theme";

type Row =
  | { kind: "message"; message: Message }
  | { kind: "pending"; pending: PendingSend };

export default function SessionScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const sessionId = decodeURIComponent(String(id));
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const listRef = useRef<FlatList<Row>>(null);
  const [draft, setDraft] = useState("");
  // Restored before the user can type, so an in-progress instruction survives
  // leaving the screen, backgrounding the app, or a reconnect.
  const draftLoaded = useRef(false);
  // Rows present on first render are the backlog and must not animate in;
  // otherwise opening a session is a cascade of fades.
  const settled = useRef<Set<string> | null>(null);

  const session = store.sessions.find((s) => s.id === sessionId);
  const messages = store.messages[sessionId] ?? [];
  const paging = store.pageState[sessionId];

  // Subscribe on focus, unsubscribe on blur. This is what keeps the whole
  // zero-storage design affordable: the daemon only tails the one transcript
  // someone is actually looking at.
  useEffect(() => {
    store.openSession(sessionId);
    return () => store.closeSession(sessionId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  useEffect(() => {
    draftLoaded.current = false;
    void loadDraft(sessionId).then((saved) => {
      // Do not clobber anything typed while the read was in flight.
      setDraft((current) => (current === "" ? saved : current));
      draftLoaded.current = true;
    });
  }, [sessionId]);

  useEffect(() => {
    // Skip the write until the restore has landed, or an empty initial value
    // would immediately erase what was saved.
    if (!draftLoaded.current) return;
    void saveDraft(sessionId, draft);
  }, [sessionId, draft]);

  useEffect(() => {
    if (settled.current === null && messages.length > 0) {
      settled.current = new Set(messages.map((m) => m.id));
    }
  }, [messages]);

  const rows = useMemo<Row[]>(() => {
    const sent = store.pending
      .filter((p) => p.sessionId === sessionId && p.status !== "delivered")
      .map((pending) => ({ kind: "pending" as const, pending }));
    const chronological: Row[] = [
      ...messages.map((message) => ({ kind: "message" as const, message })),
      ...sent,
    ];
    // Reversed to feed an inverted list — see the FlatList below.
    return chronological.reverse();
  }, [messages, store.pending, sessionId]);

  // A blocked agent will not read a new instruction until the question is
  // resolved, so the composer steps aside rather than accepting text that
  // would sit unread.
  const blocked = Boolean(session?.question);
  const canSend = session ? session.inject !== "none" && !blocked : false;

  const submit = useCallback(() => {
    const text = draft.trim();
    if (!text || !canSend) return;
    if (Platform.OS !== "web") {
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
    }
    store.sendMessage(sessionId, text);
    setDraft("");
    void clearDraft(sessionId);
    // No scroll call needed: an inverted list is already anchored to the
    // newest row, so the sent message appears in place.
  }, [draft, canSend, sessionId, store]);

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <View style={styles.header}>
        <Pressable onPress={() => router.back()} hitSlop={12} style={styles.back}>
          <Text style={styles.backGlyph}>‹</Text>
        </Pressable>
        <View style={styles.headerBody}>
          <Text style={styles.title} numberOfLines={1}>
            {session?.name ?? "Session"}
          </Text>
          <Text style={styles.subtitle} numberOfLines={1}>
            {session
              ? // Which model is answering you is worth knowing before you send
                // it something — "Codex" says which CLI is open, not what is
                // doing the work.
                [shortPath(session.cwd), session.model ?? session.kind].join("  ·  ")
              : sessionId}
          </Text>
        </View>
        {session?.state === "busy" ? (
          <Pressable
            onPress={() => store.interruptSession(sessionId)}
            hitSlop={10}
            style={({ pressed }) => [styles.stop, pressed && styles.stopPressed]}
            accessibilityRole="button"
            accessibilityLabel="Stop current turn"
          >
            <View style={styles.stopGlyph} />
          </Pressable>
        ) : null}
        {session && <Pulse state={session.state} />}
      </View>

      {session && !session.question && (
        <DeliveryNote inject={session.inject} state={session.state} />
      )}

      {/* Inverted so the feed opens on the newest message and stays pinned
          there as more arrive. Opening a long session at its beginning means
          scrolling through hours of history to find out what just happened,
          which is the opposite of what someone checking their phone wants.
          Inverting also makes "load older" the natural end-of-list action,
          and it leaves the scroll position alone when the user has
          deliberately scrolled up to read. */}
      <FlatList
        ref={listRef}
        data={rows}
        inverted
        keyExtractor={(row) =>
          row.kind === "message" ? row.message.id : row.pending.clientId
        }
        renderItem={({ item }) =>
          item.kind === "message" ? (
            <MessageRow
              message={item.message}
              fresh={settled.current !== null && !settled.current.has(item.message.id)}
            />
          ) : (
            <PendingRow pending={item.pending} onDismiss={store.dismissPending} />
          )
        }
        contentContainerStyle={styles.list}
        // Inverted, so the header renders at the visual bottom — which is
        // where a "still working" indicator belongs, under the last message.
        ListHeaderComponent={
          session?.state === "busy" ? (
            <View style={styles.thinking}>
              <Thinking />
            </View>
          ) : null
        }
        // With the list inverted, the "end" is the oldest message.
        onEndReached={() => store.loadOlder(sessionId)}
        onEndReachedThreshold={0.4}
        ListFooterComponent={
          paging?.loading ? (
            <ActivityIndicator style={styles.spinner} color={color.faint} />
          ) : paging && !paging.hasMore && messages.length > 0 ? (
            <Text style={styles.startOfSession}>Start of session</Text>
          ) : null
        }
        ListEmptyComponent={
          paging?.loading ? null : (
            <Text style={styles.emptyFeed}>No messages yet.</Text>
          )
        }
      />

      <KeyboardAvoidingView
        behavior={Platform.OS === "ios" ? "padding" : undefined}
        keyboardVerticalOffset={insets.top}
      >
        {/* Pinned above the composer, not buried in the feed: an unanswered
            question is the only thing on this screen that blocks the agent. */}
        {session?.question ? (
          <View style={styles.questionWrap}>
            <QuestionCard
              question={session.question}
              onAnswer={(answer) => store.answerQuestion(sessionId, answer)}
            />
          </View>
        ) : null}

        <View style={[styles.composer, { paddingBottom: insets.bottom + space.sm }]}>
          <TextInput
            style={[styles.input, !canSend && styles.inputDisabled]}
            value={draft}
            onChangeText={setDraft}
            editable={canSend}
            placeholder={
              blocked
                ? "Answer the question above first"
                : canSend
                  ? "Send an instruction…"
                  : "This session can't receive messages"
            }
            placeholderTextColor={color.faint}
            multiline
            onSubmitEditing={submit}
            returnKeyType="send"
          />
          <Pressable
            onPress={submit}
            disabled={!canSend || draft.trim().length === 0}
            style={({ pressed }) => [
              styles.send,
              (!canSend || draft.trim().length === 0) && styles.sendDisabled,
              pressed && styles.sendPressed,
            ]}
            hitSlop={8}
            accessibilityRole="button"
            accessibilityLabel="Send"
          >
            <Text style={styles.sendGlyph}>↑</Text>
          </Pressable>
        </View>
      </KeyboardAvoidingView>
    </View>
  );
}

/**
 * Says how a message will actually be delivered.
 *
 * The three paths are not equally good and the app refuses to imply otherwise:
 * a queued message is not a sent one, and someone who walked away from their
 * desk deserves to know which they are getting before they rely on it.
 */
function DeliveryNote({ inject, state }: { inject: string; state: string }) {
  if (inject === "tmux" || inject === "api") return null;

  const text =
    inject === "hook"
      ? state === "busy"
        ? "Messages wait until this turn ends — it can't be interrupted."
        : "Messages are handed over when this agent next finishes a turn."
      : "Start this session with `am claude` to send it messages.";

  return (
    <View style={styles.note}>
      <Text style={styles.noteText}>{text}</Text>
    </View>
  );
}

function MessageRow({ message, fresh }: { message: Message; fresh: boolean }) {
  if (message.role === "tool" && message.tool) {
    return (
      <Appear enabled={fresh}>
        <ToolRow message={message} />
      </Appear>
    );
  }

  if (message.role === "user") {
    return (
      <Appear enabled={fresh}>
        <View style={styles.userRow}>
          <Text style={styles.userText}>{message.text}</Text>
        </View>
      </Appear>
    );
  }

  if (message.role === "system") {
    return (
      <Appear enabled={fresh}>
        <Text style={styles.systemText}>{message.text}</Text>
      </Appear>
    );
  }

  return (
    <Appear enabled={fresh}>
      <View style={styles.assistantRow}>
        {message.isSidechain && <Text style={styles.sidechain}>subagent</Text>}
        {/* Agents write markdown constantly — backticked paths, fenced diffs,
            bulleted change lists. Rendering the source would mean reading
            `**done**` and counting backticks on a phone. */}
        <Markdown>{message.text ?? ""}</Markdown>
      </View>
    </Appear>
  );
}

function PendingRow({
  pending,
  onDismiss,
}: {
  pending: PendingSend;
  onDismiss: (clientId: string) => void;
}) {
  const failed = pending.status === "failed";
  const queued = pending.status === "queued";

  return (
    <Pressable
      onPress={() => failed && onDismiss(pending.clientId)}
      style={[styles.userRow, styles.pendingRow, failed && styles.pendingFailed]}
    >
      <Text style={styles.userText}>{pending.text}</Text>
      <Text style={[styles.pendingStatus, failed && { color: color.error }]}>
        {pending.status === "sending"
          ? "Sending…"
          : queued
            ? "Queued — arrives when this turn ends"
            : failed
              ? `Didn't send${pending.error ? `: ${pending.error}` : ""} · tap to dismiss`
              : ""}
      </Text>
    </Pressable>
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
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: color.line,
  },
  back: { paddingHorizontal: space.xs },
  backGlyph: { color: color.muted, fontSize: 30, lineHeight: 32, marginTop: -4 },
  headerBody: { flex: 1 },
	stop: {
	  width: 30,
	  height: 30,
	  alignItems: "center",
	  justifyContent: "center",
	  borderRadius: radius.sm,
	  borderWidth: StyleSheet.hairlineWidth,
	  borderColor: color.lineStrong,
	},
	stopPressed: { opacity: 0.6 },
	stopGlyph: { width: 10, height: 10, borderRadius: 2, backgroundColor: color.error },
  title: { fontFamily: font.monoMedium, fontSize: size.title, color: color.text },
  subtitle: { fontFamily: font.mono, fontSize: size.caption, color: color.muted },

  note: {
    paddingHorizontal: space.lg,
    paddingVertical: space.sm,
    backgroundColor: color.sunken,
  },
  noteText: { fontFamily: font.sans, fontSize: size.caption, color: color.muted },

  // Inverted, so paddingTop is the gap under the newest row. Without it the
  // last message sits behind the composer and gets clipped.
  list: { padding: space.lg, paddingTop: space.md, gap: space.lg },
  spinner: { marginVertical: space.lg },
  startOfSession: {
    fontFamily: font.sans,
    fontSize: size.caption,
    color: color.faint,
    textAlign: "center",
    marginBottom: space.lg,
  },
  emptyFeed: {
    fontFamily: font.sans,
    fontSize: size.body,
    color: color.faint,
    textAlign: "center",
    marginTop: space.xxl,
  },

  // The user's own words get a surface; the agent's sit on the page. That is
  // enough to tell them apart without turning a transcript into a chat app.
  userRow: {
    alignSelf: "flex-end",
    maxWidth: "88%",
    backgroundColor: color.surface,
    borderRadius: radius.lg,
    borderTopRightRadius: radius.sm,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
  },
  userText: { fontFamily: font.sans, fontSize: size.body, color: color.text, lineHeight: 21 },

  assistantRow: { gap: space.xs },
  assistantText: { fontFamily: font.sans, fontSize: size.body, color: color.text, lineHeight: 22 },
  sidechain: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1,
    textTransform: "uppercase",
    color: color.faint,
  },

  systemText: {
    fontFamily: font.sans,
    fontSize: size.caption,
    color: color.faint,
    textAlign: "center",
  },

  toolRow: { flexDirection: "row", gap: space.sm, alignItems: "flex-start" },
  toolGlyph: { fontFamily: font.mono, fontSize: size.caption, color: color.faint, marginTop: 2 },
  toolBody: { flex: 1 },
  toolLine: { fontFamily: font.mono, fontSize: size.caption, color: color.muted },
  toolName: { color: color.text },
  toolOutput: {
    fontFamily: font.mono,
    fontSize: size.caption,
    color: color.muted,
    marginTop: space.sm,
    padding: space.sm,
    backgroundColor: color.sunken,
    borderRadius: radius.sm,
  },

  pendingRow: { opacity: 0.75 },
  pendingFailed: { opacity: 1, borderWidth: StyleSheet.hairlineWidth, borderColor: color.error },
  pendingStatus: {
    fontFamily: font.sans,
    fontSize: size.label,
    color: color.faint,
    marginTop: space.xs,
  },

  questionWrap: { paddingHorizontal: space.md, paddingBottom: space.sm },
  thinking: { paddingTop: space.sm },
  composer: {
    flexDirection: "row",
    alignItems: "flex-end",
    gap: space.sm,
    paddingHorizontal: space.md,
    paddingTop: space.sm,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: color.line,
    backgroundColor: color.ink,
  },
  input: {
    flex: 1,
    maxHeight: 120,
    backgroundColor: color.surface,
    borderRadius: radius.lg,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
    fontFamily: font.sans,
    fontSize: size.body,
    color: color.text,
  },
  inputDisabled: { opacity: 0.5 },
  send: {
    width: 38,
    height: 38,
    borderRadius: 19,
    backgroundColor: color.working,
    alignItems: "center",
    justifyContent: "center",
  },
  sendDisabled: { backgroundColor: color.line },
  sendPressed: { opacity: 0.7 },
  sendGlyph: { color: color.ink, fontSize: 19, fontFamily: font.sansBold, marginTop: -2 },
});
