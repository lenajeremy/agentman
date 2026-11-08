import Feather from "@expo/vector-icons/Feather";
import { memo, useEffect, useMemo, useRef, useState, type ComponentProps, type ComponentRef, type ReactNode } from "react";
import { ActivityIndicator, Platform, Pressable, StyleSheet, Text, TextInput, useWindowDimensions, View } from "react-native";

import type { PendingAgentAction } from "../lib/action-state";
import { useStyles, useTheme } from "../lib/appearance";
import {
  boardDigest,
  boardFolderLabel,
  boardTextIsLong,
  plainInline,
  promptRepeatsDetail,
  sameWords,
  type BoardColumn,
  type BoardDigest,
  type BoardProgress,
  type BoardStep,
} from "../lib/board";
import { cardQuestion, missionProgress } from "../lib/manager-feed";
import type { Brief, ManagerCard, Message, Mission, QuestionAnswer, Session, Verification } from "../lib/protocol";
import { ago, font, layout, Palette, radius, scaled, size, space } from "../lib/theme";
import { AgentIcon } from "./AgentIcon";
import { BrandMark } from "./BrandMark";
import { FolderInitials, FolderPicture, useFolderIcon } from "./FolderBadge";
import { Loader } from "./Loader";
import { MotionPressable } from "./MotionPressable";
import { QuestionCard } from "./QuestionCard";

/** What a card asks of the screen. One object for the Board's whole life, so
 *  a card redraws only when its own agent changes. */
export interface BoardActions {
  open(sessionId: string): void;
  openChanges(sessionId: string): void;
  openServers(sessionId: string): void;
  openDocuments(sessionId: string): void;
  answer(sessionId: string, answer: QuestionAnswer): void;
  replyToCard(card: ManagerCard, answer: QuestionAnswer & { text?: string }): Promise<string | null>;
  dismissCard(card: ManagerCard): Promise<string | null>;
  send(sessionId: string, text: string): void;
  /** Follows an agent's messages while its card shows. Returns the way to stop. */
  follow(sessionId: string): () => void;
  changedFiles(sessionId: string): Promise<number>;
  menu(session: Session, anchor: ViewRef | null): void;
}

const NO_MESSAGES: Message[] = [];

export type ViewRef = ComponentRef<typeof View>;

interface Tint {
  backgroundColor: string;
  borderColor: string;
}

interface Props {
  session: Session;
  column: BoardColumn;
  messages: Message[] | undefined;
  brief?: Brief;
  check?: Verification;
  /** The team this agent is part of, while it runs. */
  goal?: string;
  /** Agentman's card waiting on this agent, if any. */
  card?: ManagerCard;
  answerAction?: PendingAgentAction;
  /** A message sent from here that the Mac could not deliver. */
  sendFailed?: boolean;
  online: boolean;
  focused: boolean;
  tint?: Tint;
  actions: BoardActions;
}

/**
 * One agent on the Board: where it is, what it is called, and what it wants,
 * answerable or followable on the spot. Tapping anywhere else opens its
 * conversation. Quiet on purpose: one size of type, colour only on status.
 */
export const BoardCard = memo(function BoardCard({
  session,
  column,
  messages,
  brief,
  check,
  goal,
  card,
  answerAction,
  sendFailed,
  online,
  focused,
  tint,
  actions,
}: Props) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const digest = useMemo(() => boardDigest(messages ?? NO_MESSAGES), [messages]);
  const menuAnchor = useRef<ViewRef>(null);
  const open = () => actions.open(session.id);

  useEffect(() => {
    if (!focused) return;
    return actions.follow(session.id);
  }, [focused, session.id, actions]);

  const when = column === "working" ? digest.turnStarted : session.lastActivityAt;
  const stateLabel = column === "needsYou" ? "needs you" : column === "working" ? "working" : "idle";

  return (
    // Not one big pressable to a screen reader: the head opens the agent,
    // and each answer stays its own button.
    <MotionPressable onPress={open} style={[styles.card, tint]} pressedScale={0.99} accessible={false}>
      <View style={styles.head}>
        <Pressable
          onPress={open}
          style={styles.headMain}
          accessibilityRole="button"
          accessibilityLabel={`${session.name}, ${stateLabel}. Open conversation`}
        >
          <BoardFolderMark session={session} />
          <Text style={styles.folder} numberOfLines={1}>{boardFolderLabel(session.cwd)}</Text>
          <View style={styles.headGap} />
          {when ? <Text style={styles.time}>{ago(when)}</Text> : null}
          <AgentIcon kind={session.kind} size={15} />
        </Pressable>
        <View ref={menuAnchor} collapsable={false} style={styles.menuAnchor}>
          <MotionPressable
            onPress={() => actions.menu(session, menuAnchor.current)}
            hitSlop={14}
            style={styles.menuButton}
            accessibilityRole="button"
            accessibilityLabel={`More for ${session.name}`}
          >
            <Feather name="more-horizontal" size={17} color={color.faint} />
          </MotionPressable>
        </View>
      </View>

      {goal ? <Text style={styles.goal} numberOfLines={1}>{goal}</Text> : null}
      <Text style={styles.title} numberOfLines={2}>{session.name}</Text>

      {column === "needsYou" ? (
        <NeedsYouBody
          session={session}
          digest={digest}
          brief={brief}
          card={card}
          answerAction={answerAction}
          online={online}
          actions={actions}
          onOpen={open}
        />
      ) : column === "working" ? (
        <WorkingBody digest={digest} brief={brief} />
      ) : (
        <IdleBody session={session} digest={digest} brief={brief} check={check} focused={focused} actions={actions} />
      )}

      {sendFailed ? <Text style={styles.problem}>Your message did not reach the Mac. Open the agent to try again.</Text> : null}
    </MotionPressable>
  );
});

function BoardFolderMark({ session }: { session: Session }) {
  const picture = useFolderIcon(session.id, session.cwd, session.folderIcon);
  return picture ? <FolderPicture uri={picture} size={16} /> : <FolderInitials cwd={session.cwd} size={16} />;
}

/** The web's clicks bubble past responders: kept from reaching the card. */
const STOP_CLICK = Platform.OS === "web"
  ? { onClick: (event: { stopPropagation(): void }) => event.stopPropagation() }
  : {};

/** Stops a tap between the buttons, or in a reply box, from opening the agent. */
function ControlArea({ children }: { children: ReactNode }) {
  return <View onStartShouldSetResponder={() => true} {...STOP_CLICK}>{children}</View>;
}

function NeedsYouBody({
  session,
  digest,
  brief,
  card,
  answerAction,
  online,
  actions,
  onOpen,
}: {
  session: Session;
  digest: BoardDigest;
  brief?: Brief;
  card?: ManagerCard;
  answerAction?: PendingAgentAction;
  online: boolean;
  actions: BoardActions;
  onOpen(): void;
}) {
  const question = session.question;
  const summary = brief?.summary || undefined;
  const need = brief?.needsYou || undefined;

  if (question) {
    const title = question.title || "Needs your answer";
    return (
      <>
        {summary ? <BoardText text={summary} lines={3} /> : null}
        {need && !sameWords(need, title) ? <BoardText text={need} lines={3} /> : null}
        <StatusLine text={title} lines={3} />
        {question.detail ? <CommandBox text={question.detail} /> : null}
        {question.prompt && !promptRepeatsDetail(question) ? <BoardText text={question.prompt} lines={4} /> : null}
        <ControlArea>
          <QuestionCard
            question={question}
            bare
            suggestedKey={card?.questionId === question.id ? card.suggested : undefined}
            onAnswer={(answer) => actions.answer(session.id, answer)}
            onOpen={onOpen}
            disabled={!online}
            submissionStatus={answerAction?.status}
            submissionError={answerAction?.error}
          />
        </ControlArea>
      </>
    );
  }

  if (card) {
    return (
      <>
        {summary ? <BoardText text={summary} lines={3} /> : null}
        {need && !sameWords(need, card.title) ? <BoardText text={need} lines={3} /> : null}
        <StatusLine text={card.title} lines={3} />
        {card.detail ? <CommandBox text={card.detail} /> : null}
        <AgentmanCardAnswer card={card} online={online} actions={actions} />
      </>
    );
  }

  // Its question is at the end of what it said, and the brief says what it's
  // all about.
  const asked = need ?? digest.asked ?? "Waiting for your answer";
  return (
    <>
      <StatusLine text={asked} lines={5} />
      {summary && !sameWords(summary, asked) ? <BoardText text={summary} lines={3} /> : null}
      {session.inject !== "none" ? (
        <ControlArea>
          <BoardReply
            placeholder={`Reply to ${session.name}`}
            disabled={!online}
            onSend={async (text) => {
              actions.send(session.id, text);
              return null;
            }}
          />
        </ControlArea>
      ) : null}
    </>
  );
}

/** Answering a card Agentman put up: its choices, or a line back. */
function AgentmanCardAnswer({
  card,
  online,
  actions,
}: {
  card: ManagerCard;
  online: boolean;
  actions: BoardActions;
}) {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | undefined>();
  const question = cardQuestion(card);
  const act = async (work: Promise<string | null>) => {
    setBusy(true);
    const result = await work;
    setProblem(result ?? undefined);
    setBusy(false);
    return result;
  };
  return (
    <ControlArea>
      {question ? (
        <QuestionCard
          question={question}
          bare
          suggestedKey={card.suggested}
          onAnswer={(answer) => void act(actions.replyToCard(card, answer))}
          disabled={!online || busy}
          submissionStatus={busy ? "sending" : problem ? "failed" : undefined}
          submissionError={problem}
          customPlaceholder="Or reply to Agentman…"
        />
      ) : (
        <BoardReply
          placeholder="Reply to Agentman"
          disabled={!online || busy}
          onSend={(text) => act(actions.replyToCard(card, { text }))}
          problem={problem}
        />
      )}
    </ControlArea>
  );
}

function WorkingBody({ digest, brief }: { digest: BoardDigest; brief?: Brief }) {
  const styles = useStyles(makeStyles);
  const summary = brief?.summary || brief?.goal;
  // Not when a step below already says the same.
  const now = brief?.now && brief.now !== brief.summary && !digest.steps.some((step) => sameWords(step.text, brief.now!))
    ? brief.now
    : undefined;
  return (
    <>
      {summary ? <BoardText text={summary} lines={3} /> : null}
      {now ? <BoardText text={now} lines={2} /> : null}
      {digest.steps.length > 0 ? (
        <View style={styles.steps}>
          {digest.steps.map((step) => <BoardStepRow key={step.id} step={step} />)}
        </View>
      ) : null}
      {digest.progress ? <BoardProgressRow progress={digest.progress} /> : null}
      {digest.command ? (
        <View style={styles.commandWell}>
          <Text style={styles.command} numberOfLines={1} ellipsizeMode="middle">{digest.command}</Text>
        </View>
      ) : null}
    </>
  );
}

function IdleBody({
  session,
  digest,
  brief,
  check,
  focused,
  actions,
}: {
  session: Session;
  digest: BoardDigest;
  brief?: Brief;
  check?: Verification;
  focused: boolean;
  actions: BoardActions;
}) {
  const styles = useStyles(makeStyles);
  const [changes, setChanges] = useState(0);

  // What is left to commit, read again only when it did something: a card
  // on screen is no reason to keep asking the Mac.
  useEffect(() => {
    if (!focused) return;
    let current = true;
    void actions.changedFiles(session.id).then((count) => {
      if (current) setChanges(count);
    }).catch(() => {});
    return () => {
      current = false;
    };
  }, [focused, session.id, session.lastActivityAt, actions]);

  const said = brief?.summary || digest.said;
  const documents = session.artifactsToReview ?? 0;
  const servers = (session.servers ?? []).slice(0, 2);
  return (
    <>
      {said ? <BoardText text={said} lines={4} /> : null}
      {check ? <BoardCheckRow check={check} /> : null}
      {changes > 0 || documents > 0 || servers.length > 0 ? (
        <ControlArea>
          <View style={styles.links}>
            {changes > 0 ? (
              <BoardLink
                icon="file-text"
                label={changes === 1 ? "Review 1 file" : `Review ${changes} files`}
                onPress={() => actions.openChanges(session.id)}
              />
            ) : null}
            {documents > 0 ? (
              <BoardLink
                icon="book-open"
                label={documents === 1 ? "Review document" : `Review ${documents} documents`}
                onPress={() => actions.openDocuments(session.id)}
              />
            ) : null}
            {servers.map((server) => (
              <BoardLink
                key={server.port}
                icon="globe"
                label={`localhost:${server.port}`}
                onPress={() => actions.openServers(session.id)}
              />
            ))}
          </View>
        </ControlArea>
      ) : null}
    </>
  );
}

/** A card's one size of running text, cut to a few lines with the rest a tap
 *  away. */
function BoardText({ text, lines }: { text: string; lines: number }) {
  const styles = useStyles(makeStyles);
  const [open, setOpen] = useState(false);
  const { width } = useWindowDimensions();
  const plain = useMemo(() => plainInline(text), [text]);
  const long = boardTextIsLong(plain, lines, Math.min(width, layout.contentMax));
  return (
    <View>
      <Text style={styles.text} numberOfLines={open ? undefined : lines}>{plain}</Text>
      {long ? (
        <MotionPressable
          onPress={() => setOpen((value) => !value)}
          hitSlop={10}
          style={styles.more}
          accessibilityRole="button"
          accessibilityLabel={open ? "Show less" : "Show more"}
        >
          <Text style={styles.moreText}>{open ? "Less" : "More"}</Text>
        </MotionPressable>
      ) : null}
    </View>
  );
}

/** The one line that says what it wants, behind a small orange dot. */
function StatusLine({ text, lines }: { text: string; lines: number }) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.statusLine}>
      <View style={styles.statusDot} />
      <View style={styles.statusText}><BoardText text={text} lines={lines} /></View>
    </View>
  );
}

function CommandBox({ text }: { text: string }) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.commandWell}>
      {/* Never cut: approving a command you cannot read is worse than not
          approving it. */}
      <Text style={styles.command} selectable>{text}</Text>
    </View>
  );
}

/** One step of the turn an agent is on. */
function BoardStepRow({ step }: { step: BoardStep }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.step}>
      <View style={styles.stepIcon}>
        {step.state === "running" ? (
          <Loader size={10} />
        ) : (
          <Feather
            name={step.state === "failed" ? "x" : "check"}
            size={12}
            color={step.state === "failed" ? color.errorText : color.faint}
          />
        )}
      </View>
      <Text
        style={[styles.stepText, step.state === "running" && styles.stepRunning, step.state === "failed" && styles.stepFailed]}
        numberOfLines={1}
      >
        {plainInline(step.text)}
      </Text>
    </View>
  );
}

/** How far through its checklist an agent is. Only when it reports one. */
function BoardProgressRow({ progress }: { progress: BoardProgress }) {
  const styles = useStyles(makeStyles);
  const line = progress.now ? `${progress.done} of ${progress.total} · ${progress.now}` : `${progress.done} of ${progress.total}`;
  const fraction = `${Math.round((progress.done / Math.max(1, progress.total)) * 100)}%` as const;
  return (
    <View style={styles.progress} accessibilityLabel={line} accessible>
      <View style={styles.progressTrack}>
        <View style={[styles.progressFill, { width: fraction }]} />
      </View>
      <Text style={styles.progressText} numberOfLines={1}>{line}</Text>
    </View>
  );
}

function checkFallback(state: string): string {
  switch (state) {
    case "verified": return "Verified";
    case "passed": return "Tests pass";
    case "concerns": return "Something looks off";
    case "failed": return "Tests fail";
    default: return "Checking its work";
  }
}

/** What Agentman found checking the agent's finished work. */
function BoardCheckRow({ check }: { check: Verification }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const good = check.state === "verified" || check.state === "passed";
  const summary = plainInline(check.summary ?? checkFallback(check.state));
  return (
    <View style={styles.check} accessible accessibilityLabel={summary}>
      <View style={styles.checkHead}>
        {good ? (
          <Feather name="check-circle" size={14} color={color.ok} style={styles.checkIcon} />
        ) : check.state === "concerns" ? (
          <Feather name="alert-triangle" size={14} color={color.needsYou} style={styles.checkIcon} />
        ) : check.state === "failed" ? (
          <Feather name="x-circle" size={14} color={color.error} style={styles.checkIcon} />
        ) : (
          <View style={styles.checkLoader}><Loader size={11} /></View>
        )}
        <Text
          style={[
            styles.checkSummary,
            good && { color: color.text },
            check.state === "concerns" && { color: color.needsYouText },
            check.state === "failed" && { color: color.errorText },
          ]}
          numberOfLines={3}
        >
          {summary}
        </Text>
      </View>
      {(check.issues ?? []).map((issue) => (
        <Text key={issue} style={styles.checkIssue} numberOfLines={2}>{plainInline(issue)}</Text>
      ))}
      {check.sentBack ? <Text style={styles.checkNote}>Sent back for it to fix</Text> : null}
    </View>
  );
}

function BoardLink({
  icon,
  label,
  onPress,
}: {
  icon: ComponentProps<typeof Feather>["name"];
  label: string;
  onPress(): void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <MotionPressable onPress={onPress} hitSlop={5} style={styles.link} accessibilityRole="button" accessibilityLabel={label}>
      <Feather name={icon} size={13} color={color.muted} />
      <Text style={styles.linkText} numberOfLines={1}>{label}</Text>
    </MotionPressable>
  );
}

/** A line back, for an agent waiting with nothing to pick from. */
function BoardReply({
  placeholder,
  disabled,
  onSend,
  problem,
}: {
  placeholder: string;
  disabled: boolean;
  /** Returns the problem, if any; a line sent is cleared. */
  onSend(text: string): Promise<string | null>;
  problem?: string;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const typed = text.trim().length > 0;
  const send = async () => {
    if (!typed || busy || disabled) return;
    setBusy(true);
    const result = await onSend(text.trim());
    if (!result) setText("");
    setBusy(false);
  };
  return (
    <View style={styles.replyWrap}>
      <View style={styles.reply}>
        <TextInput
          style={styles.replyInput}
          value={text}
          onChangeText={setText}
          placeholder={placeholder}
          placeholderTextColor={color.faint}
          multiline
          editable={!disabled && !busy}
          accessibilityLabel={placeholder}
        />
        <MotionPressable
          onPress={() => void send()}
          disabled={!typed || busy || disabled}
          hitSlop={8}
          style={[styles.replySend, (!typed || disabled) && styles.replySendOff]}
          accessibilityRole="button"
          accessibilityLabel="Send"
        >
          {busy ? (
            <ActivityIndicator size="small" color={color.onAccent} />
          ) : (
            <Feather name="arrow-up" size={15} color={typed && !disabled ? color.onInverse : color.faint} />
          )}
        </MotionPressable>
      </View>
      {problem ? <Text style={styles.problem}>{problem}</Text> : null}
    </View>
  );
}

/** Something Agentman offers to do, answered on the spot. */
export const BoardOfferCard = memo(function BoardOfferCard({
  card,
  online,
  actions,
}: {
  card: ManagerCard;
  online: boolean;
  actions: BoardActions;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [busy, setBusy] = useState(false);
  return (
    <View style={styles.card}>
      <View style={styles.head}>
        <View style={styles.headMain} accessible accessibilityLabel="Agentman offers">
          <BrandMark size={16} ring={color.surface} />
          <Text style={styles.folder}>Agentman</Text>
        </View>
        <MotionPressable
          onPress={() => {
            setBusy(true);
            void actions.dismissCard(card).finally(() => setBusy(false));
          }}
          disabled={busy}
          hitSlop={14}
          style={styles.menuButton}
          accessibilityRole="button"
          accessibilityLabel="Put this away"
        >
          <Feather name="x" size={16} color={color.faint} />
        </MotionPressable>
      </View>
      <BoardText text={card.title} lines={4} />
      <AgentmanCardAnswer card={card} online={online} actions={actions} />
    </View>
  );
});

/** A team at work: its goal, and how far along it is. */
export function BoardMissionRow({ mission }: { mission: Mission }) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.mission} accessible accessibilityLabel={`${mission.goal}. ${missionProgress(mission)}`}>
      <Text style={styles.missionGoal} numberOfLines={2}>{mission.goal}</Text>
      <Text style={styles.missionProgress}>{missionProgress(mission)}</Text>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    card: {
      gap: 8,
      padding: space.lg,
      borderRadius: radius.xl,
      borderWidth: 1,
      borderColor: c.line,
      backgroundColor: c.surface,
    },
    head: { flexDirection: "row", alignItems: "center", gap: space.sm },
    headMain: { flex: 1, flexDirection: "row", alignItems: "center", gap: 6, minHeight: 20 },
    headGap: { flex: 1, minWidth: space.xs },
    folder: { flexShrink: 1, ...font.sans, fontSize: size.label, color: c.muted },
    time: { ...font.sans, fontSize: size.label, color: c.muted, fontVariant: ["tabular-nums"] },
    menuAnchor: { marginLeft: -2 },
    menuButton: { alignItems: "center", justifyContent: "center", width: 22, height: 20 },
    goal: { ...font.sans, fontSize: size.label, color: c.workingText },
    title: { ...font.sansMedium, fontSize: size.caption, lineHeight: scaled(19), color: c.text },

    text: { ...font.sans, fontSize: size.caption, lineHeight: scaled(19), color: c.textSecondary },
    more: { alignSelf: "flex-start", paddingTop: 2 },
    moreText: { ...font.sansMedium, fontSize: size.label, color: c.muted },

    statusLine: { flexDirection: "row", gap: 7 },
    // On the line's middle, however tall the words are set.
    statusDot: { width: 6, height: 6, borderRadius: 3, marginTop: scaled(7), backgroundColor: c.needsYou },
    statusText: { flex: 1 },

    commandWell: { paddingHorizontal: space.md, paddingVertical: 8, borderRadius: 10, backgroundColor: c.fill },
    command: { ...font.mono, fontSize: scaled(12), lineHeight: scaled(17), color: c.textSecondary },

    steps: { gap: 5 },
    step: { flexDirection: "row", alignItems: "center", gap: 7 },
    stepIcon: { width: 14, height: 14, alignItems: "center", justifyContent: "center" },
    stepText: { flex: 1, ...font.sans, fontSize: size.caption, color: c.muted },
    stepRunning: { color: c.text },
    stepFailed: { color: c.errorText },

    progress: { gap: 6 },
    progressTrack: { height: 4, borderRadius: 2, overflow: "hidden", backgroundColor: c.fill },
    progressFill: { height: 4, borderRadius: 2, backgroundColor: c.working },
    progressText: { ...font.sans, fontSize: size.caption, color: c.muted },

    check: { gap: 4 },
    checkHead: { flexDirection: "row", gap: 7 },
    checkIcon: { marginTop: scaled(2) },
    checkLoader: { width: 14, height: scaled(18), alignItems: "center", justifyContent: "center" },
    checkSummary: { flex: 1, ...font.sans, fontSize: size.caption, lineHeight: scaled(19), color: c.muted },
    checkIssue: { paddingLeft: 21, ...font.sans, fontSize: size.caption, lineHeight: scaled(19), color: c.textSecondary },
    checkNote: { paddingLeft: 21, ...font.sans, fontSize: size.label, color: c.muted },

    links: { flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: 2 },
    link: {
      flexDirection: "row",
      alignItems: "center",
      gap: 5,
      height: 34,
      paddingHorizontal: 10,
      borderRadius: radius.sm,
      borderWidth: 1,
      borderColor: c.line,
    },
    linkText: { ...font.sans, fontSize: size.label, color: c.textSecondary },

    replyWrap: { gap: space.xs },
    reply: {
      flexDirection: "row",
      alignItems: "flex-end",
      gap: space.sm,
      minHeight: 44,
      paddingLeft: 14,
      paddingRight: 6,
      paddingVertical: 6,
      borderRadius: 22,
      backgroundColor: c.fill,
    },
    replyInput: { flex: 1, maxHeight: 110, paddingTop: 6, paddingBottom: 6, ...font.sans, fontSize: size.caption, color: c.text },
    replySend: { width: 32, height: 32, borderRadius: 16, alignItems: "center", justifyContent: "center", backgroundColor: c.inverse },
    replySendOff: { backgroundColor: c.fillStrong },
    problem: { ...font.sans, fontSize: size.label, lineHeight: scaled(16), color: c.errorText },

    mission: { gap: 2, paddingHorizontal: space.xs, paddingBottom: space.xs },
    missionGoal: { ...font.sansMedium, fontSize: size.caption, lineHeight: scaled(18), color: c.text },
    missionProgress: { ...font.sans, fontSize: size.label, color: c.muted, fontVariant: ["tabular-nums"] },
  });
