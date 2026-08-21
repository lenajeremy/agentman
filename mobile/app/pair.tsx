import * as Haptics from "expo-haptics";
import Feather from "@expo/vector-icons/Feather";
import Ionicons from "@expo/vector-icons/Ionicons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Appear } from "../components/Appear";
import { ContentColumn } from "../components/ContentColumn";
import { MotionPressable } from "../components/MotionPressable";
import { pair, pairWithToken } from "../lib/client";
import { DEFAULT_RELAY } from "../lib/pairing";
import { useStore } from "../lib/store";
import { PAIRING_CODE_LENGTH } from "../lib/protocol";
import { color, font, radius, size, space } from "../lib/theme";

export default function Pair() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
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
            paddingTop: insets.top + space.xl,
            paddingBottom: insets.bottom + space.xl,
          },
        ]}
        keyboardShouldPersistTaps="handled"
      >
        <ContentColumn narrow style={styles.column}>
          <Appear>
            <View style={styles.brandRow}>
              <View style={styles.brandMark}>
                <Text style={styles.brandMonogram}>am</Text>
              </View>
              <Text style={styles.wordmark}>
                agentman<Text style={styles.wordmarkAccent}>.</Text>
              </Text>
            </View>
            <Text style={styles.title}>Take your agents with you.</Text>
            <Text style={styles.lede}>
              Pair this phone once to monitor live work, answer questions, and send the
              next instruction from anywhere.
            </Text>
          </Appear>

          <Appear delay={45}>
            <View style={styles.stepCard}>
              <View style={styles.stepNumber}>
                <Text style={styles.stepNumberText}>1</Text>
              </View>
              <View style={styles.stepCopy}>
                <Text style={styles.stepLabel}>Open a terminal on your Mac</Text>
                <Text style={styles.stepBody}>
                  Run <Text style={styles.code}>am pair</Text>. The QR code and
                  ten-digit fallback expire after one minute.
                </Text>
              </View>
            </View>
          </Appear>

          <Appear delay={90}>
            <MotionPressable
              onPress={() => router.push("/scan")}
              style={styles.scanButton}
              accessibilityRole="button"
            >
              <Ionicons name="qr-code-outline" size={24} color={color.ink} />
              <View style={styles.buttonCopy}>
                <Text style={styles.buttonText}>Scan the QR code</Text>
                <Text style={styles.buttonHint}>Fastest and most secure</Text>
              </View>
              <Feather name="chevron-right" size={20} color={color.ink} />
            </MotionPressable>
          </Appear>

          <View style={styles.divider}>
            <View style={styles.dividerLine} />
            <Text style={styles.dividerLabel}>or enter the fallback code</Text>
            <View style={styles.dividerLine} />
          </View>

          <View style={styles.formCard}>
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
              />
            </View>

            {error ? (
              <View style={styles.errorBox} accessibilityRole="alert">
                <Feather name="alert-circle" size={14} color={color.error} />
                <Text style={styles.error}>{error}</Text>
              </View>
            ) : null}

            <MotionPressable
              onPress={submit}
              disabled={!canSubmit || busy}
              style={[
                styles.pairButton,
                (!canSubmit || busy) && styles.buttonDisabled,
              ]}
              accessibilityRole="button"
              accessibilityState={{ disabled: !canSubmit || busy, busy }}
            >
              {busy ? (
                <ActivityIndicator color={color.ink} />
              ) : (
                <Text style={styles.pairButtonText}>Pair with this code</Text>
              )}
            </MotionPressable>
          </View>

          <View style={styles.privacyNote}>
            <View style={styles.privacyMark}>
              <View style={styles.lockBody} />
              <View style={styles.lockLoop} />
            </View>
            <Text style={styles.footnote}>
              Transcripts stay on your Mac. Live traffic passes through your relay, so
              use an operator you trust or self-host.
            </Text>
          </View>
        </ContentColumn>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function firstParam(value: string | string[] | undefined): string {
  return Array.isArray(value) ? value[0] ?? "" : value ?? "";
}

const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: color.ink },
  content: { flexGrow: 1, paddingHorizontal: space.lg },
  column: { gap: space.lg },

  brandRow: { flexDirection: "row", alignItems: "center", gap: space.md },
  brandMark: {
    width: 38,
    height: 38,
    borderRadius: radius.md,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.working,
  },
  brandMonogram: {
    fontFamily: font.monoMedium,
    fontSize: size.body,
    color: color.ink,
    letterSpacing: -0.5,
  },

  wordmark: {
    fontFamily: font.mono,
    fontSize: 24,
    color: color.text,
    letterSpacing: -0.8,
  },
  wordmarkAccent: { color: color.working },
  title: {
    fontFamily: font.sansBold,
    fontSize: 31,
    lineHeight: 36,
    letterSpacing: -0.8,
    color: color.text,
    marginTop: space.xl,
  },
  lede: {
    fontFamily: font.sans,
    fontSize: size.body,
    color: color.muted,
    lineHeight: 23,
    marginTop: space.sm,
  },

  stepCard: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: space.md,
    backgroundColor: color.surface,
    borderRadius: radius.lg,
    padding: space.lg,
    borderWidth: 1,
    borderColor: color.line,
  },
  stepNumber: {
    width: 28,
    height: 28,
    borderRadius: 14,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.workingWash,
  },
  stepNumberText: {
    fontFamily: font.monoMedium,
    fontSize: size.caption,
    color: color.working,
  },
  stepCopy: { flex: 1, gap: space.xs },
  stepLabel: {
    fontFamily: font.sansMedium,
    fontSize: size.body,
    color: color.text,
  },
  stepBody: {
    fontFamily: font.sans,
    fontSize: size.caption,
    color: color.muted,
    lineHeight: 19,
  },
  code: { fontFamily: font.mono, color: color.working },

  scanButton: {
    minHeight: 68,
    flexDirection: "row",
    alignItems: "center",
    gap: space.md,
    backgroundColor: color.working,
    borderRadius: radius.xxl,
    paddingVertical: space.md,
    paddingHorizontal: space.lg,
  },
  buttonCopy: { flex: 1 },
  buttonText: { fontFamily: font.sansBold, fontSize: size.body, color: color.ink },
  buttonHint: {
    fontFamily: font.sans,
    fontSize: size.label,
    color: "#183543",
    marginTop: 1,
  },

  field: { gap: space.sm },
  fieldLabel: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.4,
    textTransform: "uppercase",
    color: color.faint,
  },
  input: {
    minHeight: 52,
    backgroundColor: color.sunken,
    borderRadius: radius.lg,
    paddingHorizontal: space.lg,
    paddingVertical: space.md,
    fontFamily: font.mono,
    fontSize: size.body,
    color: color.text,
    borderWidth: 1,
    borderColor: color.line,
  },
  inputFocused: { borderColor: color.working },
  codeInput: { fontSize: 24, letterSpacing: 6, textAlign: "center" },

  formCard: {
    gap: space.lg,
    backgroundColor: color.surface,
    borderRadius: radius.lg,
    padding: space.lg,
    borderWidth: 1,
    borderColor: color.line,
  },
  errorBox: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: space.sm,
    borderRadius: radius.lg,
    padding: space.md,
    backgroundColor: color.errorWash,
    borderWidth: 1,
    borderColor: "#563039",
  },
  error: { flex: 1, fontFamily: font.sans, fontSize: size.caption, color: color.error },

  pairButton: {
    minHeight: 52,
    backgroundColor: color.working,
    borderRadius: radius.pill,
    alignItems: "center",
    justifyContent: "center",
  },
  buttonDisabled: { backgroundColor: color.line },
  divider: { flexDirection: "row", alignItems: "center", gap: space.md },
  dividerLine: { flex: 1, height: StyleSheet.hairlineWidth, backgroundColor: color.line },
  dividerLabel: { fontFamily: font.sans, fontSize: size.caption, color: color.faint },
  pairButtonText: { fontFamily: font.sansBold, fontSize: size.body, color: color.ink },

  privacyNote: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: space.md,
    padding: space.sm,
  },
  privacyMark: {
    width: 28,
    height: 28,
    borderRadius: 14,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.okWash,
  },
  lockBody: {
    width: 9,
    height: 7,
    borderRadius: 2,
    backgroundColor: color.ok,
    marginTop: 5,
  },
  lockLoop: {
    position: "absolute",
    width: 7,
    height: 7,
    top: 7,
    borderWidth: 1.5,
    borderColor: color.ok,
    borderRadius: 5,
  },
  footnote: {
    flex: 1,
    fontFamily: font.sans,
    fontSize: size.caption,
    color: color.faint,
    lineHeight: 18,
  },
});

