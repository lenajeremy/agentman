import * as Haptics from "expo-haptics";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Keyboard,
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  useWindowDimensions,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Appear } from "../../components/Appear";
import { ContentColumn } from "../../components/ContentColumn";
import { Markdown } from "../../components/Markdown";
import { MotionPressable } from "../../components/MotionPressable";
import { Pulse } from "../../components/Pulse";
import { QuestionCard } from "../../components/QuestionCard";
import { Thinking } from "../../components/Thinking";
import { ToolRow } from "../../components/ToolRow";
import { draftNamespace } from "../../lib/draft-policy";
import { clearDraft, loadDraft, saveDraft } from "../../lib/drafts";
import { Message } from "../../lib/protocol";
import { sessionNeedsAnswer } from "../../lib/question-alerts";
import { PendingSend, useStore } from "../../lib/store";
import { color, font, layout, radius, shortPath, size, space, stateStyle } from "../../lib/theme";

type Row =
  | { kind: "message"; message: Message }
  | { kind: "pending"; pending: PendingSend };

export default function SessionScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const sessionId = decodeURIComponent(String(id));
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { height: viewportHeight } = useWindowDimensions();
  const listRef = useRef<FlatList<Row>>(null);
  const [draft, setDraft] = useState("");
  const [draftReady, setDraftReady] = useState(false);
  const [submittedClientId, setSubmittedClientId] = useState<string | null>(null);
  const [inputFocused, setInputFocused] = useState(false);
  const [keyboardVisible, setKeyboardVisible] = useState(
    () => Platform.OS !== "web" && Keyboard.isVisible(),
  );
  // Restored before the user can type, so an in-progress instruction survives
  // leaving the screen, backgrounding the app, or a reconnect.
  const draftRef = useRef("");
  const draftReadyRef = useRef(false);
  const draftWriteTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Rows present on first render are the backlog and must not animate in;
  // otherwise opening a session is a cascade of fades.
  const settled = useRef<Set<string> | null>(null);

  const session = store.sessions.find((s) => s.id === sessionId);
  const messages = store.messages[sessionId] ?? [];
  const paging = store.pageState[sessionId];
  const answerAction = store.actions.find(
    (action) => action.sessionId === sessionId && action.kind === "answer",
  );
  const interruptAction = store.actions.find(
    (action) => action.sessionId === sessionId && action.kind === "interrupt",
  );
  const effectiveState = session && sessionNeedsAnswer(session)
    ? "waiting_input"
    : session?.state;
  const draftScope = useMemo(
    () => store.credentials ? draftNamespace(store.credentials) : null,
    [store.credentials],
  );

  // Subscribe on focus, unsubscribe on blur. This is what keeps the whole
  // zero-storage design affordable: the daemon only tails the one transcript
  // someone is actually looking at.
  useEffect(() => {
    store.openSession(sessionId);
    return () => store.closeSession(sessionId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  useEffect(() => {
    setDraftReady(false);
    draftReadyRef.current = false;
    setDraft("");
    draftRef.current = "";
    if (!draftScope) return;
    let active = true;
    void loadDraft(draftScope, sessionId).then((saved) => {
      if (!active) return;
      // Do not clobber anything typed while the read was in flight.
      setDraft((current) => {
        const restored = current === "" ? saved : current;
        draftRef.current = restored;
        return restored;
      });
      draftReadyRef.current = true;
      setDraftReady(true);
    });
    return () => {
      active = false;
    };
  }, [draftScope, sessionId]);

  useEffect(() => {
    draftRef.current = draft;
    draftReadyRef.current = draftReady;
    // Skip writes until restore lands, or the initial empty state would erase
    // the saved value. Coalesce phone keystrokes instead of hitting storage on
    // every character.
    if (!draftReady || !draftScope) return;
    if (draftWriteTimer.current) clearTimeout(draftWriteTimer.current);
    draftWriteTimer.current = setTimeout(() => {
      draftWriteTimer.current = null;
      void saveDraft(draftScope, sessionId, draftRef.current);
    }, 350);
  }, [draft, draftReady, draftScope, sessionId]);

  useEffect(() => () => {
    // Navigation can happen inside the debounce window; flush the final value
    // so leaving quickly never loses the last few characters.
    if (draftWriteTimer.current) clearTimeout(draftWriteTimer.current);
    draftWriteTimer.current = null;
    if (draftReadyRef.current && draftScope) {
      void saveDraft(draftScope, sessionId, draftRef.current);
    }
  }, [draftScope, sessionId]);

  useEffect(() => {
    if (settled.current === null && messages.length > 0) {
      settled.current = new Set(messages.map((m) => m.id));
    }
  }, [messages]);

  useEffect(() => {
    if (Platform.OS === "web") return;
    const show = Keyboard.addListener(
      Platform.OS === "ios" ? "keyboardWillShow" : "keyboardDidShow",
      () => setKeyboardVisible(true),
    );
    const hide = Keyboard.addListener(
      Platform.OS === "ios" ? "keyboardWillHide" : "keyboardDidHide",
      () => setKeyboardVisible(false),
    );
    return () => {
      show.remove();
      hide.remove();
    };
  }, []);

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
  const submittedSend = submittedClientId
    ? store.pending.find((item) => item.clientId === submittedClientId)
    : undefined;
  const awaitingSend = submittedClientId !== null;

  useEffect(() => {
    if (!submittedClientId) return;
    if (submittedSend?.status === "failed") {
      // The exact text stays in the composer for a deliberate retry.
      setSubmittedClientId(null);
      return;
    }
    if (submittedSend) return;
    if (!session) {
      // A removed session also drops its pending row, but that is not proof the
      // text landed. Keep the account-scoped draft for inspection/recovery.
      setSubmittedClientId(null);
      return;
    }
    // Pending entries disappear only after delivery (or after a confirmed
    // queued handoff), so this is the first safe time to clear the draft.
    setSubmittedClientId(null);
    setDraft("");
    draftRef.current = "";
    if (draftScope) void clearDraft(draftScope, sessionId);
  }, [draftScope, session, sessionId, submittedClientId, submittedSend]);

  const submit = useCallback(() => {
    const text = draft.trim();
    if (!text || !canSend || awaitingSend) return;
    if (Platform.OS !== "web") {
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
    }
    setSubmittedClientId(store.sendMessage(sessionId, text));
    // No scroll call needed: an inverted list is already anchored to the
    // newest row, so the sent message appears in place.
  }, [draft, canSend, awaitingSend, sessionId, store]);

  const requestInterrupt = useCallback(() => {
    if (!store.daemonOnline) return;
    if (interruptAction?.status === "sending" || interruptAction?.status === "delivered") {
      return;
    }
    const perform = () => store.interruptSession(sessionId);
    if (Platform.OS === "web") {
      if (globalThis.confirm("Stop the agent’s current turn?")) perform();
      return;
    }
    Alert.alert(
      "Stop the current turn?",
      "The agent will be interrupted immediately. Work already completed stays in the transcript.",
      [
        { text: "Keep working", style: "cancel" },
        { text: "Stop turn", style: "destructive", onPress: perform },
      ],
    );
  }, [interruptAction?.status, sessionId, store]);

  const interruptLocked = !store.daemonOnline ||
    interruptAction?.status === "sending" ||
    interruptAction?.status === "delivered";

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <View style={styles.headerShell}>
        <ContentColumn style={styles.header}>
          <MotionPressable
            onPress={() => router.back()}
            hitSlop={12}
            style={styles.back}
            pressedScale={0.92}
            accessibilityRole="button"
            accessibilityLabel="Back to agents"
          >
            <Text style={styles.backGlyph}>‹</Text>
          </MotionPressable>
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
            <MotionPressable
              onPress={requestInterrupt}
              disabled={interruptLocked}
              hitSlop={10}
              style={[styles.stop, interruptLocked && styles.stopDisabled]}
              pressedScale={0.92}
              accessibilityRole="button"
              accessibilityLabel="Stop current turn"
              accessibilityState={{
                disabled: interruptLocked,
                busy: interruptAction?.status === "sending",
              }}
            >
              {interruptAction?.status === "sending" ? (
                <ActivityIndicator size="small" color={color.error} />
              ) : (
                <View style={styles.stopGlyph} />
              )}
            </MotionPressable>
          ) : null}
          {session ? (
            <View
              style={[
                styles.statePill,
                effectiveState === "waiting_input" && styles.statePillAttention,
              ]}
            >
              <Pulse state={effectiveState ?? session.state} size={6} />
              <Text
                style={[
                  styles.stateLabel,
                  { color: stateStyle(effectiveState ?? session.state).color },
                ]}
              >
                {stateStyle(effectiveState ?? session.state).label}
              </Text>
            </View>
          ) : null}
        </ContentColumn>
      </View>

      <KeyboardAvoidingView
        style={styles.keyboardAvoider}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        {!store.daemonOnline ? (
          <ContentColumn>
            <ConnectionNote />
          </ContentColumn>
        ) : null}
        {interruptAction ? (
          <ContentColumn>
            <InterruptNote status={interruptAction.status} error={interruptAction.error} />
          </ContentColumn>
        ) : null}
        {session && !session.question && (
          <ContentColumn>
            <DeliveryNote inject={session.inject} state={session.state} />
          </ContentColumn>
        )}

      {/* Inverted so the feed opens on the newest message and stays pinned
          there as more arrive. Opening a long session at its beginning means
          scrolling through hours of history to find out what just happened,
          which is the opposite of what someone checking their phone wants.
          Inverting also makes "load older" the natural end-of-list action,
          and it leaves the scroll position alone when the user has
          deliberately scrolled up to read. */}
      <FlatList
        style={styles.feed}
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
        keyboardDismissMode="interactive"
        keyboardShouldPersistTaps="handled"
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
          ) : paging?.retentionLimited ? (
            <Text style={styles.startOfSession}>
              Older messages remain available on your Mac.
            </Text>
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

        {/* Pinned above the composer, not buried in the feed: an unanswered
            question is the only thing on this screen that blocks the agent. */}
        {session?.question ? (
          <ContentColumn
            style={[
              styles.questionWrap,
              { maxHeight: Math.max(220, Math.min(440, viewportHeight * 0.52)) },
            ]}
          >
            <ScrollView
              style={styles.questionScroll}
              contentContainerStyle={styles.questionScrollContent}
              keyboardShouldPersistTaps="handled"
              nestedScrollEnabled
            >
              <QuestionCard
                question={session.question}
                onAnswer={(answer) => store.answerQuestion(sessionId, answer)}
                disabled={!store.daemonOnline}
                submissionStatus={answerAction?.status}
                submissionError={answerAction?.error}
              />
            </ScrollView>
          </ContentColumn>
        ) : null}

        <View style={styles.composerShell}>
          <ContentColumn
            style={[
              styles.composer,
              { paddingBottom: (keyboardVisible ? 0 : insets.bottom) + space.sm },
            ]}
          >
            <TextInput
              style={[
                styles.input,
                inputFocused && canSend && styles.inputFocused,
                !canSend && styles.inputDisabled,
              ]}
              value={draft}
              onChangeText={setDraft}
              onFocus={() => setInputFocused(true)}
              onBlur={() => setInputFocused(false)}
              editable={canSend && !awaitingSend}
              placeholder={
                blocked
                  ? "Answer the question above first"
                  : awaitingSend
                    ? "Waiting for delivery…"
                    : canSend
                    ? "Send an instruction…"
                    : "This session can't receive messages"
              }
              placeholderTextColor={color.faint}
              multiline
              onSubmitEditing={submit}
              returnKeyType="send"
              accessibilityLabel="Instruction"
            />
            <MotionPressable
              onPress={submit}
              disabled={!canSend || awaitingSend || draft.trim().length === 0}
              style={[
                styles.send,
                (!canSend || awaitingSend || draft.trim().length === 0) &&
                  styles.sendDisabled,
              ]}
              pressedScale={0.92}
              hitSlop={8}
              accessibilityRole="button"
              accessibilityLabel="Send instruction"
              accessibilityState={{
                disabled: !canSend || awaitingSend || draft.trim().length === 0,
                busy: submittedSend?.status === "sending",
              }}
            >
              {submittedSend?.status === "sending" ? (
                <ActivityIndicator size="small" color={color.faint} />
              ) : (
                <Text style={styles.sendGlyph}>↑</Text>
              )}
            </MotionPressable>
          </ContentColumn>
        </View>
      </KeyboardAvoidingView>
    </View>
  );
}

function ConnectionNote() {
  return (
    <View
      style={[styles.note, styles.connectionNote]}
      accessibilityRole="alert"
      accessibilityLiveRegion="polite"
    >
      <View style={[styles.noteIcon, styles.connectionNoteIcon]}>
        <Text style={[styles.noteGlyph, { color: color.error }]}>!</Text>
      </View>
      <Text style={styles.noteText}>
        Your Mac is offline. Answers and turn controls unlock when it reconnects.
      </Text>
    </View>
  );
}

function InterruptNote({
  status,
  error,
}: {
  status: "sending" | "delivered" | "queued" | "failed";
  error?: string;
}) {
  const failed = status === "failed";
  const text = failed
    ? `Couldn’t stop the turn${error ? `: ${error}` : "."} Use Stop to try again.`
    : status === "delivered"
      ? "Stop signal sent. Waiting for the agent to settle…"
      : "Stopping the current turn…";
  return (
    <View
      style={[styles.note, failed && styles.interruptFailed]}
      accessibilityRole={failed ? "alert" : "text"}
      accessibilityLiveRegion="polite"
    >
      <View style={styles.noteIcon}>
        {status === "sending" ? (
          <ActivityIndicator size="small" color={color.error} />
        ) : (
          <Text style={[styles.noteGlyph, { color: failed ? color.error : color.muted }]}>■</Text>
        )}
      </View>
      <Text style={[styles.noteText, failed && { color: color.error }]}>{text}</Text>
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
      <View style={styles.noteIcon}>
        <Text style={styles.noteGlyph}>i</Text>
      </View>
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
    <MotionPressable
      onPress={() => failed && onDismiss(pending.clientId)}
      disabled={!failed}
      style={[styles.userRow, styles.pendingRow, failed && styles.pendingFailed]}
      pressedScale={0.98}
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
    </MotionPressable>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: color.ink },
  keyboardAvoider: { flex: 1 },
  feed: { flex: 1 },

  headerShell: {
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: color.line,
    backgroundColor: color.ink,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    gap: space.sm,
    paddingHorizontal: space.lg,
    paddingVertical: space.md,
  },
  back: {
    width: 40,
    height: 40,
    borderRadius: radius.pill,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.surface,
    borderWidth: 1,
    borderColor: color.line,
  },
  backGlyph: { color: color.text, fontSize: 30, lineHeight: 32, marginTop: -4 },
  headerBody: { flex: 1 },
  stop: {
    width: 36,
    height: 36,
    alignItems: "center",
    justifyContent: "center",
    borderRadius: radius.pill,
    backgroundColor: color.errorWash,
    borderWidth: 1,
    borderColor: "#563039",
  },
  stopGlyph: { width: 10, height: 10, borderRadius: 2, backgroundColor: color.error },
  stopDisabled: { opacity: 0.55 },
  title: { fontFamily: font.monoMedium, fontSize: size.title, color: color.text },
  subtitle: { fontFamily: font.mono, fontSize: size.label, color: color.muted, marginTop: 1 },
  statePill: {
    minHeight: 30,
    flexDirection: "row",
    alignItems: "center",
    paddingRight: space.sm,
    borderRadius: radius.pill,
    backgroundColor: color.surface,
    borderWidth: 1,
    borderColor: color.line,
  },
  statePillAttention: { backgroundColor: color.needsYouWash, borderColor: "#594523" },
  stateLabel: { fontFamily: font.sansMedium, fontSize: size.label },

  note: {
    flexDirection: "row",
    alignItems: "center",
    gap: space.sm,
    marginHorizontal: space.lg,
    marginTop: space.sm,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
    borderRadius: radius.md,
    backgroundColor: color.sunken,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  noteIcon: {
    width: 20,
    height: 20,
    borderRadius: 10,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.surfaceRaised,
  },
  noteGlyph: { fontFamily: font.sansMedium, fontSize: size.label, color: color.muted },
  noteText: { flex: 1, fontFamily: font.sans, fontSize: size.caption, lineHeight: 17, color: color.muted },
  connectionNote: { backgroundColor: color.errorWash, borderColor: "#563039" },
  connectionNoteIcon: { backgroundColor: "#402127" },
  interruptFailed: { backgroundColor: color.errorWash, borderColor: "#563039" },

  // Inverted, so paddingTop is the gap under the newest row. Without it the
  // last message sits behind the composer and gets clipped.
  list: {
    alignSelf: "center",
    width: "100%",
    maxWidth: layout.contentMax,
    padding: space.lg,
    paddingTop: space.md,
    gap: space.xl,
  },
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
    maxWidth: "86%",
    backgroundColor: color.surfaceRaised,
    borderRadius: radius.lg,
    borderTopRightRadius: radius.sm,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
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

  questionWrap: {
    paddingHorizontal: space.lg,
    paddingBottom: space.sm,
  },
  questionScroll: { borderRadius: radius.lg },
  questionScrollContent: { paddingBottom: space.xs },
  thinking: { paddingTop: space.sm },
  composerShell: {
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: color.line,
    backgroundColor: color.ink,
  },
  composer: {
    flexDirection: "row",
    alignItems: "flex-end",
    gap: space.sm,
    paddingHorizontal: space.lg,
    paddingTop: space.md,
  },
  input: {
    flex: 1,
    minHeight: 44,
    maxHeight: 120,
    backgroundColor: color.surface,
    borderRadius: radius.xxl,
    paddingHorizontal: space.lg,
    paddingVertical: space.md,
    fontFamily: font.sans,
    fontSize: size.body,
    color: color.text,
    borderWidth: 1,
    borderColor: color.line,
  },
  inputFocused: { borderColor: color.working },
  inputDisabled: { opacity: 0.5 },
  send: {
    width: 44,
    height: 44,
    borderRadius: 22,
    backgroundColor: color.working,
    alignItems: "center",
    justifyContent: "center",
  },
  sendDisabled: { backgroundColor: color.line },
  sendGlyph: { color: color.ink, fontSize: 19, fontFamily: font.sansBold, marginTop: -2 },
});
