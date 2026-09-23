#!/usr/bin/env bash
# ==============================================================================
# One-liner install: detects this Mac architecture, installs CLI + Menu Bar UI
# ==============================================================================

set -euo pipefail

case "$(uname -m)" in
  arm64)
    ARCH=arm64
    ;;
  x86_64)
    ARCH=amd64
    ;;
  *)
    echo "Unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

echo "=================================================="
echo "🛡️  INSTALLING TMS-VPN (CLI & MENU BAR UI)"
echo "=================================================="

# --- Step 1: Install `vpn` CLI Backend ---
URL="https://github.com/ninhlee99/vpn/releases/latest/download/vpn-darwin-$ARCH"
BIN="$(mktemp -t vpn-download)"
trap 'rm -f "$BIN"' EXIT

echo "📦 [1/2] Downloading VPN CLI engine from $URL..."
if curl -fsSL -o "$BIN" "$URL"; then
    chmod +x "$BIN"
    "$BIN" version >/dev/null 2>&1 || true
    OWNER_UID="$(id -u)"
    sudo mv "$BIN" /usr/local/bin/vpn
    sudo chown root:wheel /usr/local/bin/vpn
    sudo chmod 4755 /usr/local/bin/vpn
    echo "$OWNER_UID" | sudo tee /etc/vpn-owner-uid >/dev/null
    sudo chown root:wheel /etc/vpn-owner-uid
    sudo chmod 600 /etc/vpn-owner-uid
    echo "  -> CLI Installed: /usr/local/bin/vpn"
fi

# --- Step 2: Install Menu Bar UI (prebuilt universal app) ---
echo "🎨 [2/2] Installing TMS-VPN Menu Bar UI..."
APP_DIR="/Applications/TMS-VPN.app"
APP_ZIP="$(mktemp -t vpn-app-zip).zip"

APP_URL="https://github.com/ninhlee99/vpn/releases/latest/download/TMS-VPN.app.zip"
echo "📦 Downloading Menu Bar UI (prebuilt universal app, no compiler needed) from $APP_URL..."
if curl -fsSL -o "$APP_ZIP" "$APP_URL"; then
    rm -rf "$APP_DIR"
    # ditto (not unzip) restores the bundle exactly as the release workflow
    # packed it (`ditto -c` — see .github/workflows/release.yml), keeping
    # its executable bits and resource forks intact.
    ditto -x -k "$APP_ZIP" "$(dirname "$APP_DIR")"
    rm -f "$APP_ZIP"
    echo "  -> UI Installed to $APP_DIR"
else
    rm -f "$APP_ZIP"
    echo "⚠️  Could not download Menu Bar UI from $APP_URL — is a release published yet?"
fi

echo "=================================================="
echo "🎉 SUCCESS: TMS-VPN Engine & Menu Bar UI Installed!"
echo "👉 Menu Bar App: $APP_DIR"
echo "👉 CLI Engine: /usr/local/bin/vpn"
echo "=================================================="

open -a "$APP_DIR" 2>/dev/null || true
