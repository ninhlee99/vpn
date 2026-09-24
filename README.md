# TMS VPN

VPN client L2TP/IPsec thuần macOS. Gồm hai phần:

- **App TMS VPN trên menu bar**: cách dùng hằng ngày. Thêm hồ sơ, bật/tắt kết nối, xem IP.
- **CLI `vpn`**: engine thật sự làm việc (IKE, ESP, L2TP, PPP, route, DNS). App gọi CLI này. Bạn cũng dùng nó trực tiếp được từ terminal.

Không cần strongSwan, xl2tpd, pppd, Docker hay WireGuard.

## Trước khi bắt đầu

**Máy của bạn**

- macOS 12 trở lên, Apple Silicon hoặc Intel.
- Quyền admin (installer cần `sudo` một lần).

**Server VPN phải là loại client hỗ trợ**

- L2TP/IPsec, **IKEv1** (Main Mode) xác thực bằng **pre-shared key (PSK)**, đăng nhập PPP bằng **MS-CHAPv2**.
- Chỉ IPv4. Có hỗ trợ NAT-T (UDP 4500) khi đi qua NAT.
- Không hỗ trợ IKEv2, chứng chỉ, PAP hay CHAP-MD5.

**Xin admin VPN 4 thông tin**

| Thông tin | Ví dụ |
|---|---|
| Địa chỉ server (host hoặc IP) | `vpn.example.com` |
| Pre-shared key (PSK) | *(chuỗi bí mật dùng chung)* |
| Username | `nguyenvana` |
| Password | *(mật khẩu của riêng bạn)* |

## Cài đặt

```bash
curl -fsSL https://raw.githubusercontent.com/tms-ninhle/vpn/main/install.sh | bash
```

Script tự nhận diện kiến trúc máy. Muốn chỉ định thẳng:

```bash
curl -fsSL https://raw.githubusercontent.com/tms-ninhle/vpn/main/install-arm64.sh | bash   # Apple Silicon (M1/M2/M3...)
curl -fsSL https://raw.githubusercontent.com/tms-ninhle/vpn/main/install-intel.sh | bash   # Mac Intel
```

Installer sẽ:
1. Cài CLI vào `/usr/local/bin/vpn` (setuid-root, xem [Bảo mật](#bảo-mật)). Sau bước này không cần gõ `sudo` nữa.
2. Cài app vào `/Applications/TMS VPN.app` rồi mở app.

Cả CLI lẫn app đều được đối chiếu với `SHA256SUMS` của release. File nào không khớp thì không được cài.

**Kiểm tra cài xong:**

```bash
vpn version           # in ra phiên bản, ví dụ: vpn v1.0.0
```

Biểu tượng TMS VPN sẽ xuất hiện trên menu bar.

## Kết nối lần đầu

### Cách 1: qua app (khuyến nghị)

1. Bấm biểu tượng **TMS VPN** trên menu bar, chọn **Add**.
2. Nhập **Display name** (tuỳ đặt), **Server address**, **Account name**, **Password**, **Shared secret** (tức PSK), rồi bấm **Create**.
3. Giữ bật **Send all traffic over VPN** nếu muốn mọi traffic đi qua VPN (xem [Full tunnel và split tunnel](#full-tunnel-và-split-tunnel)).
4. Bật **công tắc** bên phải hồ sơ để kết nối (tắt công tắc để ngắt). Khi thành công, app hiện **ACTIVE** kèm IP được cấp.

Muốn sửa hoặc xoá hồ sơ, mở menu bên cạnh công tắc rồi chọn **Edit** hoặc **Delete**.

PSK và mật khẩu được lưu trong **Keychain** của macOS, không nằm trong file cấu hình.

### Cách 2: qua terminal

```bash
vpn profile add work --server vpn.example.com       # hỏi PSK (không hiện ký tự khi gõ)
vpn account add work nguyenvana --default           # hỏi password
vpn connect                                         # tự chạy nền, trả lại terminal khi biết kết quả
```

`vpn connect` chỉ chờ tới khi kết nối thành công hoặc thất bại rồi trả lại terminal. Tiến trình VPN vẫn chạy nền cho tới khi bạn `vpn disconnect`.

> Tránh truyền `--psk`/`--password` trên dòng lệnh: chúng nằm lại trong shell history, và user khác trên máy xem được qua `ps`. Cứ để CLI hỏi.

### Kiểm tra đã thật sự đi qua VPN

```bash
vpn status                     # Phase: CONNECTED, kèm tunnel (utunN) và IP được cấp
curl -4 https://ifconfig.co    # phải ra IP của VPN, không phải IP mạng bạn đang dùng
```

## Dùng hằng ngày

```bash
vpn connect                    # kết nối profile đang active
vpn connect --profile home     # kết nối profile khác
vpn disconnect
vpn status
```

Khi đang kết nối, client tự **rekey** khoá mã hoá trước khi hết hạn, khoảng mỗi 30 phút. Việc này không làm rớt kết nối và không phải đăng nhập lại.

## Profile và account

- Một **profile** là một server: địa chỉ, PSK, chế độ tunnel.
- Mỗi profile có thể có **nhiều account**; một trong số đó là **default**.
- **Profile active** là profile `vpn connect` dùng khi không có `--profile`. Đó là profile được thêm đầu tiên; khi profile active bị xoá, profile đứng đầu theo thứ tự tên sẽ thay thế.

```bash
vpn profile list                           # * = profile active / account default
vpn profile add <tên> --server <host>      # thêm, hoặc cập nhật nếu tên đã có (giữ nguyên các account)
vpn profile remove <tên>                   # xoá profile, kèm PSK và password trong Keychain
vpn account add <profile> <user> --default # thêm account, đặt làm default
vpn connect --profile <tên> --account <user>
```

### Full tunnel và split tunnel

| | Full tunnel (mặc định) | Split tunnel (`--full-tunnel=false`) |
|---|---|---|
| Traffic IPv4 | Tất cả qua VPN | Chỉ tới server VPN (đầu bên kia tunnel) và DNS server được cấp |
| IPv6 | **Bị chặn** (tunnel chỉ mang IPv4, chặn để IPv6 không lọt ra ngoài) | Đi mạng thường |
| DNS | DNS server do VPN cấp | DNS server do VPN cấp, route qua tunnel |

Nếu server không cấp DNS, `vpn connect` và `vpn status` sẽ cảnh báo: truy vấn DNS vẫn đi tới resolver của mạng hiện tại, nên mạng đó thấy được các tên miền bạn truy cập.

### Tuỳ chọn nâng cao của `profile add`

| Flag | Ý nghĩa |
|---|---|
| `--server-id <id>` | ID mà server phải tự khai trong IKE (IP hoặc FQDN). Để trống thì chấp nhận mọi ID |
| `--mtu <n>` | MTU của tunnel, mặc định 1400 |
| `--full-tunnel=false` | Split tunnel |

## Sự cố

```bash
vpn diagnose      # kiểm tra mạng, DNS, UDP 500/4500, MTU trước khi connect; không thay đổi gì trên máy
vpn logs -f       # xem log; thêm chi tiết bằng: vpn connect --verbose
vpn repair        # dọn route/DNS nếu vpn bị crash hoặc bị kill giữa chừng
```

### Mã lỗi thường gặp

Lỗi hiện ra dạng `STAGE: mô tả: chi tiết`. Đọc phần **chi tiết** để biết nguyên nhân cụ thể.

| Mã / nội dung | Nguyên nhân thường gặp | Cách xử lý |
|---|---|---|
| `IKE_TIMEOUT` + `IKE_AUTH_FAILED` / `HASH_R mismatch` | Sai PSK | Kiểm tra lại PSK với admin, rồi chạy `vpn profile add <tên> --server <host>` để nhập lại |
| `IKE_TIMEOUT` + `no response` | Server không trả lời, hoặc mạng chặn UDP 500/4500 | `vpn diagnose`; thử mạng khác (4G/hotspot) |
| `IKE_TIMEOUT` + `IKE_PROPOSAL_MISMATCH` | Server không chấp nhận bộ thuật toán nào client đề xuất | Báo admin kèm `vpn logs` |
| `PPP_AUTH_FAILURE` + `CHAP authentication rejected` | Sai username/password | `vpn account add <profile> <user>` để nhập lại |
| `already logged in` | Phiên cũ trên server chưa hết | App tự thử lại. Với CLI, đợi vài giây rồi `vpn connect` lại |
| `authenticator response mismatch` | Server **không chứng minh được** nó biết mật khẩu của bạn, có thể bị giả mạo | **Đừng nhập lại mật khẩu.** Đổi mạng và báo admin |
| `L2TP_TIMEOUT`, `LCP_FAILED`, `IPCP_FAILURE` | Server nhận IPsec nhưng lỗi ở tầng L2TP/PPP | Báo admin kèm `vpn logs` |
| `DNS_FAILURE` + `resolve VPN server` | Không phân giải được tên server | Kiểm tra mạng, hoặc dùng IP thay cho tên |
| `ROUTE_FAILURE`, `TUN_FAILURE` | Lỗi cấu hình mạng trên máy | `vpn repair` rồi thử lại |
| `TUNNEL_FAILURE` | Tunnel đang chạy thì rớt. **Traffic hiện đi mạng thường, không được bảo vệ** | `vpn connect` lại |
| `installed by a different user` | Chỉ user đã cài mới chạy được `vpn` | Dùng đúng user đó, hoặc cài lại |
| `no account selected` / `no PSK stored` | Profile thiếu account hoặc thiếu secret | Làm theo lệnh gợi ý trong thông báo lỗi |

### File nằm ở đâu

| Đường dẫn | Nội dung |
|---|---|
| `~/.config/vpn/config.json` | Profile, account, tuỳ chọn (không chứa secret) |
| Keychain: `vpn.psk.<profile>`, `vpn.pwd.<profile>.<account>` | PSK và password |
| `/var/log/vpn.log`, `/var/log/vpn.log.1` | Log (tự xoay vòng khi vượt 5 MiB) |
| `/var/run/vpn/state.json` | Trạng thái kết nối hiện tại (app đọc file này) |
| `/etc/vpn-owner-uid` | UID của user đã cài |

## Cập nhật / gỡ cài đặt

```bash
vpn update        # chỉ cập nhật CLI: verify chữ ký + SHA-256, chỉ cài nếu mới hơn bản đang chạy
```

Muốn cập nhật **cả app**, chạy lại lệnh cài ở trên.

| Lệnh | Gỡ gì |
|---|---|
| `vpn uninstall` | CLI, log, state, toàn bộ profile/account kèm secret trong Keychain. **App vẫn còn** |
| `uninstall.sh` (trong repo) | Tất cả những thứ trên **và** app |

```bash
curl -fsSL https://raw.githubusercontent.com/tms-ninhle/vpn/main/uninstall.sh | bash
```

## Bảo mật

### Vì sao không cần sudo

Khi cài, installer ghi UID của người vừa chạy vào file root-only `/etc/vpn-owner-uid`; binary đọc file này lúc chạy. Cách này cho phép cùng một binary build sẵn từ CI dùng được cho mọi máy, nhưng vẫn chỉ user đã cài mới chạy được.

Binary tự hạ quyền về user thường ngay khi khởi động, và chỉ tạm nâng lại quyền root đúng lúc cần (mở utun, bind UDP/500, đổi route/DNS). Nó không giữ quyền root suốt phiên kết nối. **Chỉ đúng user đã cài mới gọi được `vpn`**; user khác trên máy chạy lệnh này sẽ bị từ chối ngay, kể cả với các lệnh không cần quyền root.

### Những gì client bảo vệ

- **Traffic:** full tunnel đưa toàn bộ IPv4 qua VPN và chặn IPv6. ESP dùng đúng thuật toán đã negotiate với server (AES hoặc 3DES, HMAC-SHA256 hoặc SHA1).
- **Xác thực server:** kiểm tra `S=` của MS-CHAPv2, nên ai chỉ có PSK (vốn thường dùng chung) cũng không giả được server.
- **Secret:** nằm trong Keychain. Secret được truyền cho `security` qua stdin, nên không lộ qua `ps`. Log không ghi payload đã giải mã.
- **Cập nhật:** `vpn update` verify chữ ký ed25519 của release bằng public key nhúng sẵn trong binary đang cài. Installer chỉ kiểm tra `SHA256SUMS`: chống được file tải về bị hỏng, nhưng không chống được việc repo bị chiếm, vì chính script cũng tải từ repo này.

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
| `src/`, `index.html`, `package.json` | **Prototype giao diện** (React/Vite, chạy bằng dữ liệu giả), không phải app thật |

### Yêu cầu

- Go theo `go.mod` (hiện là 1.27): `brew install go`.
- App: Xcode 26 / Swift 6, đúng như CI dùng. Swift 5.9 không build được `main.swift`; `build.sh` sẽ cảnh báo nếu toolchain cũ. Không có toolchain phù hợp thì tải bản app CI build sẵn cho mỗi PR: tab **Checks** của PR → workflow `test` → artifact **TMS-VPN-app**.
- Prototype (tuỳ chọn): [bun](https://bun.sh).

### Build và cài bản local

Mọi lệnh dưới đây chạy **ở thư mục gốc của repo** (nơi có `go.mod`). Chạy ở chỗ khác sẽ báo `go.mod file not found`.

```bash
git clone https://github.com/tms-ninhle/vpn.git && cd vpn
git checkout <branch>          # tuỳ chọn: thử code của một branch / PR

# CLI — cài đúng chỗ và đúng quyền như installer (setuid-root + file owner)
go build -o vpn ./cmd/vpn
sudo install -o root -g wheel -m 4755 vpn /usr/local/bin/vpn
id -u | sudo tee /etc/vpn-owner-uid >/dev/null && sudo chmod 600 /etc/vpn-owner-uid

# App — build.sh chỉ build ra build/TMS VPN.app, bước ditto mới cài vào /Applications
bash build.sh
ditto "build/TMS VPN.app" "/Applications/TMS VPN.app"
```

### Thử một PR mà không có Xcode 26

CI build sẵn app cho mọi PR. Cài CLI từ branch của PR như trên, rồi lấy app từ artifact thay cho `bash build.sh`:

```bash
gh run download <run-id> --repo tms-ninhle/vpn -n TMS-VPN-app     # run-id: tab Checks của PR → workflow test
unzip TMS-VPN.app.zip && ditto "TMS VPN.app" "/Applications/TMS VPN.app"
```

### Test

```bash
go vet ./... && go test ./...                      # CI chạy gofmt, vet và test trên macOS
go test -tags keychain_live ./internal/keychain    # ghi và đọc thật vào login Keychain (chỉ chạy tay)
vpn connect --verbose --rekey-after 90s            # test live: ép rekey mỗi 90 giây
```

### Prototype giao diện

```bash
bun install && bun run dev     # http://localhost:3000
```

Prototype chỉ là mockup để thiết kế UI. Đoạn `main.swift` hiển thị trong đó là bản chụp minh hoạ; mã nguồn thật là `main.swift` ở gốc repo.

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

`install.sh` cần `SHA256SUMS` của release mới nhất, nên phải có ít nhất một release được tạo từ tag thì lệnh cài ở đầu README mới chạy được.
