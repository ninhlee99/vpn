#!/usr/bin/env bash
# ==============================================================================
# One-liner install: detects this Mac's architecture, installs both:
# 1) `vpn` CLI Engine (native Go setuid-root client from latest GitHub Release)
# 2) `TMS-VPN.app` Menu Bar UI (native Swift/SwiftUI status bar app)
# ==============================================================================

set -euo pipefail

case "$(uname -m)" in
  arm64)
    ARCH=arm64
    SWIFT_TARGET="arm64-apple-macos12.0"
    ;;
  x86_64)
    ARCH=amd64
    SWIFT_TARGET="x86_64-apple-macos12.0"
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
    echo "  -> CLI Installed: /usr/local/bin/vpn ($(vpn version 2>/dev/null || echo 'OK'))"
else
    echo "⚠️ Warning: Could not download prebuilt release CLI. Skipping CLI download."
fi

# --- Step 2: Build & Install Swift Menu Bar UI ---
echo "🎨 [2/2] Installing TMS-VPN Menu Bar UI..."
APP_DIR="/Applications/TMS-VPN.app"
SWIFT_SRC="$(mktemp -t vpn-swift-src).swift"
UI_BIN="$(mktemp -t vpn-ui-bin)"

echo "  -> Fetching main.swift..."
DOWNLOADED=false
for ref in "${BRANCH:-}" "feat/menubar-ui-and-installer" "main" "master"; do
    [ -z "$ref" ] && continue
    if curl -fsSL "https://raw.githubusercontent.com/ninhlee99/vpn/${ref}/main.swift" -o "$SWIFT_SRC" 2>/dev/null; then
        echo "  -> Fetched main.swift from ref: ${ref}"
        DOWNLOADED=true
        break
    fi
done

if [ "$DOWNLOADED" = false ]; then
    echo "❌ Error: Could not download main.swift from repository." >&2
    exit 1
fi

if command -v swiftc &>/dev/null; then
    echo "  -> Compiling native Swift Menu Bar UI ($ARCH)..."
    swiftc -O -target "$SWIFT_TARGET" -framework Cocoa -framework SwiftUI "$SWIFT_SRC" -o "$UI_BIN"

    pkill -f "TMS-VPN" 2>/dev/null || true
    pkill -f "tms-vpn-bar" 2>/dev/null || true

    rm -rf "$APP_DIR"
    mkdir -p "$APP_DIR/Contents/MacOS"
    mkdir -p "$APP_DIR/Contents/Resources"
    cp "$UI_BIN" "$APP_DIR/Contents/MacOS/TMS-VPN"
    chmod +x "$APP_DIR/Contents/MacOS/TMS-VPN"

    cat <<EOF > "$APP_DIR/Contents/Info.plist"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>TMS-VPN</string>
    <key>CFBundleIdentifier</key>
    <string>com.tms.vpn.menubar</string>
    <key>CFBundleName</key>
    <string>TMS-VPN</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>1.0.0</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
EOF

    sudo mkdir -p /usr/local/bin 2>/dev/null || true
    sudo cp "$UI_BIN" /usr/local/bin/tms-vpn-bar 2>/dev/null || cp "$UI_BIN" /usr/local/bin/tms-vpn-bar 2>/dev/null || true
    sudo chmod +x /usr/local/bin/tms-vpn-bar 2>/dev/null || true
    echo "  -> UI Installed to /Applications/TMS-VPN.app"
else
    echo "⚠️ swiftc compiler not found. Please install Command Line Tools (xcode-select --install) to build UI."
fi

rm -f "$SWIFT_SRC" "$UI_BIN"

echo "=================================================="
echo "🎉 SUCCESS: TMS-VPN Engine & Menu Bar UI Installed!"
echo "👉 Menu Bar App: /Applications/TMS-VPN.app"
echo "👉 CLI Engine: /usr/local/bin/vpn"
echo "=================================================="

# Launch Menu Bar App
open -a "/Applications/TMS-VPN.app" 2>/dev/null || true
