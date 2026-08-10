import { Pressable, StyleSheet, Text, View } from "react-native";

import { Question } from "../lib/protocol";
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
  onAnswer: (optionKey: string) => void;
  /** In a list row, show the question without the choices. */
  compact?: boolean;
}) {
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
                onPress={() => onAnswer(option.key)}
                style={({ pressed }) => [
                  styles.option,
                  affirmative && styles.optionAffirmative,
                  pressed && styles.optionPressed,
                ]}
                accessibilityRole="button"
                accessibilityLabel={option.label}
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
  optionPressed: { opacity: 0.65 },
  optionKey: {
    fontFamily: font.monoMedium,
    fontSize: size.caption,
    color: color.faint,
    minWidth: 12,
  },
  optionLabel: { flex: 1, fontFamily: font.sans, fontSize: size.body, color: color.text },
  optionLabelAffirmative: { fontFamily: font.sansMedium, color: color.needsYou },
});
