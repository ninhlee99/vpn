# vpn-l2tp

VPN client L2TP/IPsec thuần macOS.

## Cài đặt

```bash
./install.sh
```

Script tự kiểm tra Go, chưa có thì cài Go (qua Homebrew, cài luôn Homebrew nếu máy chưa có), xong tự build và cài `vpn` vào `/usr/local/bin` (setuid-root — xem phần dưới), sau đó dùng không cần gõ `sudo` nữa.

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

`install.sh` cài `vpn` với setuid-root. Binary tự hạ quyền về user thường ngay
khi khởi động, chỉ tạm nâng lại quyền root đúng lúc thật sự cần (mở utun, bind
UDP/500, đổi route/DNS) rồi hạ ngay sau đó — không giữ quyền root suốt phiên
kết nối. Đây là đánh đổi có chủ đích: **mọi user trên máy đều gọi được `vpn`
với quyền root** (không riêng người cài). Phù hợp cho máy cá nhân 1 người
dùng; máy nhiều tài khoản thì cân nhắc kỹ hoặc bỏ setuid (`sudo chmod u-s
/usr/local/bin/vpn`) và quay lại dùng `sudo vpn connect`.

## Nhiều server / nhiều account

```bash
vpn profile add <tên> --server <host>     # thêm server khác
vpn profile use <tên>                     # chuyển server đang dùng
vpn account add <profile> <username>      # thêm account cho 1 server
vpn account use <profile> <username>      # chuyển account đang dùng
```

Không truyền `--profile`/`--account` cho `connect` thì CLI tự dùng cái đang được `use`.

## Sự cố

```bash
vpn diagnose   # kiểm tra mạng trước khi connect, không đổi gì trên máy
vpn repair     # dọn route/DNS nếu connect bị crash/kill giữa chừng
vpn logs -f    # xem log
```
