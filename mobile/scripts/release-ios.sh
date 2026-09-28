#!/usr/bin/env bash
#
# Build the iOS app here and send it to TestFlight, without EAS.
#
# EAS was never slow at building — tonight's build finished in minutes. It was
# slow at *submitting*: forty-three minutes sitting in a queue for a transfer
# that takes five seconds. This does both steps locally, so neither waits on
# anyone else's queue.
#
# What this needs, once:
#
#   - An App Store Connect API key at
#     ~/.appstoreconnect/private_keys/AuthKey_<KEY_ID>.p8, which is where
#     both xcodebuild and altool look for it by default.
#   - ASC_KEY_ID and ASC_ISSUER_ID in the environment, or in mobile/.asc.env
#     (gitignored). Neither is a secret — the .p8 is — but they are account
#     identifiers and do not belong in a public repository.
#
# Signing goes through the Apple ID already signed into Xcode (Settings →
# Accounts), which is what holds the distribution certificate and the App
# Store profile for this bundle. The API key is for the upload only.
#
# Worth knowing, because it cost an hour to find: passing the key to
# xcodebuild as well does not add a fallback, it *overrides* the working
# account session — and a key scoped for submission cannot create signing
# assets, so the export fails with "Cloud signing permission error" on a
# machine that was able to sign all along.
#
# Usage:
#   mobile/scripts/release-ios.sh            # bump build, build, upload
#   mobile/scripts/release-ios.sh --no-upload  # stop after the .ipa
#   mobile/scripts/release-ios.sh --keep     # skip prebuild --clean
set -euo pipefail

cd "$(dirname "$0")/.."

upload=1
clean="--clean"
for arg in "$@"; do
  case "$arg" in
    --no-upload) upload=0 ;;
    --keep) clean="" ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done

[ -f .asc.env ] && . ./.asc.env

# Apple refuses an App Store upload built with a beta Xcode — error 90534,
# "Unsupported SDK or Xcode version", raised at validation after the whole
# archive is already built. xcode-select on this machine points at the beta,
# so the release install is chosen explicitly rather than inherited. Setting
# DEVELOPER_DIR yourself still wins, and it covers altool too, which would
# otherwise run out of whichever Xcode xcode-select names.
if [ -z "${DEVELOPER_DIR:-}" ] && [ -d /Applications/Xcode.app ]; then
  export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer
fi
echo "==> $(xcodebuild -version | tr '\n' ' ')"

: "${ASC_KEY_ID:?set ASC_KEY_ID (App Store Connect → Users and Access → Integrations)}"
: "${ASC_ISSUER_ID:?set ASC_ISSUER_ID (the Issuer ID above the key table)}"

key="$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID}.p8"
if [ ! -f "$key" ]; then
  echo "no API key at $key" >&2
  echo "download it from App Store Connect and put it there; it is shown once." >&2
  exit 1
fi

scheme="agentman"
workspace="ios/${scheme}.xcworkspace"
archive="build/${scheme}.xcarchive"
export_dir="build/ipa"

# The build number is ours to manage now. EAS incremented it server-side,
# which is why app.json never carried one; keeping it here instead puts every
# submitted build in the git history, where a duplicate is obvious before
# Apple rejects it rather than after.
build_number=$(node -e '
const fs = require("fs");
const path = "app.json";
const config = JSON.parse(fs.readFileSync(path, "utf8"));
const ios = (config.expo.ios ??= {});
ios.buildNumber = String((parseInt(ios.buildNumber ?? "0", 10) || 0) + 1);
fs.writeFileSync(path, JSON.stringify(config, null, 2) + "\n");
process.stdout.write(ios.buildNumber);
')
version=$(node -p 'require("./app.json").expo.version')
echo "==> agentman ${version} (${build_number})"

# Regenerated rather than reused: ios/ is gitignored build output, and a
# stale one silently ships whatever app.json said last time.
echo "==> prebuild"
npx expo prebuild --platform ios $clean

team=$(node -p 'require("./app.json").expo.ios?.appleTeamId || ""')
if [ -z "$team" ]; then
  echo "set expo.ios.appleTeamId in app.json (10 characters, App Store Connect → Membership)" >&2
  exit 1
fi

echo "==> archive"
rm -rf "$archive" "$export_dir"
xcodebuild archive \
  -workspace "$workspace" \
  -scheme "$scheme" \
  -configuration Release \
  -destination "generic/platform=iOS" \
  -archivePath "$archive" \
  DEVELOPMENT_TEAM="$team" \
  CODE_SIGN_STYLE=Automatic \
  -allowProvisioningUpdates \
  | { command -v xcbeautify >/dev/null && xcbeautify || cat; }

# Written per run rather than committed: one less file to drift.
options="build/ExportOptions.plist"
cat > "$options" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>method</key><string>app-store-connect</string>
  <key>teamID</key><string>${team}</string>
  <key>signingStyle</key><string>automatic</string>
  <key>uploadSymbols</key><true/>
</dict>
</plist>
PLIST

echo "==> export"
xcodebuild -exportArchive \
  -archivePath "$archive" \
  -exportOptionsPlist "$options" \
  -exportPath "$export_dir" \
  -allowProvisioningUpdates

ipa=$(find "$export_dir" -name "*.ipa" -maxdepth 1 | head -1)
[ -n "$ipa" ] || { echo "no .ipa produced" >&2; exit 1; }
echo "==> $ipa ($(du -h "$ipa" | cut -f1))"

if [ "$upload" -eq 0 ]; then
  echo "==> not uploading (--no-upload)"
  exit 0
fi

# Validated before uploading: a rejection costs seconds here and an email
# from Apple twenty minutes later otherwise.
echo "==> validate"
xcrun altool --validate-app -f "$ipa" -t ios \
  --apiKey "$ASC_KEY_ID" --apiIssuer "$ASC_ISSUER_ID"

echo "==> upload"
xcrun altool --upload-app -f "$ipa" -t ios \
  --apiKey "$ASC_KEY_ID" --apiIssuer "$ASC_ISSUER_ID"

echo
echo "==> ${version} (${build_number}) uploaded — Apple processes it before it"
echo "    appears in TestFlight, usually within fifteen minutes."
echo "    Commit the app.json build number so the next run does not reuse it."
