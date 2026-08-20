const { getDefaultConfig } = require("expo/metro-config");

const config = getDefaultConfig(__dirname);

// image-size 1.x, pulled in by Metro in Expo SDK 54, can loop forever while
// parsing crafted HEIC/HEIF files (GHSA-5p2g-fcmc-qvqq). Expo's default asset
// set exposes .heic even though Agentman does not use that format. Keep it out
// of Metro until the SDK adopts a patched parser release.
config.resolver.assetExts = config.resolver.assetExts.filter(
  (extension) => extension.toLowerCase() !== "heic",
);

module.exports = config;
