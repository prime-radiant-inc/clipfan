import SwiftUI
import AppKit

@main
struct ClipfanApp: App {
    @StateObject private var daemon = DaemonClient.shared

    // Held for the app's lifetime so the global hotkey stays registered.
    // ⇧⌘V may require Accessibility/Input Monitoring permission on first use.
    private let historyHotkey: GlobalHotkey

    init() {
        DaemonClient.shared.start()
        Task { await DaemonClient.shared.ensureDaemonRunning() }

        historyHotkey = GlobalHotkey {
            NSApp.activate(ignoringOtherApps: true)
            HistoryWindowController.shared.show()
        }
    }

    var body: some Scene {
        MenuBarExtra("clipfan", systemImage: "doc.on.clipboard") {
            StatusMenuView()
                .environmentObject(daemon)
        }
        .menuBarExtraStyle(.menu)

        Window("clipfan", id: "settings") {
            SettingsView()
                .environmentObject(daemon)
                .frame(minWidth: 720, minHeight: 480)
        }
        .windowResizability(.contentMinSize)
    }
}
