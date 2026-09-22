#!/usr/bin/env bash
# One-liner install: detects this Mac's architecture and downloads the
# matching prebuilt binary straight from the latest GitHub Release, then
# installs it. No source code, no git clone, no Go toolchain needed.
set -euo pipefail

case "$(uname -m)" in
  arm64) ARCH=arm64 ;;
  x86_64) ARCH=amd64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

URL="https://github.com/ninhlee99/vpn/releases/latest/download/vpn-darwin-$ARCH"
BIN="$(mktemp -t vpn-download)"
trap 'rm -f "$BIN"' EXIT

echo "Downloading $URL..."
curl -fsSL -o "$BIN" "$URL"
chmod +x "$BIN"
"$BIN" version >/dev/null # sanity check it actually runs before installing it

OWNER_UID="$(id -u)"
sudo mv "$BIN" /usr/local/bin/vpn
trap - EXIT
sudo chown root:wheel /usr/local/bin/vpn
sudo chmod 4755 /usr/local/bin/vpn

echo "$OWNER_UID" | sudo tee /etc/vpn-owner-uid >/dev/null
sudo chown root:wheel /etc/vpn-owner-uid
sudo chmod 600 /etc/vpn-owner-uid

echo
echo "Installed: $(vpn version)"
echo "Next: vpn init"
