#!/usr/bin/env bash
#
# Build, sign and notarize Agentman for Mac, and prepare everything that
# ships it: the appcast item the app updates itself from, the site's Download
# links, and the Homebrew cask. Nothing public changes unless --publish is
# given; without it the script ends by printing what is left to do.
#
# This was a page of steps done by hand at every release, each easy to get
# wrong in a way that only showed later: a DMG left attached makes notarytool
# hang, a VPN makes it fail, a forgotten version bump ships a build that
# claims to be the last one. The script refuses to start in any of those
# states, and also with uncommitted changes, so a release is always a commit.
#
# What it does, in order:
#
#   1. Archives desktop/ in Release, signed with the Developer ID certificate,
#      and exports it (desktop/ExportOptions.plist). The export re-signs
#      Sparkle's helpers with the same certificate; each one is checked.
#   2. Puts the app in a DMG beside a link to /Applications, signs the DMG,
#      notarizes it, staples the ticket and checks Gatekeeper accepts it.
#   3. Signs the stapled DMG for Sparkle (sign_update) with the EdDSA key in
#      the login Keychain, under the account "agentman". Stapling changes the
#      file, so this has to come after it.
#   4. Writes the release into this checkout: a new item at the top of
#      site/appcast.xml, and the site's Download links pointed at the new
#      DMG. The Homebrew cask for the app goes in the output folder.
#
# Left for publishing, in this order (--publish does them):
#
#   - upload the DMG to the release GoReleaser made for the tag;
#   - open the site PR; merging it deploys the appcast, which is the moment
#     every copy of the app starts offering the update, so the DMG must be
#     up first;
#   - commit the cask to the tap, once `agentman` there is the app's name
#     (see docs/development.md, "Homebrew").
#
# What this needs, once:
#
#   - The Developer ID Application certificate in the login Keychain.
#   - An App Store Connect API key at
#     ~/.appstoreconnect/private_keys/AuthKey_<KEY_ID>.p8, with ASC_KEY_ID and
#     ASC_ISSUER_ID in mobile/.asc.env (the release-ios.sh file). A worktree
#     without one uses the main checkout's.
#   - The Sparkle signing key, made by Sparkle's generate_keys:
#       <tools>/generate_keys --account agentman
#     with its public half in desktop/project.yml (SUPublicEDKey). The private
#     half never leaves the Keychain; the first signing asks the Keychain to
#     let sign_update use it ("Always Allow" stops it asking again).
#
# Before running it, bump MARKETING_VERSION and CURRENT_PROJECT_VERSION in
# desktop/project.yml and commit, push the CLI's tag, and wait for its
# release workflow: the am inside the app checks the web app's file on that
# release (agentman-web_v<version>.tar.gz), whose SHA-256 is read from it.
#
# Usage:
#   scripts/release-mac.sh                    # build and prepare; publish nothing
#   scripts/release-mac.sh --notes notes.md   # the update window's notes (Markdown)
#   scripts/release-mac.sh --publish          # and upload, open the site PR, update the tap
#
# Settings, all optional:
#   AGENTMAN_RELEASE_DIR=<dir>  where the archive, DMG and cask go
#                               (default $TMPDIR/agentman-mac-release)
#   ASC_ENV=<file>              the App Store Connect settings file
#   SPARKLE_KEY_FILE=<file>     sign with this throwaway key instead, to try the
#                               script; the item it writes cannot install, so
#                               --publish refuses it
#   WITHOUT_WEB=1               build before the release has the web app's
#                               file, to try the script: the app's am then
#                               can't fetch the web app, so --publish refuses it
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="lenajeremy/agentman"
TAP="lenajeremy/homebrew-agentman"
SITE="https://agentman-nu.vercel.app"
TEAM="N58PT393QS"
KEY_ACCOUNT="agentman"
OUT="${AGENTMAN_RELEASE_DIR:-${TMPDIR:-/tmp}/agentman-mac-release}"
OUT="${OUT%/}"

say() { printf '%s\n' "$*"; }
step() { printf '\n\033[1m%s\033[0m\n' "$*"; }
fail() { printf '\nrelease-mac: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

publish=0
notes=""
while [ $# -gt 0 ]; do
  case "$1" in
    --publish) publish=1 ;;
    --notes) [ $# -ge 2 ] || fail "--notes takes a Markdown file."; notes="$2"; shift ;;
    -h|--help) sed -n '2,/^set -euo/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
    *) fail "unknown option $1 (see --help)" ;;
  esac
  shift
done
if [ -n "$notes" ]; then
  [ -f "$notes" ] || fail "no notes file at $notes"
  notes="$(cd "$(dirname "$notes")" && pwd)/$(basename "$notes")"
fi

cd "$ROOT"
yml="desktop/project.yml"
setting() { sed -n "s/^ *$1: *\"\{0,1\}\([^\"]*\)\"\{0,1\} *\$/\1/p" "$yml" | head -1; }
version="$(setting MARKETING_VERSION)"
build="$(setting CURRENT_PROJECT_VERSION)"
min_macos="$(setting MACOSX_DEPLOYMENT_TARGET)"
public_key="$(setting SUPublicEDKey)"
[ -n "$version" ] && [ -n "$build" ] && [ -n "$min_macos" ] && [ -n "$public_key" ] ||
  fail "could not read the version, build, minimum macOS and SUPublicEDKey from $yml."
tag="v$version"
release_url="https://github.com/$REPO/releases/tag/$tag"
dmg_url="https://github.com/$REPO/releases/download/$tag/Agentman.dmg"

# MARK: Refusing to start

step "Checking Agentman $version ($build) can be released"

for tool in xcodegen xcodebuild python3 shasum hdiutil; do
  have "$tool" || fail "$tool is not installed."
done
if [ "$publish" = 1 ]; then have gh || fail "--publish needs the GitHub CLI (gh)."; fi

[ -z "$(git status --porcelain)" ] ||
  fail "there are uncommitted changes. A release is built from a commit: commit or stash them first."

# The version must be past the last one shipped: the newest item in the
# appcast, or, before the appcast had any, the DMG the site links to. Sparkle
# compares builds, so the build must be past the appcast's newest too.
read -r shipped_version shipped_build < <(python3 -I - site/appcast.xml site/*.html <<'PY'
import re, sys
appcast = open(sys.argv[1]).read()
builds = [(int(b), v) for b, v in re.findall(
    r"<sparkle:version>(\d+)</sparkle:version>\s*<sparkle:shortVersionString>([\d.]+)</sparkle:shortVersionString>", appcast)]
versions = [v for _, v in builds]
for page in sys.argv[2:]:
    versions += re.findall(r"releases/download/v(\d+\.\d+\.\d+)/Agentman\.dmg", open(page).read())
key = lambda v: tuple(int(p) for p in v.split("."))
print(max(versions, key=key) if versions else "0.0.0", max(b for b, _ in builds) if builds else 0)
PY
)
newer() { python3 -I -c 'import sys; k=lambda v: tuple(int(p) for p in v.split(".")); sys.exit(0 if k(sys.argv[1]) > k(sys.argv[2]) else 1)' "$1" "$2"; }
newer "$version" "$shipped_version" ||
  fail "$yml still says $version, and $shipped_version has shipped. Bump MARKETING_VERSION and CURRENT_PROJECT_VERSION, and commit."
[ "$build" -gt "$shipped_build" ] ||
  fail "build $build is not past the appcast's newest, $shipped_build: Sparkle would not offer it. Bump CURRENT_PROJECT_VERSION."
say "Version $version ($build), after $shipped_version."

# A DMG left attached makes notarytool wait forever.
# (Read whole first: under pipefail, grep -q leaving early can hide a match.)
attached="$(hdiutil info)"
if grep '^image-path' <<<"$attached" | grep -qi 'agentman' || ls -d /Volumes/Agentman* >/dev/null 2>&1; then
  fail "an Agentman disk image is attached, which makes notarization hang. Eject it (hdiutil detach /Volumes/Agentman) and run again."
fi

# Notarization fails through a VPN, after the whole build.
iface="$(route -n get default 2>/dev/null | awk '/interface:/ { print $2 }')"
case "$iface" in
  "") fail "there is no default route. Is the Mac online?" ;;
  utun*|ipsec*|ppp*|tun*|tap*|wg*) fail "the network goes through a VPN ($iface), which breaks notarization. Turn it off and run again." ;;
esac

asc_env="${ASC_ENV:-$ROOT/mobile/.asc.env}"
if [ ! -f "$asc_env" ] && [ -z "${ASC_ENV:-}" ]; then
  main="$(git worktree list --porcelain | sed -n '1s/^worktree //p')"
  asc_env="$main/mobile/.asc.env"
fi
[ -f "$asc_env" ] || fail "no App Store Connect settings at $asc_env (ASC_KEY_ID, ASC_ISSUER_ID)."
# Loaded, never echoed: these name the account and the key.
set -a
# shellcheck disable=SC1090
. "$asc_env"
set +a
[ -n "${ASC_KEY_ID:-}" ] && [ -n "${ASC_ISSUER_ID:-}" ] || fail "$asc_env must set ASC_KEY_ID and ASC_ISSUER_ID."
asc_key="$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID}.p8"
[ -f "$asc_key" ] || fail "the App Store Connect key that ASC_KEY_ID names is not in ~/.appstoreconnect/private_keys."

# The signing key. Its public half is stored beside it as a comment, which
# reads without unlocking the private half, so this check never prompts.
if [ -n "${SPARKLE_KEY_FILE:-}" ]; then
  [ "$publish" = 0 ] || fail "SPARKLE_KEY_FILE is for trying the script. A release is signed with the Keychain's key."
  [ -f "$SPARKLE_KEY_FILE" ] || fail "no key at SPARKLE_KEY_FILE ($SPARKLE_KEY_FILE)."
  signing=(--ed-key-file "$SPARKLE_KEY_FILE")
  say "Signing for Sparkle with the test key in SPARKLE_KEY_FILE: the appcast item will not install."
else
  keychain_key="$(security find-generic-password -s https://sparkle-project.org -a "$KEY_ACCOUNT" 2>/dev/null |
    sed -n 's/^ *"icmt"<blob>=.*\\012\\012\([A-Za-z0-9+/=]*\)"$/\1/p')"
  [ -n "$keychain_key" ] ||
    fail "the login Keychain has no Sparkle key for \"$KEY_ACCOUNT\". Make it once with Sparkle's generate_keys --account $KEY_ACCOUNT (see the top of this script)."
  [ "$keychain_key" = "$public_key" ] ||
    fail "the Keychain's Sparkle key ($keychain_key) is not the one the app trusts ($public_key in $yml). Updates signed with it would be refused."
  signing=(--account "$KEY_ACCOUNT")
fi

release_exists=0
if have gh && gh release view "$tag" -R "$REPO" >/dev/null 2>&1; then release_exists=1; fi
if [ "$release_exists" = 0 ]; then
  [ "$publish" = 0 ] || fail "GitHub has no $tag release to upload to. Push the tag first: GoReleaser makes the release."
  say "GitHub has no $tag release yet; push the tag before uploading."
fi

# The am inside the app fetches the web app's screens from this release the
# first time `am web` runs, and uses them only if they match this SHA-256:
# the file the release workflow attached, read here from the release itself.
web_sha=""
web_file="agentman-web_$tag.tar.gz"
if [ "$release_exists" = 1 ] && rm -f "$OUT/$web_file" && mkdir -p "$OUT" &&
   gh release download "$tag" -R "$REPO" -p "$web_file" -D "$OUT" >/dev/null 2>&1; then
  web_sha="$(shasum -a 256 "$OUT/$web_file" | cut -d' ' -f1)"
  say "The web app's file on $tag: $web_sha."
elif [ "${WITHOUT_WEB:-}" = 1 ] && [ "$publish" = 0 ]; then
  say "Building without the web app's file (WITHOUT_WEB=1): this app's am can't fetch the web app."
else
  fail "the $tag release has no $web_file yet: push the tag and wait for its release workflow, which attaches it. (WITHOUT_WEB=1 builds a test copy without it.)"
fi

# MARK: Building

mkdir -p "$OUT"
dd="$OUT/DerivedData"
archive="$OUT/Agentman.xcarchive"
app="$OUT/export/Agentman.app"
dmg="$OUT/Agentman.dmg"
rm -rf "$archive" "$OUT/export" "$OUT/dmg" "$dmg" "$OUT/agentman.rb"

step "Archiving (a few minutes; the log is $OUT/archive.log)"
(cd desktop && xcodegen -q)
xcodebuild archive -project desktop/Agentman.xcodeproj -scheme Agentman -configuration Release \
  -destination generic/platform=macOS -archivePath "$archive" -derivedDataPath "$dd" \
  DEVELOPMENT_TEAM="$TEAM" CODE_SIGN_STYLE=Manual "CODE_SIGN_IDENTITY=Developer ID Application" \
  OTHER_CODE_SIGN_FLAGS=--timestamp AGENTMAN_WEB_SHA256="$web_sha" >"$OUT/archive.log" 2>&1 ||
  { tail -25 "$OUT/archive.log" >&2; fail "the archive failed; the whole log is $OUT/archive.log"; }
xcodebuild -exportArchive -archivePath "$archive" -exportPath "$OUT/export" \
  -exportOptionsPlist desktop/ExportOptions.plist >"$OUT/export.log" 2>&1 ||
  { tail -25 "$OUT/export.log" >&2; fail "the export failed; the whole log is $OUT/export.log"; }

plist="$app/Contents/Info.plist"
info() { plutil -extract "$1" raw -o - "$plist" 2>/dev/null; }
[ "$(info CFBundleShortVersionString)" = "$version" ] && [ "$(info CFBundleVersion)" = "$build" ] ||
  fail "the app says $(info CFBundleShortVersionString) ($(info CFBundleVersion)), not $version ($build)."
[ "$(info SUFeedURL)" = "$SITE/appcast.xml" ] && [ "$(info SUPublicEDKey)" = "$public_key" ] ||
  fail "the app's SUFeedURL or SUPublicEDKey is not what this script publishes for."

# Notarization refuses any code inside that is not ours, timestamped and
# under the hardened runtime. Sparkle ships its helpers signed ad hoc, so
# this is where an export that forgot them would show.
codesign --verify --deep --strict "$app" || fail "the app's signature does not verify."
sparkle="$app/Contents/Frameworks/Sparkle.framework/Versions/B"
[ -x "$app/Contents/Helpers/am" ] || fail "the app carries no am: Contents/Helpers/am is missing."
for code in "$app" "$app/Contents/Helpers/am" "$app/Contents/Frameworks/Sparkle.framework" "$sparkle/Autoupdate" \
            "$sparkle/Updater.app" "$sparkle"/XPCServices/*.xpc; do
  details="$(codesign -dvv "$code" 2>&1)"
  grep -q "^TeamIdentifier=$TEAM\$" <<<"$details" && grep -q "^Authority=Developer ID Application" <<<"$details" &&
    grep -q "^Timestamp=" <<<"$details" && grep -q "flags=.*runtime" <<<"$details" ||
    fail "${code#"$app"/} is not signed with the Developer ID, a timestamp and the hardened runtime."
done
# The am the app carries is the daemon it installs, so it must be the release
# being made: a stale build left in the bundle would set up an older daemon.
bundled="$("$app/Contents/Helpers/am" version --json 2>/dev/null | /usr/bin/python3 -c 'import json,sys; print(json.load(sys.stdin).get("version", ""))')"
[ "$bundled" = "$version" ] || fail "the am inside the app says it is ${bundled:-nothing}, not $version."
say "Signed: the app, the am it carries ($bundled) and Sparkle's helpers, with the Developer ID and the hardened runtime."

step "Making the disk image"
mkdir -p "$OUT/dmg"
ditto "$app" "$OUT/dmg/Agentman.app"
ln -s /Applications "$OUT/dmg/Applications"
# hdiutil warns on every run that this form is deprecated; only a failure is
# worth showing.
hdiutil create -volname Agentman -srcfolder "$OUT/dmg" -ov -format UDZO "$dmg" >/dev/null 2>"$OUT/hdiutil.err" ||
  { cat "$OUT/hdiutil.err" >&2; fail "could not make the disk image."; }
codesign --timestamp -s "Developer ID Application" "$dmg"

step "Notarizing (usually a few minutes)"
submission="$(xcrun notarytool submit "$dmg" --key "$asc_key" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER_ID" \
  --wait --output-format json 2>"$OUT/notarize.err" || true)"
read -r status submission_id < <(python3 -I -c 'import json, sys
try: s = json.loads(sys.stdin.read() or "{}")
except ValueError: s = {}
print(s.get("status") or "-", s.get("id") or "-")' <<<"$submission")
if [ "$status" != "Accepted" ]; then
  if [ "$submission_id" != "-" ]; then
    xcrun notarytool log "$submission_id" --key "$asc_key" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER_ID" \
      "$OUT/notary-log.json" >/dev/null 2>&1 || true
  fi
  fail "notarization ended \"$status\". Apple's log is $OUT/notary-log.json, notarytool's errors $OUT/notarize.err."
fi
xcrun stapler staple "$dmg" >/dev/null
gatekeeper="$(spctl -a -t open --context context:primary-signature -vv "$dmg" 2>&1 || true)"
grep -q "source=Notarized Developer ID" <<<"$gatekeeper" || fail "Gatekeeper does not accept the stapled DMG: $gatekeeper"
say "Notarized and stapled (submission $submission_id)."

step "Signing the update for Sparkle"
tools="$dd/SourcePackages/artifacts/sparkle/Sparkle/bin"
[ -x "$tools/sign_update" ] || fail "Sparkle's sign_update is not at $tools."
signature="$("$tools/sign_update" "${signing[@]}" -p "$dmg")"
"$tools/sign_update" "${signing[@]}" --verify "$dmg" "$signature" >/dev/null 2>&1 ||
  fail "the DMG's Sparkle signature does not verify."
length="$(stat -f%z "$dmg")"
sha256="$(shasum -a 256 "$dmg" | cut -d' ' -f1)"
say "Signed: $length bytes."

# MARK: Writing the release

# Adds the release to <dir>/site: the appcast's newest item, and every
# Download link pointed at the new DMG.
write_site() {
  local dir="$1"
  python3 -I - "$dir/site/appcast.xml" "$version" "$build" "$min_macos" "$dmg_url" "$length" "$signature" \
    "$release_url" "$notes" <<'PY'
import re, sys, xml.etree.ElementTree as ET
from email.utils import formatdate
path, version, build, min_macos, url, length, signature, release_url, notes_file = sys.argv[1:]
notes = open(notes_file).read().strip() if notes_file else f"[What's new in Agentman {version}]({release_url})"
notes = notes.replace("]]>", "]]]]><![CDATA[>")
item = f"""    <item>
      <title>Agentman {version}</title>
      <link>{release_url}</link>
      <pubDate>{formatdate(usegmt=True)}</pubDate>
      <sparkle:version>{build}</sparkle:version>
      <sparkle:shortVersionString>{version}</sparkle:shortVersionString>
      <sparkle:minimumSystemVersion>{min_macos}</sparkle:minimumSystemVersion>
      <sparkle:fullReleaseNotesLink>{release_url}</sparkle:fullReleaseNotesLink>
      <description sparkle:format="markdown"><![CDATA[{notes}]]></description>
      <enclosure url="{url}" length="{length}" type="application/octet-stream" sparkle:edSignature="{signature}"/>
    </item>
"""
text = open(path).read()
# A second run for the same build replaces its item rather than adding one.
text = re.sub(r"[ \t]*<item>(?:(?!</item>).)*<sparkle:version>" + re.escape(build) + r"</sparkle:version>.*?</item>\n?",
              "", text, flags=re.S)
at = text.find("    <item>")
if at < 0:
    at = text.index("  </channel>")
text = text[:at] + item + text[at:]
ET.fromstring(text.encode())  # still well formed
open(path, "w").write(text)
PY
  local page
  for page in "$dir"/site/*.html; do
    sed -i '' -E "s#releases/download/v[0-9]+\.[0-9]+\.[0-9]+/Agentman\.dmg#releases/download/$tag/Agentman.dmg#g" "$page"
  done
}

# The cask behind `brew install --cask agentman`. It installs the same DMG
# the site links to; auto_updates tells Homebrew that Sparkle keeps the app
# current, so `brew upgrade` leaves it alone. zap leaves ~/.agentman, which
# belongs to am as much as to the app.
write_cask() {
  cat >"$1" <<EOF
# Agentman for Mac, written for each Mac release by scripts/release-mac.sh
# in https://github.com/$REPO: change the script, not this file.
cask "agentman" do
  version "$version"
  sha256 "$sha256"

  url "https://github.com/$REPO/releases/download/v#{version}/Agentman.dmg"
  name "Agentman"
  desc "Watch your coding agents and answer them when they get stuck"
  homepage "$SITE/mac"

  livecheck do
    url "$SITE/appcast.xml"
    strategy :sparkle, &:short_version
  end

  auto_updates true
  depends_on macos: :sequoia

  app "Agentman.app"
  # Once the app carries am, this puts it on PATH from inside the app, so
  # one install gives both; it then also needs
  # conflicts_with cask: "agentman-cli", which links an am of its own.
  # binary "#{appdir}/Agentman.app/Contents/Helpers/am"

  zap trash: [
    "~/Library/Application Support/dev.agentman.mac",
    "~/Library/Caches/dev.agentman.mac",
    "~/Library/HTTPStorages/dev.agentman.mac",
    "~/Library/Preferences/dev.agentman.mac.plist",
    "~/Library/Saved Application State/dev.agentman.mac.savedState",
    "~/Library/WebKit/dev.agentman.mac",
  ]
end
EOF
}

# The tap's `agentman` stays the CLI until it has been renamed agentman-cli
# and the rename has had time to reach people (docs/development.md,
# "Homebrew"). Writing the app over it before then would turn every CLI
# install into the app on its next upgrade.
tap_ready() {
  local dir="$1"
  if [ -f "$dir/Casks/agentman.rb" ] && grep -q 'binary "am"' "$dir/Casks/agentman.rb"; then return 1; fi
  if [ -f "$dir/cask_renames.json" ] && grep -q '"agentman"[[:space:]]*:' "$dir/cask_renames.json"; then return 1; fi
  return 0
}

write_cask "$OUT/agentman.rb"

if [ "$publish" = 0 ]; then
  write_site "$ROOT"
  step "Ready. Nothing public has changed."
  say "Built:   $dmg"
  say "Cask:    $OUT/agentman.rb"
  say "Changed: site/appcast.xml and the Download links in site/, in this checkout."
  say ""
  say "Left to do, in this order (--publish does these three after the build):"
  say ""
  say "  1. Upload the DMG to the $tag release:"
  say "       gh release upload $tag \"$dmg\" --clobber -R $REPO"
  say "  2. Open the site PR. Merging it deploys the appcast, and every copy of the app"
  say "     then offers the update, so the DMG must be up first:"
  say "       git fetch origin && git switch -c site/mac-$version origin/main"
  say "       git commit -am \"Site: Download for Mac gets Agentman $version\""
  say "       git push -u origin site/mac-$version && gh pr create --fill"
  say "  3. Put $OUT/agentman.rb in $TAP as Casks/agentman.rb,"
  say "     once \`agentman\` there is the app's name (docs/development.md, \"Homebrew\")."
  exit 0
fi

# MARK: Publishing

step "Uploading Agentman.dmg to the $tag release"
gh release upload "$tag" "$dmg" --clobber -R "$REPO"

step "Opening the site PR"
branch="site/mac-$version"
site_tree="$OUT/site-worktree"
git fetch -q origin main
git worktree remove --force "$site_tree" >/dev/null 2>&1 || rm -rf "$site_tree"
git worktree add -q -B "$branch" "$site_tree" origin/main
write_site "$site_tree"
git -C "$site_tree" add site
git -C "$site_tree" commit -q -m "Site: Download for Mac gets Agentman $version" \
  -m "The Download buttons link to the $version DMG, and the appcast offers it to every copy of the app, signed for Sparkle. The DMG is already on the $tag release."
git -C "$site_tree" push -q -u origin "$branch"
(cd "$site_tree" && gh pr create --base main --head "$branch" --title "Site: Download for Mac gets Agentman $version" \
  --body "The Download buttons and the appcast move to Agentman $version ($build). Merging deploys the appcast, which is when the app starts offering the update.")
git worktree remove --force "$site_tree"

step "Updating the Homebrew tap"
tap_tree="$OUT/tap"
rm -rf "$tap_tree"
gh repo clone "$TAP" "$tap_tree" -- -q
if tap_ready "$tap_tree"; then
  mkdir -p "$tap_tree/Casks"
  cp "$OUT/agentman.rb" "$tap_tree/Casks/agentman.rb"
  git -C "$tap_tree" add Casks/agentman.rb
  git -C "$tap_tree" commit -q -m "agentman $version" -m "Agentman for Mac $version ($build), from scripts/release-mac.sh."
  git -C "$tap_tree" push -q
  say "Casks/agentman.rb is Agentman $version."
else
  say "Skipped: \`agentman\` in $TAP is still the CLI, or still renamed to agentman-cli."
  say "The cask is $OUT/agentman.rb for when it moves (docs/development.md, \"Homebrew\")."
fi

step "Released Agentman $version. Merge the site PR to offer the update."
