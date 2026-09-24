# TMS VPN

VPN client L2TP/IPsec thuần macOS, không phụ thuộc strongSwan, xl2tpd, pppd, Docker hay WireGuard. Gồm hai phần:

- **App TMS VPN** (menu bar): dùng hằng ngày — thêm hồ sơ, bật/tắt kết nối, xem IP.
- **CLI `vpn`**: engine thực hiện kết nối (IKE, ESP, L2TP, PPP, route, DNS). App gọi CLI này; bạn cũng dùng trực tiếp từ terminal được.

## Trước khi bắt đầu

**Yêu cầu máy:** macOS 12 trở lên (Apple Silicon hoặc Intel), quyền admin để cài (`sudo` một lần).

**Yêu cầu server VPN:** L2TP/IPsec, IKEv1 Main Mode xác thực bằng pre-shared key (PSK), đăng nhập PPP bằng MS-CHAPv2, chỉ IPv4 (có NAT-T qua UDP 4500). Không hỗ trợ IKEv2, chứng chỉ, PAP hay CHAP-MD5.

**Xin admin 4 thông tin:** địa chỉ server, pre-shared key (PSK), username, password.

## Cài đặt

```bash
curl -fsSL https://raw.githubusercontent.com/tms-ninhle/vpn/main/install.sh | bash
```

Script tự nhận diện kiến trúc máy. Muốn chỉ định thẳng:

```bash
curl -fsSL https://raw.githubusercontent.com/tms-ninhle/vpn/main/install-arm64.sh | bash   # Apple Silicon (M1/M2/M3...)
curl -fsSL https://raw.githubusercontent.com/tms-ninhle/vpn/main/install-intel.sh | bash   # Mac Intel
```

Installer cài CLI vào `/usr/local/bin/vpn` (setuid-root, xem [Bảo mật](#bảo-mật)) và app vào `/Applications/TMS VPN.app`, sau khi đối chiếu `SHA256SUMS` của release.

```bash
vpn version    # xác nhận cài xong
```

## Kết nối lần đầu

### Qua app (khuyến nghị)

1. Bấm biểu tượng **TMS VPN** trên menu bar → **Add**.
2. Nhập Display name, Server address, Account name, Password, Shared secret (PSK) → **Create**.
3. Giữ bật **Send all traffic over VPN** nếu muốn full tunnel (xem [Full tunnel và split tunnel](#full-tunnel-và-split-tunnel)).
4. Bật công tắc bên phải hồ sơ để kết nối. Menu **Edit**/**Delete** cạnh công tắc để sửa hoặc xoá.

PSK và mật khẩu lưu trong Keychain của macOS, không nằm trong file cấu hình.

### Qua terminal

```bash
vpn profile add work --server vpn.example.com     # hỏi PSK
vpn account add work nguyenvana --default         # hỏi password
vpn connect                                       # chạy nền, trả lại terminal khi biết kết quả
```

> Không truyền `--psk`/`--password` trên dòng lệnh — chúng lưu lại trong shell history và lộ qua `ps` cho user khác trên máy. Để CLI hỏi trực tiếp.

### Xác nhận đã qua VPN

```bash
vpn status                     # Phase: CONNECTED, kèm tunnel và IP được cấp
curl -4 https://ifconfig.co    # phải trả về IP của VPN
```

## Dùng hằng ngày

```bash
vpn connect                    # kết nối profile đang active
vpn connect --profile home     # kết nối profile khác
vpn disconnect
vpn status
```

Khoá mã hoá được tự làm mới định kỳ trong lúc kết nối, không làm rớt phiên và không cần đăng nhập lại. Khi mất kết nối, client tự nối lại; `vpn status` hiện `Reconnecting` trong lúc đó.

## Profile và account

- **Profile** = một server (địa chỉ, PSK, chế độ tunnel). Mỗi profile có thể có nhiều **account**, một trong số đó là default.
- **Profile active** là profile `vpn connect` dùng khi không truyền `--profile`.

```bash
vpn profile list                           # * = profile / account đang active
vpn profile add <tên> --server <host>      # thêm, hoặc cập nhật nếu tên đã có
vpn profile remove <tên>
vpn account add <profile> <user> --default
vpn connect --profile <tên> --account <user>
```

### Full tunnel và split tunnel

| | Full tunnel (mặc định) | Split tunnel (`--full-tunnel=false`) |
|---|---|---|
| Traffic IPv4 | Tất cả qua VPN | Chỉ tới server VPN và DNS server được cấp |
| IPv6 | Bị chặn | Đi mạng thường |
| DNS | DNS do VPN cấp | DNS do VPN cấp, route qua tunnel |

Nếu server không cấp DNS, `vpn connect`/`vpn status` sẽ cảnh báo: DNS vẫn đi qua resolver của mạng hiện tại.

### Tuỳ chọn nâng cao của `profile add`

| Flag | Ý nghĩa |
|---|---|
| `--server-id <id>` | ID server phải tự khai trong IKE. Để trống thì chấp nhận mọi ID |
| `--mtu <n>` | MTU riêng của profile (mặc định 1400); `vpn mtu` chung được ưu tiên hơn |
| `--full-tunnel=false` | Split tunnel |

### Cài đặt chung

```bash
vpn mtu [1280|1400]      # MTU cho mọi profile — 1280 nếu mạng hay đứng khi tải lớn (hotspot, PPPoE)
vpn verbose [on|off]     # log chi tiết giao thức để chẩn đoán, mặc định off
vpn killswitch [on|off]  # chặn internet khi VPN full-tunnel rớt và đang tự nối lại, mặc định off
```

Đổi có hiệu lực ở lần `connect` tiếp theo. Menu bar app có các mục tương ứng trong **Settings** (biểu tượng bánh răng).

## Sự cố

```bash
vpn diagnose      # kiểm tra mạng, DNS, UDP 500/4500, MTU — không thay đổi gì trên máy
vpn logs -f       # xem log (/var/log/vpn.log)
vpn repair        # dọn route/DNS nếu vpn bị crash hoặc bị kill giữa chừng
```

Lỗi hiện ra dạng `STAGE: mô tả: chi tiết` — đọc phần chi tiết để biết nguyên nhân cụ thể.

| Mã / nội dung | Nguyên nhân thường gặp | Cách xử lý |
|---|---|---|
| `IKE_TIMEOUT` + `IKE_AUTH_FAILED` / `HASH_R mismatch` | Sai PSK | `vpn profile add <tên> --server <host>` để nhập lại |
| `IKE_TIMEOUT` + `no response` | Server không trả lời, mạng chặn UDP 500/4500 | `vpn diagnose`; thử mạng khác |
| `IKE_TIMEOUT` + `IKE_PROPOSAL_MISMATCH` | Server không chấp nhận thuật toán nào client đề xuất | Báo admin kèm `vpn logs` |
| `PPP_AUTH_FAILURE` + `CHAP authentication rejected` | Sai username/password | `vpn account add <profile> <user>` để nhập lại |
| `already logged in` | Phiên cũ trên server chưa hết | Đợi vài giây rồi `vpn connect` lại |
| `authenticator response mismatch` | Server không chứng minh được biết mật khẩu — có thể bị giả mạo | Đừng nhập lại mật khẩu; đổi mạng và báo admin |
| `L2TP_TIMEOUT`, `LCP_FAILED`, `IPCP_FAILURE` | Lỗi ở tầng L2TP/PPP | Báo admin kèm `vpn logs` |
| `DNS_FAILURE` + `resolve VPN server` | Không phân giải được tên server | Kiểm tra mạng, hoặc dùng IP thay tên |
| `ROUTE_FAILURE`, `TUN_FAILURE` | Lỗi cấu hình mạng trên máy | `vpn repair` rồi thử lại |
| `installed by a different user` | Chỉ user đã cài mới chạy được `vpn` | Dùng đúng user đó, hoặc cài lại |
| `no account selected` / `no PSK stored` | Profile thiếu account hoặc secret | Làm theo lệnh gợi ý trong thông báo lỗi |

### File nằm ở đâu

| Đường dẫn | Nội dung |
|---|---|
| `~/.config/vpn/config.json` | Profile, account, tuỳ chọn (không chứa secret) |
| Keychain: `vpn.psk.<profile>`, `vpn.pwd.<profile>.<account>` | PSK và password |
| `/var/log/vpn.log`, `/var/log/vpn.log.1` | Log (tự xoay vòng) |
| `/var/run/vpn/state.json` | Trạng thái kết nối hiện tại |
| `/etc/vpn-owner-uid` | UID của user đã cài |

## Cập nhật / gỡ cài đặt

```bash
vpn update    # cập nhật CLI: verify chữ ký + SHA-256, chỉ cài nếu mới hơn bản đang chạy
```

Muốn cập nhật cả app, chạy lại lệnh cài ở trên.

| Lệnh | Gỡ gì |
|---|---|
| `vpn uninstall` | CLI, log, state, mọi profile/account kèm secret trong Keychain (giữ lại app) |
| `uninstall.sh` (bên dưới) | Tất cả những thứ trên **và** app |

```bash
curl -fsSL https://raw.githubusercontent.com/tms-ninhle/vpn/main/uninstall.sh | bash
```

## Bảo mật

CLI cài setuid-root, nhưng tự hạ quyền về user thường ngay khi khởi động và chỉ tạm nâng lại đúng lúc cần (mở utun, bind UDP/500, đổi route/DNS) — không giữ quyền root suốt phiên kết nối. Chỉ user đã chạy installer mới gọi được `vpn`; user khác trên máy bị từ chối ngay.

- **Traffic:** full tunnel đưa toàn bộ IPv4 qua VPN và chặn IPv6; ESP dùng đúng thuật toán đã negotiate với server (AES hoặc 3DES, HMAC-SHA256 hoặc SHA1).
- **Xác thực server:** kiểm tra `S=` của MS-CHAPv2, nên chỉ có PSK (thường dùng chung) không giả được server.
- **Secret:** lưu trong Keychain, truyền cho `security` qua stdin nên không lộ qua `ps`. Log không ghi payload đã giải mã.
- **Cập nhật:** `vpn update` verify chữ ký ed25519 bằng public key nhúng sẵn trong binary. Installer chỉ kiểm `SHA256SUMS`, chống được file tải hỏng nhưng không chống được repo bị chiếm.

## Dành cho developer

### Cấu trúc repo

| Đường dẫn | Nội dung |
|---|---|
| `cmd/vpn` | Entry point của CLI |
| `internal/ike`, `ipsec`, `l2tp`, `ppp` | Các tầng giao thức: IKEv1, ESP, L2TP, PPP/MS-CHAPv2 |
| `internal/engine` | Điều phối kết nối, data plane, rekey |
| `internal/routing`, `dnsmgr`, `tun` | Route, DNS, thiết bị utun của macOS |
| `internal/cli`, `config`, `keychain`, `state` | Lệnh CLI, cấu hình, Keychain, file trạng thái |
| `internal/privilege`, `sysbin`, `release` | setuid, đường dẫn lệnh hệ thống, ký release |
| `cmd/releasesign` | Công cụ ký release (dùng trong CI) |
| `main.swift`, `build.sh` | App menu bar (Swift) và script build |
| `src/`, `index.html`, `package.json` | Prototype giao diện (React/Vite, dữ liệu giả) — không phải app thật |

### Yêu cầu

- Go theo `go.mod` (hiện là 1.27): `brew install go`. Nếu `go version` vẫn ra bản cũ, gỡ bản `.pkg` cũ ở `/usr/local/go`.
- App: Xcode 26 / Swift 6 (Swift 5.9 không build được `main.swift`). Không có toolchain phù hợp thì tải app CI build sẵn cho mỗi PR: tab **Checks** → workflow `test` → artifact **TMS-VPN-app**.
- Prototype (tuỳ chọn): [bun](https://bun.sh).

### Build và cài bản local

Chạy ở thư mục gốc repo (nơi có `go.mod`):

```bash
git clone https://github.com/tms-ninhle/vpn.git && cd vpn

# CLI — cài đúng chỗ và đúng quyền như installer
go build -o vpn ./cmd/vpn
sudo install -o root -g wheel -m 4755 vpn /usr/local/bin/vpn
id -u | sudo tee /etc/vpn-owner-uid >/dev/null && sudo chmod 600 /etc/vpn-owner-uid

# App
bash build.sh
ditto "build/TMS VPN.app" "/Applications/TMS VPN.app"
```

Thử một PR mà không có Xcode 26: tải app từ artifact CI thay cho `bash build.sh`:

```bash
gh run download <run-id> --repo tms-ninhle/vpn -n TMS-VPN-app
unzip TMS-VPN.app.zip && ditto "TMS VPN.app" "/Applications/TMS VPN.app"
```

### Test

```bash
go vet ./... && go test ./...                      # CI chạy gofmt, vet và test trên macOS
go test -tags keychain_live ./internal/keychain    # ghi/đọc thật vào login Keychain (chạy tay)
vpn connect --verbose --rekey-after 90s            # test live: ép rekey mỗi 90 giây
```

### Prototype giao diện

```bash
bun install && bun run dev     # http://localhost:3000
```

Chỉ là mockup thiết kế UI; mã nguồn app thật là `main.swift` ở gốc repo.

## Phát hành

Release được tạo khi push tag semver, sau khi CI pass:

```bash
git tag v1.2.3 && git push origin v1.2.3
```

Mỗi release gồm `vpn-darwin-arm64`, `vpn-darwin-amd64`, `TMS-VPN.app.zip`, `SHA256SUMS` và `SHA256SUMS.sig` (chữ ký ed25519). `vpn update` từ chối release nếu chữ ký hoặc SHA-256 không khớp, hoặc version không mới hơn bản đang chạy (`--force` để bỏ qua).

Khoá ký nằm trong secret `RELEASE_SIGNING_KEY` của repo. Tạo cặp khoá mới:

```bash
go run ./cmd/releasesign keygen   # stdout: seed → secret; stderr: public key → release.PublicKey
```
