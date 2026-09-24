import {
  Geist_400Regular,
  Geist_500Medium,
  Geist_600SemiBold,
  useFonts,
} from "@expo-google-fonts/geist";
import { GeistMono_400Regular, GeistMono_500Medium } from "@expo-google-fonts/geist-mono";
import * as Notifications from "expo-notifications";
import { Stack, useRouter } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { useEffect } from "react";
import { Platform, View } from "react-native";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { SafeAreaProvider } from "react-native-safe-area-context";

import { ThemeProvider, useTheme } from "../lib/appearance";
import { StoreProvider } from "../lib/store";
import { palettes } from "../lib/theme";

// Notifications are the product, not a nicety: an alert that arrives silently
// while the app is open would defeat the point of walking away from the desk.
Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldShowBanner: true,
    shouldShowList: true,
    shouldPlaySound: true,
    shouldSetBadge: false,
  }),
});

function NotificationNavigation() {
  const router = useRouter();

  useEffect(() => {
    if (Platform.OS === "web") return;
    const handled = new Set<string>();
    const openSession = (response: Notifications.NotificationResponse | null) => {
      if (!response) return;
      const identifier = response.notification.request.identifier;
      if (handled.has(identifier)) return;
      handled.add(identifier);
      const sessionId = response.notification.request.content.data?.sessionId;
      if (typeof sessionId === "string" && sessionId) {
        router.push(`/session/${encodeURIComponent(sessionId)}`);
      }
      void Notifications.clearLastNotificationResponseAsync().catch(() => {});
    };

    const subscription = Notifications.addNotificationResponseReceivedListener(openSession);
    void Notifications.getLastNotificationResponseAsync().then(openSession).catch(() => {});
    return () => subscription.remove();
  }, [router]);

  return null;
}

export default function RootLayout() {
  const [fontsLoaded] = useFonts({
    Geist_400Regular,
    Geist_500Medium,
    Geist_600SemiBold,
    GeistMono_400Regular,
    GeistMono_500Medium,
  });

  useEffect(() => {
    // Web has no notification permission model here; asking throws.
    if (Platform.OS !== "web") {
      void (async () => {
        // Android 13 does not present the notification permission prompt until
        // the app has created a channel. Questions and completion alerts share
        // one high-priority local-alert channel.
        if (Platform.OS === "android") {
          await Notifications.setNotificationChannelAsync("default", {
            name: "Agent alerts",
            importance: Notifications.AndroidImportance.HIGH,
            sound: "default",
            vibrationPattern: [0, 200, 100, 200],
          });
        }
        await Notifications.requestPermissionsAsync();
      })().catch(() => {});
    }
  }, []);

  return (
    <ThemeProvider>
      <ThemedApp fontsLoaded={fontsLoaded} />
    </ThemeProvider>
  );
}

function ThemedApp({ fontsLoaded }: { fontsLoaded: boolean }) {
  const { color, scheme, ready } = useTheme();

  // Held until the stored appearance is known too, so someone who chose dark
  // never sees a light frame first.
  if (!fontsLoaded || !ready) {
    return <View style={{ flex: 1, backgroundColor: palettes.light.paper }} />;
  }

  return (
    // Gesture handling has to be rooted above everything that uses it, or the
    // swipe on the status board silently does nothing on Android.
    <GestureHandlerRootView style={{ flex: 1, backgroundColor: color.paper }}>
      <SafeAreaProvider>
        <StoreProvider>
          <NotificationNavigation />
          <StatusBar style={scheme === "dark" ? "light" : "dark"} />
          <Stack
            screenOptions={{
              headerShown: false,
              contentStyle: { backgroundColor: color.paper },
              animation: "slide_from_right",
            }}
          />
        </StoreProvider>
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}
