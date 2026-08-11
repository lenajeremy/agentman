import * as Haptics from "expo-haptics";
import { CameraView, useCameraPermissions } from "expo-camera";
import { useRouter } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Platform,
  StyleProp,
  StyleSheet,
  Text,
  View,
  ViewStyle,
} from "react-native";
import Animated, {
  Easing,
  cancelAnimation,
  useAnimatedStyle,
  useReducedMotion,
  useSharedValue,
  withRepeat,
  withTiming,
} from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Appear } from "../components/Appear";
import { ContentColumn } from "../components/ContentColumn";
import { MotionPressable } from "../components/MotionPressable";
import { pairWithToken } from "../lib/client";
import { parsePairingPayload } from "../lib/pairing";
import { useStore } from "../lib/store";
import { color, font, radius, size, space } from "../lib/theme";

/**
 * Pairing by camera.
 *
 * Scanning is the better path in both directions: nothing to type, and the
 * secret behind it is long enough that guessing it is not a threat, so it
 * skips the rate limiting the ten-digit code needs. Typing stays available
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
        if (Platform.OS !== "web") {
          void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success).catch(
            () => {},
          );
        }
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
      <View style={[styles.page, { paddingTop: insets.top, paddingBottom: insets.bottom }]}>
        <ContentColumn narrow style={styles.permissionContent}>
          <Appear style={styles.permissionCard}>
            <View style={styles.cameraIcon}>
              <View style={styles.cameraBody}>
                <View style={styles.cameraLens} />
              </View>
              <View style={styles.cameraTop} />
            </View>
            <Text style={styles.title}>Scan securely</Text>
            <Text style={styles.body}>
              Camera access lets Agentman read the one-time QR code from your terminal.
              Frames are never recorded or saved.
            </Text>
            <MotionPressable
              style={styles.button}
              onPress={() => void requestPermission()}
              accessibilityRole="button"
            >
              <Text style={styles.buttonText}>Allow camera</Text>
            </MotionPressable>
            <MotionPressable
              onPress={() => router.back()}
              style={styles.secondaryButton}
              accessibilityRole="button"
            >
              <Text style={styles.link}>Type the code instead</Text>
            </MotionPressable>
          </Appear>
        </ContentColumn>
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

      <View
        style={[
          styles.overlay,
          { paddingTop: insets.top + space.md, paddingBottom: insets.bottom + space.xl },
        ]}
      >
        <View style={styles.scanHeader}>
          <MotionPressable
            onPress={() => router.back()}
            style={styles.closeButton}
            pressedScale={0.92}
            accessibilityRole="button"
            accessibilityLabel="Close scanner"
          >
            <Text style={styles.closeGlyph}>×</Text>
          </MotionPressable>
          <View style={styles.scanHeading}>
            <Text style={styles.scanEyebrow}>Secure pairing</Text>
            <Text style={styles.overlayTitle}>{busy ? "Pairing…" : "Scan QR code"}</Text>
          </View>
          <View style={styles.headerSpacer} />
        </View>

        <View style={styles.scanCentre}>
          <Text style={styles.scanHint}>
            Hold the code inside the frame. It pairs automatically.
          </Text>
          <View style={styles.reticle}>
            <Corner style={styles.cornerTopLeft} />
            <Corner style={styles.cornerTopRight} />
            <Corner style={styles.cornerBottomLeft} />
            <Corner style={styles.cornerBottomRight} />
            {!busy ? <ScanLine /> : <ActivityIndicator color={color.working} />}
          </View>
          {error ? (
            <View style={styles.errorBox} accessibilityRole="alert">
              <Text style={styles.error}>{error}</Text>
            </View>
          ) : null}
        </View>

        <MotionPressable
          onPress={() => router.back()}
          style={styles.dismiss}
          accessibilityRole="button"
        >
          <Text style={styles.dismissText}>Type the code instead</Text>
        </MotionPressable>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: color.ink },
  permissionContent: { flex: 1, justifyContent: "center", paddingHorizontal: space.lg },
  permissionCard: {
    alignItems: "center",
    borderRadius: radius.xl,
    padding: space.xl,
    backgroundColor: color.surface,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },

  overlay: {
    ...StyleSheet.absoluteFillObject,
    alignItems: "center",
    justifyContent: "space-between",
    backgroundColor: "rgba(4, 7, 12, 0.25)",
  },
  scanHeader: {
    width: "100%",
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: space.lg,
  },
  closeButton: {
    width: 44,
    height: 44,
    borderRadius: 22,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.scrim,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: "rgba(255,255,255,0.18)",
  },
  closeGlyph: { fontFamily: font.sans, fontSize: 26, lineHeight: 28, color: color.text },
  scanHeading: {
    flex: 1,
    alignItems: "center",
    marginHorizontal: space.sm,
    paddingVertical: 6,
    paddingHorizontal: space.md,
    borderRadius: radius.md,
    backgroundColor: color.scrim,
  },
  scanEyebrow: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    textTransform: "uppercase",
    letterSpacing: 1.3,
    color: "#D1D8E5",
  },
  overlayTitle: {
    fontFamily: font.sansBold,
    fontSize: size.title,
    color: color.text,
  },
  headerSpacer: { width: 44 },
  scanCentre: { width: "100%", alignItems: "center", gap: space.lg },
  scanHint: {
    maxWidth: 290,
    fontFamily: font.sans,
    fontSize: size.caption,
    lineHeight: 18,
    textAlign: "center",
    color: color.text,
    backgroundColor: color.scrim,
    paddingVertical: space.sm,
    paddingHorizontal: space.md,
    borderRadius: radius.pill,
    overflow: "hidden",
  },
  reticle: {
    width: 248,
    height: 248,
    alignItems: "center",
    justifyContent: "center",
    borderRadius: radius.xl,
    backgroundColor: "rgba(9,12,18,0.08)",
    overflow: "hidden",
  },
  corner: { position: "absolute", width: 42, height: 42, borderColor: color.working },
  cornerTopLeft: {
    top: 0,
    left: 0,
    borderTopWidth: 3,
    borderLeftWidth: 3,
    borderTopLeftRadius: radius.lg,
  },
  cornerTopRight: {
    top: 0,
    right: 0,
    borderTopWidth: 3,
    borderRightWidth: 3,
    borderTopRightRadius: radius.lg,
  },
  cornerBottomLeft: {
    bottom: 0,
    left: 0,
    borderBottomWidth: 3,
    borderLeftWidth: 3,
    borderBottomLeftRadius: radius.lg,
  },
  cornerBottomRight: {
    bottom: 0,
    right: 0,
    borderBottomWidth: 3,
    borderRightWidth: 3,
    borderBottomRightRadius: radius.lg,
  },
  scanLine: {
    width: 200,
    height: 2,
    borderRadius: 1,
    backgroundColor: color.working,
    shadowColor: color.working,
    shadowOpacity: 0.75,
    shadowRadius: 8,
  },

  cameraIcon: {
    width: 68,
    height: 68,
    borderRadius: radius.xl,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.workingWash,
    marginBottom: space.lg,
  },
  cameraBody: {
    width: 32,
    height: 23,
    borderRadius: radius.sm,
    borderWidth: 2,
    borderColor: color.working,
    alignItems: "center",
    justifyContent: "center",
  },
  cameraLens: {
    width: 10,
    height: 10,
    borderRadius: 5,
    borderWidth: 2,
    borderColor: color.working,
  },
  cameraTop: {
    position: "absolute",
    top: 19,
    width: 13,
    height: 5,
    borderTopLeftRadius: 3,
    borderTopRightRadius: 3,
    backgroundColor: color.working,
  },
  title: {
    fontFamily: font.sansBold,
    fontSize: 28,
    letterSpacing: -0.6,
    color: color.text,
    textAlign: "center",
  },
  body: {
    fontFamily: font.sans,
    fontSize: size.body,
    color: color.muted,
    lineHeight: 22,
    textAlign: "center",
    marginTop: space.sm,
  },
  errorBox: {
    maxWidth: 320,
    backgroundColor: color.scrim,
    borderRadius: radius.md,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.error,
  },
  error: {
    fontFamily: font.sans,
    fontSize: size.caption,
    color: color.error,
    lineHeight: 18,
    textAlign: "center",
  },

  button: {
    width: "100%",
    minHeight: 50,
    backgroundColor: color.working,
    borderRadius: radius.lg,
    alignItems: "center",
    justifyContent: "center",
    marginTop: space.xl,
  },
  buttonText: { fontFamily: font.sansBold, fontSize: size.body, color: color.ink },
  secondaryButton: {
    paddingVertical: space.md,
    paddingHorizontal: space.lg,
    marginTop: space.sm,
  },
  link: { fontFamily: font.sansMedium, fontSize: size.body, color: color.working },
  dismiss: {
    minHeight: 44,
    justifyContent: "center",
    paddingHorizontal: space.lg,
    borderRadius: radius.pill,
    backgroundColor: color.scrim,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: "rgba(255,255,255,0.18)",
  },
  dismissText: { fontFamily: font.sansMedium, fontSize: size.body, color: color.text },
});

function Corner({ style }: { style: StyleProp<ViewStyle> }) {
  return <View style={[styles.corner, style]} />;
}

function ScanLine() {
  const translateY = useSharedValue(-92);
  const reduceMotion = useReducedMotion();

  useEffect(() => {
    if (reduceMotion) {
      translateY.value = 0;
      return;
    }
    translateY.value = withRepeat(
      withTiming(92, { duration: 1600, easing: Easing.linear }),
      -1,
      true,
    );
    return () => cancelAnimation(translateY);
  }, [reduceMotion, translateY]);

  const style = useAnimatedStyle(() => ({
    transform: [{ translateY: translateY.value }],
  }));

  return <Animated.View style={[styles.scanLine, style]} />;
}
