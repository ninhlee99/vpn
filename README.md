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

```bash
vpn init
```

CLI sẽ hỏi server, username, PSK, password rồi tự lưu vào Keychain.

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
vpn update       # tải binary release đúng kiến trúc rồi cài đè
vpn uninstall    # xoá sạch: binary, log, state, toàn bộ profile/account (kể cả PSK/password trong Keychain)
```
