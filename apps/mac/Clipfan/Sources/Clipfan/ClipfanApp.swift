import SwiftUI
import AppKit

@main
struct ClipfanApp: App {
    @StateObject private var daemon = DaemonClient.shared
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    // Held for the app's lifetime so the global hotkey stays registered.
    private let historyHotkey: GlobalHotkey

    init() {
        DaemonClient.shared.start()

        historyHotkey = GlobalHotkey {
            CommandPanelController.shared.toggle()
        }
    }

    var body: some Scene {
        MenuBarExtra {
            StatusMenuView()
                .environmentObject(daemon)
        } label: {
            MenuBarLabel()
        }
        .menuBarExtraStyle(.window)

        Window("clipfan", id: "settings") {
            SettingsView()
                .environmentObject(daemon)
                .frame(minWidth: 720, minHeight: 480)
        }
        .windowResizability(.contentMinSize)
    }
}

/// The always-present menubar icon. Rendered at launch, so it's the reliable
/// place to capture the scene's `openWindow` action for AppKit call sites.
private struct MenuBarLabel: View {
    @Environment(\.openWindow) private var openWindow
    var body: some View {
        Image(systemName: "doc.on.clipboard")
            .task { WindowOpener.shared.openWindow = openWindow }
    }
}

/// Decides on launch whether the daemon is healthy, needs a kickstart, or the app
/// is being run for the first time and must install + onboard.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        Task { @MainActor in
            await DaemonClient.shared.refresh()
            switch LaunchDecision.decide(binaryInstalled: Bootstrap.binaryInstalled,
                                         daemonHealthy: DaemonClient.shared.connected) {
            case .normal:
                break
            case .restartExisting:
                await DaemonClient.shared.ensureDaemonRunning()
            case .firstRunInstall:
                WelcomeWindowController.shared.show(startInstall: true)
            }
        }
    }
}
