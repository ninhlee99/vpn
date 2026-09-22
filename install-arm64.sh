#!/usr/bin/env bash
# ==============================================================================
# Installs `vpn` CLI + `TMS-VPN.app` Menu Bar UI for Apple Silicon (arm64)
# ==============================================================================

set -euo pipefail

ARCH=arm64
SWIFT_TARGET="arm64-apple-macos12.0"

echo "=================================================="
echo "🛡️  INSTALLING TMS-VPN FOR APPLE SILICON (ARM64)"
echo "=================================================="

# --- Step 1: Install `vpn` CLI Backend ---
URL="https://github.com/ninhlee99/vpn/releases/latest/download/vpn-darwin-$ARCH"
BIN="$(mktemp -t vpn-download)"
trap 'rm -f "$BIN"' EXIT

echo "📦 [1/2] Downloading VPN CLI engine ($ARCH)..."
if curl -fsSL -o "$BIN" "$URL"; then
    chmod +x "$BIN"
    "$BIN" version >/dev/null 2>&1 || true
    OWNER_UID="$(id -u)"
    sudo mv "$BIN" /usr/local/bin/vpn
    sudo chown root:wheel /usr/local/bin/vpn
    sudo chmod 4755 /usr/local/bin/vpn
    echo "$OWNER_UID" | sudo tee /etc/vpn-owner-uid >/dev/null
    sudo chown root:wheel /etc/vpn-owner-uid
    sudo chmod 600 /etc/vpn-owner-uid
    echo "  -> CLI Installed: /usr/local/bin/vpn"
fi

# --- Step 2: Build & Install Swift Menu Bar UI ---
echo "🎨 [2/2] Installing TMS-VPN Menu Bar UI..."
APP_DIR="/Applications/TMS-VPN.app"
SWIFT_SRC="$(mktemp -t vpn-swift-src).swift"
UI_BIN="$(mktemp -t vpn-ui-bin)"

cat <<'SWIFT_EOF' > "$SWIFT_SRC"
import Cocoa
import SwiftUI

struct VPNProfile: Identifiable, Codable {
    var id: String = UUID().uuidString
    var name: String
    var serverAddress: String
    var username: String?
    var sharedSecret: String?
    var sendAllTraffic: Bool = true
    var isEnabled: Bool = false
    var isReconnecting: Bool = false
}

@MainActor
final class VPNManager: ObservableObject {
    static let shared = VPNManager()

    @Published var profiles: [VPNProfile] = [
        VPNProfile(name: "Trụ sở chính (Prod)", serverAddress: "vpn-prod.company.internal", sendAllTraffic: true, isEnabled: true, isReconnecting: false),
        VPNProfile(name: "Môi trường Dev & Staging", serverAddress: "vpn-staging.company.internal", sendAllTraffic: true, isEnabled: false, isReconnecting: false),
        VPNProfile(name: "Chi nhánh TP.HCM", serverAddress: "vpn-hcm.company.internal", sendAllTraffic: false, isEnabled: false, isReconnecting: false)
    ]
    @Published var autoConnectOnLaunch: Bool = true
    @Published var isConnected: Bool = true

    func toggleProfile(_ profile: VPNProfile) {
        if let idx = profiles.firstIndex(where: { $0.id == profile.id }) {
            let wasActive = profiles[idx].isEnabled
            for i in profiles.indices { 
                profiles[i].isEnabled = false 
                profiles[i].isReconnecting = false
            }
            
            if !wasActive {
                profiles[idx].isEnabled = true
                isConnected = true
                connectBackend(server: profiles[idx].serverAddress)
            } else {
                isConnected = false
                disconnectBackend()
            }
        }
    }

    func addProfile(name: String, server: String, user: String?, secret: String?, sendAllTraffic: Bool) {
        let p = VPNProfile(name: name, serverAddress: server, username: user, sharedSecret: secret, sendAllTraffic: sendAllTraffic, isEnabled: false, isReconnecting: false)
        profiles.append(p)
    }

    func deleteProfile(id: String) {
        profiles.removeAll(where: { $0.id == id })
    }

    func toggleReconnecting(id: String) {
        if let idx = profiles.firstIndex(where: { $0.id == id }) {
            profiles[idx].isReconnecting.toggle()
        }
    }

    private func connectBackend(server: String) {
        let task = Process()
        task.executableURL = URL(fileURLWithPath: "/usr/local/bin/vpn")
        task.arguments = ["connect", server]
        try? task.run()
    }

    private func disconnectBackend() {
        let task = Process()
        task.executableURL = URL(fileURLWithPath: "/usr/local/bin/vpn")
        task.arguments = ["disconnect"]
        try? task.run()
    }
}

struct RotatingBorderModifier: ViewModifier {
    @State private var rotation: Double = 0
    var color: Color = .green

    func body(content: Content) -> some View {
        content
            .overlay(
                RoundedRectangle(cornerRadius: 14)
                    .stroke(
                        AngularGradient(
                            gradient: Gradient(colors: [
                                Color.clear,
                                Color.clear,
                                color.opacity(0.3),
                                color,
                                Color.white.opacity(0.8)
                            ]),
                            center: .center,
                            angle: .degrees(rotation)
                        ),
                        lineWidth: 2
                    )
            )
            .onAppear {
                withAnimation(Animation.linear(duration: 2.5).repeatForever(autoreverses: false)) {
                    rotation = 360
                }
            }
    }
}

struct MenuBarPopupView: View {
    @ObservedObject var vpn = VPNManager.shared
    @State private var showingAddModal = false

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                ZStack {
                    RoundedRectangle(cornerRadius: 12)
                        .fill(Color(red: 0.05, green: 0.18, blue: 0.23))
                        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color(red: 0.12, green: 0.55, blue: 0.65).opacity(0.6), lineWidth: 1))
                    Image(systemName: "checkmark.shield.fill")
                        .foregroundColor(Color(red: 0.2, green: 0.85, blue: 0.95))
                    Circle()
                        .fill(Color(red: 0.2, green: 0.95, blue: 0.6))
                        .frame(width: 6, height: 6)
                        .offset(x: 9, y: -9)
                }
                .frame(width: 42, height: 42)

                HStack(spacing: 6) {
                    Text("TMS-VPN")
                        .font(.system(size: 16, weight: .bold))
                        .foregroundColor(.white)
                    Circle()
                        .fill(vpn.isConnected ? Color(red: 0.2, green: 0.85, blue: 0.55) : Color.gray)
                        .frame(width: 7, height: 7)
                    Text(vpn.isConnected ? "Đang kết nối" : "Đã ngắt kết nối")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundColor(vpn.isConnected ? Color(red: 0.2, green: 0.85, blue: 0.55) : Color.gray)
                }

                Spacer()

                Button(action: {}) {
                    Image(systemName: "gearshape")
                        .font(.system(size: 16))
                        .foregroundColor(Color(red: 0.55, green: 0.6, blue: 0.66))
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 16)
            .padding(.top, 16)
            .padding(.bottom, 12)

            Divider().background(Color.white.opacity(0.08))

            HStack {
                Text("HỒ SƠ VPN CÔNG TY")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(Color(red: 0.55, green: 0.6, blue: 0.66))
                Spacer()
                Button(action: { showingAddModal = true }) {
                    HStack(spacing: 3) {
                        Image(systemName: "plus")
                        Text("Thêm điểm nối")
                    }
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(Color(red: 0.2, green: 0.85, blue: 0.95))
                    .padding(.horizontal, 10)
                    .padding(.vertical, 4)
                    .background(RoundedRectangle(cornerRadius: 8).fill(Color(red: 0.05, green: 0.16, blue: 0.22)).overlay(RoundedRectangle(cornerRadius: 8).stroke(Color(red: 0.12, green: 0.55, blue: 0.65).opacity(0.6), lineWidth: 1)))
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 16)
            .padding(.top, 14)
            .padding(.bottom, 8)

            VStack(spacing: 8) {
                ForEach(vpn.profiles) { profile in
                    HStack(spacing: 12) {
                        Circle()
                            .fill(profile.isReconnecting ? Color(red: 0.95, green: 0.25, blue: 0.3) : (profile.isEnabled ? Color(red: 0.2, green: 0.88, blue: 0.55) : Color.gray))
                            .frame(width: 9, height: 9)
                            .shadow(color: profile.isReconnecting ? Color.red : (profile.isEnabled ? Color.green.opacity(0.5) : Color.clear), radius: 4)

                        VStack(alignment: .leading, spacing: 2) {
                            Text(profile.name)
                                .font(.system(size: 14, weight: .medium))
                                .foregroundColor(profile.isReconnecting ? Color(red: 1.0, green: 0.85, blue: 0.85) : (profile.isEnabled ? .white : Color(red: 0.8, green: 0.84, blue: 0.88)))

                            if profile.isReconnecting {
                                Text("Đang kết nối lại...")
                                    .font(.system(size: 11, weight: .medium))
                                    .foregroundColor(Color(red: 0.95, green: 0.45, blue: 0.45))
                            }
                        }

                        Spacer()

                        Toggle("", isOn: Binding(get: { profile.isEnabled }, set: { _ in vpn.toggleProfile(profile) }))
                            .toggleStyle(SwitchToggleStyle(tint: profile.isReconnecting ? Color(red: 0.95, green: 0.25, blue: 0.35) : Color(red: 0.15, green: 0.8, blue: 0.55)))
                            .labelsHidden()

                        Menu {
                            Button("Chỉnh sửa hồ sơ") { }
                            Button("Xóa hồ sơ", role: .destructive) {
                                vpn.deleteProfile(id: profile.id)
                            }
                        } label: {
                            Image(systemName: "ellipsis")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundColor(Color.gray.opacity(0.8))
                        }
                        .menuStyle(.borderlessButton)
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 12)
                    .background(
                        RoundedRectangle(cornerRadius: 14)
                            .fill(profile.isReconnecting ? Color(red: 0.22, green: 0.08, blue: 0.1) : (profile.isEnabled ? Color(red: 0.04, green: 0.16, blue: 0.12) : Color(red: 0.1, green: 0.12, blue: 0.16).opacity(0.7)))
                            .overlay(RoundedRectangle(cornerRadius: 14).stroke(profile.isEnabled ? Color(red: 0.15, green: 0.7, blue: 0.45).opacity(0.6) : Color.white.opacity(0.08), lineWidth: 1))
                    )
                }
            }
            .padding(.horizontal, 16)

            Divider().background(Color.white.opacity(0.08)).padding(.top, 12)

            HStack {
                Button(action: { NSApp.sendAction(Selector(("showPreferencesWindow:")), to: nil, from: nil) }) {
                    HStack(spacing: 5) {
                        Image(systemName: "gearshape")
                        Text("Cài đặt...").font(.system(size: 12))
                        Text("⌘,").font(.system(size: 10)).foregroundColor(Color.gray.opacity(0.6))
                    }
                    .foregroundColor(Color.gray)
                }
                .buttonStyle(.plain)

                Spacer()

                Button(action: { NSApplication.shared.terminate(nil) }) {
                    HStack(spacing: 5) {
                        Image(systemName: "rectangle.portrait.and.arrow.right")
                        Text("Thoát").font(.system(size: 12, weight: .medium))
                        Text("⌘Q").font(.system(size: 10, weight: .semibold)).foregroundColor(Color.gray.opacity(0.6))
                    }
                    .foregroundColor(Color.gray)
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
        }
        .frame(width: 360)
        .background(Color(red: 0.07, green: 0.09, blue: 0.12))
        .sheet(isPresented: $showingAddModal) {
            AddL2TPProfileSheet(isPresented: $showingAddModal)
        }
    }
}

struct AddL2TPProfileSheet: View {
    @Binding var isPresented: Bool
    @State private var name = ""
    @State private var server = ""
    @State private var user = ""
    @State private var secret = ""
    @State private var sendAllTraffic = true

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Thêm Hồ Sơ VPN L2TP")
                .font(.headline)
                .foregroundColor(.white)

            TextField("Tên điểm nối (VD: Chi nhánh HN)", text: $name)
                .textFieldStyle(.roundedBorder)

            TextField("Địa chỉ máy chủ (IP/Host)", text: $server)
                .textFieldStyle(.roundedBorder)

            TextField("Tài khoản (Username)", text: $user)
                .textFieldStyle(.roundedBorder)

            SecureField("Khóa bí mật chia sẻ (Secret)", text: $secret)
                .textFieldStyle(.roundedBorder)

            Toggle("Gửi toàn bộ traffic (Send all traffic)", isOn: $sendAllTraffic)
                .font(.system(size: 13))

            HStack {
                Spacer()
                Button("Hủy") { isPresented = false }
                Button("Lưu điểm nối") {
                    guard !name.isEmpty, !server.isEmpty else { return }
                    VPNManager.shared.addProfile(name: name, server: server, user: user, secret: secret, sendAllTraffic: sendAllTraffic)
                    isPresented = false
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 340)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    var statusItem: NSStatusItem?
    var popover = NSPopover()

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        guard let button = statusItem?.button else { return }
        button.image = NSImage(systemSymbolName: "shield.fill", accessibilityDescription: "TMS-VPN")
        button.action = #selector(togglePopover(_:))
        button.target = self

        popover.contentSize = NSSize(width: 360, height: 460)
        popover.behavior = .transient
        popover.contentViewController = NSHostingController(rootView: MenuBarPopupView())
    }

    @objc func togglePopover(_ sender: AnyObject?) {
        guard let button = statusItem?.button else { return }
        if popover.isShown {
            popover.performClose(sender)
        } else {
            popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
            NSApp.activate(ignoringOtherApps: true)
        }
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
_ = NSApplicationMain(CommandLine.argc, CommandLine.unsafeArgv)
SWIFT_EOF

if command -v swiftc &>/dev/null; then
    echo "  -> Compiling native Swift UI (arm64)..."
    swiftc -O -target "$SWIFT_TARGET" -framework Cocoa -framework SwiftUI "$SWIFT_SRC" -o "$UI_BIN"

    pkill -f "TMS-VPN" 2>/dev/null || true
    pkill -f "tms-vpn-bar" 2>/dev/null || true

    rm -rf "$APP_DIR"
    mkdir -p "$APP_DIR/Contents/MacOS"
    mkdir -p "$APP_DIR/Contents/Resources"
    cp "$UI_BIN" "$APP_DIR/Contents/MacOS/TMS-VPN"
    chmod +x "$APP_DIR/Contents/MacOS/TMS-VPN"

    cat <<EOF > "$APP_DIR/Contents/Info.plist"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>TMS-VPN</string>
    <key>CFBundleIdentifier</key>
    <string>com.tms.vpn.menubar</string>
    <key>CFBundleName</key>
    <string>TMS-VPN</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>1.0.0</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
EOF

    sudo mkdir -p /usr/local/bin 2>/dev/null || true
    sudo cp "$UI_BIN" /usr/local/bin/tms-vpn-bar 2>/dev/null || cp "$UI_BIN" /usr/local/bin/tms-vpn-bar 2>/dev/null || true
    sudo chmod +x /usr/local/bin/tms-vpn-bar 2>/dev/null || true
    echo "  -> UI Installed to /Applications/TMS-VPN.app"
fi

rm -f "$SWIFT_SRC" "$UI_BIN"

echo "=================================================="
echo "🎉 SUCCESS: TMS-VPN Engine & Menu Bar UI Installed!"
echo "=================================================="

open -a "/Applications/TMS-VPN.app" 2>/dev/null || true
