import * as Haptics from "expo-haptics";
import Feather from "@expo/vector-icons/Feather";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Keyboard,
  KeyboardAvoidingView,
  Platform,
  Pressable,
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
import { AttachmentStrip } from "../../components/AttachmentStrip";
import { chooseImageSource } from "../../lib/image-source-sheet";
import { openSessionMenu } from "../../lib/session-menu";
import { draftNamespace } from "../../lib/draft-policy";
import { clearDraft, loadDraft, saveDraft } from "../../lib/drafts";
import { useAttachments } from "../../lib/use-attachments";
import { Message } from "../../lib/protocol";
import { sessionNeedsAnswer } from "../../lib/question-alerts";
import { useStyles, useTheme } from "../../lib/appearance";
import { PendingSend, useStore } from "../../lib/store";
import {
  agentLabel,
  font,
  layout,
  Palette,
  radius,
  shortPath,
  size,
  space,
  stateStyle,
} from "../../lib/theme";

type Row =
  | { kind: "message"; message: Message }
  | { kind: "pending"; pending: PendingSend };

export default function SessionScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const sessionId = decodeURIComponent(String(id));
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const { height: viewportHeight } = useWindowDimensions();
  const listRef = useRef<FlatList<Row>>(null);
  const [draft, setDraft] = useState("");
  const images = useAttachments(store.credentials);
  const [sendingImages, setSendingImages] = useState(false);
  const [draftReady, setDraftReady] = useState(false);
  const [submittedClientId, setSubmittedClientId] = useState<string | null>(
    null,
  );
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
  const effectiveState =
    session && sessionNeedsAnswer(session) ? "waiting_input" : session?.state;
  const draftScope = useMemo(
    () => (store.credentials ? draftNamespace(store.credentials) : null),
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

  useEffect(
    () => () => {
      // Navigation can happen inside the debounce window; flush the final value
      // so leaving quickly never loses the last few characters.
      if (draftWriteTimer.current) clearTimeout(draftWriteTimer.current);
      draftWriteTimer.current = null;
      if (draftReadyRef.current && draftScope) {
        void saveDraft(draftScope, sessionId, draftRef.current);
      }
    },
    [draftScope, sessionId],
  );

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

  // An image on its own is enough to send.
  const nothingToSend =
    draft.trim().length === 0 && images.attachments.length === 0;

  const submit = useCallback(async () => {
    const text = draft.trim();
    // An image with no words is a real message: "look at this".
    if ((!text && images.attachments.length === 0) || !canSend || awaitingSend)
      return;
    if (sendingImages) return;
    if (Platform.OS !== "web") {
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(
        () => {},
      );
    }

    // Uploaded here rather than when the image was chosen: the relay holds one
    // for two minutes, and the gap between picking a screenshot and finishing
    // the sentence about it is easily longer than that.
    let uploadIds: string[] = [];
    if (images.attachments.length > 0) {
      setSendingImages(true);
      try {
        uploadIds = await images.upload();
      } catch (reason) {
        images.setError(
          reason instanceof Error ? reason.message : String(reason),
        );
        setSendingImages(false);
        return;
      }
      setSendingImages(false);
    }

    setSubmittedClientId(store.sendMessage(sessionId, text, uploadIds));
    images.clear();
    // No scroll call needed: an inverted list is already anchored to the
    // newest row, so the sent message appears in place.
  }, [draft, canSend, awaitingSend, sessionId, store, images, sendingImages]);

  const requestInterrupt = useCallback(() => {
    if (!store.daemonOnline) return;
    if (
      interruptAction?.status === "sending" ||
      interruptAction?.status === "delivered"
    ) {
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

  const interruptLocked =
    !store.daemonOnline ||
    interruptAction?.status === "sending" ||
    interruptAction?.status === "delivered";

  const menuServers = session?.servers?.length ?? 0;
  const canStopTurn = session?.state === "busy" && !interruptLocked;

  const displayState = effectiveState ?? session?.state ?? "ended";
  const state = stateStyle(displayState, color);

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <ContentColumn style={styles.header}>
        <MotionPressable
          onPress={() => router.back()}
          hitSlop={12}
          style={styles.iconButton}
          pressedScale={0.92}
          accessibilityRole="button"
          accessibilityLabel="Back to agents"
        >
          <Feather name="chevron-left" size={20} color={color.text} />
        </MotionPressable>
        <View style={styles.headerBody}>
          <Text style={styles.title} numberOfLines={1}>
            {session?.name ?? "Session"}
          </Text>
          {session ? (
            <View style={styles.subtitleRow}>
              {/* Which model is answering you is worth knowing before you send
                  it something — "Codex" says which CLI is open, not what is
                  doing the work. */}
              <Text style={[styles.subtitle, styles.subtitleModel]} numberOfLines={1}>
                {session.model ?? agentLabel(session.kind).name}
                {" · "}
              </Text>
              {/* Truncated from the front, because a path's last segment is the
                  one that says which project this is. Cut from the end,
                  ~/Desktop/agentman became "~/Desktop/agent…", which identifies
                  nothing. */}
              <Text
                style={[styles.subtitle, styles.subtitlePath]}
                numberOfLines={1}
                ellipsizeMode="head"
              >
                {shortPath(session.cwd)}
              </Text>
            </View>
          ) : (
            <Text style={styles.subtitle} numberOfLines={1}>
              {sessionId}
            </Text>
          )}
        </View>
        {session ? (
          <View style={[styles.statePill, { backgroundColor: state.wash }]}>
            <Pulse state={displayState} size={6} />
            <Text style={[styles.stateLabel, { color: state.text }]}>
              {state.label}
            </Text>
          </View>
        ) : null}
        {/* Servers and stop live behind this rather than beside the title. Five
            controls across a phone-width row left the name under half of it and
            cut the working directory to "~/Deskt…", and what a session is gets
            read far more often than either of them gets pressed. */}
        {session && (menuServers > 0 || canStopTurn) ? (
          <MotionPressable
            onPress={() =>
              openSessionMenu(
                { servers: menuServers, canStop: canStopTurn },
                (action) => {
                  if (action === "servers") {
                    router.push(`/servers/${encodeURIComponent(session.id)}`);
                    return;
                  }
                  requestInterrupt();
                },
              )
            }
            hitSlop={10}
            style={styles.iconButton}
            pressedScale={0.92}
            accessibilityRole="button"
            accessibilityLabel={
              menuServers > 0
                ? `Session actions. ${menuServers} server${menuServers === 1 ? "" : "s"} running`
                : "Session actions"
            }
          >
            {interruptAction?.status === "sending" ? (
              <ActivityIndicator size="small" color={color.text} />
            ) : (
              <Feather name="more-horizontal" size={20} color={color.text} />
            )}
            {/* The count still has to be visible without opening anything: a
                running server is a fact about the session, not an action. */}
            {menuServers > 0 ? (
              <View style={styles.serverDot}>
                <Text style={styles.serverDotText}>{menuServers}</Text>
              </View>
            ) : null}
          </MotionPressable>
        ) : null}
      </ContentColumn>

      {session ? (
        <ContentColumn style={styles.workspaceBar}>
          <MotionPressable
            onPress={() =>
              router.push(`/workspace/${encodeURIComponent(session.id)}`)
            }
            style={styles.workspaceButton}
            pressedScale={0.98}
            accessibilityRole="button"
            accessibilityLabel="Browse files and working tree changes"
          >
            <Feather name="folder" size={16} color={color.workingText} />
            <Text style={styles.workspaceLabel}>Files & changes</Text>
            <Feather name="chevron-right" size={15} color={color.faint} />
          </MotionPressable>
        </ContentColumn>
      ) : null}

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
            <InterruptNote
              status={interruptAction.status}
              error={interruptAction.error}
            />
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
                cwd={session?.cwd ?? ""}
                fresh={
                  settled.current !== null &&
                  !settled.current.has(item.message.id)
                }
              />
            ) : (
              <PendingRow
                pending={item.pending}
                onDismiss={store.dismissPending}
              />
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

        {/* A blocked agent reads nothing else until this is answered, so the
            question takes the composer's place as a sheet docked to the
            bottom: impossible to miss, but the transcript above stays
            readable for context. */}
        {session?.question ? (
          <View
            style={[
              styles.sheet,
              {
                maxHeight: Math.max(260, Math.min(520, viewportHeight * 0.62)),
                paddingBottom: (keyboardVisible ? 0 : insets.bottom) + space.md,
              },
            ]}
          >
            <View style={styles.grabber} />
            <ScrollView
              contentContainerStyle={styles.sheetContent}
              keyboardShouldPersistTaps="handled"
              nestedScrollEnabled
            >
              <ContentColumn>
                <QuestionCard
                  question={session.question}
                  onAnswer={(answer) => store.answerQuestion(sessionId, answer)}
                  disabled={!store.daemonOnline}
                  submissionStatus={answerAction?.status}
                  submissionError={answerAction?.error}
                />
              </ContentColumn>
            </ScrollView>
          </View>
        ) : (
          <View style={styles.composerShell}>
            <ContentColumn
              style={[
                styles.composer,
                {
                  paddingBottom:
                    (keyboardVisible ? 0 : insets.bottom) + space.sm,
                },
              ]}
            >
              {images.error ? (
                <Text style={styles.attachError}>{images.error}</Text>
              ) : null}
              <View
                style={[
                  styles.field,
                  images.attachments.length > 0 && styles.fieldWithImages,
                  inputFocused && canSend && styles.fieldFocused,
                  !canSend && styles.fieldDisabled,
                ]}
              >
                <AttachmentStrip
                  attachments={images.attachments}
                  onRemove={images.remove}
                />
                <View style={styles.fieldRow}>
                  {/* Two ways in, because copying a screenshot and choosing one
                    from the library are different habits and neither
                    substitutes for the other. Paste only appears when there is
                    actually an image on the clipboard. */}
                  <MotionPressable
                    onPress={() =>
                      chooseImageSource(
                        images.clipboardReady,
                        (source) =>
                          void (source === "paste"
                            ? images.paste()
                            : images.pick()),
                      )
                    }
                    disabled={
                      !canSend || awaitingSend || !images.canAdd || images.busy
                    }
                    style={styles.attach}
                    pressedScale={0.92}
                    hitSlop={8}
                    accessibilityRole="button"
                    accessibilityLabel="Add an image"
                    accessibilityState={{ disabled: !images.canAdd }}
                  >
                    {images.busy ? (
                      <ActivityIndicator size="small" color={color.faint} />
                    ) : (
                      <Feather
                        name="plus"
                        size={19}
                        color={
                          !canSend || awaitingSend || !images.canAdd
                            ? color.faint
                            : color.muted
                        }
                      />
                    )}
                  </MotionPressable>
                  {/* iOS's own edit menu can paste text into a TextInput but not
                    an image: RCTUITextView.paste: hands straight to UITextView,
                    and no onPaste reaches JS. So a long press here offers the
                    image instead, in the place the gesture already suggests. */}
                  <Pressable
                    onLongPress={() => {
                      if (!images.clipboardReady || !images.canAdd) return;
                      void images.paste();
                    }}
                    delayLongPress={400}
                    style={styles.inputWrap}
                    accessibilityLabel={
                      images.clipboardReady
                        ? "Hold to paste the image on the clipboard"
                        : undefined
                    }
                  >
                    <TextInput
                      style={styles.input}
                      value={draft}
                      onChangeText={setDraft}
                      onFocus={() => setInputFocused(true)}
                      onBlur={() => setInputFocused(false)}
                      editable={canSend && !awaitingSend}
                      placeholder={
                        awaitingSend
                          ? "Waiting for delivery…"
                          : canSend
                            ? `Message ${session ? agentLabel(session.kind).name : "the agent"}…`
                            : "This session can't receive messages"
                      }
                      placeholderTextColor={color.faint}
                      multiline
                      onSubmitEditing={() => void submit()}
                      returnKeyType="send"
                      accessibilityLabel="Instruction"
                    />
                  </Pressable>
                  <MotionPressable
                    onPress={() => void submit()}
                    disabled={!canSend || awaitingSend || nothingToSend}
                    style={[
                      styles.send,
                      (!canSend || awaitingSend || nothingToSend) &&
                        styles.sendDisabled,
                    ]}
                    pressedScale={0.92}
                    hitSlop={8}
                    accessibilityRole="button"
                    accessibilityLabel="Send instruction"
                    accessibilityState={{
                      disabled: !canSend || awaitingSend || nothingToSend,
                      busy:
                        submittedSend?.status === "sending" || sendingImages,
                    }}
                  >
                    {submittedSend?.status === "sending" || sendingImages ? (
                      <ActivityIndicator size="small" color={color.faint} />
                    ) : (
                      <Feather
                        name="arrow-up"
                        size={18}
                        color={
                          !canSend || awaitingSend || nothingToSend
                            ? color.faint
                            : "#FFFFFF"
                        }
                      />
                    )}
                  </MotionPressable>
                </View>
              </View>
            </ContentColumn>
          </View>
        )}
      </KeyboardAvoidingView>
    </View>
  );
}

function ConnectionNote() {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View
      style={[styles.note, styles.connectionNote]}
      accessibilityRole="alert"
      accessibilityLiveRegion="polite"
    >
      <View style={styles.noteIcon}>
        <Feather name="wifi-off" size={13} color={color.errorText} />
      </View>
      <Text style={styles.noteText}>
        Your Mac is offline. Answers and turn controls unlock when it
        reconnects.
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
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
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
          <ActivityIndicator size="small" color={color.errorText} />
        ) : (
          <Feather
            name="square"
            size={12}
            color={failed ? color.errorText : color.muted}
          />
        )}
      </View>
      <Text style={[styles.noteText, failed && { color: color.errorText }]}>
        {text}
      </Text>
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
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
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
        <Feather name="info" size={13} color={color.muted} />
      </View>
      <Text style={styles.noteText}>{text}</Text>
    </View>
  );
}

function MessageRow({
  message,
  cwd,
  fresh,
}: {
  message: Message;
  cwd: string;
  fresh: boolean;
}) {
  const styles = useStyles(makeStyles);
  if (message.role === "tool" && message.tool) {
    return (
      <Appear enabled={fresh}>
        <ToolRow message={message} cwd={cwd} />
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
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const failed = pending.status === "failed";
  const queued = pending.status === "queued";

  return (
    <MotionPressable
      onPress={() => failed && onDismiss(pending.clientId)}
      disabled={!failed}
      style={[
        styles.userRow,
        styles.pendingRow,
        failed && styles.pendingFailed,
      ]}
      pressedScale={0.98}
    >
      <Text style={styles.userText}>{pending.text}</Text>
      <Text
        style={[styles.pendingStatus, failed && { color: color.errorText }]}
      >
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

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    keyboardAvoider: { flex: 1 },
    feed: { flex: 1 },

    header: {
      flexDirection: "row",
      alignItems: "center",
      gap: 10,
      paddingHorizontal: space.lg,
      paddingTop: space.sm,
      paddingBottom: space.md,
    },
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
    headerBody: { flex: 1 },
    workspaceBar: { paddingHorizontal: space.lg, paddingBottom: space.sm },
    workspaceButton: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      minHeight: 38,
      paddingHorizontal: space.md,
      borderRadius: radius.md,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    workspaceLabel: {
      flex: 1,
      color: c.text,
      fontFamily: font.sansMedium,
      fontSize: size.caption,
    },
    // A count on the overflow control, so a running server is still visible
    // without opening anything: it is a fact about the session, not an action.
    serverDot: {
      position: "absolute",
      top: 0,
      right: 0,
      minWidth: 15,
      height: 15,
      borderRadius: 8,
      paddingHorizontal: 3,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.working,
    },
    serverDotText: {
      fontFamily: font.sansMedium,
      fontSize: 9.5,
      lineHeight: 12,
      color: "#FFFFFF",
    },
    title: { fontFamily: font.monoMedium, fontSize: 15, color: c.text },
    subtitleRow: { flexDirection: "row", alignItems: "center", marginTop: 2 },
    subtitle: {
      fontFamily: font.sans,
      fontSize: size.label,
      color: c.muted,
    },
    // The model holds its width and the path gives way. Without the explicit
    // 0 both shrink, and you end up with two half-truncated facts instead of
    // one whole one.
    subtitleModel: { flexShrink: 0 },
    subtitlePath: { flexShrink: 1 },
    statePill: {
      height: 28,
      flexDirection: "row",
      alignItems: "center",
      paddingRight: 10,
      borderRadius: radius.pill,
    },
    stateLabel: { fontFamily: font.sansBold, fontSize: size.label },

    note: {
      flexDirection: "row",
      alignItems: "center",
      gap: 10,
      marginHorizontal: space.lg,
      marginBottom: space.sm,
      paddingHorizontal: space.md,
      paddingVertical: 10,
      borderRadius: radius.lg,
      backgroundColor: c.fill,
    },
    noteIcon: {
      width: 24,
      height: 24,
      borderRadius: 8,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
    },
    noteText: {
      flex: 1,
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 18,
      color: c.textSecondary,
    },
    connectionNote: { backgroundColor: c.errorWash },
    interruptFailed: { backgroundColor: c.errorWash },

    // Inverted, so paddingTop is the gap under the newest row. Without it the
    // last message sits behind the composer and gets clipped.
    list: {
      alignSelf: "center",
      width: "100%",
      maxWidth: layout.contentMax,
      padding: space.lg,
      paddingTop: space.md,
      gap: 18,
    },
    spinner: { marginVertical: space.lg },
    startOfSession: {
      fontFamily: font.sans,
      fontSize: size.caption,
      color: c.faint,
      textAlign: "center",
      marginBottom: space.lg,
    },
    emptyFeed: {
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.faint,
      textAlign: "center",
      marginTop: space.xxl,
    },

    // The user's own words get a bubble; the agent's sit on the page. That is
    // enough to tell them apart without turning a transcript into a chat app.
    userRow: {
      alignSelf: "flex-end",
      maxWidth: "84%",
      backgroundColor: c.fill,
      borderRadius: radius.xl,
      borderBottomRightRadius: 6,
      paddingHorizontal: 14,
      paddingVertical: 10,
    },
    userText: {
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.text,
      lineHeight: 21,
    },

    assistantRow: { gap: space.xs },
    sidechain: {
      alignSelf: "flex-start",
      fontFamily: font.sansBold,
      fontSize: 11,
      color: c.muted,
      backgroundColor: c.fill,
      borderRadius: radius.pill,
      overflow: "hidden",
      paddingHorizontal: space.sm,
      paddingVertical: 2,
    },

    systemText: {
      fontFamily: font.sans,
      fontSize: size.caption,
      color: c.faint,
      textAlign: "center",
    },

    pendingRow: { opacity: 0.7 },
    pendingFailed: { opacity: 1, backgroundColor: c.errorWash },
    pendingStatus: {
      fontFamily: font.sans,
      fontSize: size.label,
      color: c.muted,
      marginTop: space.xs,
    },

    sheet: {
      backgroundColor: c.surface,
      borderTopLeftRadius: radius.sheet,
      borderTopRightRadius: radius.sheet,
      paddingTop: 10,
      shadowColor: c.shadow,
      shadowOpacity: 0.12,
      shadowRadius: 24,
      shadowOffset: { width: 0, height: -6 },
      elevation: 12,
    },
    grabber: {
      alignSelf: "center",
      width: 38,
      height: 5,
      borderRadius: 3,
      backgroundColor: c.fillStrong,
      marginBottom: space.md,
    },
    sheetContent: { paddingHorizontal: 20, paddingBottom: space.xs },
    thinking: { paddingTop: space.sm },
    composerShell: { backgroundColor: c.paper },
    composer: { paddingHorizontal: space.lg, paddingTop: space.sm },
    // Input and send share one container, so the send button reads as part of
    // the field instead of sitting next to it.
    field: {
      backgroundColor: c.surface,
      borderRadius: radius.xxl,
      borderWidth: 1,
      borderColor: c.line,
      paddingLeft: 6,
      paddingRight: 6,
      paddingVertical: 6,
      shadowColor: c.shadow,
      shadowOpacity: 0.06,
      shadowRadius: 16,
      shadowOffset: { width: 0, height: 6 },
      elevation: 2,
    },
    fieldFocused: { borderColor: c.working },
    fieldDisabled: { opacity: 0.55 },
    input: {
      flex: 1,
      minHeight: 38,
      maxHeight: 120,
      paddingVertical: space.sm,
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.text,
    },
    // Thumbnails square off the pill: a row of 64pt pictures inside a fully
    // rounded container loses its corners to the radius.
    fieldWithImages: {
      borderRadius: radius.lg,
      paddingTop: space.sm,
      paddingLeft: space.sm,
    },
    fieldRow: { flexDirection: "row", alignItems: "flex-end", gap: space.xs },
    // A wrapper so the long press has something to land on: the gesture cannot
    // be attached to the TextInput itself without swallowing taps that should
    // place the caret.
    inputWrap: { flex: 1 },
    attachError: {
      fontFamily: font.sans,
      fontSize: size.label,
      color: c.errorText,
      paddingHorizontal: space.md,
      paddingBottom: space.xs,
    },
    // Square and unfilled against the send button's filled circle: one is the
    // action, the other is a way of adding to it.
    attach: {
      width: 34,
      height: 34,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
    },
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
