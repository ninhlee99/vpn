# vpn

VPN client L2TP/IPsec thuần macOS.

## Cài đặt

Máy chưa có sẵn source code — cài thẳng bằng 1 lệnh:

```bash
curl -fsSL https://raw.githubusercontent.com/ninhlee99/vpn/main/bootstrap.sh | bash
```

Lệnh này tự `git clone` source vào `~/.local/share/vpn-src` rồi chạy `install.sh` ở đó.

Hoặc nếu đã có sẵn source code trong thư mục này:

```bash
./install.sh
```

Cả 2 cách: script tự kiểm tra Go, chưa có thì cài Go (qua Homebrew, cài luôn Homebrew nếu máy chưa có), xong tự build và cài `vpn` vào `/usr/local/bin` (setuid-root — xem phần dưới), sau đó dùng không cần gõ `sudo` nữa.

## Setup lần đầu

```bash
vpn init
```

CLI sẽ hỏi server, username, PSK, password rồi tự lưu vào Keychain.

## Dùng hằng ngày

```bash
vpn connect      # kết nối, giữ chạy tới khi Ctrl-C — không cần sudo
vpn disconnect   # ngắt (gõ từ terminal khác)
vpn status       # xem đang connected hay chưa
```

Muốn chạy nền thay vì giữ terminal: thêm `&` vào cuối lệnh connect.

```bash
vpn connect &
```

`disconnect` vẫn hoạt động bình thường dù connect chạy nền hay foreground.

### Vì sao không cần sudo

`install.sh` cài `vpn` với setuid-root, đồng thời khoá cứng UID của người vừa
chạy `install.sh` ngay trong binary. Binary tự hạ quyền về user thường ngay
khi khởi động, chỉ tạm nâng lại quyền root đúng lúc thật sự cần (mở utun, bind
UDP/500, đổi route/DNS) rồi hạ ngay sau đó — không giữ quyền root suốt phiên
kết nối. **Chỉ đúng user đã cài mới gọi được `vpn`** — user khác trên máy chạy
lệnh này sẽ bị từ chối ngay lập tức, kể cả các lệnh không cần quyền root.

## Nhiều server / nhiều account

```bash
vpn profile add <tên> --server <host>       # thêm server khác
vpn profile use <tên>                       # chuyển server đang dùng
vpn profile rename <tên cũ> <tên mới>       # đổi tên profile (PSK/password trong Keychain tự chuyển theo)
vpn account add <profile> <username>        # thêm account cho 1 server
vpn account use <profile> <username>        # chuyển account đang dùng
```

Không truyền `--profile`/`--account` cho `connect` thì CLI tự dùng cái đang được `use`.

## Sự cố

```bash
vpn diagnose   # kiểm tra mạng trước khi connect, không đổi gì trên máy
vpn repair     # dọn route/DNS nếu connect bị crash/kill giữa chừng
vpn logs -f    # xem log
```

## Cập nhật / gỡ cài đặt

```bash
vpn update       # git pull + build lại + cài đè bản mới nhất (chỉ chạy được nếu cài qua install.sh)
vpn uninstall    # xoá sạch: binary, log, state, toàn bộ profile/account (kể cả PSK/password trong Keychain)
```
