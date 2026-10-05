import Feather from "@expo/vector-icons/Feather";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { ContentColumn } from "../../components/ContentColumn";
import { ImageViewer } from "../../components/ImageViewer";
import { Markdown } from "../../components/Markdown";
import { MotionPressable } from "../../components/MotionPressable";
import { SyntaxCode } from "../../components/SyntaxCode";
import { useStyles, useTheme } from "../../lib/appearance";
import { artifactKind, artifactTitle, isMarkdown } from "../../lib/artifacts";
import { Artifact, WorkspaceResult } from "../../lib/protocol";
import { useStore } from "../../lib/store";
import { agentLabel, font, Palette, radius, size, space } from "../../lib/theme";

type Review =
  | { step: "idle" }
  | { step: "composing" }
  | { step: "sending"; approve: boolean }
  | { step: "sent"; approve: boolean }
  | { step: "failed"; error: string };

/**
 * One artifact: a plan rendered as the document it is, an image on the photo
 * viewer, anything else as source. When the agent asked for a review, the
 * answer is docked at the foot — Approve, or Request changes with an optional
 * note — the way a question takes the composer's place in a session.
 */
export default function ArtifactViewScreen() {
  const { session: sessionParam, name: nameParam } = useLocalSearchParams<{
    session: string;
    name: string;
  }>();
  const sessionId = typeof sessionParam === "string" ? sessionParam : "";
  const name = typeof nameParam === "string" ? nameParam : "";
  const store = useStore();
  const session = store.findSession(sessionId);
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [result, setResult] = useState<WorkspaceResult | null>(null);
  // The list entry, for the heading, the kind and whether a review is asked
  // for. Read fresh rather than passed in, so a plan approved a moment ago
  // never offers its buttons again.
  const [meta, setMeta] = useState<Artifact | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  const [review, setReview] = useState<Review>({ step: "idle" });
  const [comment, setComment] = useState("");

  useEffect(() => {
    let current = true;
    setLoading(true);
    setError("");
    void store
      .readArtifact(sessionId, name)
      .then((value) => {
        if (current) setResult(value);
      })
      .catch((reason) => {
        if (current) setError(reason instanceof Error ? reason.message : String(reason));
      })
      .finally(() => {
        if (current) setLoading(false);
      });
    void store
      .listArtifacts(sessionId)
      .then((list) => {
        if (current) setMeta(list.find((artifact) => artifact.name === name) ?? null);
      })
      // Without it the artifact still reads; there is just no review to offer.
      .catch(() => {});
    return () => {
      current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, name, refresh]);

  const kind = artifactKind(meta?.kind ?? (result?.image ? "image" : "file"));
  const title = meta ? artifactTitle(meta) : name;
  const agent = session ? agentLabel(session.kind).name : "The agent";
  const imageUri =
    result?.image && result.mime ? `data:${result.mime};base64,${result.image}` : "";
  const markdown = !imageUri && isMarkdown(name, result?.mime);

  const send = (approve: boolean) => {
    setReview({ step: "sending", approve });
    store
      .reviewArtifact(sessionId, name, approve, approve ? undefined : comment)
      .then(() => {
        setReview({ step: "sent", approve });
        setComment("");
      })
      .catch((reason: Error) => setReview({ step: "failed", error: reason.message }));
  };

  const showReview = meta?.review === true || review.step === "sent";

  return (
    <KeyboardAvoidingView
      style={[styles.page, { paddingTop: insets.top }]}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <ContentColumn style={styles.header}>
        <MotionPressable
          onPress={() => router.back()}
          style={styles.iconButton}
          accessibilityRole="button"
          accessibilityLabel="Back to artifacts"
        >
          <Feather name="chevron-left" size={20} color={color.text} />
        </MotionPressable>
        <View style={styles.kindTile}>
          <Feather name={kind.icon} size={15} color={color.muted} />
        </View>
        <View style={styles.heading}>
          <Text style={styles.title} numberOfLines={1} accessibilityRole="header">
            {title}
          </Text>
          <Text style={styles.subtitle} numberOfLines={1} ellipsizeMode="middle">
            {name}
          </Text>
        </View>
        <MotionPressable
          onPress={() => setRefresh((value) => value + 1)}
          style={styles.iconButton}
          accessibilityRole="button"
          accessibilityLabel="Reload artifact"
        >
          <Feather name="refresh-cw" size={17} color={color.text} />
        </MotionPressable>
      </ContentColumn>

      <View style={styles.body}>
        {loading && !result ? (
          <ActivityIndicator style={styles.center} color={color.muted} />
        ) : null}
        {error ? (
          <ContentColumn style={styles.message}>
            <Text style={styles.error}>{readError(error, kind.label)}</Text>
          </ContentColumn>
        ) : null}

        {!error && imageUri ? (
          // The picture gets the rest of the screen, on the photo viewer's own
          // ground, so it pinches and pans like any other image in the app.
          <ImageViewer uri={imageUri} onDismiss={() => router.back()} />
        ) : null}

        {!error && result && !imageUri ? (
          <ScrollView
            contentContainerStyle={[
              styles.document,
              { paddingBottom: (showReview ? space.lg : insets.bottom) + space.xl },
            ]}
          >
            {markdown ? (
              <ContentColumn style={styles.markdown}>
                <Markdown>{result.text ?? ""}</Markdown>
              </ContentColumn>
            ) : (
              <SyntaxCode source={result.text ?? ""} filename={name} wrap />
            )}
            {result.truncated ? (
              <ContentColumn style={styles.message}>
                <Text style={styles.note}>
                  Preview truncated. The whole artifact stays on your Mac.
                </Text>
              </ContentColumn>
            ) : null}
          </ScrollView>
        ) : null}
      </View>

      {showReview ? (
        <View style={[styles.sheet, { paddingBottom: insets.bottom + space.md }]}>
          <ContentColumn style={styles.sheetContent}>
            <ReviewPanel
              agent={agent}
              kind={kind.label.toLowerCase()}
              review={review}
              comment={comment}
              onComment={setComment}
              onCompose={() => setReview({ step: "composing" })}
              onCancel={() => setReview({ step: "idle" })}
              onSend={send}
              disabled={!store.daemonOnline}
            />
          </ContentColumn>
        </View>
      ) : null}
    </KeyboardAvoidingView>
  );
}

/** The Mac's refusal, in words about the artifact rather than about files. */
function readError(error: string, kind: string): string {
  if (/binary|cannot be resized|too large/i.test(error)) {
    return `This ${kind.toLowerCase()} can't be shown on a phone. It stays on your Mac.`;
  }
  return error;
}

function ReviewPanel({
  agent,
  kind,
  review,
  comment,
  onComment,
  onCompose,
  onCancel,
  onSend,
  disabled,
}: {
  agent: string;
  kind: string;
  review: Review;
  comment: string;
  onComment(text: string): void;
  onCompose(): void;
  onCancel(): void;
  onSend(approve: boolean): void;
  disabled: boolean;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();

  if (review.step === "sent") {
    return (
      <View style={styles.sent} accessibilityRole="text" accessibilityLiveRegion="polite">
        <Feather name="check-circle" size={16} color={color.ok} />
        <Text style={styles.sentText}>
          {review.approve
            ? `Approved. ${agent} has your answer.`
            : `Changes requested. ${agent} has your note.`}
        </Text>
      </View>
    );
  }

  const sending = review.step === "sending";
  const composing = review.step === "composing" || (sending && !review.approve);

  return (
    <View style={styles.panel}>
      <View style={styles.eyebrowRow}>
        <View style={styles.pill}>
          <View style={styles.pillDot} />
          <Text style={styles.pillLabel}>Needs your review</Text>
        </View>
      </View>
      <Text style={styles.prompt}>
        {composing
          ? `Tell ${agent} what to change. A note is optional.`
          : `${agent} is waiting for you to approve this ${kind} before it goes on.`}
      </Text>

      {composing ? (
        <TextInput
          style={styles.input}
          value={comment}
          onChangeText={onComment}
          placeholder="What should change?"
          placeholderTextColor={color.faint}
          multiline
          autoFocus
          editable={!sending}
          accessibilityLabel="What should change"
        />
      ) : null}

      {review.step === "failed" ? (
        <Text style={styles.failed} accessibilityRole="alert">
          {review.error}
        </Text>
      ) : null}
      {disabled ? (
        <Text style={styles.offline}>Your Mac is offline. Reviews send when it reconnects.</Text>
      ) : null}

      <View style={styles.buttons}>
        <MotionPressable
          onPress={composing ? onCancel : onCompose}
          disabled={sending}
          style={[styles.button, styles.buttonSecondary, sending && styles.buttonDisabled]}
          accessibilityRole="button"
        >
          <Text style={styles.buttonLabel}>{composing ? "Cancel" : "Request changes"}</Text>
        </MotionPressable>
        <MotionPressable
          onPress={() => onSend(!composing)}
          disabled={sending || disabled}
          style={[styles.button, styles.buttonPrimary, (sending || disabled) && styles.buttonDisabled]}
          accessibilityRole="button"
          accessibilityState={{ busy: sending, disabled: sending || disabled }}
        >
          {sending ? (
            <ActivityIndicator size="small" color={color.onInverse} />
          ) : (
            <Text style={[styles.buttonLabel, styles.buttonLabelPrimary]}>
              {composing ? "Send" : "Approve"}
            </Text>
          )}
        </MotionPressable>
      </View>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    header: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingHorizontal: space.lg,
      paddingTop: space.sm,
      paddingBottom: space.md,
    },
    heading: { flex: 1, minWidth: 0 },
    title: {
      color: c.text,
      fontFamily: font.sansBold,
      fontSize: size.title,
      letterSpacing: -0.3,
    },
    subtitle: { color: c.muted, fontFamily: font.mono, fontSize: 11, marginTop: 1 },
    iconButton: {
      width: 36,
      height: 36,
      alignItems: "center",
      justifyContent: "center",
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    kindTile: {
      width: 30,
      height: 30,
      borderRadius: radius.sm,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
    },

    body: { flex: 1 },
    document: { paddingTop: space.xs },
    markdown: { paddingHorizontal: space.lg },
    center: { marginTop: space.xl },
    message: { paddingHorizontal: space.lg, paddingTop: space.md },
    error: { color: c.errorText, fontFamily: font.sans, fontSize: size.body, lineHeight: 21 },
    note: { color: c.muted, fontFamily: font.sans, fontSize: size.caption },

    // The session's question sheet, reused: docked, rounded on top, and on the
    // surface colour so it reads as something to act on rather than more page.
    sheet: {
      backgroundColor: c.surface,
      borderTopLeftRadius: radius.sheet,
      borderTopRightRadius: radius.sheet,
      paddingTop: space.lg,
      shadowColor: c.shadow,
      shadowOpacity: 0.12,
      shadowRadius: 24,
      shadowOffset: { width: 0, height: -6 },
      elevation: 12,
    },
    sheetContent: { paddingHorizontal: 20 },
    panel: { gap: space.md },
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
    prompt: {
      fontFamily: font.sans,
      fontSize: size.body,
      lineHeight: 21,
      color: c.textSecondary,
    },
    input: {
      minHeight: 84,
      maxHeight: 160,
      borderRadius: radius.lg,
      backgroundColor: c.fill,
      color: c.text,
      fontFamily: font.sans,
      fontSize: size.body,
      paddingHorizontal: space.lg,
      paddingTop: space.md,
      paddingBottom: space.md,
      textAlignVertical: "top",
    },
    failed: { fontFamily: font.sans, fontSize: size.caption, lineHeight: 18, color: c.errorText },
    offline: { fontFamily: font.sans, fontSize: size.caption, lineHeight: 18, color: c.muted },
    buttons: { flexDirection: "row", gap: space.sm },
    button: {
      flex: 1,
      minHeight: 48,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
    },
    buttonPrimary: { backgroundColor: c.inverse },
    buttonSecondary: { backgroundColor: c.surface, borderWidth: 1, borderColor: c.fillStrong },
    buttonDisabled: { opacity: 0.4 },
    buttonLabel: { fontFamily: font.sansBold, fontSize: size.body, color: c.text },
    buttonLabelPrimary: { color: c.onInverse },
    sent: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      minHeight: 44,
      paddingHorizontal: space.md,
      borderRadius: radius.md,
      backgroundColor: c.okWash,
    },
    sentText: {
      flex: 1,
      fontFamily: font.sansMedium,
      fontSize: size.caption,
      lineHeight: 18,
      color: c.text,
    },
  });
