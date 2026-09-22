#!/usr/bin/env bash
# Installs `vpn` for Intel Macs (amd64) by downloading the prebuilt binary
# directly — no Go, no source checkout needed. Standalone: safe to curl
# and run without cloning the repo first.
set -euo pipefail

URL="https://github.com/ninhlee99/vpn/releases/latest/download/vpn-darwin-amd64"
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
