import SwiftUI

struct StatusMenuView: View {
    @EnvironmentObject var daemon: DaemonClient
    @Environment(\.openWindow) var openWindow

    var body: some View {
        if daemon.connected {
            Text("origin: \(daemon.origin)")
        } else {
            Text("(daemon not running)")
        }

        Divider()

        if daemon.peers.isEmpty {
            Text("no peers configured")
        } else {
            ForEach(daemon.peers) { peer in
                Text(label(for: peer))
            }
        }

        Divider()

        Button("Clipboard History…") {
            NSApp.activate(ignoringOtherApps: true)
            HistoryWindowController.shared.show()
        }
        .keyboardShortcut("h")
        Button("Add peer…") {
            NSApp.activate(ignoringOtherApps: true)
            openWindow(id: "settings")
        }
        Button("Settings…") {
            NSApp.activate(ignoringOtherApps: true)
            openWindow(id: "settings")
        }
        Button("Restart daemon") {
            daemon.restartDaemon()
            Task { await daemon.refresh() }
        }

        Divider()

        Button("Quit clipfan menubar") {
            NSApp.terminate(nil)
        }
    }

    func label(for peer: Peer) -> String {
        let indicator: String
        if peer.last_push_ok {
            indicator = "●"
        } else if let ts = peer.last_push_ts, ts > Date.distantPast {
            indicator = "✗"
        } else {
            indicator = "○"
        }
        var suffix = ""
        if let rx = peer.last_recv_ts, rx > Date.distantPast, Date().timeIntervalSince(rx) < 300 {
            suffix = "  (rx)"
        }
        return "\(indicator)  \(peer.hostname)\(suffix)"
    }
}
