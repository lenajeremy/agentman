#!/bin/sh
# Install agentman's command-line tool, `am`, on macOS or Linux.
#
#   curl -fsSL https://agentman-nu.vercel.app/install | sh
#
# With Homebrew it installs from the agentman tap, so `brew upgrade` keeps it
# current. Without Homebrew it downloads the release for this machine from
# GitHub, checks it against the release's published checksums, and puts `am`
# in /usr/local/bin when that is writable, or ~/.local/bin when it is not. It
# never asks for sudo.
#
# Settings, all optional:
#   AGENTMAN_VERSION=0.14.0      a specific release instead of the latest
#   AGENTMAN_INSTALL_DIR=<dir>   where `am` goes, for the download route
#   AGENTMAN_NO_BREW=1           download even when Homebrew is installed
#
# The whole script is wrapped in main and called on the last line, so a
# download cut off half way through runs nothing at all.

set -eu

REPO="lenajeremy/agentman"
TAP="lenajeremy/agentman"

say() { printf '%s\n' "$*"; }
step() { printf '\n\033[1m%s\033[0m\n' "$*"; }
fail() { printf '\nagentman install: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

fetch() { # url [output]
  if have curl; then
    if [ -n "${2:-}" ]; then curl -fsSL --retry 2 -o "$2" "$1"; else curl -fsSL --retry 2 "$1"; fi
  elif have wget; then
    if [ -n "${2:-}" ]; then wget -q -O "$2" "$1"; else wget -q -O - "$1"; fi
  else
    fail "this needs curl or wget to download agentman."
  fi
}

sha256_of() {
  if have sha256sum; then sha256sum "$1" | cut -d' ' -f1
  elif have shasum; then shasum -a 256 "$1" | cut -d' ' -f1
  else fail "this needs sha256sum or shasum to check the download."
  fi
}

latest_version() {
  # The latest release's page redirects to its tag; reading the redirect needs
  # no API token and is not rate limited the way the API is.
  if have curl; then
    url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")
  else
    url=$(wget -q -S --spider "https://github.com/$REPO/releases/latest" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -1)
  fi
  tag=${url##*/}
  case "$tag" in
    v[0-9]*) printf '%s' "${tag#v}" ;;
    *) fail "could not work out the latest release (got \"$tag\")." ;;
  esac
}

install_with_brew() {
  step "Installing with Homebrew"
  # The fully qualified name, not `brew tap` then `brew install agentman`:
  # Homebrew 7 refuses a bare name from a tap nobody has trusted yet, while
  # naming the tap in the install is taken as that trust. Checked on 7.0.8.
  if brew list --cask agentman >/dev/null 2>&1; then
    brew upgrade --cask "$TAP/agentman" || true
  else
    brew install --cask "$TAP/agentman"
  fi
}

install_from_release() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    darwin|linux) ;;
    *) fail "agentman runs on macOS and Linux; this is $os." ;;
  esac
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) fail "there is no agentman build for $arch." ;;
  esac

  version=${AGENTMAN_VERSION:-$(latest_version)}
  version=${version#v}
  archive="agentman_v${version}_${os}_${arch}.tar.gz"
  base="https://github.com/$REPO/releases/download/v$version"

  step "Downloading agentman $version for $os/$arch"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT INT TERM
  fetch "$base/$archive" "$tmp/$archive" || fail "could not download $base/$archive"
  fetch "$base/checksums.txt" "$tmp/checksums.txt" || fail "could not download the release checksums."

  want=$(awk -v f="$archive" '$2 == f || $2 == "*"f { print $1 }' "$tmp/checksums.txt")
  [ -n "$want" ] || fail "the release checksums do not list $archive."
  got=$(sha256_of "$tmp/$archive")
  [ "$want" = "$got" ] || fail "the download does not match its published checksum. Nothing was installed."
  say "Checksum verified."

  tar -xzf "$tmp/$archive" -C "$tmp"
  [ -f "$tmp/am" ] || fail "the archive did not contain am."

  dir=${AGENTMAN_INSTALL_DIR:-}
  if [ -z "$dir" ]; then
    if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
  fi
  mkdir -p "$dir"
  # Copy to a temporary name and rename, so a running `am` is never left half
  # written.
  cp "$tmp/am" "$dir/.am.new"
  chmod 755 "$dir/.am.new"
  mv -f "$dir/.am.new" "$dir/am"
  if [ "$os" = darwin ]; then xattr -d com.apple.quarantine "$dir/am" 2>/dev/null || true; fi
  say "Installed $dir/am"

  case ":$PATH:" in
    *":$dir:"*) ;;
    *)
      say ""
      say "$dir is not on your PATH yet. Add this line to your shell's profile (~/.zshrc or ~/.bashrc):"
      say "  export PATH=\"$dir:\$PATH\""
      ;;
  esac
}

check_tmux() {
  have tmux && return 0
  say ""
  say "One more thing: agentman uses tmux to type into your agents and answer their prompts."
  if have brew; then say "Install it with:  brew install tmux"
  elif have apt-get; then say "Install it with:  sudo apt-get install tmux"
  elif have dnf; then say "Install it with:  sudo dnf install tmux"
  elif have pacman; then say "Install it with:  sudo pacman -S tmux"
  elif have apk; then say "Install it with:  sudo apk add tmux"
  else say "Install tmux with your package manager."
  fi
}

next_steps() {
  step "Next, set it up"
  say "  am install-hooks    let your agents tell agentman when they finish or need you (once)"
  say "  am serve            start agentman, and leave it running"
  say "  am pair             in a second terminal: shows a code to scan with the app"
  say ""
  say "Then start agents with am claude, am codex, am cursor, am kiro or am agy,"
  say "so you can message them from your phone. Get the app at https://agentman-nu.vercel.app/start"
}

main() {
  if have brew && [ -z "${AGENTMAN_NO_BREW:-}" ]; then
    install_with_brew
  else
    install_from_release
  fi
  check_tmux
  next_steps
}

main "$@"
