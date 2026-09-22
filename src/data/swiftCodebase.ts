import { SwiftSourceFile } from '../types';

export const SWIFT_CODEBASE_FILES: SwiftSourceFile[] = [
  {
    filename: 'Package.swift',
    path: 'Package.swift',
    category: 'build',
    description: 'Swift Package Manager - Biên dịch 1 lệnh ra 1 binary duy nhất cho macOS',
    content: `// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "TMSVPNMenuBar",
    platforms: [.macOS(.v13)],
    products: [
        .executable(name: "tms-vpn-bar", targets: ["TMSVPNMenuBar"])
    ],
    dependencies: [],
    targets: [
        .executableTarget(
            name: "TMSVPNMenuBar",
            dependencies: [],
            path: "Sources/TMSVPNMenuBar"
        )
    ]
)
`
  },
  {
    filename: 'main.swift',
    path: 'Sources/TMSVPNMenuBar/main.swift',
    category: 'core',
    description: 'Khởi tạo ứng dụng chạy thuần túy trên Menu Bar (LSUIElement = true)',
    content: `import Cocoa
import SwiftUI

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory) // Chạy thuần trên Menu Bar, không chiếm diện tích Dock

_ = NSApplicationMain(CommandLine.argc, CommandLine.unsafeArgv)
`
  },
  {
    filename: 'AppDelegate.swift',
    path: 'Sources/TMSVPNMenuBar/AppDelegate.swift',
    category: 'core',
    description: 'Quản lý NSStatusItem, NSPopover và hiển thị modal ở vị trí cố định dưới Menu Bar icon',
    content: `import Cocoa
import SwiftUI

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    var statusItem: NSStatusItem?
    var popover = NSPopover()
    let vpnService = VPNService.shared

    func applicationDidFinishLaunching(_ notification: Notification) {
        setupStatusItem()
        setupPopover()
    }

    private func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        guard let button = statusItem?.button else { return }
        
        button.image = NSImage(systemSymbolName: "shield.fill", accessibilityDescription: "TMS-VPN")
        button.action = #selector(togglePopover(_:))
        button.target = self
    }

    private func setupPopover() {
        let contentView = MenuBarPopupView()
            .environmentObject(vpnService)
        
        popover.contentSize = NSSize(width: 360, height: 460)
        popover.behavior = .transient
        popover.animates = true
        popover.contentViewController = NSHostingController(rootView: contentView)
    }

    @objc func togglePopover(_ sender: AnyObject?) {
        guard let button = statusItem?.button else { return }
        if popover.isShown {
            popover.performClose(sender)
        } else {
            // Luôn mở ở vị trí đồng nhất ngay dưới status icon
            popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
            NSApp.activate(ignoringOtherApps: true)
        }
    }
}
`
  },
  {
    filename: 'MenuBarPopupView.swift',
    path: 'Sources/TMSVPNMenuBar/Views/MenuBarPopupView.swift',
    category: 'views',
    description: 'Giao diện Popup Menu Bar khớp 100% thiết kế TMS-VPN',
    content: `import SwiftUI

struct MenuBarPopupView: View {
    @EnvironmentObject var vpnService: VPNService
    @State private var showingAddProfile = false
    @State private var showingSettings = false

    var body: some View {
        VStack(spacing: 0) {
            // MARK: - Header
            HStack(spacing: 12) {
                ZStack {
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
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
                        .fill(vpnService.isConnected ? Color(red: 0.2, green: 0.85, blue: 0.55) : Color.gray)
                        .frame(width: 7, height: 7)
                    Text(vpnService.isConnected ? "Đang kết nối" : "Đã ngắt kết nối")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundColor(vpnService.isConnected ? Color(red: 0.2, green: 0.85, blue: 0.55) : Color.gray)
                }

                Spacer()

                Button(action: { showingSettings = true }) {
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

            // MARK: - Hồ sơ VPN L2TP
            HStack {
                Text("HỒ SƠ VPN CÔNG TY")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(Color(red: 0.55, green: 0.6, blue: 0.66))
                Spacer()
                Button(action: { showingAddProfile = true }) {
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

            // Profile List
            ScrollView(.vertical, showsIndicators: false) {
                VStack(spacing: 8) {
                    ForEach(vpnService.profiles) { profile in
                        ProfileRowView(profile: profile, isActive: profile.isEnabled) {
                            vpnService.toggleProfile(profile)
                        }
                    }
                }
                .padding(.horizontal, 16)
            }

            Divider().background(Color.white.opacity(0.08)).padding(.top, 8)

            // MARK: - Bottom Controls
            VStack(spacing: 12) {
                HStack {
                    Image(systemName: "bolt.fill").foregroundColor(Color.gray)
                    Text("Tự động kết nối khi khởi động").font(.system(size: 13)).foregroundColor(Color.white.opacity(0.9))
                    Spacer()
                    Toggle("", isOn: $vpnService.settings.autoConnectOnLaunch).labelsHidden()
                }
                .padding(.horizontal, 16)
                .padding(.top, 12)

                HStack {
                    Spacer()
                    Button(action: {
                        vpnService.disconnectAll()
                        NSApplication.shared.terminate(nil)
                    }) {
                        HStack(spacing: 6) {
                            Image(systemName: "rectangle.portrait.and.arrow.right")
                            Text("Thoát TMS-VPN").font(.system(size: 12, weight: .medium))
                            Text("⌘Q").font(.system(size: 11, weight: .semibold)).foregroundColor(Color.gray.opacity(0.7))
                        }
                        .foregroundColor(Color.gray)
                    }
                    .buttonStyle(.plain)
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 14)
            }
        }
        .frame(width: 360)
        .background(Color(red: 0.07, green: 0.09, blue: 0.12))
        .cornerRadius(18)
    }
}
`
  },
  {
    filename: 'ProfileRowView.swift',
    path: 'Sources/TMSVPNMenuBar/Views/ProfileRowView.swift',
    category: 'views',
    description: 'Hàng item profile với màu viền và switch toggle',
    content: `import SwiftUI

struct ProfileRowView: View {
    let profile: L2TPProfile
    let isActive: Bool
    let onToggle: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            Circle()
                .fill(beaconColor)
                .frame(width: 9, height: 9)
                .shadow(color: beaconColor.opacity(isActive ? 0.8 : 0.2), radius: 6)

            Text(profile.name)
                .font(.system(size: 14, weight: .medium))
                .foregroundColor(isActive && profile.statusColor == "red" ? Color(red: 1.0, green: 0.85, blue: 0.85) : .white)

            Spacer()

            Toggle("", isOn: Binding(get: { isActive }, set: { _ in onToggle() }))
                .toggleStyle(SwitchToggleStyle(tint: profile.statusColor == "red" ? Color(red: 0.95, green: 0.25, blue: 0.35) : Color(red: 0.15, green: 0.8, blue: 0.55)))
                .labelsHidden()

            Image(systemName: "ellipsis")
                .font(.system(size: 14, weight: .bold))
                .foregroundColor(Color.gray.opacity(0.8))
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .background(
            RoundedRectangle(cornerRadius: 14)
                .fill(backgroundColor)
                .overlay(RoundedRectangle(cornerRadius: 14).stroke(borderColor, lineWidth: 1))
        )
    }

    private var beaconColor: Color {
        if !isActive { return Color.gray.opacity(0.6) }
        return profile.statusColor == "red" ? Color(red: 0.95, green: 0.3, blue: 0.3) : Color(red: 0.2, green: 0.88, blue: 0.55)
    }

    private var backgroundColor: Color {
        if !isActive { return Color(red: 0.1, green: 0.12, blue: 0.16).opacity(0.7) }
        if profile.statusColor == "red" { return Color(red: 0.22, green: 0.08, blue: 0.1).opacity(0.8) }
        return Color(red: 0.06, green: 0.18, blue: 0.14).opacity(0.8)
    }

    private var borderColor: Color {
        if !isActive { return Color.white.opacity(0.08) }
        if profile.statusColor == "red" { return Color(red: 0.85, green: 0.25, blue: 0.3).opacity(0.6) }
        return Color(red: 0.15, green: 0.7, blue: 0.45).opacity(0.6)
    }
}
`
  },
  {
    filename: 'VPNService.swift',
    path: 'Sources/TMSVPNMenuBar/Services/VPNService.swift',
    category: 'core',
    description: 'Quản lý kết nối L2TP và danh sách profile',
    content: `import SwiftUI

struct L2TPProfile: Identifiable, Codable {
    var id: String = UUID().uuidString
    var name: String
    var serverAddress: String
    var username: String?
    var sharedSecret: String?
    var statusColor: String // "green", "red", "gray"
    var isEnabled: Bool
}

struct AppSettings: Codable {
    var autoConnectOnLaunch: Bool = true
    var killSwitch: Bool = true
}

@MainActor
final class VPNService: ObservableObject {
    static let shared = VPNService()
    @Published var profiles: [L2TPProfile] = [
        L2TPProfile(name: "Trụ sở chính (Prod)", serverAddress: "vpn-prod.company.internal", statusColor: "green", isEnabled: true),
        L2TPProfile(name: "Môi trường Dev & Staging", serverAddress: "vpn-staging.company.internal", statusColor: "red", isEnabled: true),
        L2TPProfile(name: "Chi nhánh TP.HCM", serverAddress: "vpn-hcm.company.internal", statusColor: "gray", isEnabled: false)
    ]
    @Published var settings = AppSettings()
    @Published var isConnected = true

    func toggleProfile(_ profile: L2TPProfile) {
        if let idx = profiles.firstIndex(where: { $0.id == profile.id }) {
            let wasActive = profiles[idx].isEnabled
            for i in profiles.indices { profiles[i].isEnabled = false }
            if !wasActive {
                profiles[idx].isEnabled = true
                isConnected = true
                connectL2TP(profiles[idx])
            } else {
                disconnectAll()
            }
        }
    }

    private func connectL2TP(_ profile: L2TPProfile) {
        // Gọi backend ninhlee99/vpn hoặc scutil / pppd L2TP native
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/local/bin/vpn")
        process.arguments = ["connect", profile.serverAddress]
        try? process.run()
    }

    func disconnectAll() {
        isConnected = false
        for i in profiles.indices { profiles[i].isEnabled = false }
    }
}
`
  },
  {
    filename: 'Makefile',
    path: 'Makefile',
    category: 'build',
    description: '1 Lệnh duy nhất build single universal binary chạy ngay trên macOS',
    content: `# Build 1 binary duy nhất cho macOS (Universal Apple Silicon + Intel)
.PHONY: build

build:
	@echo "==> Đang biên dịch 1 binary duy nhất..."
	swift build -c release --arch arm64 --arch x86_64
	@echo "==> Xong! Binary duy nhất tại: .build/apple/Products/Release/tms-vpn-bar"
`
  }
];

