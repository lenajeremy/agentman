import { useFonts } from "expo-font";
import * as Notifications from "expo-notifications";
import { Stack, useRouter } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { useEffect } from "react";
import { LogBox, Platform, View } from "react-native";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { SafeAreaProvider } from "react-native-safe-area-context";

import { ThemeProvider, useTheme } from "../lib/appearance";
import { StoreProvider } from "../lib/store";
import { ManagerProvider } from "../lib/manager";
import { UpdatesProvider } from "../lib/updates-context";
import { simulatorRoute } from "../lib/simulator";
import { palettes } from "../lib/theme";

// Notifications are the product, not a nicety: an alert that arrives silently
// while the app is open would defeat the point of walking away from the desk.
// iOS asks this only while the app is open, and the answer is always no.
// You are already looking: a banner over the screen you are reading, or a
// chime for a turn you watched finish, is the noise this app is meant to cut.
// Alerts that arrive while it is closed are untouched by this.
Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldShowBanner: false,
    shouldShowList: false,
    shouldPlaySound: false,
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
      const data = response.notification.request.content.data;
      const sessionId = data?.sessionId;
      // Agentman's word, cards included, is in its conversation.
      if (data?.agentman === true) {
        router.push("/agentman");
      } else if (typeof sessionId === "string" && sessionId) {
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

/**
 * For screenshots and tests in the simulator, where nothing can be tapped
 * from outside: started with `-AGENTMAN_OPEN <route>`, the app opens that
 * route, such as a pairing link or a session. Never on a real phone.
 */
function SimulatorLaunch() {
  const router = useRouter();
  useEffect(() => {
    const route = simulatorRoute();
    if (!route) return;
    const timer = setTimeout(() => router.push(route as never), 600);
    return () => clearTimeout(timer);
  }, [router]);
  return null;
}

// A simulator started on a route is taking pictures: the development
// warnings' toast would sit over the bottom of every one.
if (simulatorRoute()) LogBox.ignoreAllLogs();

export default function RootLayout() {
  // Google Sans and Google Sans Code, bundled: the same faces as the Mac.
  const [fontsLoaded] = useFonts({
    "GoogleSans-Regular": require("../assets/fonts/GoogleSans-Regular.ttf"),
    "GoogleSans-Italic": require("../assets/fonts/GoogleSans-Italic.ttf"),
    "GoogleSans-Medium": require("../assets/fonts/GoogleSans-Medium.ttf"),
    "GoogleSans-SemiBold": require("../assets/fonts/GoogleSans-SemiBold.ttf"),
    "GoogleSans-SemiBoldItalic": require("../assets/fonts/GoogleSans-SemiBoldItalic.ttf"),
    "GoogleSansCode-Regular": require("../assets/fonts/GoogleSansCode-Regular.ttf"),
    "GoogleSansCode-Medium": require("../assets/fonts/GoogleSansCode-Medium.ttf"),
  });

  useEffect(() => {
    // Web has no notification permission model here; asking throws. Nor
    // does a simulator started on a route ask (lib/simulator).
    if (Platform.OS !== "web" && !simulatorRoute()) {
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
          <ManagerProvider>
          <UpdatesProvider>
            <NotificationNavigation />
            <SimulatorLaunch />
            <StatusBar style={scheme === "dark" ? "light" : "dark"} />
            <Stack
              screenOptions={{
                headerShown: false,
                contentStyle: { backgroundColor: color.paper },
                animation: "slide_from_right",
              }}
            >
              {/* A sheet, not a page: it is news to read and put away, and
                  swiping it down is the natural "not now". */}
              <Stack.Screen name="update" options={{ presentation: "modal", animation: "default" }} />
            </Stack>
          </UpdatesProvider>
          </ManagerProvider>
        </StoreProvider>
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}
