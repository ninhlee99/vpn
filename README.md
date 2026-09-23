# vpn

VPN client L2TP/IPsec thuần macOS.

## Cài đặt

Tải thẳng binary build sẵn từ GitHub Release rồi copy vào `/usr/local/bin` — không tải source code, không cần Go trên máy đích:

```bash
curl -fsSL https://raw.githubusercontent.com/ninhlee99/vpn/main/install.sh | bash
```

Tự nhận diện kiến trúc máy (Apple Silicon hay Intel). Muốn chỉ định thẳng thay vì tự nhận diện:

```bash
curl -fsSL https://raw.githubusercontent.com/ninhlee99/vpn/main/install-arm64.sh | bash   # Apple Silicon (M1/M2/M3...)
curl -fsSL https://raw.githubusercontent.com/ninhlee99/vpn/main/install-intel.sh | bash   # Mac Intel
```

Cả 3 cách đều cài `vpn` vào `/usr/local/bin` (setuid-root — xem phần dưới), sau đó dùng không cần gõ `sudo` nữa.

Installer đối chiếu CLI và app với `SHA256SUMS` của release, nên chống được file tải về bị hỏng. Nhưng vì chính script cũng được tải từ repo này, nó **không** chống được trường hợp repo bị chiếm. `vpn update` thì mạnh hơn: nó verify chữ ký ed25519 của release bằng public key nhúng sẵn trong binary đang cài (xem mục Phát hành).

## Setup lần đầu

Mở app **TMS VPN** trên menu bar → **+ Thêm điểm nối**, nhập server, tài khoản, mật khẩu, PSK. App gọi CLI để lưu, PSK/password nằm trong Keychain.

Hoặc từ terminal:

```bash
vpn profile add <tên> --server <host>       # hỏi PSK nếu không truyền --psk
vpn account add <tên> <username> --default  # hỏi password nếu không truyền --password
```

## Dùng hằng ngày

```bash
vpn connect       # kết nối, tự chạy nền — trả lại terminal ngay khi biết kết quả, không cần sudo
vpn disconnect    # ngắt
vpn status        # xem đang connected hay chưa
```

`vpn connect` luôn tự tách tiến trình chạy nền (không cần `&`) — lệnh chỉ đứng chờ tới khi biết chắc kết nối thành công hay thất bại rồi mới trả lại terminal, sau đó tiến trình vẫn tiếp tục chạy nền cho tới khi bạn `vpn disconnect`.

### Vì sao không cần sudo

Khi cài, installer ghi UID của người vừa chạy vào file root-only
`/etc/vpn-owner-uid`; binary đọc file này lúc chạy. Cách này cho phép cùng
binary build sẵn từ CI dùng cho mọi máy, nhưng vẫn chỉ cho user đã cài chạy.
Binary tự hạ quyền về user thường ngay
khi khởi động, chỉ tạm nâng lại quyền root đúng lúc thật sự cần (mở utun, bind
UDP/500, đổi route/DNS) rồi hạ ngay sau đó — không giữ quyền root suốt phiên
kết nối. **Chỉ đúng user đã cài mới gọi được `vpn`** — user khác trên máy chạy
lệnh này sẽ bị từ chối ngay lập tức, kể cả các lệnh không cần quyền root.

## Nhiều server

```bash
vpn profile add <tên> --server <host>       # thêm server khác
vpn profile remove <tên>                    # xoá profile (kèm PSK/password trong Keychain)
vpn connect --profile <tên>                 # kết nối vào profile cụ thể
```

Chọn server, bật/tắt kết nối và sửa hồ sơ hằng ngày thì dùng app TMS VPN trên menu bar.

## Sự cố

```bash
vpn diagnose   # kiểm tra mạng trước khi connect, không đổi gì trên máy
vpn repair     # dọn route/DNS nếu connect bị crash/kill giữa chừng
vpn logs -f    # xem log
```

## Cập nhật / gỡ cài đặt

```bash
vpn update       # tải release mới nhất, verify chữ ký + SHA-256, chỉ cài nếu mới hơn bản đang chạy
vpn uninstall    # xoá sạch: binary, log, state, toàn bộ profile/account (kể cả PSK/password trong Keychain)
```

## Phát hành

Release chỉ được tạo khi push tag semver, sau khi CI (`gofmt`, `go vet`, `go test` trên macOS) pass:

```bash
git tag v1.2.3 && git push origin v1.2.3
```

Mỗi release gồm `vpn-darwin-arm64`, `vpn-darwin-amd64`, `TMS-VPN.app.zip`, `SHA256SUMS` và `SHA256SUMS.sig` (chữ ký ed25519 của `SHA256SUMS`, phủ cả 3 asset). `vpn update` từ chối release nếu chữ ký không khớp `release.PublicKey`, nếu SHA-256 không khớp, hoặc nếu version không mới hơn bản đang chạy (chống replay bản cũ; `--force` để bỏ qua).

Khóa ký nằm trong secret `RELEASE_SIGNING_KEY` của repo (base64 seed ed25519). Tạo cặp khóa mới:

```bash
go run ./cmd/releasesign keygen   # stdout: seed → secret; stderr: public key → release.PublicKey
```
