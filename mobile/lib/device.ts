import Constants from "expo-constants";
import { Platform } from "react-native";

import type { DeviceInfo } from "./protocol";

/**
 * What this phone says about itself, for the Mac's list of devices: its name
 * where the system gives it (iOS gives "iPhone" unless the app may read the
 * name the user chose), the kind of device, and the system's version.
 */
export function deviceInfo(): DeviceInfo {
  // The web build has no platform constants at all.
  const constants = (Platform.constants ?? {}) as { interfaceIdiom?: string; Model?: string; Brand?: string };
  let model: string | undefined;
  if (Platform.OS === "ios") {
    model = constants.interfaceIdiom === "pad" ? "iPad" : "iPhone";
  } else if (constants.Model) {
    model = constants.Brand && !constants.Model.toLowerCase().startsWith(constants.Brand.toLowerCase())
      ? `${capitalised(constants.Brand)} ${constants.Model}`
      : constants.Model;
  }
  const name = Constants.deviceName?.trim();
  return {
    ...(name ? { name: name.slice(0, 128) } : {}),
    ...(model ? { model: model.slice(0, 128) } : {}),
    platform: Platform.OS,
    os: String(Platform.Version).slice(0, 32),
  };
}

function capitalised(word: string): string {
  return word.charAt(0).toUpperCase() + word.slice(1);
}
