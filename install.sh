#!/usr/bin/env bash
# ==============================================================================
# One-liner install: detects this Mac architecture, installs CLI + Menu Bar UI
# ==============================================================================

set -euo pipefail

case "$(uname -m)" in
  arm64)
    ARCH=arm64
    SWIFT_TARGET="arm64-apple-macos12.0"
    ;;
  x86_64)
    ARCH=amd64
    SWIFT_TARGET="x86_64-apple-macos12.0"
    ;;
  *)
    echo "Unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

echo "=================================================="
echo "🛡️  INSTALLING TMS-VPN (CLI & MENU BAR UI)"
echo "=================================================="

# --- Step 1: Install `vpn` CLI Backend ---
URL="https://github.com/ninhlee99/vpn/releases/latest/download/vpn-darwin-$ARCH"
BIN="$(mktemp -t vpn-download)"
trap 'rm -f "$BIN"' EXIT

echo "📦 [1/2] Downloading VPN CLI engine from $URL..."
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

// MARK: - Models for CLI Config and State

struct CLIAccount: Codable {
    var username: String?
}

struct CLIProfile: Codable {
    var server: String?
    var server_id: String?
    var full_tunnel: Bool?
    var default_account: String?
    var accounts: [String: CLIAccount]?
}

struct CLIConfig: Codable {
    var active_profile: String?
    var profiles: [String: CLIProfile]?
}

struct CLIState: Codable {
    var phase: String? // "CONNECTED", "CONNECTING", "DISCONNECTED", "FAILED"
    var profile: String?
    var account: String?
    var server: String?
    var pid: Int?
    var tun_device: String?
    var local_ip: String?
    var fail_stage: String?
    var fail_detail: String?
}

struct VPNProfileItem: Identifiable, Hashable {
    var id: String { name }
    var name: String
    var server: String
    var username: String
    var isFullTunnel: Bool
    var isConnected: Bool
    var isConnecting: Bool
}

// MARK: - Status Bar Icon Generator

func makeMenuBarIcon(phase: String) -> NSImage {
    let size = NSSize(width: 18, height: 18)
    let img = NSImage(size: size, flipped: false) { rect in
        if phase == "CONNECTED" {
            // Vibrant Green / Emerald Shield with active badge
            let config = NSImage.SymbolConfiguration(pointSize: 14, weight: .bold)
            if let shield = NSImage(systemSymbolName: "checkmark.shield.fill", accessibilityDescription: nil)?.withSymbolConfiguration(config) {
                NSColor(red: 0.15, green: 0.9, blue: 0.55, alpha: 1.0).set()
                shield.draw(in: NSRect(x: 1, y: 1, width: 15, height: 15))
            }
            let badgeRect = NSRect(x: 12, y: 11, width: 5, height: 5)
            let badgePath = NSBezierPath(ovalIn: badgeRect)
            NSColor(red: 0.2, green: 0.98, blue: 0.65, alpha: 1.0).setFill()
            badgePath.fill()
            NSColor(red: 0.05, green: 0.3, blue: 0.15, alpha: 0.8).setStroke()
            badgePath.lineWidth = 0.5
            badgePath.stroke()
        } else if phase == "CONNECTING" {
            // Orange / Yellow Connecting Shield
            let config = NSImage.SymbolConfiguration(pointSize: 14, weight: .semibold)
            if let shield = NSImage(systemSymbolName: "shield.lefthalf.filled", accessibilityDescription: nil)?.withSymbolConfiguration(config) {
                NSColor(red: 0.98, green: 0.7, blue: 0.15, alpha: 1.0).set()
                shield.draw(in: NSRect(x: 1, y: 1, width: 15, height: 15))
            }
            let badgeRect = NSRect(x: 12, y: 11, width: 5, height: 5)
            let badgePath = NSBezierPath(ovalIn: badgeRect)
            NSColor.systemOrange.setFill()
            badgePath.fill()
        } else {
            // Clean Disconnected Shield
            let config = NSImage.SymbolConfiguration(pointSize: 14, weight: .medium)
            if let shield = NSImage(systemSymbolName: "shield", accessibilityDescription: nil)?.withSymbolConfiguration(config) {
                NSColor.labelColor.withAlphaComponent(0.85).set()
                shield.draw(in: NSRect(x: 1, y: 1, width: 15, height: 15))
            }
        }
        return true
    }
    
    // When connected or connecting, keep isTemplate = false so macOS preserves vivid RGB green/orange!
    img.isTemplate = (phase == "DISCONNECTED")
    return img
}

// MARK: - VPN Manager (Real CLI & File Sync)

@MainActor
final class VPNManager: ObservableObject {
    static let shared = VPNManager()

    @Published var profiles: [VPNProfileItem] = []
    @Published var activeProfileName: String?
    @Published var isConnected: Bool = false
    @Published var isConnecting: Bool = false
    @Published var currentPhase: String = "DISCONNECTED"
    @Published var currentIP: String = ""
    @Published var currentTunDevice: String = ""
    @Published var errorMessage: String?

    // Settings
    @Published var autoConnectOnLaunch: Bool = false
    @Published var startAtLogin: Bool = true

    var onStatusChanged: ((String) -> Void)?
    private var pollTimer: Timer?

    private var configURL: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config")
            .appendingPathComponent("vpn")
            .appendingPathComponent("config.json")
    }

    private var stateURL: URL {
        URL(fileURLWithPath: "/var/run/vpn/state.json")
    }

    init() {
        syncFromDisk()
        startPolling()
    }

    func startPolling() {
        pollTimer?.invalidate()
        pollTimer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            Task { @MainActor in
                self?.syncFromDisk()
            }
        }
    }

    func syncFromDisk() {
        // 1. Read State (/var/run/vpn/state.json)
        var activeProf: String?
        var phase = "DISCONNECTED"
        var localIP = ""
        var tunDev = ""

        if let stateData = try? Data(contentsOf: stateURL),
           let st = try? JSONDecoder().decode(CLIState.self, from: stateData) {
            phase = st.phase ?? "DISCONNECTED"
            activeProf = st.profile
            localIP = st.local_ip ?? ""
            tunDev = st.tun_device ?? ""
            if phase == "FAILED" {
                self.errorMessage = "\(st.fail_stage ?? "Lỗi"): \(st.fail_detail ?? "")"
            } else {
                self.errorMessage = nil
            }
        }

        let oldPhase = self.currentPhase
        self.currentPhase = phase
        self.isConnected = (phase == "CONNECTED")
        self.isConnecting = (phase == "CONNECTING")
        self.currentIP = localIP
        self.currentTunDevice = tunDev

        if oldPhase != phase || self.onStatusChanged != nil {
            self.onStatusChanged?(phase)
        }

        // 2. Read Config (~/.config/vpn/config.json)
        if let configData = try? Data(contentsOf: configURL),
           let cfg = try? JSONDecoder().decode(CLIConfig.self, from: configData) {
            
            if activeProf == nil {
                activeProf = cfg.active_profile
            }
            self.activeProfileName = activeProf

            var items: [VPNProfileItem] = []
            if let profDict = cfg.profiles {
                for (pName, pVal) in profDict {
                    let isConn = (self.isConnected && (activeProf == pName))
                    let isConnIng = (self.isConnecting && (activeProf == pName))
                    let user = pVal.default_account ?? pVal.accounts?.keys.first ?? ""
                    items.append(VPNProfileItem(
                        name: pName,
                        server: pVal.server ?? "",
                        username: user,
                        isFullTunnel: pVal.full_tunnel ?? true,
                        isConnected: isConn,
                        isConnecting: isConnIng
                    ))
                }
            }
            self.profiles = items.sorted { $0.name.lowercased() < $1.name.lowercased() }
        } else {
            if self.profiles.isEmpty {
                self.profiles = []
            }
        }
    }

    func toggleConnect(profile: VPNProfileItem) {
        if profile.isConnected || profile.isConnecting {
            disconnect()
        } else {
            connect(profileName: profile.name)
        }
    }

    func connect(profileName: String) {
        self.isConnecting = true
        self.currentPhase = "CONNECTING"
        self.activeProfileName = profileName
        self.errorMessage = nil
        self.onStatusChanged?("CONNECTING")

        DispatchQueue.global(qos: .userInitiated).async {
            let task = Process()
            task.executableURL = URL(fileURLWithPath: "/usr/local/bin/vpn")
            task.arguments = ["connect", "--profile", profileName]
            try? task.run()
        }
    }

    func disconnect() {
        self.isConnecting = false
        self.isConnected = false
        self.currentPhase = "DISCONNECTED"
        self.onStatusChanged?("DISCONNECTED")

        DispatchQueue.global(qos: .userInitiated).async {
            let task = Process()
            task.executableURL = URL(fileURLWithPath: "/usr/local/bin/vpn")
            task.arguments = ["disconnect"]
            try? task.run()
        }
    }

    func repairNetwork() {
        DispatchQueue.global(qos: .userInitiated).async {
            let task = Process()
            task.executableURL = URL(fileURLWithPath: "/usr/local/bin/vpn")
            task.arguments = ["repair"]
            try? task.run()
        }
    }

    func deleteProfile(name: String) {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let task = Process()
            task.executableURL = URL(fileURLWithPath: "/usr/local/bin/vpn")
            task.arguments = ["profile", "remove", name]
            try? task.run()
            task.waitUntilExit()

            Task { @MainActor in
                self?.syncFromDisk()
            }
        }
    }

    func saveProfile(name: String, server: String, user: String, psk: String, password: String, isFullTunnel: Bool, isNew: Bool) {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let cli = "/usr/local/bin/vpn"
            
            // 1. Add/Update Profile
            let addProf = Process()
            addProf.executableURL = URL(fileURLWithPath: cli)
            var args = ["profile", "add", name, "--server", server, "--psk", psk]
            if isFullTunnel { args.append("--full-tunnel") }
            addProf.arguments = args
            try? addProf.run()
            addProf.waitUntilExit()

            // 2. Add/Update Account
            if !user.isEmpty {
                let addAcct = Process()
                addAcct.executableURL = URL(fileURLWithPath: cli)
                addAcct.arguments = ["account", "add", name, user, "--password", password, "--default"]
                try? addAcct.run()
                addAcct.waitUntilExit()
            }

            Task { @MainActor in
                self?.syncFromDisk()
            }
        }
    }
}

// MARK: - Animated Rotating Linear Border Component (SwiftUI Native 60/120fps Metal-accelerated)

struct RotatingLinearBorder: View {
    var isConnecting: Bool
    var isConnected: Bool
    var cornerRadius: CGFloat = 13
    @State private var isSpinning = false

    var body: some View {
        ZStack {
            Rectangle()
                .fill(
                    AngularGradient(
                        gradient: Gradient(colors: isConnecting
                            ? [Color.clear, Color.clear, Color.orange.opacity(0.15), Color.orange, Color(red: 1.0, green: 0.9, blue: 0.6)]
                            : [Color.clear, Color.clear, Color(red: 0.1, green: 0.8, blue: 0.5).opacity(0.15), Color(red: 0.2, green: 0.95, blue: 0.6), Color(red: 0.7, green: 1.0, blue: 0.85)]
                        ),
                        center: .center
                    )
                )
                .scaleEffect(2.2)
                .rotationEffect(.degrees(isSpinning ? 360 : 0))
                .animation(Animation.linear(duration: 2.8).repeatForever(autoreverses: false), value: isSpinning)
                .mask(
                    RoundedRectangle(cornerRadius: cornerRadius)
                        .stroke(lineWidth: 1.8)
                )
        }
        .clipShape(RoundedRectangle(cornerRadius: cornerRadius))
        .shadow(
            color: isConnecting ? Color.orange.opacity(0.6) : Color(red: 0.15, green: 0.9, blue: 0.55).opacity(0.6),
            radius: 8
        )
        .onAppear {
            isSpinning = true
        }
    }
}

// MARK: - Native Helper for Menu Popups without ugly Dropdown Chevrons

struct CustomMenuButton: View {
    var onEdit: () -> Void
    var onDelete: () -> Void

    var body: some View {
        Button(action: showNativeMenu) {
            Image(systemName: "ellipsis")
                .font(.system(size: 15, weight: .bold))
                .foregroundColor(Color(red: 0.6, green: 0.65, blue: 0.72))
                .frame(width: 28, height: 28)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private func showNativeMenu() {
        let menu = NSMenu()
        let editItem = NSMenuItem(title: "Chỉnh sửa hồ sơ", action: #selector(MenuHelper.editAction), keyEquivalent: "")
        let deleteItem = NSMenuItem(title: "Xóa hồ sơ", action: #selector(MenuHelper.deleteAction), keyEquivalent: "")
        deleteItem.attributedTitle = NSAttributedString(string: "Xóa hồ sơ", attributes: [.foregroundColor: NSColor.systemRed])

        let helper = MenuHelper(onEdit: onEdit, onDelete: onDelete)
        editItem.target = helper
        deleteItem.target = helper
        
        menu.addItem(editItem)
        menu.addItem(NSMenuItem.separator())
        menu.addItem(deleteItem)

        if let event = NSApp.currentEvent {
            NSMenu.popUpContextMenu(menu, with: event, for: NSApp.keyWindow?.contentView ?? NSView())
        }
    }
}

@MainActor
final class MenuHelper: NSObject {
    let onEdit: () -> Void
    let onDelete: () -> Void

    init(onEdit: @escaping () -> Void, onDelete: @escaping () -> Void) {
        self.onEdit = onEdit
        self.onDelete = onDelete
        super.init()
    }

    @objc func editAction() { onEdit() }
    @objc func deleteAction() { onDelete() }
}

// MARK: - Main Menu Bar Popup View

struct ProfileCardRow: View {
    let profile: VPNProfileItem
    @ObservedObject var vpn: VPNManager
    var onEdit: () -> Void
    var onDelete: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            // Status Dot
            Circle()
                .fill(profile.isConnected ? Color(red: 0.2, green: 0.88, blue: 0.55) : (profile.isConnecting ? Color.orange : Color.gray.opacity(0.6)))
                .frame(width: 9, height: 9)
                .shadow(color: profile.isConnected ? Color.green.opacity(0.8) : (profile.isConnecting ? Color.orange.opacity(0.8) : Color.clear), radius: 4)

            // Profile Name & Subtitle
            VStack(alignment: .leading, spacing: 2) {
                Text(profile.name)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundColor(profile.isConnected ? .white : (profile.isConnecting ? Color(red: 1.0, green: 0.9, blue: 0.7) : Color(red: 0.85, green: 0.88, blue: 0.92)))
                    .lineLimit(1)
                    .truncationMode(.tail)

                HStack(spacing: 4) {
                    Text(profile.server)
                        .font(.system(size: 11))
                        .foregroundColor(Color.gray)
                        .lineLimit(1)
                    if !profile.username.isEmpty {
                        Text("• \(profile.username)")
                            .font(.system(size: 11))
                            .foregroundColor(Color.gray.opacity(0.8))
                            .lineLimit(1)
                    }
                }
            }

            Spacer(minLength: 8)

            // Toggle Switch
            Toggle("", isOn: Binding(
                get: { profile.isConnected || profile.isConnecting },
                set: { _ in vpn.toggleConnect(profile: profile) }
            ))
            .toggleStyle(SwitchToggleStyle(tint: profile.isConnecting ? Color.orange : Color(red: 0.15, green: 0.8, blue: 0.55)))
            .labelsHidden()

            // Context Menu Button
            CustomMenuButton(
                onEdit: onEdit,
                onDelete: onDelete
            )
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .background(
            ZStack {
                RoundedRectangle(cornerRadius: 13)
                    .fill(
                        profile.isConnected ? Color(red: 0.04, green: 0.18, blue: 0.12) :
                        (profile.isConnecting ? Color(red: 0.2, green: 0.12, blue: 0.04) : Color(red: 0.1, green: 0.12, blue: 0.16).opacity(0.85))
                    )

                if profile.isConnected || profile.isConnecting {
                    RotatingLinearBorder(isConnecting: profile.isConnecting, isConnected: profile.isConnected, cornerRadius: 13)
                } else {
                    RoundedRectangle(cornerRadius: 13)
                        .stroke(Color.white.opacity(0.08), lineWidth: 1)
                }
            }
        )
    }
}

struct MenuBarPopupView: View {
    @ObservedObject var vpn = VPNManager.shared
    @State private var showingAddModal = false
    @State private var editingProfile: VPNProfileItem?

    var body: some View {
        VStack(spacing: 0) {
            // Header Bar (Clean, NO Settings Icon)
            HStack(spacing: 12) {
                ZStack {
                    RoundedRectangle(cornerRadius: 12)
                        .fill(
                            vpn.isConnected ? Color(red: 0.04, green: 0.18, blue: 0.12) :
                            (vpn.isConnecting ? Color(red: 0.2, green: 0.12, blue: 0.04) : Color(red: 0.05, green: 0.16, blue: 0.22))
                        )

                    if vpn.isConnected || vpn.isConnecting {
                        RotatingLinearBorder(isConnecting: vpn.isConnecting, isConnected: vpn.isConnected, cornerRadius: 12)
                    } else {
                        RoundedRectangle(cornerRadius: 12)
                            .stroke(Color(red: 0.12, green: 0.55, blue: 0.65).opacity(0.6), lineWidth: 1)
                    }

                    Image(systemName: vpn.isConnected ? "checkmark.shield.fill" : (vpn.isConnecting ? "shield.lefthalf.filled" : "shield.fill"))
                        .font(.system(size: 20))
                        .foregroundColor(
                            vpn.isConnected ? Color(red: 0.2, green: 0.9, blue: 0.6) :
                            (vpn.isConnecting ? Color.orange : Color(red: 0.2, green: 0.75, blue: 0.95))
                        )
                    
                    if vpn.isConnected {
                        Circle()
                            .fill(Color(red: 0.2, green: 0.95, blue: 0.6))
                            .frame(width: 7, height: 7)
                            .offset(x: 9, y: -9)
                    } else if vpn.isConnecting {
                        Circle()
                            .fill(Color.orange)
                            .frame(width: 7, height: 7)
                            .offset(x: 9, y: -9)
                    }
                }
                .frame(width: 40, height: 40)

                HStack(spacing: 6) {
                    Text("TMS-VPN")
                        .font(.system(size: 16, weight: .bold))
                        .foregroundColor(.white)
                    Circle()
                        .fill(vpn.isConnected ? Color(red: 0.2, green: 0.85, blue: 0.55) : (vpn.isConnecting ? Color.orange : Color.gray))
                        .frame(width: 7, height: 7)
                    Text(vpn.isConnected ? "Đã kết nối" : (vpn.isConnecting ? "Đang kết nối..." : "Đã ngắt kết nối"))
                        .font(.system(size: 13, weight: .medium))
                        .foregroundColor(vpn.isConnected ? Color(red: 0.2, green: 0.85, blue: 0.55) : (vpn.isConnecting ? Color.orange : Color.gray))
                }

                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.top, 16)
            .padding(.bottom, 12)

            Divider().background(Color.white.opacity(0.08))

            // Subheader: Profile List Header
            HStack {
                Text("HỒ SƠ VPN CÔNG TY")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(Color(red: 0.52, green: 0.58, blue: 0.66))
                Spacer()
                Button(action: { showingAddModal = true }) {
                    HStack(spacing: 4) {
                        Image(systemName: "plus")
                            .font(.system(size: 10, weight: .bold))
                        Text("Thêm điểm nối")
                            .font(.system(size: 11, weight: .semibold))
                    }
                    .foregroundColor(Color(red: 0.2, green: 0.85, blue: 0.95))
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(
                        RoundedRectangle(cornerRadius: 8)
                            .fill(Color(red: 0.05, green: 0.16, blue: 0.22))
                            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color(red: 0.12, green: 0.55, blue: 0.65).opacity(0.6), lineWidth: 1))
                    )
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 16)
            .padding(.top, 14)
            .padding(.bottom, 8)

            // Profile List or Empty State
            if vpn.profiles.isEmpty {
                VStack(spacing: 8) {
                    Image(systemName: "network.badge.shield.half.filled")
                        .font(.system(size: 32))
                        .foregroundColor(Color.gray.opacity(0.5))
                        .padding(.top, 10)
                    Text("Chưa có hồ sơ VPN nào")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundColor(.gray)
                    Text("Nhấn nút \"+ Thêm điểm nối\" để cấu hình máy chủ đầu tiên.")
                        .font(.system(size: 11))
                        .foregroundColor(Color.gray.opacity(0.7))
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 20)
                }
                .padding(.vertical, 20)
                .frame(maxWidth: .infinity)
            } else {
                VStack(spacing: 8) {
                    ForEach(vpn.profiles) { profile in
                        ProfileCardRow(
                            profile: profile,
                            vpn: vpn,
                            onEdit: { editingProfile = profile },
                            onDelete: { vpn.deleteProfile(name: profile.name) }
                        )
                    }
                }
                .padding(.horizontal, 16)
            }

            // Error notice if any
            if let err = vpn.errorMessage {
                HStack(spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundColor(.orange)
                        .font(.system(size: 12))
                    Text(err)
                        .font(.system(size: 11))
                        .foregroundColor(.orange)
                        .lineLimit(2)
                }
                .padding(.horizontal, 16)
                .padding(.top, 6)
            }

            Divider().background(Color.white.opacity(0.08)).padding(.top, 12)

            // Footer Bar (Clean, NO Settings button)
            HStack {
                Text("TMS-VPN Client")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundColor(Color.gray.opacity(0.7))

                Spacer()

                Button(action: { NSApplication.shared.terminate(nil) }) {
                    HStack(spacing: 5) {
                        Image(systemName: "rectangle.portrait.and.arrow.right")
                        Text("Thoát").font(.system(size: 12, weight: .medium))
                        Text("⌘Q").font(.system(size: 10, weight: .semibold)).foregroundColor(Color.gray.opacity(0.6))
                    }
                    .foregroundColor(Color(red: 0.7, green: 0.74, blue: 0.8))
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
        }
        .frame(width: 370)
        .background(Color(red: 0.07, green: 0.09, blue: 0.12))
        .sheet(isPresented: $showingAddModal) {
            ProfileFormSheet(isPresented: $showingAddModal, initialProfile: nil)
        }
        .sheet(item: $editingProfile) { prof in
            ProfileFormSheet(isPresented: Binding(get: { editingProfile != nil }, set: { if !$0 { editingProfile = nil } }), initialProfile: prof)
        }
    }
}

// MARK: - Add / Edit Profile Sheet (Polished Dark Theme matching Web Demo exactly)

struct CleanDarkTextField: View {
    var placeholder: String
    @Binding var text: String
    var isDisabled: Bool = false

    var body: some View {
        ZStack(alignment: .leading) {
            if text.isEmpty {
                Text(placeholder)
                    .foregroundColor(Color(red: 0.4, green: 0.48, blue: 0.58))
                    .font(.system(size: 12.5))
                    .padding(.horizontal, 12)
            }
            TextField("", text: $text)
                .textFieldStyle(.plain)
                .font(.system(size: 13))
                .foregroundColor(isDisabled ? Color.gray : Color.white)
                .padding(.horizontal, 12)
                .padding(.vertical, 8)
                .disabled(isDisabled)
        }
        .background(
            RoundedRectangle(cornerRadius: 10)
                .fill(Color(red: 0.05, green: 0.07, blue: 0.1).opacity(0.85))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(Color.white.opacity(0.12), lineWidth: 1)
        )
    }
}

struct CleanDarkSecureField: View {
    var placeholder: String
    @Binding var text: String

    var body: some View {
        ZStack(alignment: .leading) {
            if text.isEmpty {
                Text(placeholder)
                    .foregroundColor(Color(red: 0.4, green: 0.48, blue: 0.58))
                    .font(.system(size: 12.5))
                    .padding(.horizontal, 12)
            }
            SecureField("", text: $text)
                .textFieldStyle(.plain)
                .font(.system(size: 13))
                .foregroundColor(Color.white)
                .padding(.horizontal, 12)
                .padding(.vertical, 8)
        }
        .background(
            RoundedRectangle(cornerRadius: 10)
                .fill(Color(red: 0.05, green: 0.07, blue: 0.1).opacity(0.85))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(Color.white.opacity(0.12), lineWidth: 1)
        )
    }
}

struct ProfileFormSheet: View {
    @Binding var isPresented: Bool
    var initialProfile: VPNProfileItem?

    @State private var name = ""
    @State private var server = ""
    @State private var user = ""
    @State private var password = ""
    @State private var psk = ""
    @State private var isFullTunnel = true

    var isEdit: Bool { initialProfile != nil }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Text(isEdit ? "Chỉnh Sửa Hồ Sơ VPN" : "Thêm Hồ Sơ VPN L2TP")
                    .font(.system(size: 15, weight: .bold))
                    .foregroundColor(.white)
                Spacer()
                Button(action: { isPresented = false }) {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundColor(Color.gray.opacity(0.7))
                        .font(.system(size: 16))
                }
                .buttonStyle(.plain)
            }

            VStack(alignment: .leading, spacing: 5) {
                Text("Tên điểm nối")
                    .font(.system(size: 11.5, weight: .semibold))
                    .foregroundColor(Color(red: 0.2, green: 0.85, blue: 0.95))
                CleanDarkTextField(placeholder: "VD: Trụ sở chính (Prod)", text: $name, isDisabled: isEdit)
            }

            VStack(alignment: .leading, spacing: 5) {
                Text("Địa chỉ máy chủ (IP / Host)")
                    .font(.system(size: 11.5, weight: .semibold))
                    .foregroundColor(Color(red: 0.2, green: 0.85, blue: 0.95))
                CleanDarkTextField(placeholder: "VD: vpn.company.com hoặc 1.2.3.4", text: $server)
            }

            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 5) {
                    Text("Tài khoản (Username)")
                        .font(.system(size: 11.5, weight: .semibold))
                        .foregroundColor(Color(red: 0.2, green: 0.85, blue: 0.95))
                    CleanDarkTextField(placeholder: "Tên đăng nhập", text: $user)
                }

                VStack(alignment: .leading, spacing: 5) {
                    Text("Mật khẩu")
                        .font(.system(size: 11.5, weight: .semibold))
                        .foregroundColor(Color(red: 0.2, green: 0.85, blue: 0.95))
                    CleanDarkSecureField(placeholder: "Mật khẩu tài khoản", text: $password)
                }
            }

            VStack(alignment: .leading, spacing: 5) {
                Text("Khóa bí mật chia sẻ IPsec (PSK)")
                    .font(.system(size: 11.5, weight: .semibold))
                    .foregroundColor(Color(red: 0.2, green: 0.85, blue: 0.95))
                CleanDarkSecureField(placeholder: "Pre-shared key", text: $psk)
            }

            // Native macOS Switch Toggle for Send All Traffic
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Gửi toàn bộ lưu lượng qua VPN")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(Color(red: 0.88, green: 0.92, blue: 0.96))
                    Text("Định tuyến tất cả Internet qua VPN (Send all traffic)")
                        .font(.system(size: 10))
                        .foregroundColor(Color.gray)
                }
                Spacer()
                Toggle("", isOn: $isFullTunnel)
                    .toggleStyle(SwitchToggleStyle(tint: Color(red: 0.2, green: 0.85, blue: 0.95)))
                    .labelsHidden()
            }
            .padding(11)
            .background(
                RoundedRectangle(cornerRadius: 10)
                    .fill(Color.white.opacity(0.04))
            )

            HStack(spacing: 10) {
                Spacer()
                Button("Hủy") {
                    isPresented = false
                }
                .keyboardShortcut(.cancelAction)
                .buttonStyle(SecondaryButtonStyle())

                Button(isEdit ? "Cập nhật hồ sơ" : "Lưu điểm nối") {
                    guard !name.trimmingCharacters(in: .whitespaces).isEmpty,
                          !server.trimmingCharacters(in: .whitespaces).isEmpty else { return }
                    
                    VPNManager.shared.saveProfile(
                        name: name.trimmingCharacters(in: .whitespaces),
                        server: server.trimmingCharacters(in: .whitespaces),
                        user: user.trimmingCharacters(in: .whitespaces),
                        psk: psk,
                        password: password,
                        isFullTunnel: isFullTunnel,
                        isNew: !isEdit
                    )
                    isPresented = false
                }
                .keyboardShortcut(.defaultAction)
                .buttonStyle(PrimaryButtonStyle())
            }
            .padding(.top, 8)
        }
        .padding(22)
        .frame(width: 380)
        .background(Color(red: 0.08, green: 0.1, blue: 0.14))
        .onAppear {
            if let p = initialProfile {
                name = p.name
                server = p.server
                user = p.username
                isFullTunnel = p.isFullTunnel
            }
        }
    }
}

// MARK: - Custom UI Styles

struct PrimaryButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 13, weight: .semibold))
            .foregroundColor(.black)
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
            .background(Color(red: 0.2, green: 0.85, blue: 0.95))
            .cornerRadius(9)
            .opacity(configuration.isPressed ? 0.8 : 1.0)
    }
}

struct SecondaryButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 13, weight: .medium))
            .foregroundColor(Color(red: 0.85, green: 0.88, blue: 0.92))
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
            .background(Color.white.opacity(0.1))
            .cornerRadius(9)
            .opacity(configuration.isPressed ? 0.8 : 1.0)
    }
}

// MARK: - App Delegate & Menu Bar Setup

final class AppDelegate: NSObject, NSApplicationDelegate {
    static var shared: AppDelegate?
    var statusItem: NSStatusItem?
    var popover = NSPopover()

    func applicationDidFinishLaunching(_ notification: Notification) {
        AppDelegate.shared = self
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        
        let initialPhase = MainActor.assumeIsolated {
            VPNManager.shared.currentPhase
        }
        updateIcon(phase: initialPhase)

        guard let button = statusItem?.button else { return }
        button.action = #selector(togglePopover(_:))
        button.target = self

        popover.contentSize = NSSize(width: 370, height: 440)
        popover.behavior = .transient
        popover.contentViewController = NSHostingController(rootView: MenuBarPopupView())

        MainActor.assumeIsolated {
            VPNManager.shared.onStatusChanged = { [weak self] phase in
                Task { @MainActor in
                    self?.updateIcon(phase: phase)
                }
            }
        }
    }

    func updateIcon(phase: String) {
        guard let button = statusItem?.button else { return }
        button.image = makeMenuBarIcon(phase: phase)
    }

    @objc func togglePopover(_ sender: AnyObject?) {
        guard let button = statusItem?.button else { return }
        if popover.isShown {
            popover.performClose(sender)
        } else {
            MainActor.assumeIsolated {
                VPNManager.shared.syncFromDisk()
            }
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

if command -v swiftc >/dev/null 2>&1; then
    echo "🔨 Compiling Menu Bar App with swiftc ($SWIFT_TARGET)..."
    swiftc -O -target "$SWIFT_TARGET" "$SWIFT_SRC" -o "$UI_BIN" -framework Cocoa -framework SwiftUI

    echo "📂 Creating /Applications/TMS-VPN.app bundle..."
    mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"
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
else
    echo "⚠️ swiftc compiler not found. Please install Command Line Tools (xcode-select --install) to build UI."
fi

rm -f "$SWIFT_SRC" "$UI_BIN"

echo "=================================================="
echo "🎉 SUCCESS: TMS-VPN Engine & Menu Bar UI Installed!"
echo "👉 Menu Bar App: /Applications/TMS-VPN.app"
echo "👉 CLI Engine: /usr/local/bin/vpn"
echo "=================================================="

open -a "/Applications/TMS-VPN.app" 2>/dev/null || true
