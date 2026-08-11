import { CameraView, useCameraPermissions } from "expo-camera";
import { useRouter } from "expo-router";
import { useCallback, useRef, useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { pairWithToken } from "../lib/client";
import { parsePairingPayload } from "../lib/pairing";
import { useStore } from "../lib/store";
import { color, font, radius, size, space } from "../lib/theme";

/**
 * Pairing by camera.
 *
 * Scanning is the better path in both directions: nothing to type, and the
 * secret behind it is long enough that guessing it is not a threat, so it
 * skips the rate limiting the eight-digit code needs. Typing stays available
 * for anyone whose camera is unavailable or who is reading the code over a
 * remote shell.
 */
export default function Scan() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [permission, requestPermission] = useCameraPermissions();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // A camera fires this continuously while the code is in frame; without a
  // latch the same pairing is redeemed several times and every attempt after
  // the first fails, replacing a success with an error on screen.
  const handled = useRef(false);

  const onScanned = useCallback(
    async ({ data }: { data: string }) => {
      if (handled.current) return;
      const payload = parsePairingPayload(data);
      if (!payload) return; // not one of ours; keep looking

      handled.current = true;
      setBusy(true);
      try {
        const creds = await pairWithToken(payload.relayUrl, payload.token);
        store.signIn(creds);
        router.replace("/");
      } catch (err) {
        setError(err instanceof Error ? err.message : "That code did not work.");
        setBusy(false);
        // Allow another attempt: the code may simply have expired while the
        // camera was being lined up.
        handled.current = false;
      }
    },
    [router, store],
  );

  if (!permission) {
    return <View style={styles.page} />;
  }

  if (!permission.granted) {
    return (
      <View style={[styles.page, styles.centred, { paddingTop: insets.top }]}>
        <Text style={styles.title}>Camera access</Text>
        <Text style={styles.body}>
          Scanning needs the camera. Nothing is recorded — the frame is read for a code
          and discarded.
        </Text>
        <Pressable style={styles.button} onPress={() => void requestPermission()}>
          <Text style={styles.buttonText}>Allow camera</Text>
        </Pressable>
        <Pressable onPress={() => router.back()}>
          <Text style={styles.link}>Type the code instead</Text>
        </Pressable>
      </View>
    );
  }

  return (
    <View style={styles.page}>
      <CameraView
        style={StyleSheet.absoluteFill}
        facing="back"
        barcodeScannerSettings={{ barcodeTypes: ["qr"] }}
        onBarcodeScanned={busy ? undefined : onScanned}
      />

      <View style={[styles.overlay, { paddingTop: insets.top + space.lg }]}>
        <Text style={styles.overlayTitle}>
          {busy ? "Pairing…" : "Point at the code in your terminal"}
        </Text>
        <View style={styles.reticle} />
        {error ? <Text style={styles.error}>{error}</Text> : null}
        <Pressable
          onPress={() => router.back()}
          style={styles.dismiss}
          accessibilityRole="button"
        >
          <Text style={styles.link}>Type the code instead</Text>
        </Pressable>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: color.ink },
  centred: { justifyContent: "center", paddingHorizontal: space.xl, gap: space.md },

  overlay: {
    ...StyleSheet.absoluteFillObject,
    alignItems: "center",
    justifyContent: "space-between",
    paddingBottom: space.xxl,
  },
  overlayTitle: {
    fontFamily: font.sansMedium,
    fontSize: size.body,
    color: color.text,
    // The camera feed is arbitrary, so the label carries its own contrast.
    backgroundColor: "rgba(11,13,18,0.75)",
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
    borderRadius: radius.md,
    overflow: "hidden",
  },
  reticle: {
    width: 240,
    height: 240,
    borderRadius: radius.lg,
    borderWidth: 2,
    borderColor: color.working,
  },

  title: { fontFamily: font.sansBold, fontSize: size.title, color: color.text },
  body: { fontFamily: font.sans, fontSize: size.body, color: color.muted, lineHeight: 22 },
  error: {
    fontFamily: font.sans,
    fontSize: size.caption,
    color: color.error,
    backgroundColor: "rgba(11,13,18,0.85)",
    padding: space.sm,
    borderRadius: radius.sm,
    overflow: "hidden",
  },

  button: {
    backgroundColor: color.working,
    borderRadius: radius.md,
    paddingVertical: space.md,
    alignItems: "center",
  },
  buttonText: { fontFamily: font.sansBold, fontSize: size.body, color: color.ink },
  dismiss: { padding: space.md },
  link: { fontFamily: font.sansMedium, fontSize: size.body, color: color.working },
});
