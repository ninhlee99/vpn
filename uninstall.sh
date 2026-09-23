#!/usr/bin/env bash
# ==============================================================================
# TMS VPN Complete Uninstaller Script
# Removes CLI engine, Menu Bar App, Background Daemons, and Configurations
# ==============================================================================

set -euo pipefail

echo "=================================================="
echo "🗑️  UNINSTALLING TMS VPN (CLI & MENU BAR APP)"
echo "=================================================="

# 1. Stop any running processes
echo "⏹️ [1/4] Stopping running VPN processes & Menu Bar UI..."
killall "TMS VPN" 2>/dev/null || true
sudo killall -9 vpn 2>/dev/null || true
sudo /usr/local/bin/vpn disconnect 2>/dev/null || true
sudo /usr/local/bin/vpn repair 2>/dev/null || true

# 2. Remove Application & Binaries
echo "📂 [2/4] Removing Application bundle & CLI binaries..."
sudo rm -rf "/Applications/TMS VPN.app"
sudo rm -f "/usr/local/bin/vpn"
sudo rm -f "/usr/local/bin/tms-vpn-bar"
sudo rm -f "/etc/vpn-owner-uid"

# 3. Clean runtime log and temporary state
echo "🧹 [3/4] Cleaning system log files and runtime state..."
sudo rm -f "/var/log/vpn.log"
sudo rm -f "/var/run/vpn.pid" 2>/dev/null || true
sudo rm -f "/var/run/vpn.state" 2>/dev/null || true

# 4. Optional: remove user configuration & profiles
echo "⚙️ [4/4] Removing user configs (~/.config/vpn)..."
rm -rf "$HOME/.config/vpn"

echo "=================================================="
echo "✅ TMS VPN has been completely uninstalled!"
echo "=================================================="
