#!/usr/bin/env node
/**
 * Apply Expo's fix for building ExpoModulesJSI with Xcode 27.1 (Swift 6.4).
 *
 * expo-modules-jsi compiles its xcframework on this machine, then deletes the
 * C++ type extensions from the module's text interface, which no one outside
 * it can read, by matching lines that start "extension __ObjC.". Swift 6.4
 * writes those lines "extension __ObjC::expo…", so nothing matched, the
 * extension stayed in, and every module importing ExpoModulesJSI failed with
 * "cannot find type '__ObjC::expo' in scope". That stopped the TestFlight build
 * on 2026-10-19, three days after this Mac moved to Xcode 27.1.
 *
 * Expo fixed the pattern upstream (expo/expo#50569), but not in any SDK 57
 * release: 57.1.2 still has the old one. This makes the same one-line change
 * after every install, and does nothing once the installed version already
 * matches both spellings, so it can be deleted when the app moves to an Expo
 * release with the fix.
 *
 * The package rebuilds a slice only when a recorded hash of its inputs
 * changes, so a slice built before this change would be reused as it is. Its
 * hash files are removed to make the next build start over.
 */
import { existsSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const pkg = join(root, "node_modules", "expo-modules-jsi", "apple");
const script = join(pkg, "scripts", "build-xcframework.sh");

const broken = "sed '/^extension __ObjC\\./,/^}/d;";
const fixed = "sed -E '/^extension __ObjC(\\.|::)/,/^}/d;";

if (!existsSync(script)) process.exit(0); // not installed, or not on this platform

const source = readFileSync(script, "utf8");
if (source.includes(fixed) || source.includes("__ObjC(\\.|::)")) process.exit(0);
if (!source.includes(broken)) {
  // Expo changed the script in some other way; say so rather than guess.
  console.warn(
    "patch-expo-modules-jsi: build-xcframework.sh no longer has the line this patches; check whether Xcode 27.1 builds still need it",
  );
  process.exit(0);
}

writeFileSync(script, source.replace(broken, fixed));

const xcframework = join(pkg, "Products", "ExpoModulesJSI.xcframework");
if (existsSync(xcframework)) {
  for (const slice of readdirSync(xcframework, { withFileTypes: true })) {
    if (slice.isDirectory()) rmSync(join(xcframework, slice.name, ".build-hash"), { force: true });
  }
}
console.log("patch-expo-modules-jsi: applied Expo's Swift 6.4 interface fix (expo/expo#50569)");
