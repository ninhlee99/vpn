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
vpn update       # tải binary release đúng kiến trúc rồi cài đè
vpn uninstall    # xoá sạch: binary, log, state, toàn bộ profile/account (kể cả PSK/password trong Keychain)
```
