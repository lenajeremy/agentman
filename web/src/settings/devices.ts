// How Settings › Devices names and describes a phone or tablet, as the
// Mac's DeviceLabel and PairingPanel do (Views/DevicesSettings.swift).

import { since } from "../labels.ts";

/** A phone or tablet that has reached this computer through the relay
 *  (internal/protocol/devices.go). */
export interface Device {
  id: string;
  /** Its own name, where it can tell; absent from phone apps that predate it. */
  name?: string;
  model?: string;
  /** "ios" or "android". */
  platform?: string;
  os?: string;
  /** Milliseconds since 1970. */
  firstSeen: number;
  lastSeen: number;
  connected?: boolean;
}

/** A code a new device connects with, good until `expiresAt`. */
export interface PairCode {
  /** The ten digits to type on the device. */
  code: string;
  /** What the QR code holds: agentman://pair?token=…. */
  url: string;
  /** Milliseconds since 1970. */
  expiresAt: number;
}

/** Its own name where it gave one, else its model, else what it is. */
export function deviceName(device: Device): string {
  if (device.name) return device.name;
  if (device.model) return device.model;
  return device.platform === "android" ? "Android phone" : "Phone";
}

/** Which picture it gets: a tablet, an iPhone or any other phone. */
export function deviceKind(device: Device): "ipad" | "iphone" | "smartphone" {
  const model = device.model?.toLowerCase() ?? "";
  if (model.includes("ipad")) return "ipad";
  if (device.platform === "ios" || model.includes("iphone")) return "iphone";
  return "smartphone";
}

/** The model beside the name, when the name doesn't already say it. */
export function deviceModelBadge(device: Device): string | undefined {
  return device.model && device.name && device.model !== device.name ? device.model : undefined;
}

/** "Connected", or when it last was, and when it was added. */
export function deviceDetail(device: Device, now = Date.now(), locale?: string): string {
  const added = "Added " + new Date(device.firstSeen).toLocaleDateString(locale, { month: "short", day: "numeric" });
  if (device.connected) return "Connected · " + added;
  const seen = since(device.lastSeen, now);
  return (seen === "now" ? "Last connected just now" : `Last connected ${seen} ago`) + " · " + added;
}

/** The devices connected now: one connecting after pairing began is the
 *  one just paired. */
export function connectedIds(devices: readonly Device[] | undefined): Set<string> {
  return new Set((devices ?? []).filter((d) => d.connected).map((d) => d.id));
}

/** The device that connected since `before` was taken, if one has. */
export function newlyConnected(devices: readonly Device[] | undefined, before: ReadonlySet<string>): Device | undefined {
  return (devices ?? []).find((d) => d.connected && !before.has(d.id));
}

/** "0123456789" as "01234 56789", easier to read across a room. */
export function grouped(code: string): string {
  return code.length === 10 ? code.slice(0, 5) + " " + code.slice(5) : code;
}

/** How long a code has left, as "0:42"; never below 0:00. */
export function timeLeft(expiresAt: number, now = Date.now()): string {
  const seconds = Math.max(0, Math.ceil((expiresAt - now) / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

/** What a failed pair_code says, as the Mac's DevicesStore puts it. */
export function pairProblem(error: string): string {
  if (error.includes("unsupported request")) return "Update agentman to connect devices here, or run am pair in a terminal.";
  return error.replace(/^daemon: /, "");
}
