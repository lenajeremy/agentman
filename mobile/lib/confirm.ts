import { Alert, Platform } from "react-native";

/** Cross-platform confirmation; React Native Web's Alert.alert is a no-op. */
export function confirmPairingReplacement(
  title: string,
  message: string,
  onConfirm: () => void,
  onCancel: () => void,
): void {
  if (Platform.OS === "web") {
    const confirmed =
      typeof globalThis.confirm === "function" &&
      globalThis.confirm(`${title}\n\n${message}`);
    if (confirmed) onConfirm();
    else onCancel();
    return;
  }
  Alert.alert(title, message, [
    { text: "Cancel", style: "cancel", onPress: onCancel },
    { text: "Replace", style: "destructive", onPress: onConfirm },
  ]);
}
