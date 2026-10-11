#!/bin/sh
# Install OpenCouchpad on macOS as a login item (LaunchAgent). No sudo needed.
#   curl -fsSL https://raw.githubusercontent.com/maros-o/opencouchpad/main/deploy/macos/install.sh | sh
#   ... | sh -s -- --name "Mac mini" --host mac     # extra flags are passed to opencouchpad
#   ... | sh -s -- --uninstall
#   ./install.sh path/to/binary [flags]
set -eu

REPO="${REPO:-maros-o/opencouchpad}"
DIR="$HOME/Library/Application Support/OpenCouchpad"
BIN="$DIR/opencouchpad"

[ "$(uname -s)" = Darwin ] || { echo "This installer is for macOS. On Linux use deploy/linux/install.sh."; exit 1; }
[ "$(id -u)" -ne 0 ] || { echo "Run this as your normal user, without sudo: OpenCouchpad runs in your login session."; exit 1; }

if [ "${1:-}" = "--uninstall" ]; then
  [ -x "$BIN" ] && "$BIN" --uninstall || true
  rm -f "$BIN"
  echo "OpenCouchpad removed."
  exit 0
fi

src=""
if [ -n "${1:-}" ] && [ -f "$1" ]; then
  src="$1"
  shift
fi
if [ -z "$src" ]; then
  case "$(uname -m)" in
    arm64) arch=arm64 ;;
    x86_64) arch=amd64 ;;
    *) echo "Unsupported architecture $(uname -m)"; exit 1 ;;
  esac
  url="https://github.com/$REPO/releases/latest/download/opencouchpad-darwin-$arch"
  echo "Downloading $url"
  src="$(mktemp)"
  curl -fsSL "$url" -o "$src"
fi

mkdir -p "$DIR"
cp "$src" "$BIN.new"
chmod 0755 "$BIN.new"
xattr -d com.apple.quarantine "$BIN.new" 2>/dev/null || true
mv -f "$BIN.new" "$BIN"

# Starts it now and at every login. Replaces a running older version.
"$BIN" --install "$@"
echo
echo "Open http://remote.local on your phone (or the name you gave with --host)."
echo "After an update, macOS may ask for Accessibility access again."
