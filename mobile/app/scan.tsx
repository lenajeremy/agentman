import * as Haptics from "expo-haptics";
import { CameraView, useCameraPermissions } from "expo-camera";
import Feather from "@expo/vector-icons/Feather";
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
import { PairIllustration } from "../components/Illustrations";
import { MotionPressable } from "../components/MotionPressable";
import { useStyles } from "../lib/appearance";
import { pairWithToken } from "../lib/client";
import { confirmPairingReplacement } from "../lib/confirm";
import { parsePairingPayload } from "../lib/pairing";
import { useStore } from "../lib/store";
import { font, Palette, radius, size, space } from "../lib/theme";

/** Drawn over live camera video, which is dark whatever the theme. */
const ON_CAMERA = "#FFFFFF";

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
  const styles = useStyles(makeStyles);
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
      const redeem = async () => {
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
      };

      if (store.credentials) {
        // Redeeming a QR consumes its one-time token. Confirm before doing that
        // or silently replacing the Mac this phone is already paired with.
        confirmPairingReplacement(
          "Replace current pairing?",
          "This will replace the relay currently paired with Agentman on this phone.",
          () => void redeem(),
          () => {
            setBusy(false);
            handled.current = false;
          },
        );
        return;
      }

      await redeem();
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
            <PairIllustration style={styles.art} />
            <Text style={styles.title}>Allow the camera</Text>
            <Text style={styles.body}>
              Agentman uses it to read the one-time code from your terminal. Frames are
              never recorded or saved.
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
              <Text style={styles.link}>Enter the code instead</Text>
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
            <Feather name="x" size={22} color={ON_CAMERA} />
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
            {!busy ? <ScanLine /> : <ActivityIndicator color={ON_CAMERA} />}
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
          <Text style={styles.dismissText}>Enter the code instead</Text>
        </MotionPressable>
      </View>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    permissionContent: { flex: 1, justifyContent: "center", paddingHorizontal: space.lg },
    permissionCard: { alignItems: "center" },
    art: {
      width: "100%",
      aspectRatio: 340 / 214,
      borderRadius: radius.sheet,
      overflow: "hidden",
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
      marginBottom: space.xl,
    },

    overlay: {
      ...StyleSheet.absoluteFill,
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
      backgroundColor: c.cameraScrim,
    },
    scanHeading: {
      flex: 1,
      alignItems: "center",
      marginHorizontal: space.sm,
    },
    scanEyebrow: {
      fontFamily: font.sansMedium,
      fontSize: size.label,
      color: "rgba(255,255,255,0.72)",
    },
    overlayTitle: {
      fontFamily: font.sansBold,
      fontSize: size.title,
      color: ON_CAMERA,
    },
    headerSpacer: { width: 44 },
    scanCentre: { width: "100%", alignItems: "center", gap: space.lg },
    scanHint: {
      maxWidth: 290,
      fontFamily: font.sansMedium,
      fontSize: size.caption,
      lineHeight: 18,
      textAlign: "center",
      color: ON_CAMERA,
      backgroundColor: c.cameraScrim,
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
      borderRadius: radius.sheet,
      overflow: "hidden",
    },
    corner: { position: "absolute", width: 46, height: 46, borderColor: ON_CAMERA },
    cornerTopLeft: {
      top: 0,
      left: 0,
      borderTopWidth: 4,
      borderLeftWidth: 4,
      borderTopLeftRadius: radius.sheet,
    },
    cornerTopRight: {
      top: 0,
      right: 0,
      borderTopWidth: 4,
      borderRightWidth: 4,
      borderTopRightRadius: radius.sheet,
    },
    cornerBottomLeft: {
      bottom: 0,
      left: 0,
      borderBottomWidth: 4,
      borderLeftWidth: 4,
      borderBottomLeftRadius: radius.sheet,
    },
    cornerBottomRight: {
      bottom: 0,
      right: 0,
      borderBottomWidth: 4,
      borderRightWidth: 4,
      borderBottomRightRadius: radius.sheet,
    },
    scanLine: {
      width: 200,
      height: 2,
      borderRadius: 1,
      backgroundColor: "#8E9DFF",
      shadowColor: "#8E9DFF",
      shadowOpacity: 0.8,
      shadowRadius: 8,
    },

    title: {
      fontFamily: font.sansBold,
      fontSize: 28,
      letterSpacing: -0.8,
      color: c.text,
      textAlign: "center",
    },
    body: {
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.muted,
      lineHeight: 22,
      textAlign: "center",
      marginTop: space.sm,
      maxWidth: 340,
    },
    errorBox: {
      maxWidth: 320,
      backgroundColor: c.cameraScrim,
      borderRadius: radius.lg,
      paddingHorizontal: space.md,
      paddingVertical: space.sm,
    },
    error: {
      fontFamily: font.sansMedium,
      fontSize: size.caption,
      color: "#FF9A9E",
      lineHeight: 18,
      textAlign: "center",
    },

    button: {
      alignSelf: "stretch",
      minHeight: 56,
      backgroundColor: c.inverse,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      marginTop: space.xl,
    },
    buttonText: { fontFamily: font.sansBold, fontSize: 16, color: c.onInverse },
    secondaryButton: {
      paddingVertical: space.md,
      paddingHorizontal: space.lg,
      marginTop: space.xs,
    },
    link: { fontFamily: font.sansMedium, fontSize: size.body, color: c.textSecondary },
    dismiss: {
      minHeight: 48,
      justifyContent: "center",
      paddingHorizontal: space.xl,
      borderRadius: radius.pill,
      backgroundColor: c.cameraScrim,
    },
    dismissText: { fontFamily: font.sansBold, fontSize: size.body, color: ON_CAMERA },
  });

function Corner({ style }: { style: StyleProp<ViewStyle> }) {
  const styles = useStyles(makeStyles);
  return <View style={[styles.corner, style]} />;
}

function ScanLine() {
  const styles = useStyles(makeStyles);
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
