import * as Clipboard from "expo-clipboard";
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
  Share,
  StyleSheet,
  Text,
  TextInput,
  useWindowDimensions,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { AgentIcon } from "../../components/AgentIcon";
import { Appear } from "../../components/Appear";
import { addPastedImagesListener, pasteTakesImages } from "../../modules/image-paste";
import { MessageMenu, type MessageMenuItem } from "../../components/MessageMenu";
import { Toast } from "../../components/Toast";
import { ContentColumn } from "../../components/ContentColumn";
import { Markdown } from "../../components/Markdown";
import { SentImages } from "../../components/SentImages";
import { ToolGroup } from "../../components/ToolGroup";
import { KEEP_READING, ScrollToLatest, SCROLL_AWAY } from "../../components/ScrollToLatest";
import { groupTools } from "../../lib/toolgroups";
import { readImage } from "../../lib/imagepath";
import { splitSentMessage } from "../../lib/sentimages";
import { MotionPressable } from "../../components/MotionPressable";
import { Pulse } from "../../components/Pulse";
import { QuestionCard } from "../../components/QuestionCard";
import { Thinking } from "../../components/Thinking";
import { ToolRow } from "../../components/ToolRow";
import { AttachmentStrip } from "../../components/AttachmentStrip";
import { Popover, type PopoverItem } from "../../components/Popover";
import { type Frame } from "../../lib/message-menu";
import { artifactsButtonLabel } from "../../lib/artifacts";
import { canEnd, shouldResume } from "../../lib/resume";
import { modeLabel, modelLabel, showsInFeed, statusChip } from "../../lib/session-view";
import {
  defaultScopeWarning,
  switchChoices,
  switchFailure,
  type SwitchKind,
} from "../../lib/switching";
import { chooseImageSource } from "../../lib/image-source-sheet";
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
  | { kind: "tools"; key: string; calls: Message[]; said: string }
  | { kind: "pending"; pending: PendingSend }
  // A message the agent is holding until its current step ends, sent from
  // the phone or typed at the Mac.
  | { kind: "queued"; key: string; text: string };

/**
 * Where the popover hangs from: the header's own height, so the menu opens
 * directly under the control that summoned it. space.sm above the row, a 36pt
 * control, space.md below, and a few points of air.
 */
const HEADER_HEIGHT = space.sm + 36 + space.md + 4;

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
  const [scrolledAway, setScrolledAway] = useState(false);
  const [draft, setDraft] = useState("");
  const images = useAttachments(store.credentials);
  // The confirmation for a copy made from a message's menu. Held here rather
  // than in the row so it survives the row re-rendering, and so there is only
  // ever one on screen.
  const [toast, setToast] = useState<string | null>(null);
  const showCopied = useCallback(() => setToast("Copied"), []);
  const hideToast = useCallback(() => setToast(null), []);
  const [sendingImages, setSendingImages] = useState(false);
  const [draftReady, setDraftReady] = useState(false);
  const [submittedClientId, setSubmittedClientId] = useState<string | null>(
    null,
  );
  const [inputFocused, setInputFocused] = useState(false);
  // The text box, measured when the model menu opens so the menu sits on it.
  const fieldRef = useRef<View>(null);
  const [modelMenuBottom, setModelMenuBottom] = useState<number | null>(null);
  // Paste in the text box takes an image only while it is focused: a paste
  // goes to whichever box has focus, and this one is where images belong.
  const { addPasted } = images;
  useEffect(() => {
    if (!inputFocused) return;
    const subscription = addPastedImagesListener((pasted) => void addPasted(pasted));
    return () => subscription?.remove();
  }, [inputFocused, addPasted]);
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

  const session = store.findSession(sessionId);
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
  }, [sessionId, session?.id]);

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

  // The question sheet's scroll view. A note opened on a "No, and say why"
  // option brings the keyboard up, and the sheet moves above it — but its
  // content stayed where it was, which left the box being typed into below
  // the visible part of the sheet. Scrolling to the end shows it: the note
  // sits under the last option.
  const sheetScroll = useRef<ScrollView>(null);
  useEffect(() => {
    if (Platform.OS === "web") return;
    const shown = Keyboard.addListener("keyboardDidShow", () => {
      sheetScroll.current?.scrollToEnd({ animated: true });
    });
    return () => shown.remove();
  }, []);

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

  const queued = session?.queued;
  const queuedRows = session?.queuedMessageIds;
  const rows = useMemo<Row[]>(() => {
    const sent = store.pending
      .filter((p) => p.sessionId === sessionId)
      .map((pending) => ({ kind: "pending" as const, pending }));
    const waiting = (queued ?? []).map((text, index) => ({
      kind: "queued" as const,
      key: `queued:${index}:${text}`,
      text,
    }));
    // An agent that saves a message before reading it (OpenCode) has it in
    // the feed already; it is drawn below as waiting instead, until read.
    const waitingIds = new Set(queuedRows ?? []);
    const chronological: Row[] = [
      // Filtered here rather than drawn as nothing: an empty cell still takes
      // the list's gap, and a withdrawn preview would leave a hole.
      // Calls in a row read as one line saying what was done.
      // A call that read a picture stands alone, with the picture under it.
      ...groupTools(messages.filter((message) => showsInFeed(message) && !waitingIds.has(message.id)), (message) =>
        Boolean(message.tool && readImage(message.tool.name, message.tool.summary ?? "", session?.cwd ?? "")),
      ),
      ...sent,
      ...waiting,
    ];
    // Reversed to feed an inverted list — see the FlatList below.
    return chronological.reverse();
  }, [messages, store.pending, sessionId, queued, queuedRows, session?.cwd]);
  // What a message waiting in the agent's queue says under it, the same
  // whether the phone or the Mac sent it.
  const waitingLabel = session ? pendingLabel(session.kind, session.state) : undefined;
  // The agent's send-now key has to be pressed in its own terminal, which
  // the Mac says it can do. Daemons before 0.18 said nothing and offered it
  // for Claude in tmux only.
  const canSendNow = Boolean(
    session?.canSendQueued || (session?.kind === "claude" && session.inject === "tmux"),
  );
  const sendNowAction = store.actions.find(
    (action) => action.sessionId === sessionId && action.kind === "send_queued",
  );
  // A failed Send now says why once, where the copy confirmation appears; the
  // messages stay waiting, and the button is there to try again.
  const sendNowFailure = sendNowAction?.status === "failed" ? sendNowAction : undefined;
  useEffect(() => {
    if (sendNowFailure) setToast(`Couldn't send now${sendNowFailure.error ? `: ${sendNowFailure.error}` : ""}`);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sendNowFailure?.clientId]);

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
    if (submittedSend && submittedSend.status !== "delivered") return;
    if (!session) {
      // A removed session also drops its pending row, but that is not proof the
      // text landed. Keep the account-scoped draft for inspection/recovery.
      setSubmittedClientId(null);
      return;
    }
    // Cursor keeps its delivered bubble until the local transcript catches up.
    // Delivery still settles the composer immediately.
    setSubmittedClientId(null);
    setDraft("");
    draftRef.current = "";
    if (draftScope) void clearDraft(draftScope, sessionId);
  }, [draftScope, session, sessionId, submittedClientId, submittedSend]);

  // An image on its own is enough to send.
  const nothingToSend =
    draft.trim().length === 0 && images.attachments.length === 0;

  // Asked on open and again each time the agent settles, because finishing a
  // turn is exactly when files appear. Polling would cost a git status a
  // second per session for an answer that only changes when work stops.
  const settledState = session?.state;
  useEffect(() => {
    if (!sessionId || !store.daemonOnline) {
      setChangedFiles(null);
      return;
    }
    let current = true;
    void store
      .workspace(sessionId, "list_changes")
      .then((result) => {
        if (current) setChangedFiles(result.changes?.length ?? 0);
      })
      .catch(() => {
        // A session outside a repository, or a Mac that went away. Either way
        // there is nothing to review, and saying so loudly helps nobody.
        if (current) setChangedFiles(null);
      });
    return () => {
      current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, settledState, store.daemonOnline]);

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
    // Sending always comes back to the latest, wherever the reader was.
    listRef.current?.scrollToOffset({ offset: 0, animated: true });
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
  }, [interruptAction?.status, session?.inject, sessionId, store]);

  const interruptLocked =
    !store.daemonOnline ||
    // A session the daemon cannot type into cannot be interrupted either.
    session?.inject === "none" ||
    interruptAction?.status === "sending" ||
    interruptAction?.status === "delivered";

  const [menuOpen, setMenuOpen] = useState(false);
  // Opening a read-only session reopens it on the Mac.
  //
  // Most of what a folder holds has already ended, and every CLI can reopen
  // one of its own sessions by id — so "read-only" was never a property of
  // the session, only of whether anything was running. Attempted once per
  // session, because a failure that retries on every render would be a loop
  // that launches processes.
  const [resuming, setResuming] = useState(false);
  const [resumeError, setResumeError] = useState("");
  const resumeTried = useRef<string | null>(null);

  useEffect(() => {
    if (!session || !store.daemonOnline) return;
    if (!shouldResume(session)) return;
    if (resumeTried.current === session.id) return;
    resumeTried.current = session.id;
    setResuming(true);
    setResumeError("");
    store
      .resumeSession(session.id)
      .then((id) => {
        // Claude, Kiro, Antigravity and Cursor keep writing the transcript
        // they were pointed at, so the id survives. Codex opens a rollout of
        // its own, so the reopened session is a different one to watch.
        if (id && id !== session.id) {
          router.replace(`/session/${encodeURIComponent(id)}`);
        }
      })
      // Only a failure ends "reopening" here. Success does not: the request
      // returns when the pane exists, but the agent inside it takes a few
      // seconds more to start and register, and until discovery sees it the
      // session still reads as unreachable. Clearing the note on success
      // flashed the old "start this with am claude" advice at exactly the
      // moment the session was coming up.
      .catch((err: Error) => {
        setResumeError(err.message);
        setResuming(false);
      });
    // store methods change identity as sessions update; this must run once
    // per session, not once per update.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session?.id, session?.inject, store.daemonOnline]);

  // Reopening is over when the session can actually be reached.
  useEffect(() => {
    if (resuming && session && session.inject !== "none") setResuming(false);
  }, [resuming, session]);

  // An agent that never comes up should not leave the note spinning forever.
  // Long enough for a cold start with a trust prompt; short enough that a
  // missing login is reported while the person is still looking.
  useEffect(() => {
    if (!resuming) return;
    const timer = setTimeout(() => {
      setResuming(false);
      setResumeError(
        "The session did not come back up. Check the agent's login on your Mac, then open it again.",
      );
    }, 30_000);
    return () => clearTimeout(timer);
  }, [resuming]);
  // null until asked, so the bar is absent rather than flickering in and out
  // while the answer is in flight.
  const [changedFiles, setChangedFiles] = useState<number | null>(null);
  const menuServers = session?.servers?.length ?? 0;

  // Built here rather than in the header so every entry sits beside the action
  // it performs. An idle session simply has fewer rows — which is a popover
  // shrinking, not a control changing shape.
  const sessionMenuItems: PopoverItem[] = [];
  if (session) {
    sessionMenuItems.push({
      key: "files",
      label: "Files & changes",
      icon: "folder",
      onPress: () =>
        router.push(`/workspace/${encodeURIComponent(session.id)}`),
    });
    if (menuServers > 0) {
      sessionMenuItems.push({
        key: "servers",
        label: menuServers === 1 ? "Server" : "Servers",
        icon: "globe",
        detail: String(menuServers),
        onPress: () =>
          router.push(`/servers/${encodeURIComponent(session.id)}`),
      });
    }
    if (session.state === "busy") {
      sessionMenuItems.push({
        key: "stop",
        label: "Stop this turn",
        icon: "square",
        destructive: true,
        disabled: interruptLocked,
        onPress: requestInterrupt,
      });
    }
    // Closing the pane is what keeps opening sessions freely from leaving a
    // dozen of them running. The transcript survives, so this is reversible:
    // the session stays in its folder and opening it starts it again.
    if (canEnd(session)) {
      sessionMenuItems.push({
        key: "end",
        label: "End session",
        icon: "x-circle",
        destructive: true,
        onPress: () => {
          void store.endSession(session.id).catch(() => {});
          router.back();
        },
      });
    }
  }

  // Mode and model switching. Only what the agent listed is offered, and a
  // switch is one request at a time: the Mac presses keys and reads the
  // result back, and a second tap mid-way would race it.
  const [switchMenu, setSwitchMenu] = useState<SwitchKind | null>(null);
  const [headerHeight, setHeaderHeight] = useState(HEADER_HEIGHT);
  // sent: the Mac has seen the switch take, and the next sweep will say so.
  const [switching, setSwitching] = useState<{
    kind: SwitchKind;
    value: string;
    sent?: boolean;
  } | null>(null);
  const [switchConfirm, setSwitchConfirm] = useState<{
    kind: SwitchKind;
    value: string;
    warning: string;
  } | null>(null);
  const [switchError, setSwitchError] = useState("");
  useEffect(() => {
    setSwitchMenu(null);
    setSwitchConfirm(null);
    setSwitchError("");
  }, [sessionId]);
  const modeChoices = session ? switchChoices(session, "mode") : null;
  const modelChoices = session ? switchChoices(session, "model") : null;
  // The daemon refuses a switch during a question too; disabling it here
  // says so before anyone taps.
  const switchLocked = !store.daemonOnline || Boolean(session?.question) || switching !== null;

  const performSwitch = (kind: SwitchKind, value: string) => {
    if (!session) return;
    setSwitchConfirm(null);
    setSwitchError("");
    setSwitching({ kind, value });
    store
      .switchSession(session.id, kind, value)
      .then(() => setSwitching((current) => (current?.value === value ? { ...current, sent: true } : current)))
      .catch((reason: Error) => {
        setSwitchError(switchFailure(kind, value, reason.message));
        setSwitching(null);
      });
  };

  // The Mac answers once the agent shows the switch, a moment before the next
  // sweep brings the session's new mode or model. The pending value stays on
  // screen until then, rather than the old one flashing back in between —
  // and for no more than a few seconds, in case the agent names it otherwise.
  useEffect(() => {
    if (!switching?.sent || !session) return;
    const current = switching.kind === "mode" ? session.mode : session.model;
    if (current === switching.value) {
      setSwitching(null);
      return;
    }
    const timer = setTimeout(() => setSwitching(null), 4000);
    return () => clearTimeout(timer);
  }, [switching, session?.mode, session?.model]);

  const requestSwitch = (kind: SwitchKind, value: string) => {
    if (!session || switchLocked) return;
    const current = kind === "mode" ? session.mode : session.model;
    if (value === current) return;
    // A model the CLI also saves as its default changes the Mac's next
    // session as well, so that is said, naming the agent, before anything is
    // sent. Inline rather than an alert: an alert raised as the menu's Modal
    // closes is dismissed with it on iOS.
    const warning = defaultScopeWarning(session, kind, agentLabel(session.kind).name, value);
    if (warning) {
      setSwitchError("");
      setSwitchConfirm({ kind, value, warning });
      return;
    }
    performSwitch(kind, value);
  };

  const switchItems: PopoverItem[] = [];
  const openChoices = switchMenu === "mode" ? modeChoices : switchMenu === "model" ? modelChoices : null;
  if (switchMenu && openChoices) {
    for (const value of openChoices.values) {
      switchItems.push({
        key: value,
        label: switchMenu === "mode" ? modeLabel(value) : modelLabel(value),
        checked: value === openChoices.current,
        onPress: () => requestSwitch(switchMenu, value),
      });
    }
  }
  const switchTitle =
    switchMenu === "mode"
      ? "Mode"
      : session?.modelScope === "default"
        ? "Model · also the default for new sessions"
        : "Model for this session";

  const displayState = effectiveState ?? session?.state ?? "ended";
  const state = stateStyle(displayState, color);
  const chip = session
    ? statusChip(session.mode, session.contextPercent, modeChoices !== null)
    : null;
  const headerControls = session
    ? shownHeaderControls({
        mode: chip?.mode && chip.mode !== "mode" ? chip.mode : undefined,
        modePending: switching?.kind === "mode" ? switching.value : undefined,
        onMode: modeChoices ? () => setSwitchMenu("mode") : undefined,
        contextPercent: session.contextPercent,
      })
    : null;
  const openModelMenu = () => {
    const open = () =>
      fieldRef.current?.measureInWindow((_x, y) => {
        setModelMenuBottom(viewportHeight - y + space.xs);
        setSwitchMenu("model");
      });
    // The text box drops when the keyboard goes, so it is measured after.
    if (Keyboard.isVisible()) {
      const subscription = Keyboard.addListener("keyboardDidHide", () => {
        subscription.remove();
        open();
      });
      Keyboard.dismiss();
    } else {
      open();
    }
  };
  const artifactCount = session?.artifacts ?? 0;
  const artifactsToReview = session?.artifactsToReview ?? 0;

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      {/* Measured, not assumed: the chip row makes the header taller than
          the constant it replaced, and menus anchored to the constant opened
          on top of the very chips that opened them. */}
      <View onLayout={(event) => setHeaderHeight(event.nativeEvent.layout.height)}>
        <ContentColumn style={styles.header}>
          <MotionPressable
            onPress={() => router.back()}
            hitSlop={12}
            style={styles.backButton}
            pressedScale={0.92}
            accessibilityRole="button"
            accessibilityLabel="Back to agents"
          >
            <Feather name="chevron-left" size={20} color={color.text} />
          </MotionPressable>
          {/* Whose chat this is, in the shape the agents list already uses:
              the logo is read before the name is, and the same tile on both
              screens says this is the row you tapped. */}
          {session ? (
            <View style={styles.agentTile}>
              <AgentIcon kind={session.kind} size={20} />
            </View>
          ) : null}
          <View style={styles.headerBody}>
            <Text style={styles.title} numberOfLines={1}>
              {session?.name ?? "Session"}
            </Text>
            {session ? (
              <>
                <View style={styles.subtitleRow}>
                  {/* A colour, not a word: the dot pulses while the agent runs,
                      which says "working" faster than the word did, and the
                      word survives as its label. */}
                  <View
                    style={styles.stateDot}
                    accessible
                    accessibilityRole="text"
                    accessibilityLabel={state.label}
                  >
                    <Pulse state={displayState} size={6} inline />
                  </View>
                  {/* Which agent and which project: the two facts this line is
                      read for. The model and mode used to share it, and at
                      phone width that left the folder as "…ts/northstar" and
                      the mode as "kir…" — four facts, none of them whole. */}
                  <Text style={[styles.subtitle, styles.subtitleModel]} numberOfLines={1}>
                    {agentLabel(session.kind).name}
                  </Text>
                  <Text style={[styles.subtitle, styles.subtitleModel]}>{" · "}</Text>
                  {/* Truncated from the front: a path's last segment is the one
                      that says which project this is. */}
                  <Text
                    style={[styles.subtitle, styles.subtitlePath]}
                    numberOfLines={1}
                    ellipsizeMode="head"
                  >
                    {shortPath(session.cwd)}
                  </Text>
                </View>
              </>
            ) : (
              <Text style={styles.subtitle} numberOfLines={1}>
                {sessionId}
              </Text>
            )}
          </View>
          {/* Its own button rather than a row in the menu: an agent waiting on
              you to approve its plan is going nowhere until you do, and a badge
              behind "…" is one nobody sees. */}
          {session && artifactCount > 0 ? (
            <MotionPressable
              onPress={() =>
                router.push(`/artifacts/${encodeURIComponent(session.id)}`)
              }
              hitSlop={10}
              style={styles.iconButton}
              pressedScale={0.92}
              accessibilityRole="button"
              accessibilityLabel={artifactsButtonLabel(artifactCount, artifactsToReview)}
            >
              <Feather name="file-text" size={18} color={color.text} />
              {artifactsToReview > 0 ? (
                <View style={styles.reviewBadge}>
                  <Text style={styles.reviewBadgeText}>{artifactsToReview}</Text>
                </View>
              ) : null}
            </MotionPressable>
          ) : null}
          {/* Servers and stop live behind this rather than beside the title. Five
              controls across a phone-width row left the name under half of it and
              cut the working directory to "~/Deskt…", and what a session is gets
              read far more often than either of them gets pressed. */}
          {session && sessionMenuItems.length > 0 ? (
            <MotionPressable
              onPress={() => setMenuOpen(true)}
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
        {/* Its own row, the full width of the screen: beside the buttons there
            was room for two chips, and the third was cut off. The model is
            not here: it sits by the text box, where it is chosen. */}
        {session && headerControls ? (
          <ContentColumn style={styles.controlsBar}>
            <HeaderControls {...headerControls} disabled={switchLocked} />
          </ContentColumn>
        ) : null}
      </View>

      {session ? (
        <Popover
          visible={menuOpen}
          onDismiss={() => setMenuOpen(false)}
          top={headerHeight}
          items={sessionMenuItems}
        />
      ) : null}
      {session && switchMenu ? (
        <Popover
          visible
          onDismiss={() => setSwitchMenu(null)}
          {...(switchMenu === "model" && modelMenuBottom !== null
            ? { bottom: modelMenuBottom, align: "right" as const }
            : { top: headerHeight, align: "left" as const })}
          title={switchTitle}
          items={switchItems}
        />
      ) : null}

      {/* A shortcut, not a fixture: it appears when the agent has actually
          touched something, and the popover keeps the files reachable the rest
          of the time. A row that is always there says nothing; one that turns
          up when there is work to review says exactly one thing. */}
      {session && changedFiles !== null && changedFiles > 0 ? (
        <ContentColumn style={styles.workspaceBar}>
          <MotionPressable
            onPress={() =>
              router.push(
                `/workspace/${encodeURIComponent(session.id)}?tab=changes`,
              )
            }
            style={styles.workspaceButton}
            pressedScale={0.98}
            accessibilityRole="button"
            accessibilityLabel={`Review ${changedFiles} changed file${changedFiles === 1 ? "" : "s"}`}
          >
            <Feather name="folder" size={16} color={color.workingText} />
            <Text style={styles.workspaceLabel}>
              {changedFiles === 1
                ? "1 file changed"
                : `${changedFiles} files changed`}
            </Text>
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
        {switchConfirm ? (
          <ContentColumn>
            <SwitchConfirmNote
              warning={switchConfirm.warning}
              value={switchConfirm.value}
              disabled={switchLocked}
              onCancel={() => setSwitchConfirm(null)}
              onConfirm={() => performSwitch(switchConfirm.kind, switchConfirm.value)}
            />
          </ContentColumn>
        ) : null}
        {switchError ? (
          <ContentColumn>
            <SwitchErrorNote error={switchError} onDismiss={() => setSwitchError("")} />
          </ContentColumn>
        ) : null}
        {session && !session.question && (
          <ContentColumn>
            <DeliveryNote
              kind={session.kind}
              inject={session.inject}
              state={session.state}
              resuming={resuming}
              resumeError={resumeError}
            />
          </ContentColumn>
        )}

        {/* Inverted so the feed opens on the newest message and stays pinned
          there as more arrive. Opening a long session at its beginning means
          scrolling through hours of history to find out what just happened,
          which is the opposite of what someone checking their phone wants.
          Inverting also makes "load older" the natural end-of-list action.
          KEEP_READING leaves the reader where they are when they have
          scrolled up, as messages arrive below them. */}
        <View style={styles.feed}>
        <FlatList
          style={styles.feed}
          ref={listRef}
          data={rows}
          inverted
          maintainVisibleContentPosition={KEEP_READING}
          // Inverted, so how far up the reader is counts from the latest.
          onScroll={(event) => {
            const away = event.nativeEvent.contentOffset.y > SCROLL_AWAY;
            if (away !== scrolledAway) setScrolledAway(away);
          }}
          scrollEventThrottle={64}
          keyExtractor={(row) =>
            row.kind === "message"
              ? row.message.id
              : row.kind === "tools"
                ? row.key
                : row.kind === "pending"
                ? row.pending.clientId
                : row.key
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
                onCopied={showCopied}
              />
            ) : item.kind === "tools" ? (
              <ToolGroup calls={item.calls} said={item.said} cwd={session?.cwd ?? ""} />
            ) : item.kind === "pending" ? (
              <PendingRow
                pending={item.pending}
                waitingLabel={item.pending.retainUntilTranscript ? waitingLabel : undefined}
                onDismiss={store.dismissPending}
                onCopied={showCopied}
              />
            ) : (
              <PendingRow
                pending={{ clientId: item.key, sessionId, text: item.text, status: "delivered" }}
                waitingLabel={waitingLabel ?? "Pending"}
                onSendNow={canSendNow ? () => store.sendQueuedNow(sessionId) : undefined}
                sendingNow={sendNowAction?.status === "sending" || sendNowAction?.status === "delivered"}
                onDismiss={() => {}}
                onCopied={showCopied}
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
              <Text style={styles.emptyFeed}>
                {session ? "No messages yet." : "Waiting for session status from your Mac…"}
              </Text>
            )
          }
        />
        <ScrollToLatest
          visible={scrolledAway}
          onPress={() => listRef.current?.scrollToOffset({ offset: 0, animated: true })}
        />
        </View>

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
              ref={sheetScroll}
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
                ref={fieldRef}
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
                {/* Where the text box's own Paste can take an image (iOS, via
                  modules/image-paste), holding it brings up Paste like any
                  other text box. Elsewhere a long press here pastes the image
                  instead, in the place the gesture already suggests; on iOS
                  it would attach the image a second time. */}
                <Pressable
                  onLongPress={
                    pasteTakesImages
                      ? undefined
                      : () => {
                          if (!images.clipboardReady || !images.canAdd) return;
                          void images.paste();
                        }
                  }
                  delayLongPress={400}
                  style={styles.inputWrap}
                  accessibilityLabel={
                    images.clipboardReady && !pasteTakesImages
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
                <View style={styles.toolbar}>
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
                  <View style={styles.toolbarGap} />
                  {/* Beside send, as in Claude's and ChatGPT's apps: the model
                    is part of what you are about to send, and choosing it
                    belongs where the message is written, not in the header. */}
                  {session?.model || switching?.kind === "model" ? (
                    <ModelButton
                      model={modelLabel(
                        (switching?.kind === "model" ? switching.value : session?.model) ?? "",
                      )}
                      pending={switching?.kind === "model"}
                      onPress={modelChoices ? openModelMenu : undefined}
                      disabled={switchLocked}
                    />
                  ) : null}
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

      <Toast message={toast} onHidden={hideToast} top={insets.top + headerHeight + space.sm} />
    </View>
  );
}

interface HeaderControlsProps {
  mode?: string;
  modePending?: string;
  onMode?: () => void;
  contextPercent?: number;
}

/** The header's chips, or null when there is nothing to put in the row. */
function shownHeaderControls(props: HeaderControlsProps): HeaderControlsProps | null {
  const { mode, modePending, onMode, contextPercent } = props;
  const context = contextPercent !== undefined && contextPercent > 0;
  return mode || modePending || onMode || context ? props : null;
}

/**
 * Which mode the agent is in and how full its context is: one row of chips
 * under the title, each with room for its whole name.
 *
 * They were squeezed onto the line with the folder, where at phone width
 * every one of them was cut short. A chip with a chevron opens the switcher
 * for that setting; one without is only a fact. The model used to lead this
 * row; it moved to the text box, beside the button that sends with it.
 */
function HeaderControls({
  mode,
  modePending,
  onMode,
  contextPercent,
  disabled,
}: HeaderControlsProps & { disabled?: boolean }) {
  const styles = useStyles(makeStyles);
  const context =
    contextPercent && contextPercent > 0 ? `${Math.min(100, Math.round(contextPercent))}% context` : undefined;
  const shownMode = modePending ?? mode;
  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      style={styles.controls}
      contentContainerStyle={styles.controlsContent}
    >
      {shownMode || onMode ? (
        <ControlChip
          label={shownMode ? modeLabel(shownMode) : "Mode"}
          pending={Boolean(modePending)}
          onPress={onMode}
          disabled={disabled}
          accessibilityLabel={
            modePending
              ? `Switching mode to ${modeLabel(modePending)}`
              : `${shownMode ? `${modeLabel(shownMode)} mode` : "Mode"}${onMode ? ". Switch mode" : ""}`
          }
        />
      ) : null}
      {context ? (
        <View style={[styles.control, styles.controlQuiet]} accessible accessibilityRole="text" accessibilityLabel={context}>
          <Text style={[styles.controlText, styles.controlTextQuiet]}>{context}</Text>
        </View>
      ) : null}
    </ScrollView>
  );
}

/**
 * The model, at the foot of the text box beside send.
 *
 * Quiet text with a chevron rather than a filled chip, as in Claude's and
 * ChatGPT's apps: it is read far more often than it is changed, and send is
 * the one thing in the row that should look pressable first. Without a list
 * to choose from it is only the name.
 */
function ModelButton({
  model,
  pending,
  onPress,
  disabled,
}: {
  model: string;
  pending: boolean;
  onPress?: () => void;
  disabled?: boolean;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const body = (
    <>
      <Text style={[styles.modelText, pending && styles.switchPending]} numberOfLines={1}>
        {pending ? `${model}…` : model}
      </Text>
      {onPress ? <Feather name="chevron-down" size={13} color={color.muted} /> : null}
    </>
  );
  if (!onPress) {
    return (
      <View style={styles.modelButton} accessible accessibilityRole="text" accessibilityLabel={`Model ${model}`}>
        {body}
      </View>
    );
  }
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      hitSlop={6}
      style={({ pressed }) => [styles.modelButton, pressed && styles.modelButtonPressed]}
      accessibilityRole="button"
      accessibilityLabel={pending ? `Switching model to ${model}` : `Model ${model}. Switch model`}
      accessibilityState={{ disabled, busy: pending }}
    >
      {body}
    </Pressable>
  );
}

function ControlChip({
  label,
  pending,
  onPress,
  disabled,
  accessibilityLabel,
}: {
  label: string;
  pending: boolean;
  onPress?: () => void;
  disabled?: boolean;
  accessibilityLabel: string;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const body = (
    <>
      <Text style={[styles.controlText, pending && styles.switchPending]} numberOfLines={1}>
        {pending ? `${label}…` : label}
      </Text>
      {onPress ? <Feather name="chevron-down" size={12} color={color.muted} /> : null}
    </>
  );
  if (!onPress) {
    return (
      <View style={styles.control} accessible accessibilityRole="text" accessibilityLabel={accessibilityLabel}>
        {body}
      </View>
    );
  }
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      hitSlop={{ top: 8, bottom: 8 }}
      style={({ pressed }) => [styles.control, pressed && styles.switchPressed]}
      accessibilityRole="button"
      accessibilityLabel={accessibilityLabel}
      accessibilityState={{ disabled, busy: pending }}
    >
      {body}
    </Pressable>
  );
}

/**
 * Confirms a model switch the CLI also saves as its default. The sentence
 * names the agent, because "the default" means nothing without saying whose.
 */
function SwitchConfirmNote({
  warning,
  value,
  disabled,
  onCancel,
  onConfirm,
}: {
  warning: string;
  value: string;
  disabled: boolean;
  onCancel(): void;
  onConfirm(): void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={[styles.note, styles.switchConfirm]} accessibilityLiveRegion="polite">
      <View style={styles.switchConfirmTop}>
        <View style={styles.noteIcon}>
          <Feather name="alert-circle" size={13} color={color.needsYouText} />
        </View>
        <Text style={styles.noteText}>{warning}</Text>
      </View>
      <View style={styles.switchConfirmButtons}>
        <MotionPressable
          onPress={onCancel}
          style={[styles.switchButton, styles.switchButtonSecondary]}
          accessibilityRole="button"
        >
          <Text style={styles.switchButtonLabel}>Cancel</Text>
        </MotionPressable>
        <MotionPressable
          onPress={onConfirm}
          disabled={disabled}
          style={[styles.switchButton, styles.switchButtonPrimary, disabled && styles.switchButtonDisabled]}
          accessibilityRole="button"
          accessibilityLabel={`Switch to ${value}, and make it the default`}
          accessibilityState={{ disabled }}
        >
          <Text style={[styles.switchButtonLabel, styles.switchButtonLabelPrimary]} numberOfLines={1}>
            Switch
          </Text>
        </MotionPressable>
      </View>
    </View>
  );
}

function SwitchErrorNote({ error, onDismiss }: { error: string; onDismiss(): void }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={[styles.note, styles.interruptFailed]} accessibilityRole="alert" accessibilityLiveRegion="polite">
      <View style={styles.noteIcon}>
        <Feather name="alert-circle" size={13} color={color.errorText} />
      </View>
      <Text style={[styles.noteText, { color: color.errorText }]}>{error}</Text>
      <Pressable onPress={onDismiss} hitSlop={10} accessibilityRole="button" accessibilityLabel="Dismiss">
        <Feather name="x-circle" size={16} color={color.faint} />
      </Pressable>
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
function DeliveryNote({
  kind,
  inject,
  state,
  resuming,
  resumeError,
}: {
  kind: string;
  inject: string;
  state: string;
  resuming: boolean;
  resumeError: string;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  if (inject === "tmux" || inject === "api") return null;

  let text: string;
  if (resuming) {
    // Reopening replaces the old advice entirely. Telling someone to run
    // `am claude` on a Mac they are not sitting at was never useful, and it
    // is now describing something already happening.
    text = "Reopening this session on your Mac…";
  } else if (resumeError) {
    text = resumeError;
  } else if (inject === "hook") {
    text =
      state === "busy"
        ? "Messages wait until this turn ends — it can't be interrupted."
        : "Messages are handed over when this agent next finishes a turn.";
  } else if (kind === "cursor") {
    text =
      "Cursor IDE chats are read-only here. Continue in the IDE on your Mac.";
  } else if (kind === "cursor-cli") {
    text =
      "Start Cursor Agent CLI with am cursor to send this chat messages remotely.";
  } else if (kind === "claude" || kind === "codex" || kind === "kiro" || kind === "antigravity") {
    text = `Start this session with am ${kind} to send it messages.`;
  } else {
    text =
      "This session is read-only here. Continue in its agent app on your Mac.";
  }

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
  onCopied,
}: {
  message: Message;
  cwd: string;
  fresh: boolean;
  onCopied(): void;
}) {
  const styles = useStyles(makeStyles);
  if (!showsInFeed(message)) return null;
  if (message.role === "tool" && message.tool) {
    return (
      <Appear enabled={fresh}>
        <ToolRow message={message} cwd={cwd} standalone />
      </Appear>
    );
  }

  if (message.role === "user") {
    if (isCompactionSummary(message.text ?? "")) {
      return <FoldedSummary text={message.text ?? ""} fresh={fresh} />;
    }
    return (
      <SentMessage
        sessionId={message.sessionId}
        text={message.text ?? ""}
        fresh={fresh}
        onCopied={onCopied}
      />
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

/**
 * How long a message has to be held before its menu opens. Long enough that a
 * scroll which starts on a bubble never opens it; short enough not to feel
 * like waiting.
 */
const HOLD_MS = 350;

/**
 * A message that can be held to lift it out of the conversation.
 *
 * Measures the message where it sits, so the menu can lift it from exactly
 * there. A keyboard that is up is put away first and the message measured
 * once it has gone: measured mid-slide, the lifted copy would land where the
 * message was a moment earlier rather than where it ends up.
 */
function useHeldMessage() {
  const ref = useRef<View>(null);
  const [anchor, setAnchor] = useState<Frame | null>(null);

  const open = useCallback(() => {
    const measure = () =>
      ref.current?.measureInWindow((x, y, width, height) => {
        if (!width || !height) return;
        // A firmer tap than a button press: something came up out of the
        // page, which is what this weight of haptic means elsewhere on iOS.
        void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium).catch(() => {});
        setAnchor({ x, y, width, height });
      });
    if (Keyboard.isVisible()) {
      const hidden = Keyboard.addListener("keyboardDidHide", () => {
        hidden.remove();
        measure();
      });
      Keyboard.dismiss();
    } else {
      measure();
    }
  }, []);
  const close = useCallback(() => setAnchor(null), []);
  return { ref, anchor, open, close };
}

/** Copy a message, and let the screen say so — a clipboard write is silent. */
function copyMessage(text: string, onCopied: () => void) {
  if (!text) return;
  void Clipboard.setStringAsync(text)
    .then(() => {
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success).catch(() => {});
      onCopied();
    })
    .catch(() => {});
}

/** What can be done with a message you sent. */
function sentMessageActions(text: string, onCopied: () => void): MessageMenuItem[] {
  return [
    { key: "copy", label: "Copy", icon: "copy", onPress: () => copyMessage(text, onCopied) },
    {
      // For when the prompt is wanted somewhere other than the clipboard:
      // a note, a different agent, a message to someone.
      key: "share",
      label: "Share…",
      icon: "share",
      onPress: () => void Share.share({ message: text }).catch(() => {}),
    },
  ];
}

/** The summary Claude Code writes when it compacts a conversation, which
 *  arrives as a message from the user. */
function isCompactionSummary(text: string): boolean {
  return text.startsWith("This session is being continued from a previous conversation");
}

/** Claude's summary of a compacted conversation: one quiet line, opened to
 *  read it. It is the agent's note to itself, not something the user said. */
function FoldedSummary({ text, fresh }: { text: string; fresh: boolean }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [open, setOpen] = useState(false);
  return (
    <Appear enabled={fresh}>
      <Pressable
        onPress={() => setOpen((value) => !value)}
        style={styles.summaryHeader}
        accessibilityRole="button"
        accessibilityState={{ expanded: open }}
      >
        <Feather name="align-left" size={13} color={color.muted} />
        <Text style={styles.summaryLabel}>Summary of the earlier conversation</Text>
        <Feather name={open ? "chevron-down" : "chevron-right"} size={13} color={color.faint} />
      </Pressable>
      {open ? <Text style={styles.summaryText}>{text}</Text> : null}
    </Appear>
  );
}

function SentMessage({
  sessionId,
  text: message,
  fresh,
  onCopied,
}: {
  sessionId: string;
  text: string;
  fresh: boolean;
  onCopied(): void;
}) {
  const styles = useStyles(makeStyles);
  const held = useHeldMessage();
  // Images sent with it show as pictures, not as the paths the agent got.
  const { images, text } = useMemo(() => splitSentMessage(message), [message]);
  return (
    <Appear enabled={fresh}>
      {images.length > 0 && <SentImages sessionId={sessionId} paths={images} />}
      {/* The bubble itself is the target, so holding the space beside it does
          nothing. A plain Pressable rather than MotionPressable: a tap does
          nothing here, and shrinking on one would promise an action that is
          not there. */}
      {text.length > 0 && (
      <Pressable
        ref={held.ref}
        onLongPress={held.open}
        delayLongPress={HOLD_MS}
        // Hidden while its lifted copy is up, or the blur shows a ghost of it.
        style={[styles.userRow, held.anchor && styles.heldSource]}
        accessibilityRole="text"
        accessibilityHint="Touch and hold for options."
        accessibilityActions={[{ name: "copy", label: "Copy" }]}
        onAccessibilityAction={(event) =>
          event.nativeEvent.actionName === "copy" && copyMessage(text, onCopied)
        }
      >
        <Text style={styles.userText}>{text}</Text>
      </Pressable>
      )}
      <MessageMenu
        anchor={held.anchor}
        items={sentMessageActions(text, onCopied)}
        onClose={held.close}
        bubbleStyle={styles.userRow}
      >
        <Text style={styles.userText}>{text}</Text>
      </MessageMenu>
    </Appear>
  );
}

/**
 * What a message an agent is holding says under it. Claude and Antigravity
 * hold one until the turn ends; Codex and OpenCode only until the step under
 * way does, which the words say, so a wait of seconds isn't read as minutes.
 */
function pendingLabel(kind: string, state: string): string | undefined {
  const name = { claude: "Claude", antigravity: "Antigravity", codex: "Codex", opencode: "OpenCode" }[kind];
  if (!name) return undefined;
  if (state === "waiting_input") return `Pending · ${name} reads it once you answer`;
  if (state !== "busy") return undefined;
  return kind === "codex" || kind === "opencode"
    ? `Pending · ${name} reads it after this step`
    : `Pending · ${name} is working`;
}

function PendingRow({
  pending,
  waitingLabel,
  onSendNow,
  sendingNow,
  onDismiss,
  onCopied,
}: {
  pending: PendingSend;
  /** Set while the agent is holding this message until its step ends: said
   *  under it in place of "Sent", and nothing to dismiss until it is read. */
  waitingLabel?: string;
  /** Sends everything the agent is holding now (Ctrl-X Ctrl-S at the Mac).
   *  Absent where the agent can't be told to. */
  onSendNow?: () => void;
  sendingNow?: boolean;
  onDismiss: (clientId: string) => void;
  onCopied(): void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const held = useHeldMessage();
  const failed = pending.status === "failed";
  const queued = pending.status === "queued";
  const delivered = pending.status === "delivered";
  const waiting = delivered && waitingLabel !== undefined;
  const dismissable = failed || (delivered && !waiting);

  const actions = sentMessageActions(pending.text, onCopied);
  if (dismissable) {
    actions.push({
      key: "dismiss",
      label: "Dismiss",
      icon: "x",
      onPress: () => onDismiss(pending.clientId),
    });
  }

  const status =
    pending.status === "sending"
      ? "Sending…"
      : queued
        ? "Queued — arrives when this turn ends"
        : failed
          ? `Didn't send${pending.error ? `: ${pending.error}` : ""} · tap to dismiss`
          : waiting
            ? waitingLabel
            : delivered
              ? "Sent · syncing…"
              : "";
  const sendNow = waiting && onSendNow;
  const body = (
    <>
      <Text style={[styles.userText, sendNow && styles.pendingFaded]}>{pending.text}</Text>
      {sendNow ? (
        <View style={styles.pendingFooter}>
          <Text style={[styles.pendingStatus, styles.pendingFooterLabel, styles.pendingFaded]} numberOfLines={2}>
            {status}
          </Text>
          {/* Plain words rather than a button: the bubble is the thing being
              read, and this is a quiet way to hurry it. It sends the whole
              queue, as the keys at the Mac do, not just this one. */}
          <Pressable
            onPress={onSendNow}
            disabled={sendingNow}
            hitSlop={10}
            style={({ pressed }) => [styles.sendNow, (pressed || sendingNow) && styles.sendNowPressed]}
            accessibilityRole="button"
            accessibilityLabel="Send now"
            accessibilityHint="Sends every waiting message now instead of when this step ends"
            accessibilityState={{ disabled: sendingNow, busy: sendingNow }}
          >
            <Text style={styles.sendNowText}>{sendingNow ? "Sending…" : "Send now"}</Text>
          </Pressable>
        </View>
      ) : (
        <Text style={[styles.pendingStatus, failed && { color: color.errorText }]}>{status}</Text>
      )}
    </>
  );
  // With Send now in it the bubble keeps full strength and only its words
  // fade: faded, Send now would read as something that can't be pressed.
  const bubble = [styles.userRow, !sendNow && styles.pendingRow, failed && styles.pendingFailed];

  return (
    <>
      <Pressable
        ref={held.ref}
        // Held works in every state, tap only once it can dismiss: a message
        // that failed or is stuck sending is the one you most want to copy
        // and try again.
        onPress={() => dismissable && onDismiss(pending.clientId)}
        onLongPress={held.open}
        delayLongPress={HOLD_MS}
        style={({ pressed }) => [
          ...bubble,
          pressed && dismissable && styles.pendingPressed,
          held.anchor && styles.heldSource,
        ]}
        accessibilityRole={dismissable ? "button" : "text"}
        accessibilityHint={
          dismissable ? "Double tap to dismiss. Touch and hold for options." : "Touch and hold for options."
        }
        accessibilityActions={[{ name: "copy", label: "Copy" }]}
        onAccessibilityAction={(event) =>
          event.nativeEvent.actionName === "copy" && copyMessage(pending.text, onCopied)
        }
      >
        {body}
      </Pressable>
      <MessageMenu anchor={held.anchor} items={actions} onClose={held.close} bubbleStyle={bubble}>
        {body}
      </MessageMenu>
    </>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    keyboardAvoider: { flex: 1 },
    feed: { flex: 1 },

    // Top-aligned: the controls row makes the body three lines tall, and
    // centred buttons ended up beside the folder instead of the title.
    header: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: space.xs,
      paddingHorizontal: space.md,
      paddingTop: space.xs,
      paddingBottom: space.sm,
    },
    // No circle and no border: the chevron is unmistakable on its own, and the
    // chrome around it was pushing the title a quarter of the way across the
    // row before it started.
    backButton: {
      width: 30,
      // The logo's height, so the chevron sits level with its middle.
      height: 38,
      alignItems: "center",
      justifyContent: "center",
    },
    iconButton: {
      width: 40,
      height: 40,
      // Centred on the logo, like the back button: a point taller than it,
      // so a point above it.
      marginTop: -1,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    // Spans the title and the line under it, as an avatar does beside a
    // name, so it sits level with both rather than with one.
    agentTile: {
      width: 38,
      height: 38,
      marginRight: space.xs,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
    },
    headerBody: { flex: 1 },
    // Full width rather than indented under the title. It is a banner about
    // the session, not another line of the header's text, and inset it read as
    // a stray tab stop.
    workspaceBar: { paddingHorizontal: space.md, paddingBottom: space.sm },
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
    // The same badge as the server count, in the colour that asks for a tap:
    // a review is the agent waiting on you.
    reviewBadge: {
      position: "absolute",
      top: 0,
      right: 0,
      minWidth: 15,
      height: 15,
      borderRadius: 8,
      paddingHorizontal: 3,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.needsYou,
    },
    reviewBadgeText: {
      fontFamily: font.sansMedium,
      fontSize: 9.5,
      lineHeight: 12,
      color: c.onAccent,
    },
    title: {
      fontFamily: font.sansMedium,
      fontSize: 16,
      color: c.text,
    },
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
    // Gives way with the path when the row runs out, and inside it the mode
    // name gives way first: the percentage is the part worth keeping whole.
    // Starts under the logo, past the screen edge and the back button, so
    // the logo, the title and the chips read as one block.
    controlsBar: { paddingLeft: space.md + 30 + space.xs, paddingBottom: space.sm, marginTop: -2 },
    controls: { flexGrow: 0 },
    controlsContent: { flexDirection: "row", alignItems: "center", gap: 6, paddingRight: space.sm },
    control: {
      flexDirection: "row",
      alignItems: "center",
      gap: 3,
      height: 26,
      paddingHorizontal: 10,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    controlText: { fontFamily: font.sansMedium, fontSize: 12.5, color: c.text },
    // A fact, not a control: outlined rather than filled, and quieter.
    controlQuiet: { backgroundColor: "transparent", borderWidth: 1, borderColor: c.line },
    controlTextQuiet: { color: c.muted },
    switchPressed: { opacity: 0.6 },
    // On its way: the value being switched to, in the working colour.
    switchPending: { color: c.workingText },
    switchConfirm: { flexDirection: "column", alignItems: "stretch", gap: space.sm },
    switchConfirmTop: { flexDirection: "row", alignItems: "center", gap: 10 },
    switchConfirmButtons: { flexDirection: "row", justifyContent: "flex-end", gap: space.sm },
    switchButton: {
      minHeight: 36,
      minWidth: 84,
      paddingHorizontal: space.lg,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
    },
    switchButtonPrimary: { backgroundColor: c.inverse },
    switchButtonSecondary: { backgroundColor: c.surface, borderWidth: 1, borderColor: c.fillStrong },
    switchButtonDisabled: { opacity: 0.4 },
    switchButtonLabel: { fontFamily: font.sansBold, fontSize: size.caption, color: c.text },
    switchButtonLabelPrimary: { color: c.onInverse },
    // No box around it. This was a 28pt touch target from when the dot was a
    // control in the top row; on a line of text it centred a six point dot in
    // twenty-eight, which pushed it clear of the title's left edge. It is a
    // fact now, not a control, so it occupies exactly itself and starts where
    // the title starts.
    stateDot: { marginRight: 6 },

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
      fontSize: size.reading,
      color: c.text,
      lineHeight: 23,
    },
    summaryHeader: { flexDirection: "row", alignItems: "center", gap: 7, paddingVertical: 4 },
    summaryLabel: { fontFamily: font.sans, fontSize: size.caption, color: c.muted },
    summaryText: {
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 19,
      color: c.muted,
      marginTop: 6,
    },
    // The message in the list while its lifted copy is showing.
    heldSource: { opacity: 0 },
    pendingPressed: { opacity: 0.85 },

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
    pendingFaded: { opacity: 0.7 },
    pendingFooter: { flexDirection: "row", alignItems: "center", gap: space.sm, marginTop: space.xs },
    pendingFooterLabel: { flexShrink: 1, marginTop: 0 },
    sendNow: { justifyContent: "center" },
    sendNowPressed: { opacity: 0.5 },
    sendNowText: { fontFamily: font.sansMedium, fontSize: size.label, color: c.workingText },
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
    // the field instead of sitting next to it. The text has the full width
    // and the controls a row of their own under it, as in Claude's and
    // ChatGPT's apps: beside the text, the model's name would take the room
    // a sentence needs.
    field: {
      backgroundColor: c.surface,
      borderRadius: radius.xxl,
      borderWidth: 1,
      borderColor: c.line,
      paddingLeft: 6,
      paddingRight: 6,
      paddingTop: 4,
      paddingBottom: 6,
      shadowColor: c.shadow,
      shadowOpacity: 0.06,
      shadowRadius: 16,
      shadowOffset: { width: 0, height: 6 },
      elevation: 2,
    },
    fieldFocused: { borderColor: c.working },
    fieldDisabled: { opacity: 0.55 },
    // Inset so the text starts where the + under it does.
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
    // Thumbnails square off the pill: a row of 64pt pictures inside a fully
    // rounded container loses its corners to the radius.
    fieldWithImages: {
      borderRadius: radius.lg,
      paddingTop: space.sm,
      paddingLeft: space.sm,
    },
    toolbar: { flexDirection: "row", alignItems: "center", gap: space.xs },
    toolbarGap: { flex: 1 },
    modelButton: {
      flexDirection: "row",
      alignItems: "center",
      gap: 3,
      height: 32,
      maxWidth: 200,
      paddingHorizontal: 10,
      borderRadius: radius.pill,
    },
    modelButtonPressed: { backgroundColor: c.fill },
    modelText: { flexShrink: 1, fontFamily: font.sansMedium, fontSize: 13, color: c.muted },
    // A wrapper so the long press has something to land on: the gesture cannot
    // be attached to the TextInput itself without swallowing taps that should
    // place the caret.
    inputWrap: {},
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
