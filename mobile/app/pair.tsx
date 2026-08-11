import { useRouter } from "expo-router";
import { useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { pair } from "../lib/client";
import { DEFAULT_RELAY } from "../lib/pairing";
import { useStore } from "../lib/store";
import { PAIRING_CODE_LENGTH } from "../lib/protocol";
import { color, font, radius, size, space } from "../lib/theme";

export default function Pair() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [relayUrl, setRelayUrl] = useState(DEFAULT_RELAY);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const canSubmit = relayUrl.trim().length > 0 && code.replace(/\D/g, "").length === PAIRING_CODE_LENGTH;

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const creds = await pair(relayUrl, code);
      store.signIn(creds);
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
          { paddingTop: insets.top + space.xxl, paddingBottom: insets.bottom + space.xl },
        ]}
        keyboardShouldPersistTaps="handled"
      >
        <Text style={styles.wordmark}>agentman</Text>
        <Text style={styles.lede}>
          Watch your coding agents from anywhere, and send them your next instruction.
        </Text>

        <View style={styles.steps}>
          <Text style={styles.stepLabel}>On your Mac</Text>
          <Text style={styles.stepBody}>
            Run <Text style={styles.code}>am pair</Text>. It prints a QR code and ten
            digits, both good for one minute.
          </Text>
        </View>

        <Pressable
          onPress={() => router.push("/scan")}
          style={({ pressed }) => [styles.button, pressed && styles.buttonPressed]}
        >
          <Text style={styles.buttonText}>Scan the QR code</Text>
        </Pressable>

        <View style={styles.divider}>
          <View style={styles.dividerLine} />
          <Text style={styles.dividerLabel}>or type it</Text>
          <View style={styles.dividerLine} />
        </View>

        <View style={styles.field}>
          <Text style={styles.fieldLabel}>Relay address</Text>
          <TextInput
            style={styles.input}
            value={relayUrl}
            onChangeText={setRelayUrl}
            placeholder="relay.example.com"
            placeholderTextColor={color.faint}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="url"
            inputMode="url"
          />
        </View>

        <View style={styles.field}>
          <Text style={styles.fieldLabel}>Pairing code</Text>
          <TextInput
            style={[styles.input, styles.codeInput]}
            value={code}
            onChangeText={(text) =>
              setCode(text.replace(/\D/g, "").slice(0, PAIRING_CODE_LENGTH))
            }
            placeholder="0000000000"
            placeholderTextColor={color.faint}
            keyboardType="number-pad"
            maxLength={PAIRING_CODE_LENGTH}
          />
        </View>

        {error && <Text style={styles.error}>{error}</Text>}

        <Pressable
          onPress={submit}
          disabled={!canSubmit || busy}
          style={({ pressed }) => [
            styles.button,
            (!canSubmit || busy) && styles.buttonDisabled,
            pressed && styles.buttonPressed,
          ]}
        >
          {busy ? (
            <ActivityIndicator color={color.ink} />
          ) : (
            <Text style={styles.buttonText}>Pair with this code</Text>
          )}
        </Pressable>

        <Text style={styles.footnote}>
          The relay does not persist transcripts, but it can see them while forwarding.
          Self-host if you do not trust its operator with live traffic.
        </Text>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: color.ink },
  content: { paddingHorizontal: space.xl, gap: space.lg },

  wordmark: {
    fontFamily: font.mono,
    fontSize: 32,
    color: color.text,
    letterSpacing: -0.5,
  },
  lede: {
    fontFamily: font.sans,
    fontSize: size.body,
    color: color.muted,
    lineHeight: 22,
    marginTop: -space.sm,
  },

  steps: {
    backgroundColor: color.surface,
    borderRadius: radius.md,
    padding: space.md,
    gap: space.xs,
    marginTop: space.sm,
  },
  stepLabel: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.4,
    textTransform: "uppercase",
    color: color.faint,
  },
  stepBody: { fontFamily: font.sans, fontSize: size.body, color: color.text, lineHeight: 21 },
  code: { fontFamily: font.mono, color: color.working },

  field: { gap: space.sm },
  fieldLabel: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.4,
    textTransform: "uppercase",
    color: color.faint,
  },
  input: {
    backgroundColor: color.surface,
    borderRadius: radius.md,
    paddingHorizontal: space.md,
    paddingVertical: space.md,
    fontFamily: font.mono,
    fontSize: size.body,
    color: color.text,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  codeInput: { fontSize: 24, letterSpacing: 6, textAlign: "center" },

  error: { fontFamily: font.sans, fontSize: size.caption, color: color.error },

  button: {
    backgroundColor: color.working,
    borderRadius: radius.md,
    paddingVertical: space.md,
    alignItems: "center",
    marginTop: space.sm,
  },
  buttonDisabled: { backgroundColor: color.line },
  buttonPressed: { opacity: 0.8 },
  divider: { flexDirection: "row", alignItems: "center", gap: space.md },
  dividerLine: { flex: 1, height: StyleSheet.hairlineWidth, backgroundColor: color.line },
  dividerLabel: { fontFamily: font.sans, fontSize: size.caption, color: color.faint },
  buttonText: { fontFamily: font.sansBold, fontSize: size.body, color: color.ink },

  footnote: {
    fontFamily: font.sans,
    fontSize: size.caption,
    color: color.faint,
    lineHeight: 18,
  },
});
