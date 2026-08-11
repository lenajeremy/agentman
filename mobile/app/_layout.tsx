import {
  IBMPlexMono_400Regular,
  IBMPlexMono_500Medium,
} from "@expo-google-fonts/ibm-plex-mono";
import {
  IBMPlexSans_400Regular,
  IBMPlexSans_500Medium,
  IBMPlexSans_600SemiBold,
  useFonts,
} from "@expo-google-fonts/ibm-plex-sans";
import * as Notifications from "expo-notifications";
import { Stack, useRouter } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { useEffect } from "react";
import { Platform, View } from "react-native";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { SafeAreaProvider } from "react-native-safe-area-context";

import { StoreProvider } from "../lib/store";
import { color } from "../lib/theme";

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
    IBMPlexSans_400Regular,
    IBMPlexSans_500Medium,
    IBMPlexSans_600SemiBold,
    IBMPlexMono_400Regular,
    IBMPlexMono_500Medium,
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

  if (!fontsLoaded) {
    return <View style={{ flex: 1, backgroundColor: color.ink }} />;
  }

  return (
    // Gesture handling has to be rooted above everything that uses it, or the
    // swipe on the status board silently does nothing on Android.
    <GestureHandlerRootView style={{ flex: 1 }}>
      <SafeAreaProvider>
        <StoreProvider>
          <NotificationNavigation />
          <StatusBar style="light" />
          <Stack
            screenOptions={{
              headerShown: false,
              contentStyle: { backgroundColor: color.ink },
              animation: "slide_from_right",
            }}
          />
        </StoreProvider>
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}
