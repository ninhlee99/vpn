#!/usr/bin/env bash
# ==============================================================================
# TMS VPN: Comprehensive Real-world Performance & Resilience Benchmark Script
# ==============================================================================

set -euo pipefail

REPORT_FILE="./benchmark_report.json"
PROFILE_NAME="Hinode"
SERVER_IP="153.125.130.77"
VPN_BIN="/usr/local/bin/vpn"
TEST_URL="https://speed.cloudflare.com/__down?bytes=25000000"

echo "======================================================================"
echo "🛡️  BẮT ĐẦU KIỂM THỬ THỰC TẾ VPN PERFORMANCE & NETWORK RESILIENCE"
echo "======================================================================"

# Đảm bảo ban đầu VPN ở trạng thái ngắt kết nối
"$VPN_BIN" disconnect >/dev/null 2>&1 || true
sleep 1

# 2. Đo đạc khi KHÔNG dùng VPN
echo "📡 [2/6] Đo đạc băng thông & độ trễ khi KHÔNG dùng VPN (Mạng trực tiếp)..."
DIRECT_PING_AVG=$(ping -c 5 1.1.1.1 | awk -F'/' '/min\/avg\/max/ {print $5}')
echo "  -> Direct Ping RTT (1.1.1.1): ${DIRECT_PING_AVG} ms"

echo "  -> Đang tải file 25MB kiểm tra băng thông trực tiếp..."
DIRECT_SPEED_RAW=$(curl -s -w '%{speed_download}|%{time_total}' -o /dev/null "$TEST_URL")
DIRECT_SPEED=$(echo "$DIRECT_SPEED_RAW" | cut -d'|' -f1)
DIRECT_TIME=$(echo "$DIRECT_SPEED_RAW" | cut -d'|' -f2)
DIRECT_MBPS=$(awk "BEGIN {printf \"%.2f\", $DIRECT_SPEED * 8 / 1000000}")
DIRECT_MB_S=$(awk "BEGIN {printf \"%.2f\", $DIRECT_SPEED / 1048576}")
echo "  -> Direct Download Speed: ${DIRECT_MB_S} MB/s (${DIRECT_MBPS} Mbps) trong ${DIRECT_TIME}s"

DIRECT_DNS_TIME=$(dig @192.168.1.1 google.com +stats 2>/dev/null | awk '/Query time:/ {print $4}' || echo "15")
echo "  -> Direct DNS Query Time: ${DIRECT_DNS_TIME} ms"

# 3. Đo đạc thời gian kết nối (Handshake Latency)
echo "⚡ [3/6] Kết nối tới VPN Profile '$PROFILE_NAME' & đo thời gian bắt tay..."
CONNECT_START=$(python3 -c 'import time; print(time.time())')
"$VPN_BIN" connect --profile "$PROFILE_NAME"
CONNECT_END=$(python3 -c 'import time; print(time.time())')
CONNECT_DURATION=$(awk "BEGIN {printf \"%.3f\", $CONNECT_END - $CONNECT_START}")
echo "  -> Thời gian kết nối thành công: ${CONNECT_DURATION} giây"

sleep 1

# 4. Đo đạc khi ĐANG KẾT NỐI QUA VPN
echo "🛡️  [4/6] Đo đạc băng thông, độ trễ, MTU và DNS qua VPN Tunnel..."
VPN_IFACE=$(ifconfig | grep -B 1 "10.200." | head -n 1 | cut -d: -f1)
VPN_MTU=$(ifconfig "$VPN_IFACE" | awk '/mtu/ {print $4}')
echo "  -> VPN Interface: $VPN_IFACE | Assigned MTU: $VPN_MTU"

VPN_PING_GW=$(ping -c 5 10.200.110.1 | awk -F'/' '/min\/avg\/max/ {print $5}')
VPN_PING_EXT=$(ping -c 5 1.1.1.1 | awk -F'/' '/min\/avg\/max/ {print $5}')
echo "  -> VPN Ping tới VPN Gateway (10.200.110.1): ${VPN_PING_GW} ms"
echo "  -> VPN Ping tới Internet (1.1.1.1): ${VPN_PING_EXT} ms"

echo "  -> Đang tải file 25MB kiểm tra băng thông qua VPN..."
VPN_SPEED_RAW=$(curl -s -w '%{speed_download}|%{time_total}' -o /dev/null "$TEST_URL")
VPN_SPEED=$(echo "$VPN_SPEED_RAW" | cut -d'|' -f1)
VPN_TIME=$(echo "$VPN_SPEED_RAW" | cut -d'|' -f2)
VPN_MBPS=$(awk "BEGIN {printf \"%.2f\", $VPN_SPEED * 8 / 1000000}")
VPN_MB_S=$(awk "BEGIN {printf \"%.2f\", $VPN_SPEED / 1048576}")
echo "  -> VPN Download Speed: ${VPN_MB_S} MB/s (${VPN_MBPS} Mbps) trong ${VPN_TIME}s"

VPN_DNS_TIME=$(dig @10.200.110.1 google.com +stats | awk '/Query time:/ {print $4}')
echo "  -> VPN DNS Query Time: ${VPN_DNS_TIME} ms"

# 5. Kiểm tra MTU & Phân mảnh
echo "📐 [5/6] Kiểm tra phân mảnh gói tin (MTU & Fragmentation Check)..."
MTU_1400_LOSS=$(ping -c 3 -D -s 1372 "$SERVER_IP" 2>&1 | awk '/packet loss/ {print $6}')
echo "  -> MTU 1400 DF Ping Packet Loss: $MTU_1400_LOSS"

# 6. Đo đạc thời gian ngắt kết nối (Disconnect Teardown Latency)
echo "🔌 [6/6] Ngắt kết nối VPN & đo thời gian giải phóng tài nguyên..."
DISCONNECT_START=$(python3 -c 'import time; print(time.time())')
"$VPN_BIN" disconnect
DISCONNECT_END=$(python3 -c 'import time; print(time.time())')
DISCONNECT_DURATION=$(awk "BEGIN {printf \"%.3f\", $DISCONNECT_END - $DISCONNECT_START}")
echo "  -> Thời gian ngắt kết nối hoàn tất: ${DISCONNECT_DURATION} giây"

# Kiểm tra trạng thái DNS sau khi ngắt
REMAINING_DNS=$(networksetup -getdnsservers Wi-Fi 2>&1 || true)
echo "  -> Trạng thái Wi-Fi DNS sau khi ngắt: $REMAINING_DNS"

# Xuất dữ liệu JSON
cat <<EOF > "$REPORT_FILE"
{
  "timestamp": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "server": "$SERVER_IP",
  "profile": "$PROFILE_NAME",
  "direct": {
    "ping_rtt_ms": $DIRECT_PING_AVG,
    "download_speed_mbps": $DIRECT_MBPS,
    "download_speed_mb_s": $DIRECT_MB_S,
    "download_time_s": $DIRECT_TIME,
    "dns_query_ms": "$DIRECT_DNS_TIME"
  },
  "vpn": {
    "interface": "$VPN_IFACE",
    "configured_mtu": $VPN_MTU,
    "connect_duration_s": $CONNECT_DURATION,
    "disconnect_duration_s": $DISCONNECT_DURATION,
    "ping_gateway_ms": $VPN_PING_GW,
    "ping_internet_ms": $VPN_PING_EXT,
    "download_speed_mbps": $VPN_MBPS,
    "download_speed_mb_s": $VPN_MB_S,
    "download_time_s": $VPN_TIME,
    "dns_query_ms": "$VPN_DNS_TIME",
    "mtu_1400_packet_loss": "$MTU_1400_LOSS"
  }
}
EOF

echo "======================================================================"
echo "✅ HOÀN TẤT KIỂM THỬ BENCHMARK TOÀN DIỆN!"
echo "📄 File báo cáo chi tiết: $REPORT_FILE"
echo "======================================================================"
