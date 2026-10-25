import Feather from "@expo/vector-icons/Feather";
import { useRouter } from "expo-router";
import { useState } from "react";
import { ActivityIndicator, StyleSheet, Text, TextInput, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { useManager } from "../lib/manager";
import { cardIsOpen, cardQuestion } from "../lib/manager-feed";
import type { ManagerCard, Note, QuestionAnswer } from "../lib/protocol";
import { useStore } from "../lib/store";
import { font, Palette, radius, size, space } from "../lib/theme";
import { AgentIcon } from "./AgentIcon";
import { MotionPressable } from "./MotionPressable";
import { QuestionCard } from "./QuestionCard";
import { SwipeToDismiss } from "./SwipeToDismiss";

/**
 * A decision Agentman put in front of the user, answerable where it stands:
 * an agent's question with its own options, its need answered in words, a key
 * it asked for, or a note Agentman suggests keeping. Once answered, it folds
 * to what was chosen.
 */
export function ManagerCardView({ card }: { card: ManagerCard }) {
  if (card.kind === "note") return cardIsOpen(card) ? <NoteSuggestion card={card} /> : <NoteReceipt card={card} />;
  return cardIsOpen(card) ? <OpenCard card={card} /> : <Receipt card={card} />;
}

function OpenCard({ card }: { card: ManagerCard }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const manager = useManager();
  const store = useStore();
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [reply, setReply] = useState("");
  const session = store.findSession(card.sessionId);
  const name = session?.name ?? card.agentName ?? "An agent";
  const question = cardQuestion(card);

  const act = async (work: () => Promise<string | null>) => {
    setBusy(true);
    const result = await work();
    setProblem(result);
    setBusy(false);
    return result;
  };
  const answer = (choice: QuestionAnswer) => void act(() => manager.reply(card, choice));
  const sendReply = () => {
    const text = reply.trim();
    if (!text) return;
    void act(() => manager.reply(card, { text })).then((result) => {
      if (!result) setReply("");
    });
  };

  return (
    <SwipeToDismiss onDismiss={() => void manager.dismiss(card)} accessibilityLabel={`${name} needs you`}>
      <View style={styles.card}>
        <View style={styles.head}>
          <AgentIcon kind={session?.kind ?? ""} size={20} />
          <MotionPressable
            onPress={() => session && router.push(`/session/${encodeURIComponent(session.id)}`)}
            disabled={!session}
            style={styles.headName}
          >
            <Text style={styles.name} numberOfLines={1}>{name}</Text>
          </MotionPressable>
          <View style={styles.dot} />
          <Text style={styles.needs}>needs you</Text>
          <MotionPressable onPress={() => void act(() => manager.dismiss(card))} hitSlop={10}
            accessibilityRole="button" accessibilityLabel="Put away">
            <Feather name="x" size={16} color={color.muted} />
          </MotionPressable>
        </View>

        {question ? null : <Text style={styles.title} selectable>{card.title}</Text>}
        {!question && card.detail ? (
          <View style={styles.code}><Text style={styles.codeText} selectable>{card.detail}</Text></View>
        ) : null}
        {card.reason ? <Text style={styles.reason}>Waits for you: {card.reason}</Text> : null}
        {card.context ? <Text style={styles.context} numberOfLines={3}>{card.context}</Text> : null}
        {card.suggested ? (
          <Text style={styles.suggested}>Agentman's pick is marked. One tap sends it.</Text>
        ) : null}

        {question ? (
          <QuestionCard
            question={{ ...question, prompt: card.title, detail: card.detail }}
            onAnswer={answer}
            disabled={busy}
            submissionStatus={busy ? "sending" : undefined}
            submissionError={problem ?? undefined}
            customPlaceholder="Or reply to Agentman…"
          />
        ) : (
          <View style={styles.replyField}>
            <TextInput
              style={styles.replyInput}
              value={reply}
              onChangeText={setReply}
              placeholder={`Reply to Agentman about ${name}`}
              placeholderTextColor={color.faint}
              multiline
              editable={!busy}
            />
            <MotionPressable onPress={sendReply} disabled={busy || !reply.trim()}
              style={[styles.replySend, (busy || !reply.trim()) && styles.replySendOff]}
              accessibilityRole="button" accessibilityLabel={`Send to Agentman, which passes it on to ${name}`}>
              {busy ? <ActivityIndicator size="small" color={color.onAccent} /> : (
                <Feather name="arrow-up" size={16} color={reply.trim() ? color.onAccent : color.faint} />
              )}
            </MotionPressable>
          </View>
        )}
        {problem && !question ? <Text style={styles.problem}>{problem}</Text> : null}
      </View>
    </SwipeToDismiss>
  );
}

/** A card answered here, folded to what was chosen. */
function Receipt({ card }: { card: ManagerCard }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const store = useStore();
  const name = store.findSession(card.sessionId)?.name ?? card.agentName;
  return (
    <View style={styles.receipt}>
      <Feather name="check" size={13} color={color.ok} />
      <Text style={styles.receiptText} numberOfLines={1}>
        {name} · {card.outcome ?? "Answered"}
      </Text>
    </View>
  );
}

/** A note Agentman suggests keeping. Nothing is saved until the user says. */
function NoteSuggestion({ card }: { card: ManagerCard }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const manager = useManager();
  const note = card.note;
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(note?.title ?? card.title);
  const [body, setBody] = useState(note?.body ?? "");
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  const save = async () => {
    setBusy(true);
    const edited: Note | undefined = editing && note
      ? { ...note, title: title.trim() || note.title, body }
      : undefined;
    setProblem(await manager.saveNote(card, edited));
    setBusy(false);
  };
  const dismiss = async () => {
    setBusy(true);
    setProblem(await manager.dismiss(card));
    setBusy(false);
  };

  return (
    <View style={styles.card}>
      <View style={styles.head}>
        <Feather name="book-open" size={14} color={color.working} />
        <Text style={[styles.needs, styles.noteLabel]}>Agentman suggests a note</Text>
        <Text style={styles.tier}>{note?.tier === "private" ? "Private" : "Shared with agents"}</Text>
      </View>
      {editing ? (
        <>
          <TextInput style={styles.noteTitleInput} value={title} onChangeText={setTitle} placeholder="Title"
            placeholderTextColor={color.faint} />
          <TextInput style={styles.noteBodyInput} value={body} onChangeText={setBody} multiline
            placeholder="The note" placeholderTextColor={color.faint} />
        </>
      ) : (
        <>
          <Text style={styles.title}>{note?.title ?? card.title}</Text>
          {note?.body ? <Text style={styles.noteBody} numberOfLines={8}>{note.body}</Text> : null}
        </>
      )}
      <View style={styles.actions}>
        <MotionPressable onPress={() => void dismiss()} disabled={busy} style={styles.secondary}
          accessibilityRole="button">
          <Text style={styles.secondaryText}>Dismiss</Text>
        </MotionPressable>
        <MotionPressable onPress={() => setEditing((value) => !value)} disabled={busy} style={styles.secondary}
          accessibilityRole="button">
          <Text style={styles.secondaryText}>{editing ? "Cancel edit" : "Edit"}</Text>
        </MotionPressable>
        <View style={{ flex: 1 }} />
        <MotionPressable onPress={() => void save()} disabled={busy} style={styles.primary} accessibilityRole="button">
          {busy ? <ActivityIndicator size="small" color={color.onInverse} /> : (
            <Text style={styles.primaryText}>Save note</Text>
          )}
        </MotionPressable>
      </View>
      {problem ? <Text style={styles.problem}>{problem}</Text> : null}
    </View>
  );
}

function NoteReceipt({ card }: { card: ManagerCard }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.receipt}>
      <Feather name="book-open" size={13} color={color.ok} />
      <Text style={styles.receiptText} numberOfLines={1}>
        {card.outcome ?? `Saved “${card.note?.title ?? card.title}”`}
      </Text>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    card: {
      gap: space.md,
      padding: space.lg,
      borderRadius: radius.xl,
      borderWidth: 1,
      borderColor: c.needsYouEdge,
      backgroundColor: c.surface,
    },
    head: { flexDirection: "row", alignItems: "center", gap: space.sm },
    headName: { flexShrink: 1 },
    name: { fontFamily: font.sansMedium, fontSize: size.body, color: c.text },
    dot: { width: 6, height: 6, borderRadius: 3, backgroundColor: c.needsYou },
    needs: { flex: 1, fontFamily: font.sans, fontSize: size.caption, color: c.needsYouText },
    noteLabel: { color: c.workingText },
    tier: { fontFamily: font.sans, fontSize: size.label, color: c.faint },
    title: { fontFamily: font.sans, fontSize: size.body, lineHeight: 22, color: c.text },
    code: { padding: space.md, borderRadius: radius.md, backgroundColor: c.toolWell },
    codeText: { fontFamily: font.mono, fontSize: 12, lineHeight: 18, color: c.textSecondary },
    reason: { fontFamily: font.sans, fontSize: size.caption, color: c.needsYouText },
    context: { fontFamily: font.sans, fontSize: size.caption, lineHeight: 18, color: c.muted },
    suggested: { fontFamily: font.sans, fontSize: size.caption, color: c.working },
    replyField: {
      flexDirection: "row",
      alignItems: "flex-end",
      gap: space.sm,
      paddingLeft: space.md,
      paddingRight: 5,
      paddingVertical: 5,
      borderRadius: radius.xl,
      backgroundColor: c.fill,
    },
    replyInput: {
      flex: 1,
      minHeight: 32,
      maxHeight: 110,
      paddingTop: 6,
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.text,
    },
    replySend: {
      width: 32,
      height: 32,
      borderRadius: 16,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.working,
    },
    replySendOff: { backgroundColor: c.fillStrong },
    problem: { fontFamily: font.sans, fontSize: size.label, color: c.errorText },
    receipt: { flexDirection: "row", alignItems: "center", gap: 6 },
    receiptText: { flex: 1, fontFamily: font.sans, fontSize: size.caption, color: c.muted },
    noteBody: { fontFamily: font.sans, fontSize: size.caption, lineHeight: 19, color: c.textSecondary },
    noteTitleInput: {
      fontFamily: font.sansMedium,
      fontSize: size.body,
      color: c.text,
      paddingVertical: space.xs,
      borderBottomWidth: 1,
      borderBottomColor: c.line,
    },
    noteBodyInput: {
      minHeight: 90,
      maxHeight: 220,
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 19,
      color: c.text,
      textAlignVertical: "top",
    },
    actions: { flexDirection: "row", alignItems: "center", gap: space.sm },
    secondary: { height: 34, paddingHorizontal: space.md, borderRadius: radius.pill, justifyContent: "center", backgroundColor: c.fill },
    secondaryText: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.textSecondary },
    primary: {
      height: 34,
      minWidth: 96,
      paddingHorizontal: space.lg,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.inverse,
    },
    primaryText: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.onInverse },
  });
