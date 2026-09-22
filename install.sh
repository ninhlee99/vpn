#!/usr/bin/env bash
# Cài Go (nếu chưa có) rồi build + cài `vpn` vào /usr/local/bin, setuid-root
# để `vpn connect`/`disconnect`/`repair` không cần gõ `sudo` mỗi lần.
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
  echo "Chưa có Go, đang cài..."
  if ! command -v brew >/dev/null 2>&1; then
    echo "Chưa có Homebrew, đang cài Homebrew trước..."
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  fi
  brew install go
fi

echo "Go: $(go version)"

cd "$(dirname "${BASH_SOURCE[0]}")"

SRC_DIR="$(pwd)"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo 1.0.0)"
OWNER_UID="$(id -u)"

echo "Đang build vpn ($VERSION)..."
go build -ldflags "-X main.version=$VERSION -X main.sourceDir=$SRC_DIR -X main.allowedUID=$OWNER_UID" -o /tmp/vpn-build ./cmd/vpn
sudo mv /tmp/vpn-build /usr/local/bin/vpn
sudo chown root:wheel /usr/local/bin/vpn
sudo chmod 4755 /usr/local/bin/vpn

cat <<EOF

Đã cài setuid-root cho /usr/local/bin/vpn, khoá riêng cho user hiện tại
(uid $OWNER_UID) — chỉ user này gọi được \`vpn\`, mọi user khác trên máy bị
từ chối ngay khi chạy, kể cả các lệnh không cần quyền root.

Với user này, \`vpn connect\`/\`disconnect\`/\`repair\` không cần gõ sudo nữa.
Binary tự hạ quyền về user thường ngay khi khởi động, chỉ tạm nâng lại
quyền root đúng lúc cần (mở utun, đổi route/DNS) rồi hạ ngay sau đó — xem
internal/privilege trong source nếu muốn kiểm tra lại cơ chế này.
EOF

echo
echo "Cài xong: $(vpn version)"
echo "Chạy tiếp: vpn init"
