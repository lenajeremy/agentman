import * as Haptics from "expo-haptics";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Platform,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";

import { questionSnapshotIdentity } from "../lib/action-state";
import { Question, QuestionAnswer, SendStatus } from "../lib/protocol";
import { color, font, radius, size, space } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";

function terminalOptionKeys(question: Question): string[] {
  const checked = question.options.filter((option) => option.checked).map((option) => option.key);
  if (checked.length > 0) return checked;
  if (question.options.some((option) => option.preview)) {
    const highlighted = question.options.find((option) => option.selected);
    if (highlighted) return [highlighted.key];
  }
  return [];
}

/**
 * A decision the agent is blocked on.
 *
 * This is the app's reason to exist. Everything else can wait until you are
 * back at your desk; a permission prompt cannot, because the agent stops dead
 * until it is answered. So this is the one component allowed to be loud.
 *
 * The detail — the actual command under review — is never truncated away.
 * Approving something you cannot read is worse than not approving at all, and
 * a phone makes that mistake easy.
 */
export function QuestionCard({
  question,
  onAnswer,
  compact,
  disabled = false,
  submissionStatus,
  submissionError,
}: {
  question: Question;
  onAnswer: (answer: QuestionAnswer) => void;
  /** In a list row, show the question without the choices. */
  compact?: boolean;
  /** The daemon is unreachable, so no answer can safely leave the device. */
  disabled?: boolean;
  /** Result of the answer command currently associated with this question. */
  submissionStatus?: "sending" | SendStatus;
  submissionError?: string;
}) {
  const [selected, setSelected] = useState<string[]>(() => terminalOptionKeys(question));
  const [custom, setCustom] = useState("");
  const identity = useMemo(() => questionSnapshotIdentity(question), [question]);
  const terminalSelection = useMemo(() => terminalOptionKeys(question), [identity]);
  // React state does not update until after an event handler returns. This
  // synchronous latch closes the tiny window in which a fast double-tap could
  // type the same terminal answer twice.
  const commitLock = useRef(false);
  useEffect(() => {
    setSelected(terminalSelection);
    setCustom("");
    commitLock.current = false;
  }, [identity, terminalSelection]);
  useEffect(() => {
    if (submissionStatus !== "sending" && submissionStatus !== "delivered") {
      commitLock.current = false;
    }
  }, [submissionStatus]);

  const hasPreviews = question.options.some((option) => Boolean(option.preview));
  const advanced = Boolean(question.multiple || question.custom || hasPreviews);
  const customText = custom.trim();
  const answerCount = selected.length + (customText ? 1 : 0);
  const canSubmit = answerCount > 0 && (question.multiple || answerCount === 1);
  const submitting = submissionStatus === "sending";
  const submitted = submissionStatus === "delivered";
  const interactionDisabled = disabled || submitting || submitted;
  const activePreview = question.options.find(
    (option) => selected.includes(option.key) && option.preview,
  )?.preview;

  const commit = (answer: QuestionAnswer) => {
    if (interactionDisabled || commitLock.current) return;
    commitLock.current = true;
    if (Platform.OS !== "web") {
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
    }
    onAnswer(answer);
  };

  const choose = (key: string) => {
    if (interactionDisabled || commitLock.current) return;
    if (!advanced) {
      setSelected([key]);
      commit({ optionKey: key });
      return;
    }
    if (Platform.OS !== "web") {
      void Haptics.selectionAsync().catch(() => {});
    }
    if (!question.multiple) {
      setSelected([key]);
      setCustom("");
      return;
    }
    setSelected((current) =>
      current.includes(key) ? current.filter((value) => value !== key) : [...current, key],
    );
  };

  const submit = () => {
    if (!canSubmit) return;
    if (!question.multiple && selected.length === 1) {
      commit({ optionKey: selected[0] });
      return;
    }
    commit({
      optionKeys: selected.length > 0 ? selected : undefined,
      answerText: customText || undefined,
    });
  };

  return (
    <View style={[styles.card, compact && styles.cardCompact]}>
      <View style={styles.header}>
        <View style={[styles.questionMark, compact && styles.questionMarkCompact]}>
          <Text style={styles.questionGlyph}>?</Text>
        </View>
        <View style={styles.headerCopy}>
          <Text style={styles.eyebrow}>Agent needs you</Text>
          <Text style={styles.title}>{question.title || "Decision needed"}</Text>
        </View>
      </View>

      {question.detail ? (
        <View style={styles.detailBox}>
          <Text style={styles.detail} numberOfLines={compact ? 2 : undefined}>
            {question.detail}
          </Text>
        </View>
      ) : null}

      <Text style={styles.prompt}>{question.prompt}</Text>

      {!compact && (
        <View style={styles.options}>
          {question.options.map((option) => {
            // The CLI's own highlighted choice is its default. Marking it
            // helps, but nothing is preselected here — a tap is a decision.
            const affirmative = /^(yes|allow|approve|proceed)/i.test(option.label);
            return (
              <MotionPressable
                key={option.key}
                onPress={() => choose(option.key)}
                disabled={interactionDisabled}
                style={[
                  styles.option,
                  affirmative && styles.optionAffirmative,
                  selected.includes(option.key) && styles.optionSelected,
                  interactionDisabled && styles.controlDisabled,
                ]}
                pressedScale={0.985}
                accessibilityRole={question.multiple ? "checkbox" : "radio"}
                accessibilityLabel={
                  option.description ? `${option.label}. ${option.description}` : option.label
                }
                accessibilityState={
                  question.multiple
                    ? { checked: selected.includes(option.key), disabled: interactionDisabled }
                    : { selected: selected.includes(option.key), disabled: interactionDisabled }
                }
              >
                <View style={styles.optionKeyWrap}>
                  <Text style={styles.optionKey}>{option.key}</Text>
                </View>
                <View style={styles.optionCopy}>
                  <Text
                    style={[styles.optionLabel, affirmative && styles.optionLabelAffirmative]}
                  >
                    {option.label}
                  </Text>
                  {option.description ? (
                    <Text style={styles.optionDescription}>{option.description}</Text>
                  ) : null}
                </View>
                {selected.includes(option.key) ? (
                  <View style={styles.checkMark}>
                    <Text style={styles.checkGlyph}>✓</Text>
                  </View>
                ) : null}
              </MotionPressable>
            );
          })}
          {question.custom ? (
            <TextInput
              style={styles.customInput}
              value={custom}
              editable={!interactionDisabled}
              onChangeText={(value) => {
                setCustom(value);
                if (!question.multiple && value.trim()) setSelected([]);
              }}
              placeholder="Write another answer…"
              placeholderTextColor={color.faint}
              multiline
              accessibilityLabel="Custom answer"
              accessibilityState={{ disabled: interactionDisabled }}
            />
          ) : null}
          {activePreview ? (
            <View style={styles.previewBox}>
              <Text style={styles.previewEyebrow}>Preview</Text>
              <Text style={styles.previewText} selectable>
                {activePreview}
              </Text>
            </View>
          ) : null}
          {advanced ? (
            <MotionPressable
              onPress={submit}
              disabled={!canSubmit || interactionDisabled}
              style={[
                styles.submit,
                (!canSubmit || interactionDisabled) && styles.submitDisabled,
              ]}
              accessibilityRole="button"
              accessibilityLabel="Submit answer"
              accessibilityState={{
                disabled: !canSubmit || interactionDisabled,
                busy: submitting,
              }}
            >
              {submitting ? (
                <ActivityIndicator color={color.ink} />
              ) : (
                <Text style={styles.submitLabel}>
                  {submitted ? "Answer sent" : "Submit answer"}
                </Text>
              )}
            </MotionPressable>
          ) : null}
          {submissionStatus || disabled ? (
            <AnswerStatus
              status={submissionStatus}
              error={submissionError}
              offline={disabled}
            />
          ) : null}
        </View>
      )}
    </View>
  );
}

function AnswerStatus({
  status,
  error,
  offline,
}: {
  status?: "sending" | SendStatus;
  error?: string;
  offline: boolean;
}) {
  const failed = status === "failed";
  const text = failed
    ? `Couldn’t submit the answer${error ? `: ${error}` : "."} Check the terminal, then choose an answer to retry.`
    : status === "sending"
      ? "Submitting answer…"
      : status === "delivered"
        ? "Answer sent. Waiting for the agent to continue…"
        : offline
          ? "Reconnect to your Mac to answer."
          : "";

  if (!text) return null;
  return (
    <View
      style={[styles.answerStatus, failed && styles.answerStatusFailed]}
      accessibilityRole={failed ? "alert" : "text"}
      accessibilityLiveRegion="polite"
    >
      {status === "sending" ? (
        <ActivityIndicator size="small" color={color.needsYou} />
      ) : null}
      <Text style={[styles.answerStatusText, failed && styles.answerStatusTextFailed]}>
        {text}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: color.needsYouWash,
    borderRadius: radius.lg,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: "#594523",
    padding: space.lg,
    gap: space.md,
  },
  cardCompact: { padding: space.md, gap: space.sm, marginTop: space.sm },

  header: { flexDirection: "row", alignItems: "center", gap: space.md },
  questionMark: {
    width: 34,
    height: 34,
    borderRadius: 17,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "#3A2D16",
  },
  questionMarkCompact: { width: 28, height: 28, borderRadius: 14 },
  questionGlyph: { fontFamily: font.sansBold, fontSize: size.body, color: color.needsYou },
  headerCopy: { flex: 1 },
  eyebrow: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.2,
    textTransform: "uppercase",
    color: color.needsYou,
  },

  title: {
    fontFamily: font.sansBold,
    fontSize: size.body,
    color: color.text,
    marginTop: space.xxs,
  },
  // Machine text, set in mono like every other command in the app.
  detail: {
    fontFamily: font.mono,
    fontSize: size.caption,
    color: color.text,
    lineHeight: 18,
  },
  detailBox: {
    backgroundColor: color.sunken,
    borderRadius: radius.md,
    padding: space.md,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  prompt: {
    fontFamily: font.sansMedium,
    fontSize: size.body,
    lineHeight: 21,
    color: color.text,
  },

  options: { gap: space.sm, marginTop: space.xs },
  option: {
    flexDirection: "row",
    alignItems: "center",
    gap: space.sm,
    minHeight: 48,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
    borderRadius: radius.md,
    backgroundColor: color.sunken,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  optionAffirmative: { borderColor: color.needsYou },
  optionSelected: { backgroundColor: "#302A1E", borderColor: color.needsYou },
  controlDisabled: { opacity: 0.58 },
  optionKeyWrap: {
    minWidth: 26,
    height: 26,
    paddingHorizontal: space.xs,
    borderRadius: radius.sm,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.surfaceRaised,
  },
  optionKey: {
    fontFamily: font.monoMedium,
    fontSize: size.caption,
    color: color.muted,
  },
  optionCopy: { flex: 1, gap: space.xxs },
  optionLabel: { fontFamily: font.sans, fontSize: size.body, color: color.text },
  optionLabelAffirmative: { fontFamily: font.sansMedium, color: color.needsYou },
  optionDescription: {
    fontFamily: font.sans,
    fontSize: size.caption,
    lineHeight: 18,
    color: color.muted,
  },
  previewBox: {
    gap: space.sm,
    padding: space.md,
    borderRadius: radius.md,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.lineStrong,
    backgroundColor: color.surfaceRaised,
  },
  previewEyebrow: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1,
    textTransform: "uppercase",
    color: color.muted,
  },
  previewText: {
    fontFamily: font.mono,
    fontSize: size.caption,
    lineHeight: 18,
    color: color.text,
  },
  checkMark: {
    width: 22,
    height: 22,
    borderRadius: 11,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.needsYou,
  },
  checkGlyph: { fontFamily: font.sansBold, fontSize: size.label, color: color.ink },
  customInput: {
    minHeight: 52,
    borderRadius: radius.md,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
    backgroundColor: color.sunken,
    color: color.text,
    fontFamily: font.sans,
    fontSize: size.body,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
  },
  submit: {
    minHeight: 48,
    alignItems: "center",
    justifyContent: "center",
    borderRadius: radius.md,
    backgroundColor: color.needsYou,
    paddingHorizontal: space.md,
  },
  submitDisabled: { backgroundColor: color.lineStrong, opacity: 0.55 },
  submitLabel: {
    color: color.ink,
    fontFamily: font.sansMedium,
    fontSize: size.body,
  },
  answerStatus: {
    minHeight: 40,
    flexDirection: "row",
    alignItems: "center",
    gap: space.sm,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
    borderRadius: radius.md,
    backgroundColor: color.sunken,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  answerStatusFailed: {
    backgroundColor: color.errorWash,
    borderColor: color.error,
  },
  answerStatusText: {
    flex: 1,
    fontFamily: font.sans,
    fontSize: size.caption,
    lineHeight: 18,
    color: color.muted,
  },
  answerStatusTextFailed: { color: color.error },
});
