import * as Clipboard from "expo-clipboard";
import * as Haptics from "expo-haptics";
import Feather from "@expo/vector-icons/Feather";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Linking,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Appear } from "../components/Appear";
import { Wordmark } from "../components/BrandMark";
import { ContentColumn } from "../components/ContentColumn";
import { PairIllustration } from "../components/Illustrations";
import { MotionPressable } from "../components/MotionPressable";
import { useStyles, useTheme } from "../lib/appearance";
import { pair, pairWithToken } from "../lib/client";
import { DEFAULT_RELAY } from "../lib/pairing";
import { PRIVACY_POLICY_URL } from "../lib/privacy";
import { SETUP_GUIDE_URL, SETUP_STEPS } from "../lib/setup-guide";
import { useStore } from "../lib/store";
import { PAIRING_CODE_LENGTH } from "../lib/protocol";
import { font, Palette, radius, size, space } from "../lib/theme";

export default function Pair() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const params = useLocalSearchParams<{
    relay?: string | string[];
    token?: string | string[];
  }>();
  const [relayUrl, setRelayUrl] = useState(DEFAULT_RELAY);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [relayFocused, setRelayFocused] = useState(false);
  const [codeFocused, setCodeFocused] = useState(false);
  /** Typing is the fallback, so it stays folded away until asked for. */
  const [manual, setManual] = useState(false);
  const handledLink = useRef<string | null>(null);

  useEffect(() => {
    if (!store.ready) return;
    const token = firstParam(params.token);
    if (!token) return;
    const relay = firstParam(params.relay) || DEFAULT_RELAY;
    const identity = `${relay}\u0000${token}`;
    if (handledLink.current === identity) return;
    handledLink.current = identity;

    const redeem = async () => {
      setBusy(true);
      setError(null);
      try {
        const creds = await pairWithToken(relay, token);
        store.signIn(creds);
        if (Platform.OS !== "web") {
          void Haptics.notificationAsync(
            Haptics.NotificationFeedbackType.Success,
          ).catch(() => {});
        }
        router.replace("/");
      } catch (err) {
        setError(
          err instanceof Error
            ? err.message
            : "Could not redeem this pairing link. Generate a fresh link and try again.",
        );
      } finally {
        setBusy(false);
      }
    };

    if (!store.credentials) {
      void redeem();
      return;
    }

    Alert.alert(
      "Pair with a different Mac?",
      "This will replace the relay currently paired with Agentman on this phone.",
      [
        {
          text: "Cancel",
          style: "cancel",
          onPress: () => router.replace("/"),
        },
        {
          text: "Pair",
          onPress: () => void redeem(),
        },
      ],
      { cancelable: false },
    );
  }, [params.relay, params.token, router, store]);

  const canSubmit =
    store.ready &&
    relayUrl.trim().length > 0 &&
    code.replace(/\D/g, "").length === PAIRING_CODE_LENGTH;

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const creds = await pair(relayUrl, code);
      store.signIn(creds);
      if (Platform.OS !== "web") {
        void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success).catch(
          () => {},
        );
      }
      router.replace("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Pairing failed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <KeyboardAvoidingView
      style={styles.page}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <ScrollView
        contentContainerStyle={[
          styles.content,
          {
            paddingTop: insets.top + space.md,
            paddingBottom: insets.bottom + space.xl,
          },
        ]}
        keyboardShouldPersistTaps="handled"
      >
        <ContentColumn narrow style={styles.column}>
          <Appear>
            <Wordmark />
          </Appear>

          <Appear delay={45}>
            <PairIllustration style={styles.art} />
            <Text style={styles.title}>Pair with your Mac</Text>
            <Text style={styles.lede}>
              Run <Text style={styles.ledeCode}>am pair</Text> on the computer where your agents
              work, and scan the code it shows. New to Agentman? The setup steps are below.
            </Text>
          </Appear>

          {error && !manual ? <ErrorBox message={error} /> : null}

          <Appear delay={110} style={styles.actions}>
            <MotionPressable
              onPress={() => router.push("/scan")}
              style={styles.primaryButton}
              accessibilityRole="button"
              accessibilityHint="Opens the camera to read the code from your terminal"
            >
              <Feather name="maximize" size={18} color={color.onInverse} />
              <Text style={styles.primaryButtonText}>Scan QR code</Text>
            </MotionPressable>
            {!manual ? (
              <MotionPressable
                onPress={() => setManual(true)}
                style={styles.textButton}
                accessibilityRole="button"
              >
                <Text style={styles.textButtonLabel}>Enter the code instead</Text>
              </MotionPressable>
            ) : null}
          </Appear>

          {manual ? (
            <Appear style={styles.card}>
              <View style={styles.field}>
                <Text style={styles.fieldLabel}>Relay address</Text>
                <TextInput
                  style={[styles.input, relayFocused && styles.inputFocused]}
                  value={relayUrl}
                  onChangeText={setRelayUrl}
                  onFocus={() => setRelayFocused(true)}
                  onBlur={() => setRelayFocused(false)}
                  placeholder="relay.example.com"
                  placeholderTextColor={color.faint}
                  autoCapitalize="none"
                  autoCorrect={false}
                  keyboardType="url"
                  inputMode="url"
                  accessibilityLabel="Relay address"
                />
              </View>

              <View style={styles.field}>
                <Text style={styles.fieldLabel}>Pairing code</Text>
                <TextInput
                  style={[
                    styles.input,
                    styles.codeInput,
                    codeFocused && styles.inputFocused,
                  ]}
                  value={code}
                  onChangeText={(text) =>
                    setCode(text.replace(/\D/g, "").slice(0, PAIRING_CODE_LENGTH))
                  }
                  onFocus={() => setCodeFocused(true)}
                  onBlur={() => setCodeFocused(false)}
                  placeholder="0000000000"
                  placeholderTextColor={color.faint}
                  keyboardType="number-pad"
                  maxLength={PAIRING_CODE_LENGTH}
                  accessibilityLabel="Pairing code"
                  autoFocus
                />
              </View>

              {error ? <ErrorBox message={error} /> : null}

              <MotionPressable
                onPress={submit}
                disabled={!canSubmit || busy}
                style={[
                  styles.primaryButton,
                  (!canSubmit || busy) && styles.buttonDisabled,
                ]}
                accessibilityRole="button"
                accessibilityState={{ disabled: !canSubmit || busy, busy }}
              >
                {busy ? (
                  <ActivityIndicator color={color.onInverse} />
                ) : (
                  <Text style={styles.primaryButtonText}>Pair with this code</Text>
                )}
              </MotionPressable>
            </Appear>
          ) : null}

          <Appear delay={150}>
            <SetupGuide />
          </Appear>

          <View style={styles.disclosure}>
            <Text style={styles.footnote}>
              Codes expire in 60 seconds. Live traffic passes through your relay.
            </Text>
            <MotionPressable
              onPress={() => void Linking.openURL(PRIVACY_POLICY_URL)}
              style={styles.policyLink}
              hitSlop={8}
              accessibilityRole="link"
              accessibilityLabel="Read Agentman privacy policy"
            >
              <Text style={styles.policyLinkText}>Privacy policy</Text>
            </MotionPressable>
          </View>
        </ContentColumn>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

/**
 * The steps on the computer before there is a code to scan. Commands copy with
 * a tap, and with Universal Clipboard paste straight into a Mac's terminal.
 */
function SetupGuide() {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [copied, setCopied] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);

  const copy = (command: string) => {
    void Clipboard.setStringAsync(command);
    if (Platform.OS !== "web") void Haptics.selectionAsync().catch(() => {});
    setCopied(command);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(null), 1600);
  };

  return (
    <View style={styles.guide}>
      <Text style={styles.guideTitle}>First time? Set up your computer</Text>
      {SETUP_STEPS.map((step, index) => (
        <View key={step.title} style={styles.step}>
          <View style={styles.stepNumber}>
            <Text style={styles.stepNumberText}>{index + 1}</Text>
          </View>
          <View style={styles.stepCopy}>
            <Text style={styles.stepTitle}>{step.title}</Text>
            <Text style={styles.stepDetail}>{step.detail}</Text>
            {step.command ? (
              <MotionPressable
                onPress={() => copy(step.command!)}
                pressedScale={0.98}
                style={styles.stepCommand}
                accessibilityRole="button"
                accessibilityLabel={`Copy ${step.command}`}
                accessibilityHint="Copies the command so you can paste it into a terminal"
              >
                <Text style={styles.commandPrompt}>$</Text>
                <Text style={styles.stepCommandText} numberOfLines={1}>
                  {step.command}
                </Text>
                <Feather
                  name={copied === step.command ? "check" : "copy"}
                  size={14}
                  color={copied === step.command ? color.working : color.faint}
                />
              </MotionPressable>
            ) : null}
          </View>
        </View>
      ))}
      <MotionPressable
        onPress={() => void Linking.openURL(SETUP_GUIDE_URL)}
        style={styles.guideLink}
        hitSlop={8}
        accessibilityRole="link"
      >
        <Text style={styles.guideLinkText}>The full setup guide</Text>
        <Feather name="arrow-up-right" size={14} color={color.textSecondary} />
      </MotionPressable>
    </View>
  );
}

function ErrorBox({ message }: { message: string }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.errorBox} accessibilityRole="alert">
      <Feather name="alert-circle" size={15} color={color.errorText} />
      <Text style={styles.error}>{message}</Text>
    </View>
  );
}

function firstParam(value: string | string[] | undefined): string {
  return Array.isArray(value) ? value[0] ?? "" : value ?? "";
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    content: { flexGrow: 1, paddingHorizontal: space.lg },
    column: { flex: 1, gap: space.lg },

    art: {
      width: "100%",
      aspectRatio: 340 / 214,
      marginTop: space.md,
      borderRadius: radius.sheet,
      overflow: "hidden",
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    title: {
      fontFamily: font.sansBold,
      fontSize: 30,
      lineHeight: 34,
      letterSpacing: -1,
      color: c.text,
      marginTop: space.xl,
    },
    ledeCode: { fontFamily: font.mono, fontSize: 14, color: c.text },
    lede: {
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.muted,
      lineHeight: 22,
      marginTop: space.sm,
    },
    commandPrompt: { fontFamily: font.mono, fontSize: 14, color: c.faint },

    guideTitle: { fontFamily: font.sansBold, fontSize: size.caption, color: c.muted },
    guide: {
      marginTop: space.sm,
      backgroundColor: c.surface,
      borderRadius: radius.xxl,
      borderWidth: 1,
      borderColor: c.line,
      padding: space.lg,
      gap: space.lg,
    },
    step: { flexDirection: "row", gap: space.md },
    stepNumber: {
      width: 26,
      height: 26,
      borderRadius: 8,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
      marginTop: 1,
    },
    stepNumberText: { fontFamily: font.mono, fontSize: 13, color: c.textSecondary },
    stepCopy: { flex: 1, minWidth: 0, gap: 3 },
    stepTitle: { fontFamily: font.sansBold, fontSize: size.body, color: c.text },
    stepDetail: { fontFamily: font.sans, fontSize: size.caption, lineHeight: 18, color: c.muted },
    stepCommand: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      marginTop: space.sm,
      paddingHorizontal: space.md,
      paddingVertical: 11,
      borderRadius: radius.lg,
      backgroundColor: c.fill,
    },
    stepCommandText: { flex: 1, fontFamily: font.mono, fontSize: 13, color: c.text },
    guideLink: { flexDirection: "row", alignItems: "center", gap: 6, alignSelf: "flex-start" },
    guideLinkText: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.textSecondary },

    actions: { gap: space.xs, marginTop: space.sm },
    primaryButton: {
      minHeight: 56,
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "center",
      gap: 10,
      borderRadius: radius.pill,
      backgroundColor: c.inverse,
      paddingHorizontal: space.xl,
    },
    primaryButtonText: { fontFamily: font.sansBold, fontSize: 16, color: c.onInverse },
    buttonDisabled: { opacity: 0.35 },
    textButton: {
      minHeight: 44,
      alignItems: "center",
      justifyContent: "center",
    },
    textButtonLabel: { fontFamily: font.sansMedium, fontSize: size.body, color: c.textSecondary },

    card: {
      backgroundColor: c.surface,
      borderRadius: radius.xxl,
      borderWidth: 1,
      borderColor: c.line,
      padding: space.lg,
      gap: space.lg,
    },
    field: { gap: space.sm },
    fieldLabel: {
      fontFamily: font.sansBold,
      fontSize: size.caption,
      color: c.muted,
    },
    input: {
      minHeight: 52,
      backgroundColor: c.fill,
      borderRadius: radius.lg,
      paddingHorizontal: space.lg,
      paddingVertical: space.md,
      fontFamily: font.mono,
      fontSize: size.body,
      color: c.text,
      borderWidth: 1,
      borderColor: "transparent",
    },
    inputFocused: { borderColor: c.working, backgroundColor: c.surface },
    codeInput: { fontSize: 24, letterSpacing: 6, textAlign: "center" },

    errorBox: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: space.sm,
      borderRadius: radius.lg,
      padding: space.md,
      backgroundColor: c.errorWash,
    },
    error: { flex: 1, fontFamily: font.sans, fontSize: size.caption, lineHeight: 18, color: c.errorText },

    disclosure: { marginTop: "auto", alignItems: "center", paddingTop: space.sm },
    footnote: {
      fontFamily: font.sans,
      fontSize: size.label,
      color: c.faint,
      lineHeight: 17,
      textAlign: "center",
    },
    policyLink: {
      minHeight: 32,
      alignItems: "center",
      justifyContent: "center",
      marginTop: space.xs,
      paddingHorizontal: space.md,
    },
    // Cobalt, like every other tappable thing. Green reads as a status, and
    // a policy link is not one.
    policyLinkText: { fontFamily: font.sansMedium, fontSize: size.label, color: c.workingText },
  });
