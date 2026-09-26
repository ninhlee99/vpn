#!/usr/bin/env bash
set -euo pipefail

if [ -n "${SUDO_UID:-}" ] && [ "$SUDO_UID" != "0" ]; then
    OWNER_UID="$SUDO_UID"
elif [ "$(id -u)" != "0" ]; then
    OWNER_UID="$(id -u)"
else
    OWNER_UID="$(stat -f '%u' /dev/console 2>/dev/null || echo 501)"
fi

echo "🚀 [1/3] Làm sạch và biên dịch Menu Bar App + Go CLI..."
sudo rm -rf ./build
./build.sh
go build -o ./build/vpn ./cmd/vpn

echo "📦 [2/3] Cài đặt CLI Engine (/usr/local/bin/vpn)..."
sudo install -o root -g wheel -m 4755 ./build/vpn /usr/local/bin/vpn
echo "$OWNER_UID" | sudo tee /etc/vpn-owner-uid >/dev/null
sudo chown root:wheel /etc/vpn-owner-uid
sudo chmod 600 /etc/vpn-owner-uid

echo "🎨 [3/3] Cài đặt Menu Bar App (/Applications/TMS VPN.app)..."
sudo rm -rf "/Applications/TMS VPN.app"
sudo cp -R "./build/TMS VPN.app" /Applications/
sudo chown -R "$OWNER_UID:staff" "/Applications/TMS VPN.app"

echo "=================================================="
echo "🎉 Cài đặt thành công bản mới nhất (8MB Window & Pipelined Send)!"
echo "👉 CLI Engine: /usr/local/bin/vpn"
echo "👉 Menu Bar App: /Applications/TMS VPN.app"
echo "=================================================="
