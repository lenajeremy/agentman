import { ActionSheetIOS, Alert, Platform } from "react-native";

export type SessionAction = "servers" | "stop";

interface Options {
  /** How many servers the agent has running, or 0 when it has none. */
  servers: number;
  /** Whether stopping the current turn is possible right now. */
  canStop: boolean;
}

/**
 * The actions that do not earn a permanent place in the header.
 *
 * The header was carrying five controls across one phone-width row, which left
 * the title under half of it and truncated the working directory to "~/Deskt…".
 * What a session is — its name, its model, where it is running — is what you
 * read every time you open the screen; servers and stop are things you reach
 * for occasionally. So those two moved in here and the reading space went back
 * to the text.
 */
export function openSessionMenu(
  options: Options,
  choose: (action: SessionAction) => void,
): void {
  const items: {
    label: string;
    action: SessionAction;
    destructive?: boolean;
  }[] = [];
  if (options.servers > 0) {
    items.push({
      label: `Servers (${options.servers})`,
      action: "servers",
    });
  }
  if (options.canStop) {
    items.push({ label: "Stop this turn", action: "stop", destructive: true });
  }
  if (items.length === 0) return;

  if (Platform.OS === "ios") {
    ActionSheetIOS.showActionSheetWithOptions(
      {
        options: [...items.map((item) => item.label), "Cancel"],
        cancelButtonIndex: items.length,
        destructiveButtonIndex: items.findIndex((item) => item.destructive),
      },
      (index) => {
        if (index < items.length) choose(items[index].action);
      },
    );
    return;
  }

  Alert.alert("Session", undefined, [
    ...items.map((item) => ({
      text: item.label,
      style: item.destructive ? ("destructive" as const) : ("default" as const),
      onPress: () => choose(item.action),
    })),
    { text: "Cancel", style: "cancel" as const },
  ]);
}
