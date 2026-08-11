import { useEffect, useMemo, useState } from "react";
import { Pressable, StyleSheet, Text, TextInput, View } from "react-native";

import { Question, QuestionAnswer } from "../lib/protocol";
import { color, font, radius, size, space } from "../lib/theme";

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
}: {
  question: Question;
  onAnswer: (answer: QuestionAnswer) => void;
  /** In a list row, show the question without the choices. */
  compact?: boolean;
}) {
  const [selected, setSelected] = useState<string[]>([]);
  const [custom, setCustom] = useState("");
  const identity = useMemo(
    () =>
      [
        question.title,
        question.prompt,
        question.detail,
        question.multiple,
        question.custom,
        ...question.options.flatMap((option) => [option.key, option.label]),
      ].join("\u0000"),
    [question],
  );
  useEffect(() => {
    setSelected([]);
    setCustom("");
  }, [identity]);

  const advanced = Boolean(question.multiple || question.custom);
  const customText = custom.trim();
  const answerCount = selected.length + (customText ? 1 : 0);
  const canSubmit = answerCount > 0 && (question.multiple || answerCount === 1);

  const choose = (key: string) => {
    if (!advanced) {
      onAnswer({ optionKey: key });
      return;
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
      onAnswer({ optionKey: selected[0] });
      return;
    }
    onAnswer({
      optionKeys: selected.length > 0 ? selected : undefined,
      answerText: customText || undefined,
    });
  };

  return (
    <View style={[styles.card, compact && styles.cardCompact]}>
      {question.title ? <Text style={styles.title}>{question.title}</Text> : null}

      {question.detail ? (
        <Text style={styles.detail} numberOfLines={compact ? 2 : undefined}>
          {question.detail}
        </Text>
      ) : null}

      <Text style={styles.prompt}>{question.prompt}</Text>

      {!compact && (
        <View style={styles.options}>
          {question.options.map((option) => {
            // The CLI's own highlighted choice is its default. Marking it
            // helps, but nothing is preselected here — a tap is a decision.
            const affirmative = /^(yes|allow|approve|proceed)/i.test(option.label);
            return (
              <Pressable
                key={option.key}
                onPress={() => choose(option.key)}
                style={({ pressed }) => [
                  styles.option,
                  affirmative && styles.optionAffirmative,
                  selected.includes(option.key) && styles.optionSelected,
                  pressed && styles.optionPressed,
                ]}
                accessibilityRole="button"
                accessibilityLabel={option.label}
                accessibilityState={{ selected: selected.includes(option.key) }}
              >
                <Text style={styles.optionKey}>{option.key}</Text>
                <Text
                  style={[styles.optionLabel, affirmative && styles.optionLabelAffirmative]}
                >
                  {option.label}
                </Text>
              </Pressable>
            );
          })}
          {question.custom ? (
            <TextInput
              style={styles.customInput}
              value={custom}
              onChangeText={(value) => {
                setCustom(value);
                if (!question.multiple && value.trim()) setSelected([]);
              }}
              placeholder="Write another answer…"
              placeholderTextColor={color.faint}
              multiline
              accessibilityLabel="Custom answer"
            />
          ) : null}
          {advanced ? (
            <Pressable
              onPress={submit}
              disabled={!canSubmit}
              style={({ pressed }) => [
                styles.submit,
                !canSubmit && styles.submitDisabled,
                pressed && styles.optionPressed,
              ]}
              accessibilityRole="button"
              accessibilityLabel="Submit answer"
            >
              <Text style={styles.submitLabel}>Submit answer</Text>
            </Pressable>
          ) : null}
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: "#1B1A17",
    borderRadius: radius.md,
    borderLeftWidth: 2,
    borderLeftColor: color.needsYou,
    padding: space.md,
    gap: space.sm,
  },
  cardCompact: { padding: space.sm, gap: space.xs, marginTop: space.sm },

  title: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.2,
    textTransform: "uppercase",
    color: color.needsYou,
  },
  // Machine text, set in mono like every other command in the app.
  detail: {
    fontFamily: font.mono,
    fontSize: size.caption,
    color: color.text,
    lineHeight: 18,
  },
  prompt: { fontFamily: font.sansMedium, fontSize: size.body, color: color.text },

  options: { gap: space.sm, marginTop: space.xs },
  option: {
    flexDirection: "row",
    alignItems: "center",
    gap: space.sm,
    paddingVertical: space.sm,
    paddingHorizontal: space.md,
    borderRadius: radius.sm,
    backgroundColor: color.sunken,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  optionAffirmative: { borderColor: color.needsYou },
  optionSelected: { backgroundColor: "#302A1E", borderColor: color.needsYou },
  optionPressed: { opacity: 0.65 },
  optionKey: {
    fontFamily: font.monoMedium,
    fontSize: size.caption,
    color: color.faint,
    minWidth: 12,
  },
  optionLabel: { flex: 1, fontFamily: font.sans, fontSize: size.body, color: color.text },
  optionLabelAffirmative: { fontFamily: font.sansMedium, color: color.needsYou },
  customInput: {
    minHeight: 48,
    borderRadius: radius.sm,
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
    alignItems: "center",
    borderRadius: radius.sm,
    backgroundColor: color.needsYou,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
  },
  submitDisabled: { opacity: 0.35 },
  submitLabel: {
    color: color.ink,
    fontFamily: font.sansMedium,
    fontSize: size.body,
  },
});
