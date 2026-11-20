import AsyncStorage from "@react-native-async-storage/async-storage";
import * as SecureStore from "expo-secure-store";
import { Platform } from "react-native";
import { clearCredentials, loadCredentials, type Credentials } from "./client";
import { draftNamespace } from "./draft-policy";
import { normalizeCredentials } from "./pairing";

const indexKey = "agentman.devices.v1";
const secretKey = (id: string) => indexKey + "." + id;
const secrets = Platform.OS === "web" ? AsyncStorage : {
  getItem: SecureStore.getItemAsync,
  setItem: (key: string, value: string) => SecureStore.setItemAsync(key, value, {
    keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
  }),
  removeItem: SecureStore.deleteItemAsync,
};

export async function saveDevice(credentials: Credentials, devices: Credentials[]) {
  await secrets.setItem(secretKey(draftNamespace(credentials)), JSON.stringify(credentials));
  await AsyncStorage.setItem(indexKey, JSON.stringify(devices.map(draftNamespace)));
}

export async function removeDevice(id: string, remaining: Credentials[]) {
  await secrets.removeItem(secretKey(id));
  // Missing secrets are filtered when loading, including after a crash between writes.
  await AsyncStorage.setItem(indexKey, JSON.stringify(remaining.map(draftNamespace))).catch(() => {});
}

export async function loadDevices(): Promise<Credentials[]> {
  const raw = await AsyncStorage.getItem(indexKey);
  if (raw === null) {
    const legacy = await loadCredentials();
    if (!legacy) return [];
    const scope = draftNamespace(legacy);
    for (const key of ["agentman.dismissed", "agentman.managerLastRead", "agentman.notifications.v1"]) {
      const value = await AsyncStorage.getItem(key);
      if (value !== null) await AsyncStorage.setItem(key + "." + scope, value);
    }
    await saveDevice(legacy, [legacy]);
    await clearCredentials();
    return [legacy];
  }
  // Retry interrupted migration cleanup even after the device index was committed.
  await clearCredentials();
  const ids: string[] = JSON.parse(raw);
  const devices = await Promise.all(ids.map(async id => {
    const value = await secrets.getItem(secretKey(id));
    if (!value) return null;
    let credentials;
    try {
      credentials = normalizeCredentials(JSON.parse(value));
    } catch {
      return null;
    }
    return credentials && draftNamespace(credentials) === id ? credentials : null;
  }));
  return devices.filter((item): item is Credentials => !!item);
}
