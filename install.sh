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
    var updated_at: String?
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

// MARK: - Error & Alert Models

enum VPNAlertKind {
    case sessionStale
    case authFailed
    case ikeFailed
    case routeFailed
    case generic
}

struct VPNAlertInfo: Equatable {
    var kind: VPNAlertKind
    var title: String
    var message: String
    var detail: String
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
    @Published var activeAlert: VPNAlertInfo?
    @Published var isStaleSession: Bool = false
    @Published var autoRetryCountdown: Int = 0

    var onStatusChanged: ((String) -> Void)?
    private var pollTimer: Timer?
    private var retryTimer: Timer?

    /// The phase the user just asked for, shown optimistically until the CLI's state file
    /// agrees. Without it the 1s poll read the not-yet-updated state.json and flipped the
    /// switch back for a tick (ON → OFF → ON), which is what looked like jank.
    private struct PendingIntent {
        var id: Int
        var phase: String
        var profile: String?
        var since: Date
        var timeout: TimeInterval
    }
    private var pendingIntent: PendingIntent?
    private var intentCounter = 0

    var linkState: LinkState {
        isConnecting ? .connecting : (isConnected ? .connected : .idle)
    }

    private let cli = "/usr/local/bin/vpn"

    private var configURL: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config")
            .appendingPathComponent("vpn")
            .appendingPathComponent("config.json")
    }

    private var stateURL: URL {
        URL(fileURLWithPath: "/var/run/vpn/state.json")
    }

    private let operationQueue = DispatchQueue(label: "com.tms.vpn.operationQueue", qos: .userInitiated)

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

    /// @Published fires objectWillChange on every assignment, even of an equal value, so
    /// the 1s poll used to re-render the whole popover every second. Only write on change.
    private func update<T: Equatable>(_ keyPath: ReferenceWritableKeyPath<VPNManager, T>, _ value: T) {
        if self[keyPath: keyPath] != value {
            self[keyPath: keyPath] = value
        }
    }

    private static func parseDate(_ s: String?) -> Date? {
        guard let s = s else { return nil }
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = f.date(from: s) { return d }
        f.formatOptions = [.withInternetDateTime]
        return f.date(from: s)
    }

    func syncFromDisk() {
        // 1. Read State (/var/run/vpn/state.json)
        var activeProf: String?
        var phase = "DISCONNECTED"
        var localIP = ""
        var tunDev = ""
        var failStage = ""
        var failDetail = ""
        var updatedAt: Date?

        if let stateData = try? Data(contentsOf: stateURL),
           let st = try? JSONDecoder().decode(CLIState.self, from: stateData) {
            phase = st.phase ?? "DISCONNECTED"
            activeProf = st.profile
            localIP = st.local_ip ?? ""
            tunDev = st.tun_device ?? ""
            failStage = st.fail_stage ?? ""
            failDetail = st.fail_detail ?? ""
            updatedAt = Self.parseDate(st.updated_at)
        }

        if let intent = pendingIntent {
            let confirmed: Bool
            if intent.phase == "CONNECTING" {
                // A FAILED left over from an earlier attempt must not count as the answer.
                let freshFailure = phase == "FAILED" && (updatedAt.map { $0 >= intent.since.addingTimeInterval(-1) } ?? false)
                confirmed = phase == "CONNECTING" || phase == "CONNECTED" || freshFailure
            } else {
                confirmed = phase == "DISCONNECTED"
            }
            if confirmed || Date().timeIntervalSince(intent.since) > intent.timeout {
                pendingIntent = nil
            } else {
                phase = intent.phase
                activeProf = intent.profile ?? activeProf
            }
        }

        if phase == "FAILED" {
            applyFailure(stage: failStage, detail: failDetail)
        } else if phase == "CONNECTED" || phase == "CONNECTING" {
            update(\.errorMessage, nil)
            update(\.activeAlert, nil)
            update(\.isStaleSession, false)
            cancelAutoRetry()
        }

        let oldPhase = currentPhase
        update(\.currentPhase, phase)
        update(\.isConnected, phase == "CONNECTED")
        update(\.isConnecting, phase == "CONNECTING")
        update(\.currentIP, localIP)
        update(\.currentTunDevice, tunDev)

        if oldPhase != phase {
            onStatusChanged?(phase)
        }

        // 2. Read Config (~/.config/vpn/config.json)
        if let configData = try? Data(contentsOf: configURL),
           let cfg = try? JSONDecoder().decode(CLIConfig.self, from: configData) {
            if activeProf == nil {
                activeProf = cfg.active_profile
            }
            update(\.activeProfileName, activeProf)

            var items: [VPNProfileItem] = []
            for (pName, pVal) in cfg.profiles ?? [:] {
                items.append(VPNProfileItem(
                    name: pName,
                    server: pVal.server ?? "",
                    username: pVal.default_account ?? pVal.accounts?.keys.first ?? "",
                    isFullTunnel: pVal.full_tunnel ?? true,
                    isConnected: isConnected && activeProf == pName,
                    isConnecting: isConnecting && activeProf == pName
                ))
            }
            update(\.profiles, items.sorted { $0.name.lowercased() < $1.name.lowercased() })
        }
    }

    private func applyFailure(stage: String, detail: String) {
        let alert: VPNAlertInfo
        let message: String
        if detail.contains("already logged in") || detail.contains("You are already logged in") {
            alert = VPNAlertInfo(
                kind: .sessionStale,
                title: "Session Stale",
                message: "Tài khoản đang có phiên đăng nhập trên máy chủ ('Already logged in').",
                detail: detail
            )
            message = "Session Stale: Tài khoản đang có phiên đăng nhập trên server."
        } else if stage == "PPP_AUTH_FAILURE" || detail.contains("CHAP authentication rejected") {
            alert = VPNAlertInfo(
                kind: .authFailed,
                title: "Authentication Failed",
                message: "Xác thực PPP/CHAP thất bại: Sai tên tài khoản hoặc mật khẩu.",
                detail: detail.isEmpty ? "CHAP authentication rejected by peer" : detail
            )
            message = "Authentication Failed: Sai tên tài khoản hoặc mật khẩu."
        } else if stage == "IKE_FAILED" || stage.contains("IKE") {
            alert = VPNAlertInfo(
                kind: .ikeFailed,
                title: "IKE Handshake Failed",
                message: "Lỗi bắt tay IPsec IKE: Sai địa chỉ IP máy chủ hoặc sai Pre-shared Key (PSK).",
                detail: detail
            )
            message = "IKE Handshake Failed: Sai IP máy chủ hoặc sai khóa PSK."
        } else if stage == "ROUTE_FAILURE" {
            alert = VPNAlertInfo(
                kind: .routeFailed,
                title: "Routing Error",
                message: "Lỗi thiết lập định tuyến mạng. Hãy bấm Sửa mạng (Repair).",
                detail: detail
            )
            message = "Routing Error: Lỗi thiết lập định tuyến mạng."
        } else {
            alert = VPNAlertInfo(
                kind: .generic,
                title: stage.isEmpty ? "Lỗi kết nối" : stage,
                message: detail.isEmpty ? "Kết nối thất bại" : detail,
                detail: detail
            )
            message = "\(stage.isEmpty ? "Lỗi kết nối" : stage): \(detail)"
        }

        update(\.activeAlert, alert)
        update(\.errorMessage, message)
        update(\.isStaleSession, alert.kind == .sessionStale)
        if alert.kind == .sessionStale, autoRetryCountdown == 0, retryTimer == nil, let prof = activeProfileName {
            startAutoRetry(profileName: prof)
        }
    }

    func startAutoRetry(profileName: String) {
        cancelAutoRetry()
        autoRetryCountdown = 5
        retryTimer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] timer in
            Task { @MainActor in
                guard let self = self else { timer.invalidate(); return }
                if self.autoRetryCountdown > 1 {
                    self.autoRetryCountdown -= 1
                } else {
                    self.cancelAutoRetry()
                    self.cleanupAndForceConnect(profileName: profileName)
                }
            }
        }
    }

    func cancelAutoRetry() {
        retryTimer?.invalidate()
        retryTimer = nil
        update(\.autoRetryCountdown, 0)
    }

    func toggleConnect(profile: VPNProfileItem) {
        if profile.isConnected || profile.isConnecting {
            disconnect()
        } else {
            connect(profileName: profile.name)
        }
    }

    /// 0ms optimistic UI update, held by a PendingIntent until the CLI catches up.
    private func beginIntent(phase: String, profile: String?, timeout: TimeInterval) -> Int {
        intentCounter += 1
        pendingIntent = PendingIntent(id: intentCounter, phase: phase, profile: profile, since: Date(), timeout: timeout)

        update(\.currentPhase, phase)
        update(\.isConnecting, phase == "CONNECTING")
        update(\.isConnected, false)
        update(\.isStaleSession, false)
        update(\.activeAlert, nil)
        if let profile = profile {
            update(\.activeProfileName, profile)
        }
        update(\.profiles, profiles.map { p in
            var copy = p
            copy.isConnected = false
            copy.isConnecting = phase == "CONNECTING" && p.name == profile
            return copy
        })
        onStatusChanged?(phase)
        return intentCounter
    }

    /// Releases the intent once the CLI command it was waiting on has finished, so the
    /// next poll shows the real outcome instead of waiting for the timeout.
    private func endIntent(_ id: Int) {
        if pendingIntent?.id == id {
            pendingIntent = nil
        }
        syncFromDisk()
    }

    nonisolated private static func run(_ path: String, _ args: [String]) {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: path)
        p.arguments = args
        try? p.run()
        p.waitUntilExit()
    }

    /// Tears down any existing session: graceful CLI disconnect, then kill a hung daemon.
    nonisolated private static func teardown(cli: String) {
        run(cli, ["disconnect"])
        run("/usr/bin/pkill", ["-9", "-f", "vpn connect"])
    }

    /// Launches `vpn connect` without blocking the serial queue — the spawner only exits once
    /// the daemon reports success or failure, which can take the full connect timeout, and a
    /// toggle-off queued behind it would otherwise wait that long.
    nonisolated private static func launchConnect(cli: String, profileName: String, onExit: @escaping @Sendable () -> Void) {
        let task = Process()
        task.executableURL = URL(fileURLWithPath: cli)
        task.arguments = ["connect", "--profile", profileName]
        task.terminationHandler = { _ in onExit() }
        do {
            try task.run()
        } catch {
            onExit()
        }
    }

    func connect(profileName: String) {
        cancelAutoRetry()
        update(\.errorMessage, nil)
        let id = beginIntent(phase: "CONNECTING", profile: profileName, timeout: 45)

        // Serialized so rapid off/on toggles can't interleave their CLI calls.
        let cli = self.cli
        operationQueue.async {
            Self.teardown(cli: cli)
            // Short cooldown for interface & PID release.
            Thread.sleep(forTimeInterval: 0.3)
            Self.launchConnect(cli: cli, profileName: profileName) {
                Task { @MainActor in self.endIntent(id) }
            }
        }
    }

    func cleanupAndForceConnect(profileName: String) {
        cancelAutoRetry()
        let id = beginIntent(phase: "CONNECTING", profile: profileName, timeout: 45)
        update(\.errorMessage, "Đang đăng xuất phiên cũ & kết nối lại...")

        let cli = self.cli
        operationQueue.async {
            Self.teardown(cli: cli)
            Self.run(cli, ["repair"])
            // Give the server time to flush the RADIUS/L2TP session.
            Thread.sleep(forTimeInterval: 1.2)
            Self.launchConnect(cli: cli, profileName: profileName) {
                Task { @MainActor in self.endIntent(id) }
            }
        }
    }

    func disconnect() {
        cancelAutoRetry()
        update(\.errorMessage, nil)
        let id = beginIntent(phase: "DISCONNECTED", profile: nil, timeout: 10)

        let cli = self.cli
        operationQueue.async {
            Self.teardown(cli: cli)
            Task { @MainActor in self.endIntent(id) }
        }
    }

    func deleteProfile(name: String) {
        let cli = self.cli
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            Self.run(cli, ["profile", "remove", name])
            Task { @MainActor in self?.syncFromDisk() }
        }
    }

    func saveProfile(name: String, server: String, user: String, psk: String, password: String, isFullTunnel: Bool, isNew: Bool) {
        let cli = self.cli
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            var args = ["profile", "add", name, "--server", server, "--psk", psk]
            args.append("--full-tunnel=\(isFullTunnel)")
            Self.run(cli, args)

            if !user.isEmpty {
                Self.run(cli, ["account", "add", name, user, "--password", password, "--default"])
            }

            Task { @MainActor in self?.syncFromDisk() }
        }
    }
}

// MARK: - Link State & Palette

enum LinkState: Equatable {
    case idle, connecting, connected
}

extension VPNProfileItem {
    var linkState: LinkState {
        isConnecting ? .connecting : (isConnected ? .connected : .idle)
    }
}

enum VPNColors {
    static let green = Color(red: 0.2, green: 0.9, blue: 0.55)
    static let greenBright = Color(red: 0.6, green: 1.0, blue: 0.8)
    static let amber = Color(red: 0.98, green: 0.62, blue: 0.1)
    static let amberBright = Color(red: 1.0, green: 0.9, blue: 0.62)
    static let switchOff = Color(red: 0.2, green: 0.25, blue: 0.33)
}

// MARK: - Animation Primitives
//
// Every looping effect below is driven by TimelineView and derives its phase from
// wall-clock time rather than `repeatForever` + `onAppear`. The old approach restarted
// or froze whenever SwiftUI re-created the view (every state change or poll re-render),
// which is what made the borders stutter. Tracing along the outline path also keeps the
// light moving at constant speed; a rotating AngularGradient on a wide card raced along
// the long edges and dropped out at the short ones.

/// A slice of a rounded-rect outline, wrapping past the path's start point.
struct PerimeterSegment: Shape {
    var cornerRadius: CGFloat
    var inset: CGFloat
    var start: CGFloat
    var length: CGFloat

    func path(in rect: CGRect) -> Path {
        let outline = RoundedRectangle(cornerRadius: max(cornerRadius - inset, 0))
            .path(in: rect.insetBy(dx: inset, dy: inset))
        let end = start + length
        if end <= 1 {
            return outline.trimmedPath(from: start, to: end)
        }
        var path = outline.trimmedPath(from: start, to: 1)
        path.addPath(outline.trimmedPath(from: 0, to: end - 1))
        return path
    }
}

/// A comet of light that travels around a rounded rectangle, fading out along its tail.
struct TracingBorder: View {
    var cornerRadius: CGFloat
    var color: Color
    var highlight: Color
    var period: Double
    var lineWidth: CGFloat = 1.6
    var tailLength: CGFloat = 0.3

    private let tailSteps = 8

    var body: some View {
        TimelineView(.animation) { timeline in
            let t = timeline.date.timeIntervalSinceReferenceDate
            let head = CGFloat((t / period).truncatingRemainder(dividingBy: 1))
            ZStack {
                // Overlapping segments that all end at the head: alpha accumulates towards
                // the head, giving a smooth fade without a gradient along the path.
                ForEach(0..<tailSteps, id: \.self) { i in
                    let length = tailLength * CGFloat(tailSteps - i) / CGFloat(tailSteps)
                    PerimeterSegment(cornerRadius: cornerRadius, inset: lineWidth / 2,
                                     start: wrapUnit(head - length), length: length)
                        .stroke(color.opacity(0.28), style: StrokeStyle(lineWidth: lineWidth, lineCap: .round))
                }
                PerimeterSegment(cornerRadius: cornerRadius, inset: lineWidth / 2,
                                 start: wrapUnit(head - 0.04), length: 0.04)
                    .stroke(highlight, style: StrokeStyle(lineWidth: lineWidth + 0.4, lineCap: .round))
                    .shadow(color: color, radius: 4)
            }
        }
        .allowsHitTesting(false)
    }

    private func wrapUnit(_ x: CGFloat) -> CGFloat {
        x - x.rounded(.down)
    }
}

/// Outline for a card / badge: faint idle stroke, amber comet while connecting,
/// slower green comet (or a static glow) once connected. Crossfades between states.
struct StatusBorder: View {
    var state: LinkState
    var cornerRadius: CGFloat
    var idleColor: Color = Color.white.opacity(0.08)
    var lineWidth: CGFloat = 1.6
    var tracesWhenConnected: Bool = true

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: cornerRadius)
                .strokeBorder(baseColor, lineWidth: 1)

            if state == .connecting {
                TracingBorder(cornerRadius: cornerRadius, color: VPNColors.amber, highlight: VPNColors.amberBright,
                              period: 1.8, lineWidth: lineWidth, tailLength: 0.35)
                    .transition(.opacity)
            } else if state == .connected && tracesWhenConnected {
                TracingBorder(cornerRadius: cornerRadius, color: VPNColors.green, highlight: VPNColors.greenBright,
                              period: 3.6, lineWidth: lineWidth, tailLength: 0.22)
                    .transition(.opacity)
            }
        }
        .animation(.easeInOut(duration: 0.35), value: state)
    }

    private var baseColor: Color {
        switch state {
        case .idle: return idleColor
        case .connecting: return VPNColors.amber.opacity(0.4)
        case .connected: return VPNColors.green.opacity(0.6)
        }
    }
}

/// Status dot with a soft expanding halo while `pulsing`.
struct StatusDot: View {
    var color: Color
    var size: CGFloat
    var pulsing: Bool = false
    var glowing: Bool = false

    var body: some View {
        ZStack {
            if pulsing {
                TimelineView(.animation) { timeline in
                    let p = CGFloat((timeline.date.timeIntervalSinceReferenceDate / 1.4).truncatingRemainder(dividingBy: 1))
                    Circle()
                        .fill(color.opacity(0.45 * Double(1 - p)))
                        .frame(width: size, height: size)
                        .scaleEffect(1 + 1.6 * p)
                }
                .transition(.opacity)
            }
            Circle()
                .fill(color)
                .frame(width: size, height: size)
                .shadow(color: color.opacity(pulsing || glowing ? 0.85 : 0), radius: 4)
        }
        .animation(.easeInOut(duration: 0.3), value: pulsing)
    }
}

/// Switch with a spring-driven knob and crossfading tint, replacing the stock
/// SwitchToggleStyle whose tint snapped when flipping between amber and green.
struct GlowSwitch: View {
    var isOn: Bool
    var tint: Color
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            ZStack {
                Capsule()
                    .fill(isOn ? tint : VPNColors.switchOff)
                    .shadow(color: isOn ? tint.opacity(0.5) : .clear, radius: 6)
                Circle()
                    .fill(Color.white)
                    .frame(width: 18, height: 18)
                    .shadow(color: Color.black.opacity(0.3), radius: 1.5, y: 0.5)
                    .offset(x: isOn ? 9 : -9)
            }
            .frame(width: 42, height: 24)
            .contentShape(Capsule())
            .animation(.spring(response: 0.3, dampingFraction: 0.75), value: isOn)
            .animation(.easeInOut(duration: 0.3), value: tint)
        }
        .buttonStyle(.plain)
    }
}

// MARK: - MacOSMenuBar Swift Component (Live SwiftUI View in macOS Status Bar)

struct MacOSMenuBar: View {
    @ObservedObject var vpn = VPNManager.shared

    var body: some View {
        let state = vpn.linkState
        HStack(spacing: 0) {
            ZStack {
                if state == .connected {
                    RoundedRectangle(cornerRadius: 6)
                        .fill(VPNColors.green.opacity(0.14))
                        .shadow(color: VPNColors.green.opacity(0.6), radius: 3)
                        .transition(.opacity)
                }

                // Connected stays static in the menu bar: an always-visible 60fps loop
                // would keep the app redrawing for as long as the tunnel is up.
                StatusBorder(state: state, cornerRadius: 6, idleColor: Color.white.opacity(0.15),
                             lineWidth: 1.5, tracesWhenConnected: false)

                Image(systemName: state == .connected ? "checkmark.shield.fill" : (state == .connecting ? "shield.lefthalf.filled" : "shield"))
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(
                        state == .connected ? VPNColors.green :
                        (state == .connecting ? VPNColors.amber : Color.white.opacity(0.85))
                    )

                if state != .idle {
                    Circle()
                        .fill(state == .connected ? VPNColors.green : VPNColors.amber)
                        .frame(width: 5, height: 5)
                        .offset(x: 6, y: -6)
                        .transition(.scale.combined(with: .opacity))
                }
            }
            .frame(width: 22, height: 22)
            .animation(.easeInOut(duration: 0.3), value: state)
        }
        .frame(width: 28, height: 22)
        .contentShape(Rectangle())
        .onTapGesture {
            AppDelegate.shared?.togglePopover(nil)
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
        let state = profile.linkState
        HStack(spacing: 12) {
            StatusDot(
                color: state == .connected ? VPNColors.green : (state == .connecting ? VPNColors.amber : Color.gray.opacity(0.6)),
                size: 9,
                pulsing: state == .connecting,
                glowing: state == .connected
            )

            // Profile Name & Subtitle
            VStack(alignment: .leading, spacing: 2) {
                Text(profile.name)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundColor(state == .connected ? .white : (state == .connecting ? Color(red: 1.0, green: 0.9, blue: 0.7) : Color(red: 0.85, green: 0.88, blue: 0.92)))
                    .lineLimit(1)
                    .truncationMode(.tail)

                ZStack(alignment: .leading) {
                    if state == .connecting {
                        Text("Đang kết nối đến máy chủ...")
                            .font(.system(size: 11, weight: .medium))
                            .foregroundColor(VPNColors.amber)
                            .lineLimit(1)
                            .transition(.opacity)
                    } else {
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
                        .transition(.opacity)
                    }
                }
            }

            Spacer(minLength: 8)

            GlowSwitch(
                isOn: state != .idle,
                tint: state == .connecting ? VPNColors.amber : VPNColors.green,
                action: { vpn.toggleConnect(profile: profile) }
            )

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
                        state == .connected ? Color(red: 0.04, green: 0.18, blue: 0.12) :
                        (state == .connecting ? Color(red: 0.2, green: 0.12, blue: 0.04) : Color(red: 0.1, green: 0.12, blue: 0.16).opacity(0.85))
                    )
                    .shadow(
                        color: state == .connected ? VPNColors.green.opacity(0.3) : (state == .connecting ? VPNColors.amber.opacity(0.3) : .clear),
                        radius: 10
                    )

                StatusBorder(state: state, cornerRadius: 13)
            }
        )
        .animation(.easeInOut(duration: 0.3), value: state)
    }
}

struct MenuBarPopupView: View {
    @ObservedObject var vpn = VPNManager.shared
    @State private var showingAddModal = false
    @State private var editingProfile: VPNProfileItem?

    var body: some View {
        let state = vpn.linkState
        VStack(spacing: 0) {
            // Header Bar (Clean, NO Settings Icon)
            HStack(spacing: 12) {
                ZStack {
                    RoundedRectangle(cornerRadius: 12)
                        .fill(
                            state == .connected ? Color(red: 0.04, green: 0.18, blue: 0.12) :
                            (state == .connecting ? Color(red: 0.2, green: 0.12, blue: 0.04) : Color(red: 0.05, green: 0.16, blue: 0.22))
                        )
                        .shadow(
                            color: state == .connected ? VPNColors.green.opacity(0.35) : (state == .connecting ? VPNColors.amber.opacity(0.35) : Color(red: 0.08, green: 0.72, blue: 0.82).opacity(0.25)),
                            radius: 8
                        )

                    StatusBorder(state: state, cornerRadius: 12,
                                 idleColor: Color(red: 0.12, green: 0.55, blue: 0.65).opacity(0.6), lineWidth: 2)

                    Image(systemName: state == .connected ? "checkmark.shield.fill" : (state == .connecting ? "shield.lefthalf.filled" : "shield.fill"))
                        .font(.system(size: 20))
                        .foregroundColor(
                            state == .connected ? VPNColors.green :
                            (state == .connecting ? VPNColors.amber : Color(red: 0.2, green: 0.75, blue: 0.95))
                        )

                    if state != .idle {
                        Circle()
                            .fill(state == .connected ? VPNColors.green : VPNColors.amber)
                            .frame(width: 7, height: 7)
                            .offset(x: 9, y: -9)
                            .transition(.scale.combined(with: .opacity))
                    }
                }
                .frame(width: 40, height: 40)

                HStack(spacing: 6) {
                    Text("TMS-VPN")
                        .font(.system(size: 16, weight: .bold))
                        .foregroundColor(.white)
                    StatusDot(
                        color: state == .connected ? VPNColors.green : (state == .connecting ? VPNColors.amber : Color.gray),
                        size: 7,
                        pulsing: state == .connecting,
                        glowing: state == .connected
                    )
                    ZStack(alignment: .leading) {
                        Text(state == .connected ? "Đã kết nối" : (state == .connecting ? "Đang kết nối..." : "Đã ngắt kết nối"))
                            .font(.system(size: 13, weight: .medium))
                            .foregroundColor(state == .connected ? VPNColors.green : (state == .connecting ? VPNColors.amber : Color.gray))
                            .id(state)
                            .transition(.opacity)
                    }
                }

                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.top, 16)
            .padding(.bottom, 12)

            Divider().background(Color.white.opacity(0.08))

            // Active Connection Info Banner when connected
            if state == .connected {
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("ĐANG HOẠT ĐỘNG")
                            .font(.system(size: 10, weight: .bold))
                            .foregroundColor(Color(red: 0.2, green: 0.85, blue: 0.55))
                        HStack(spacing: 8) {
                            if !vpn.currentIP.isEmpty {
                                Text("IP: \(vpn.currentIP)")
                                    .font(.system(size: 11, weight: .medium))
                                    .foregroundColor(.white)
                            }
                            if !vpn.currentTunDevice.isEmpty {
                                Text("(\(vpn.currentTunDevice))")
                                    .font(.system(size: 11))
                                    .foregroundColor(.gray)
                            }
                        }
                    }
                    Spacer()
                    Button(action: { vpn.disconnect() }) {
                        Text("Ngắt kết nối")
                            .font(.system(size: 11, weight: .medium))
                            .foregroundColor(Color.red.opacity(0.9))
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(
                                RoundedRectangle(cornerRadius: 6)
                                    .fill(Color.red.opacity(0.12))
                            )
                    }
                    .buttonStyle(.plain)
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 8)
                .background(Color(red: 0.04, green: 0.15, blue: 0.1))
                .transition(.opacity)

                Divider().background(Color.white.opacity(0.08))
            }

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

            // Error or Stale Session Notice / Alert Card
            if let alert = vpn.activeAlert ?? (vpn.errorMessage != nil ? VPNAlertInfo(kind: vpn.isStaleSession ? .sessionStale : .generic, title: vpn.isStaleSession ? "Session Stale" : "Lỗi kết nối", message: vpn.errorMessage ?? "", detail: "") : nil) {
                VStack(alignment: .leading, spacing: 8) {
                    // Header Badge & Title
                    HStack(spacing: 6) {
                        Image(systemName: alert.kind == .sessionStale ? "clock.arrow.circlepath" : (alert.kind == .authFailed ? "lock.slash.fill" : "exclamationmark.triangle.fill"))
                            .font(.system(size: 13, weight: .bold))
                            .foregroundColor(alert.kind == .sessionStale ? Color.orange : (alert.kind == .authFailed ? Color(red: 1.0, green: 0.35, blue: 0.35) : Color.yellow))

                        Text(alert.title)
                            .font(.system(size: 12, weight: .bold))
                            .foregroundColor(alert.kind == .sessionStale ? Color.orange : (alert.kind == .authFailed ? Color(red: 1.0, green: 0.45, blue: 0.45) : Color.yellow))

                        Spacer()

                        Text(alert.kind == .sessionStale ? "Phiên tồn đọng" : (alert.kind == .authFailed ? "Lỗi tài khoản" : "Cảnh báo"))
                            .font(.system(size: 9.5, weight: .bold))
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(
                                Capsule()
                                    .fill(alert.kind == .sessionStale ? Color.orange.opacity(0.2) : (alert.kind == .authFailed ? Color.red.opacity(0.2) : Color.yellow.opacity(0.2)))
                            )
                            .foregroundColor(alert.kind == .sessionStale ? Color.orange : (alert.kind == .authFailed ? Color(red: 1.0, green: 0.5, blue: 0.5) : Color.yellow))
                    }

                    // Message & Detail
                    VStack(alignment: .leading, spacing: 3) {
                        Text(alert.message)
                            .font(.system(size: 11.5, weight: .medium))
                            .foregroundColor(Color.white.opacity(0.92))
                            .fixedSize(horizontal: false, vertical: true)

                        if !alert.detail.isEmpty && alert.detail != alert.message {
                            Text(alert.detail)
                                .font(.system(size: 10, design: .monospaced))
                                .foregroundColor(Color.gray)
                                .lineLimit(2)
                        }

                        if alert.kind == .sessionStale && vpn.autoRetryCountdown > 0 {
                            Text("Đang dọn dẹp tiến trình cũ... Tự động thử lại sau \(vpn.autoRetryCountdown)s")
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundColor(Color(red: 0.3, green: 0.85, blue: 1.0))
                                .padding(.top, 2)
                        }
                    }

                    // Action Controls
                    if alert.kind == .sessionStale {
                        HStack(spacing: 8) {
                            Button(action: {
                                vpn.cleanupAndForceConnect(profileName: vpn.activeProfileName ?? "")
                            }) {
                                HStack(spacing: 4) {
                                    Image(systemName: "arrow.clockwise")
                                    Text("Dọn dẹp & Thử lại ngay")
                                }
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundColor(.white)
                                .padding(.horizontal, 10)
                                .padding(.vertical, 5)
                                .background(
                                    RoundedRectangle(cornerRadius: 6)
                                        .fill(Color.orange.opacity(0.9))
                                )
                            }
                            .buttonStyle(.plain)

                            Button(action: {
                                vpn.cancelAutoRetry()
                                vpn.disconnect()
                            }) {
                                Text("Hủy")
                                    .font(.system(size: 11))
                                    .foregroundColor(.gray)
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 5)
                            }
                            .buttonStyle(.plain)
                        }
                        .padding(.top, 2)
                    } else if alert.kind == .authFailed {
                        HStack(spacing: 8) {
                            if let actProf = vpn.profiles.first(where: { $0.name == vpn.activeProfileName }) {
                                Button(action: {
                                    editingProfile = actProf
                                }) {
                                    HStack(spacing: 4) {
                                        Image(systemName: "pencil")
                                        Text("Chỉnh sửa mật khẩu hồ sơ")
                                    }
                                    .font(.system(size: 11, weight: .semibold))
                                    .foregroundColor(.white)
                                    .padding(.horizontal, 10)
                                    .padding(.vertical, 5)
                                    .background(
                                        RoundedRectangle(cornerRadius: 6)
                                            .fill(Color(red: 0.8, green: 0.25, blue: 0.25))
                                    )
                                }
                                .buttonStyle(.plain)
                            }

                            Button(action: {
                                vpn.errorMessage = nil
                                vpn.activeAlert = nil
                            }) {
                                Text("Bỏ qua")
                                    .font(.system(size: 11))
                                    .foregroundColor(.gray)
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 5)
                            }
                            .buttonStyle(.plain)
                        }
                        .padding(.top, 2)
                    }
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
                .background(
                    RoundedRectangle(cornerRadius: 10)
                        .fill(alert.kind == .sessionStale ? Color(red: 0.22, green: 0.14, blue: 0.04) : (alert.kind == .authFailed ? Color(red: 0.24, green: 0.07, blue: 0.07) : Color(red: 0.18, green: 0.12, blue: 0.05)))
                        .overlay(
                            RoundedRectangle(cornerRadius: 10)
                                .stroke(alert.kind == .sessionStale ? Color.orange.opacity(0.4) : (alert.kind == .authFailed ? Color.red.opacity(0.4) : Color.yellow.opacity(0.3)), lineWidth: 1)
                        )
                )
                .padding(.horizontal, 16)
                .padding(.top, 8)
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
        .animation(.easeInOut(duration: 0.3), value: state)
        .animation(.easeInOut(duration: 0.25), value: vpn.activeAlert)
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
    var hostingView: NSHostingView<MacOSMenuBar>?

    func applicationDidFinishLaunching(_ notification: Notification) {
        AppDelegate.shared = self
        statusItem = NSStatusBar.system.statusItem(withLength: 28)
        
        // Host the live SwiftUI MacOSMenuBar component (with 2.0s linear rotation & pulse)
        let menuBarView = MacOSMenuBar()
        let hosting = NSHostingView(rootView: menuBarView)
        hosting.frame = NSRect(x: 0, y: 0, width: 28, height: 22)
        
        if let button = statusItem?.button {
            button.addSubview(hosting)
            hosting.autoresizingMask = [.width, .height]
            button.action = #selector(togglePopover(_:))
            button.target = self
        }
        self.hostingView = hosting

        popover.contentSize = NSSize(width: 370, height: 440)
        popover.behavior = .transient
        popover.contentViewController = NSHostingController(rootView: MenuBarPopupView())
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
