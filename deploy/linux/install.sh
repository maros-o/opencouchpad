#!/bin/sh
# Install OpenCouchpad as a systemd service.
#   sudo ./install.sh                 # uses ./opencouchpad or downloads the latest release
#   sudo ./install.sh path/to/binary
#   sudo ./install.sh --uninstall
#   curl -fsSL https://raw.githubusercontent.com/maros-o/opencouchpad/main/deploy/linux/install.sh | sudo sh
set -eu

REPO="${REPO:-maros-o/opencouchpad}"
BIN=/usr/local/bin/opencouchpad
UNIT=/etc/systemd/system/opencouchpad.service
ENVF=/etc/default/opencouchpad
HERE="$(cd "$(dirname "$0")" && pwd)"

fetch() { if command -v curl >/dev/null; then curl -fsSL "$1" -o "$2"; else wget -qO "$2" "$1"; fi; }

# Open (or with "delete", close) the web port and mDNS in an active ufw / firewalld.
firewall() {
  del="${1:-}"
  port="$( [ -f "$ENVF" ] && . "$ENVF"; echo "${OPENCOUCHPAD_PORT:-80}")"
  if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
    ufw $del allow "$port/tcp" comment opencouchpad >/dev/null 2>&1 || ufw $del allow "$port/tcp" >/dev/null || true
    ufw $del allow 5353/udp comment opencouchpad >/dev/null 2>&1 || ufw $del allow 5353/udp >/dev/null || true
    if [ -n "$del" ]; then echo "ufw: removed $port/tcp and 5353/udp"; else echo "ufw: allowed $port/tcp and 5353/udp (mDNS)"; fi
  elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
    op=add; [ -n "$del" ] && op=remove
    firewall-cmd --permanent --$op-port="$port/tcp" --$op-service=mdns >/dev/null || true
    firewall-cmd --reload >/dev/null || true
    echo "firewalld: ${op} $port/tcp and mdns"
  fi
}

[ "$(id -u)" -eq 0 ] || { echo "Please run as root (sudo $0)"; exit 1; }

if [ "${1:-}" = "--uninstall" ]; then
  systemctl disable --now opencouchpad 2>/dev/null || true
  rm -f "$BIN" "$UNIT" /etc/modules-load.d/opencouchpad.conf
  firewall delete
  systemctl daemon-reload
  echo "OpenCouchpad removed (settings kept in $ENVF)."
  exit 0
fi

src="${1:-}"
if [ -z "$src" ]; then
  for c in ./opencouchpad "$HERE/../../opencouchpad"; do [ -x "$c" ] && src="$c" && break; done
fi
if [ -z "$src" ]; then
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    armv7l) arch=armv7 ;;
    armv6l) arch=armv6 ;;
    *) echo "Unsupported architecture $(uname -m)"; exit 1 ;;
  esac
  url="https://github.com/$REPO/releases/latest/download/opencouchpad-linux-$arch"
  echo "Downloading $url"
  src="$(mktemp)"
  fetch "$url" "$src"
fi

# Piped from curl: the service and config files aren't next to the script.
if [ ! -f "$HERE/opencouchpad.service" ]; then
  HERE="$(mktemp -d)"
  raw="https://raw.githubusercontent.com/$REPO/main/deploy/linux"
  fetch "$raw/opencouchpad.service" "$HERE/opencouchpad.service"
  fetch "$raw/opencouchpad.env" "$HERE/opencouchpad.env"
fi

install -m 0755 "$src" "$BIN"
install -m 0644 "$HERE/opencouchpad.service" "$UNIT"
[ -f "$ENVF" ] || install -m 0644 "$HERE/opencouchpad.env" "$ENVF"
echo uinput > /etc/modules-load.d/opencouchpad.conf
modprobe uinput || true
systemctl daemon-reload
systemctl enable opencouchpad
systemctl restart opencouchpad   # also picks up a new binary when updating
firewall
sleep 1
systemctl --no-pager --lines=0 status opencouchpad || true
echo
echo "OpenCouchpad is running. Open http://$(. "$ENVF"; echo "${OPENCOUCHPAD_HOST:-remote}").local on your phone."
echo "Settings: $ENVF   Logs: journalctl -u opencouchpad -f"
