import SwiftUI

@main
struct ClipfanApp: App {
    @StateObject private var daemon = DaemonClient.shared

    init() {
        DaemonClient.shared.start()
        Task { await DaemonClient.shared.ensureDaemonRunning() }
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
