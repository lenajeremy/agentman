import Feather from "@expo/vector-icons/Feather";
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
import { useStyles, useTheme } from "../lib/appearance";
import { Question, QuestionAnswer, SendStatus } from "../lib/protocol";
import { font, Palette, radius, size, space } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";

const AFFIRMATIVE = /^(yes|allow|approve|proceed)/i;

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
  onOpen,
  disabled = false,
  submissionStatus,
  submissionError,
}: {
  question: Question;
  onAnswer: (answer: QuestionAnswer) => void;
  /** In a list row: the question with at most two quick answers. */
  compact?: boolean;
  /** Where a compact card sends someone who needs every choice. */
  onOpen?: () => void;
  /** The daemon is unreachable, so no answer can safely leave the device. */
  disabled?: boolean;
  /** Result of the answer command currently associated with this question. */
  submissionStatus?: "sending" | SendStatus;
  submissionError?: string;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
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

  // The first affirmative choice is the one most people are reaching for, so
  // it is the filled button. Only in single-tap questions: when choices are
  // being assembled, nothing should look pre-chosen.
  const primaryKey = advanced
    ? undefined
    : question.options.find((option) => AFFIRMATIVE.test(option.label))?.key ??
      question.options[0]?.key;

  if (compact) {
    // Answering from the list is for the common two-way prompt. Anything
    // with more to weigh opens the session, where every choice is laid out.
    const quick = !advanced && question.options.length > 0;
    const shown = question.options.length <= 2 ? question.options : question.options.slice(0, 1);
    const more = !quick || question.options.length > 2;
    return (
      <View style={styles.compact}>
        <Text style={styles.compactTitle}>{question.title || "Decision needed"}</Text>
        {question.detail ? (
          // Never truncated: approving a command you cannot fully read is worse
          // than not approving it, and a quick answer makes that easy.
          <View style={styles.detailBox}>
            <Text style={styles.detail} selectable>
              {question.detail}
            </Text>
          </View>
        ) : null}
        {question.prompt ? <Text style={styles.compactPrompt}>{question.prompt}</Text> : null}
        <View style={styles.quickRow}>
          {quick
            ? shown.map((option) => {
                const primary = option.key === primaryKey;
                return (
                  <MotionPressable
                    key={option.key}
                    onPress={() => choose(option.key)}
                    disabled={interactionDisabled}
                    style={[
                      styles.quick,
                      primary ? styles.quickPrimary : styles.quickSecondary,
                      interactionDisabled && styles.controlDisabled,
                    ]}
                    pressedScale={0.97}
                    accessibilityRole="button"
                    accessibilityLabel={option.label}
                    accessibilityState={{ disabled: interactionDisabled }}
                  >
                    <Text
                      style={[styles.quickLabel, primary && styles.quickLabelPrimary]}
                      numberOfLines={1}
                    >
                      {option.label}
                    </Text>
                  </MotionPressable>
                );
              })
            : null}
          {more ? (
            <MotionPressable
              onPress={onOpen}
              disabled={!onOpen}
              style={[styles.quick, styles.quickSecondary, quick ? styles.quickMore : null]}
              pressedScale={0.97}
              accessibilityRole="button"
              accessibilityLabel="See every choice"
            >
              {quick ? (
                <Feather name="more-horizontal" size={18} color={color.text} />
              ) : (
                <Text style={styles.quickLabel}>Choose an answer</Text>
              )}
            </MotionPressable>
          ) : null}
        </View>
        {submissionStatus || disabled ? (
          <AnswerStatus status={submissionStatus} error={submissionError} offline={disabled} />
        ) : null}
      </View>
    );
  }

  return (
    <View style={styles.full}>
      <View style={styles.eyebrowRow}>
        <View style={styles.pill}>
          <View style={styles.pillDot} />
          <Text style={styles.pillLabel}>Needs you</Text>
        </View>
      </View>
      <Text style={styles.title}>{question.title || "Decision needed"}</Text>

      {question.detail ? (
        <View style={styles.detailBox}>
          <Text style={styles.detail} selectable>
            {question.detail}
          </Text>
        </View>
      ) : null}

      {question.prompt ? <Text style={styles.prompt}>{question.prompt}</Text> : null}

      <View style={styles.options}>
        {question.options.map((option) => {
          // The CLI's own highlighted choice is its default. Marking it
          // helps, but nothing is preselected here — a tap is a decision.
          const isSelected = selected.includes(option.key);
          const primary = option.key === primaryKey;
          return (
            <MotionPressable
              key={option.key}
              onPress={() => choose(option.key)}
              disabled={interactionDisabled}
              style={[
                styles.option,
                primary && styles.optionPrimary,
                advanced && isSelected && styles.optionSelected,
                interactionDisabled && styles.controlDisabled,
              ]}
              pressedScale={0.985}
              accessibilityRole={question.multiple ? "checkbox" : "radio"}
              accessibilityLabel={
                option.description ? `${option.label}. ${option.description}` : option.label
              }
              accessibilityState={
                question.multiple
                  ? { checked: isSelected, disabled: interactionDisabled }
                  : { selected: isSelected, disabled: interactionDisabled }
              }
            >
              <View style={[styles.optionKeyWrap, primary && styles.optionKeyWrapPrimary]}>
                <Text style={[styles.optionKey, primary && styles.optionKeyPrimary]}>
                  {option.key}
                </Text>
              </View>
              <View style={styles.optionCopy}>
                <Text style={[styles.optionLabel, primary && styles.optionLabelPrimary]}>
                  {option.label}
                </Text>
                {option.description ? (
                  <Text
                    style={[styles.optionDescription, primary && styles.optionDescriptionPrimary]}
                  >
                    {option.description}
                  </Text>
                ) : null}
              </View>
              {isSelected ? (
                <View style={[styles.checkMark, primary && styles.checkMarkPrimary]}>
                  <Feather
                    name="check"
                    size={13}
                    color={primary ? color.inverse : color.onInverse}
                  />
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
              <ActivityIndicator color={color.onInverse} />
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
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
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
        <ActivityIndicator size="small" color={color.needsYouText} />
      ) : null}
      <Text style={[styles.answerStatusText, failed && styles.answerStatusTextFailed]}>
        {text}
      </Text>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    full: { gap: space.md },
    compact: { gap: space.sm },

    eyebrowRow: { flexDirection: "row" },
    pill: {
      flexDirection: "row",
      alignItems: "center",
      gap: 6,
      height: 24,
      paddingHorizontal: 10,
      borderRadius: radius.pill,
      backgroundColor: c.needsYouWash,
    },
    pillDot: { width: 6, height: 6, borderRadius: 3, backgroundColor: c.needsYou },
    pillLabel: { fontFamily: font.sansBold, fontSize: size.label, color: c.needsYouText },

    title: {
      fontFamily: font.sansBold,
      fontSize: size.heading,
      lineHeight: 27,
      letterSpacing: -0.5,
      color: c.text,
    },
    compactTitle: {
      fontFamily: font.sansBold,
      fontSize: size.title,
      letterSpacing: -0.3,
      color: c.text,
    },
    // Machine text, set in mono like every other command in the app.
    detail: {
      fontFamily: font.mono,
      fontSize: size.caption,
      color: c.text,
      lineHeight: 19,
    },
    detailBox: {
      backgroundColor: c.fill,
      borderRadius: radius.md,
      paddingHorizontal: space.md,
      paddingVertical: 10,
    },
    prompt: {
      fontFamily: font.sans,
      fontSize: size.body,
      lineHeight: 21,
      color: c.textSecondary,
    },
    compactPrompt: {
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 18,
      color: c.muted,
    },

    quickRow: { flexDirection: "row", gap: space.sm, marginTop: space.xs },
    quick: {
      flex: 1,
      minHeight: 44,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
    },
    quickPrimary: { backgroundColor: c.inverse },
    quickSecondary: { backgroundColor: c.surface, borderWidth: 1, borderColor: c.fillStrong },
    // Fixed square: flex-basis must be set too, or web keeps the 0% basis
    // from `flex: 1` above and collapses it.
    quickMore: { flexGrow: 0, flexShrink: 0, flexBasis: 44, width: 44, paddingHorizontal: 0 },
    quickLabel: { fontFamily: font.sansBold, fontSize: size.body, color: c.text },
    quickLabelPrimary: { color: c.onInverse },

    options: { gap: space.sm, marginTop: space.xs },
    option: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      minHeight: 54,
      paddingHorizontal: space.lg,
      paddingVertical: 10,
      borderRadius: radius.lg,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.fillStrong,
    },
    optionPrimary: { backgroundColor: c.inverse, borderColor: c.inverse },
    optionSelected: { backgroundColor: c.workingWash, borderColor: c.working },
    controlDisabled: { opacity: 0.5 },
    optionKeyWrap: {
      minWidth: 24,
      height: 24,
      paddingHorizontal: space.xs,
      borderRadius: 7,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
    },
    // Neutral grey reads on the ink fill in both themes (dark ink in light,
    // light ink in dark).
    optionKeyWrapPrimary: { backgroundColor: "rgba(128,128,128,0.22)" },
    optionKey: {
      fontFamily: font.monoMedium,
      fontSize: size.label,
      color: c.muted,
    },
    optionKeyPrimary: { color: c.onInverse },
    optionCopy: { flex: 1, gap: space.xxs },
    optionLabel: { fontFamily: font.sansMedium, fontSize: size.body, color: c.text },
    optionLabelPrimary: { fontFamily: font.sansBold, color: c.onInverse },
    optionDescription: {
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 18,
      color: c.muted,
    },
    optionDescriptionPrimary: { color: c.onInverse, opacity: 0.72 },
    previewBox: {
      gap: space.sm,
      padding: space.md,
      borderRadius: radius.md,
      backgroundColor: c.fill,
    },
    previewEyebrow: {
      fontFamily: font.sansBold,
      fontSize: size.label,
      color: c.muted,
    },
    previewText: {
      fontFamily: font.mono,
      fontSize: size.caption,
      lineHeight: 19,
      color: c.text,
    },
    checkMark: {
      width: 22,
      height: 22,
      borderRadius: 11,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.working,
    },
    checkMarkPrimary: { backgroundColor: c.onInverse },
    customInput: {
      minHeight: 54,
      borderRadius: radius.lg,
      backgroundColor: c.fill,
      color: c.text,
      fontFamily: font.sans,
      fontSize: size.body,
      paddingHorizontal: space.lg,
      paddingVertical: 14,
    },
    submit: {
      minHeight: 52,
      alignItems: "center",
      justifyContent: "center",
      borderRadius: radius.pill,
      backgroundColor: c.inverse,
      paddingHorizontal: space.lg,
    },
    submitDisabled: { opacity: 0.35 },
    submitLabel: {
      color: c.onInverse,
      fontFamily: font.sansBold,
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
      backgroundColor: c.fill,
    },
    answerStatusFailed: {
      backgroundColor: c.errorWash,
    },
    answerStatusText: {
      flex: 1,
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 18,
      color: c.muted,
    },
    answerStatusTextFailed: { color: c.errorText },
  });
