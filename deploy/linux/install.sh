#!/bin/sh
# Install OpenRemote as a systemd service.
#   sudo ./install.sh                 # uses ./openremote or downloads the latest release
#   sudo ./install.sh path/to/binary
#   sudo ./install.sh --uninstall
#   curl -fsSL https://raw.githubusercontent.com/maros-o/openremote/main/deploy/linux/install.sh | sudo sh
set -eu

REPO="${REPO:-maros-o/openremote}"
BIN=/usr/local/bin/openremote
UNIT=/etc/systemd/system/openremote.service
ENVF=/etc/default/openremote
HERE="$(cd "$(dirname "$0")" && pwd)"

fetch() { if command -v curl >/dev/null; then curl -fsSL "$1" -o "$2"; else wget -qO "$2" "$1"; fi; }

[ "$(id -u)" -eq 0 ] || { echo "Please run as root (sudo $0)"; exit 1; }

if [ "${1:-}" = "--uninstall" ]; then
  systemctl disable --now openremote 2>/dev/null || true
  rm -f "$BIN" "$UNIT" /etc/modules-load.d/openremote.conf
  systemctl daemon-reload
  echo "OpenRemote removed (settings kept in $ENVF)."
  exit 0
fi

src="${1:-}"
if [ -z "$src" ]; then
  for c in ./openremote "$HERE/../../openremote"; do [ -x "$c" ] && src="$c" && break; done
fi
if [ -z "$src" ]; then
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    armv7l) arch=armv7 ;;
    armv6l) arch=armv6 ;;
    *) echo "Unsupported architecture $(uname -m)"; exit 1 ;;
  esac
  url="https://github.com/$REPO/releases/latest/download/openremote-linux-$arch"
  echo "Downloading $url"
  src="$(mktemp)"
  fetch "$url" "$src"
fi

# Piped from curl: the service and config files aren't next to the script.
if [ ! -f "$HERE/openremote.service" ]; then
  HERE="$(mktemp -d)"
  raw="https://raw.githubusercontent.com/$REPO/main/deploy/linux"
  fetch "$raw/openremote.service" "$HERE/openremote.service"
  fetch "$raw/openremote.env" "$HERE/openremote.env"
fi

install -m 0755 "$src" "$BIN"
install -m 0644 "$HERE/openremote.service" "$UNIT"
[ -f "$ENVF" ] || install -m 0644 "$HERE/openremote.env" "$ENVF"
echo uinput > /etc/modules-load.d/openremote.conf
modprobe uinput || true
systemctl daemon-reload
systemctl enable openremote
systemctl restart openremote   # also picks up a new binary when updating
sleep 1
systemctl --no-pager --lines=0 status openremote || true
echo
echo "OpenRemote is running. Open http://$(. "$ENVF"; echo "${OPENREMOTE_HOST:-remote}").local on your phone."
echo "Settings: $ENVF   Logs: journalctl -u openremote -f"
