import Foundation
import ServiceManagement
import SwiftUI

/// Manages "launch Clipfan at login" via SMAppService.mainApp (macOS 13+).
///
/// We register only the GUI app itself, NOT the Go daemon. SMAppService.agent /
/// .daemon would register the daemon as a launchd job — but launchd-spawned
/// daemons hit Sequoia's Local Network privacy gate (silent EHOSTUNREACH to
/// RFC1918 peers), which is the exact problem this work exists to avoid. The
/// reliable path is: the GUI app holds the Local Network grant, launches at
/// login via SMAppService.mainApp, and shell-launches the daemon as a child so
/// the daemon inherits the app's grant (see DaemonClient.ensureDaemonRunning).
@MainActor
final class LoginItemManager: ObservableObject {
    static let shared = LoginItemManager()

    @Published private(set) var status: SMAppService.Status = SMAppService.mainApp.status
    @Published var lastError: String?

    private init() {}

    var isEnabled: Bool { status == .enabled }

    func refresh() {
        status = SMAppService.mainApp.status
    }

    func setEnabled(_ enabled: Bool) {
        lastError = nil
        do {
            if enabled {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
        } catch {
            lastError = error.localizedDescription
        }
        refresh()
    }

    var statusText: String {
        switch status {
        case .enabled: return "enabled"
        case .requiresApproval: return "needs approval in System Settings"
        case .notRegistered: return "off"
        case .notFound: return "not found"
        @unknown default: return "unknown"
        }
    }
}
