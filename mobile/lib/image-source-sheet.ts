import { ActionSheetIOS, Alert, Platform } from "react-native";

export type ImageSource = "paste" | "library" | "file";

/**
 * Ask what to attach: a pasted image, one from the photos, or, where the
 * message goes to an agent, any file.
 *
 * A menu rather than one button that changes meaning: pasting a screenshot and
 * choosing from the library are different habits, and a control that silently
 * becomes the other one depending on what is on the clipboard is a control
 * nobody can predict. Paste is only offered when there is something to paste.
 */
export function chooseImageSource(
  clipboardReady: boolean,
  choose: (source: ImageSource) => void,
  files = false,
): void {
  const options: { label: string; source: ImageSource }[] = [];
  if (clipboardReady) options.push({ label: "Paste image", source: "paste" });
  options.push({ label: "Choose from photos", source: "library" });
  if (files) options.push({ label: "Choose a file", source: "file" });

  // Nothing to choose between, so do not make anyone choose.
  if (options.length === 1) {
    choose(options[0].source);
    return;
  }

  if (Platform.OS === "ios") {
    ActionSheetIOS.showActionSheetWithOptions(
      {
        options: [...options.map((option) => option.label), "Cancel"],
        cancelButtonIndex: options.length,
      },
      (index) => {
        if (index < options.length) choose(options[index].source);
      },
    );
    return;
  }

  if (Platform.OS === "web") {
    choose("library");
    return;
  }

  Alert.alert("Attach", undefined, [
    ...options.map((option) => ({ text: option.label, onPress: () => choose(option.source) })),
    { text: "Cancel", style: "cancel" as const },
  ]);
}
