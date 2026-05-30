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
        MenuBarExtra("clipfan", systemImage: "doc.on.clipboard") {
            StatusMenuView()
                .environmentObject(daemon)
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
