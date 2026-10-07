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
# Signing uses Apple's cloud signing, which needs an identity that is allowed
# to sign. There are two ways to give it one:
#
#   - ASC_SIGNING_KEY_ID: an App Store Connect key with Admin access. This is
#     the one to use. It is a file, so nothing about it expires between runs,
#     and the script needs no one signed into anything.
#   - Without it, whatever Apple ID is signed into Xcode (Settings →
#     Accounts). That works while the session lasts. It lapsed overnight
#     once already and the export failed with "No Accounts" — which is why
#     the key exists.
#
# The two do not combine. Passing a key to xcodebuild replaces the Xcode
# session rather than adding a fallback, and a key without Admin cannot sign
# at all: the export fails with "Cloud signing permission error". So only a
# key named as the signing key is ever given to xcodebuild. The upload key
# needs much less and is kept separate so a submission-only key keeps
# working for that.
#
# Every build ships with "What to Test" notes, which is what the TestFlight
# app shows under it. They are the app changes since the previous build,
# worked out from git: each commit contributes its `Changelog:` trailer if it
# has one, otherwise its subject. Commit subjects are written for the history
# and some read well out of context — "Keep green for the one thing it means"
# does not — so the trailer is how to say it for a tester instead.
#
# That only works if the build and the history agree, so the script refuses
# to run with uncommitted app code, and after a successful upload it commits
# the build number and tags that commit ios-build-<n>. The tag is where the
# next build's notes start from.
#
# Every release has a semantic version, MAJOR.MINOR.PATCH, and saying which
# part moves is part of releasing (see scripts/semver.mjs):
#
#   patch  fixes only
#   minor  something new, and nothing that stops an older Mac working with it
#   major  a change people must act on, such as needing a newer agentman
#
# The new version is counted from the last one shipped, not from app.json, so
# a run that fails before uploading cannot skip a number. The build number
# goes on counting across versions: Apple needs it to, and the app compares
# builds to tell when it is behind.
#
# After the upload, the build goes to the testers outside the team too, the
# public link's included (scripts/testflight-publish.mjs). For two weeks that
# step did not exist and the public link served the same old build. The first
# build of a new version waits on Beta App Review before those testers can
# install it, so the app is only told about a build, through the website,
# once it is approved (scripts/announce.mjs, also `npm run announce:ios`).
#
# Usage:
#   npm run release:ios -- minor               # bump, build, upload, notes, tag, publish
#   mobile/scripts/release-ios.sh patch --no-upload  # stop after the .ipa
#   mobile/scripts/release-ios.sh patch --keep       # skip prebuild --clean
#   mobile/scripts/release-ios.sh patch --allow-dirty  # build uncommitted app code
set -euo pipefail

cd "$(dirname "$0")/.."

upload=1
clean="--clean"
allow_dirty=0
bump=""
for arg in "$@"; do
  case "$arg" in
    patch|minor|major) bump="$arg" ;;
    --no-upload) upload=0 ;;
    --keep) clean="" ;;
    --allow-dirty) allow_dirty=1 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done
if [ -z "$bump" ]; then
  echo "say which part of the version this release moves:" >&2
  echo "  npm run release:ios -- patch   fixes only" >&2
  echo "  npm run release:ios -- minor   something new" >&2
  echo "  npm run release:ios -- major   a change people must act on" >&2
  exit 2
fi

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

# The signing identity, passed to both xcodebuild calls. Empty means "use the
# Apple ID signed into Xcode".
signing=()
if [ -n "${ASC_SIGNING_KEY_ID:-}" ]; then
  signing_key="$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_SIGNING_KEY_ID}.p8"
  if [ ! -f "$signing_key" ]; then
    echo "no signing key at $signing_key" >&2
    exit 1
  fi
  signing=(
    -authenticationKeyPath "$signing_key"
    -authenticationKeyID "$ASC_SIGNING_KEY_ID"
    -authenticationKeyIssuerID "$ASC_ISSUER_ID"
  )
  echo "==> signing with API key ${ASC_SIGNING_KEY_ID}"
else
  echo "==> signing with the Apple ID in Xcode (set ASC_SIGNING_KEY_ID to stop depending on it)"
fi

scheme="agentman"
workspace="ios/${scheme}.xcworkspace"
archive="build/${scheme}.xcarchive"
export_dir="build/ipa"

# The app code that goes into the build. Paths are relative to mobile/.
app_paths=(app components lib assets)

if [ "$allow_dirty" -eq 0 ] && [ -n "$(git status --porcelain -- "${app_paths[@]}")" ]; then
  echo "app code has uncommitted changes — commit them first, so this build" >&2
  echo "matches its notes and its tag. (--allow-dirty to build anyway.)" >&2
  exit 1
fi

# What changed since the last build that shipped. The first build after this
# script learned to tag has nothing to count from, so it takes the last ten.
last_build=$(git describe --tags --match 'ios-build-*' --abbrev=0 2>/dev/null || true)
if [ -n "$last_build" ]; then range=("$last_build..HEAD"); else range=(-n 10 HEAD); fi

# The version this release ships as, counted from the last one that shipped.
if [ -n "$last_build" ]; then
  shipped=$(git show "${last_build}:mobile/app.json" | node -p 'JSON.parse(require("fs").readFileSync(0, "utf8")).expo.version')
else
  shipped=$(node -p 'require("./app.json").expo.version')
fi
version=$(node scripts/semver.mjs "$bump" "$shipped")
if git rev-parse -q --verify "refs/tags/ios-v${version}" >/dev/null; then
  echo "ios-v${version} is already tagged; nothing shipped it from here since?" >&2
  exit 1
fi
mkdir -p build
notes_file="build/whats-new.txt"
git log --no-merges "${range[@]}" \
    --format='%(trailers:key=Changelog,valueonly,separator= )%x1f%s' -- "${app_paths[@]}" \
  | node -e '
      const lines = require("fs").readFileSync(0, "utf8").split("\n").filter(Boolean);
      const notes = lines.map((line) => {
        const [trailer, subject] = line.split("\x1f");
        return "• " + ((trailer || "").trim() || subject.trim());
      });
      process.stdout.write(
        notes.join("\n") || "• No app changes since the last build — same app, new build.",
      );
    ' > "$notes_file"

# The build number is ours to manage now. EAS incremented it server-side,
# which is why app.json never carried one; keeping it here instead puts every
# submitted build in the git history, where a duplicate is obvious before
# Apple rejects it rather than after.
build_number=$(VERSION="$version" node -e '
const fs = require("fs");
const write = (path, value) => fs.writeFileSync(path, JSON.stringify(value, null, 2) + "\n");
const config = JSON.parse(fs.readFileSync("app.json", "utf8"));
const ios = (config.expo.ios ??= {});
ios.buildNumber = String((parseInt(ios.buildNumber ?? "0", 10) || 0) + 1);
config.expo.version = process.env.VERSION;
write("app.json", config);
// The same version everywhere npm would show one.
const pkg = JSON.parse(fs.readFileSync("package.json", "utf8"));
pkg.version = process.env.VERSION;
write("package.json", pkg);
const lock = JSON.parse(fs.readFileSync("package-lock.json", "utf8"));
lock.version = process.env.VERSION;
if (lock.packages?.[""]) lock.packages[""].version = process.env.VERSION;
write("package-lock.json", lock);
process.stdout.write(ios.buildNumber);
')
echo "==> agentman ${version} (${build_number}), a ${bump} release after ${shipped}; changes since ${last_build:-the last ten commits}:"
sed 's/^/    /' "$notes_file"

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
  ${signing[@]+"${signing[@]}"} \
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
  -allowProvisioningUpdates \
  ${signing[@]+"${signing[@]}"}

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

# Record what shipped. Only the version files are committed, even if other
# files are dirty. ios-build-<n> marks where the next build's notes start, and
# ios-v<version> where the next version is counted from.
echo "==> tag ios-build-${build_number} and ios-v${version}"
git commit -q -m "Ship iOS build ${build_number} (${version})" -- app.json package.json package-lock.json
git tag "ios-build-${build_number}"
git tag "ios-v${version}"
if ! git push -q origin HEAD "ios-build-${build_number}" "ios-v${version}"; then
  # The build is already with Apple; a failed push must not look like a
  # failed release. It needs pushing before the next run, though, or that
  # run's notes and version will start from the wrong place on another machine.
  echo "    push failed — run: git push origin HEAD ios-build-${build_number} ios-v${version}" >&2
fi

# Last, because it waits for Apple to register the upload. A failure here is
# reported, not fatal: the build shipped either way.
echo "==> what to test"
if ! node scripts/testflight-notes.mjs "$build_number" "$notes_file"; then
  echo "    notes not set — retry: node scripts/testflight-notes.mjs $build_number $notes_file" >&2
fi

# Then to the testers outside the team, the public link's among them. Like
# the notes, a failure is reported, not fatal: the build shipped either way.
echo "==> publish to external testers"
if ! node scripts/testflight-publish.mjs "$build_number"; then
  echo "    not published — retry: node scripts/testflight-publish.mjs $build_number" >&2
fi

# And, once they can install it, to the app. A build of a version Apple has
# already approved is usually let through within minutes; a new version waits
# on Beta App Review, which this does not wait out.
echo "==> announce to the app"
if node scripts/announce.mjs "$build_number" --wait 15; then
  :
else
  echo "    not announced yet — once Apple approves it, run: npm run announce:ios" >&2
fi

echo
echo "==> ${version} (${build_number}) shipped."
