import SwiftUI

struct StatusMenuView: View {
    @EnvironmentObject var daemon: DaemonClient
    @Environment(\.openWindow) var openWindow

    var body: some View {
        Text(daemon.connected ? "clipfan · \(daemon.origin)" : "clipfan · daemon not running")

        Divider()

        Button("Open Clipboard") {
            CommandPanelController.shared.show()
        }
        .keyboardShortcut("v", modifiers: [.command, .shift])

        Divider()

        if daemon.peers.isEmpty {
            Text("No peers yet — add one in Settings")
        } else {
            Section("Fleet") {
                ForEach(daemon.peers) { peer in
                    Button {
                        NSApp.activate(ignoringOtherApps: true)
                        openWindow(id: "settings")
                    } label: {
                        Label("\(peer.hostname) — \(lastSync(peer))", systemImage: dotSymbol(peer))
                    }
                }
            }
        }

        Divider()

        Button("Settings…") {
            NSApp.activate(ignoringOtherApps: true)
            openWindow(id: "settings")
        }
        .keyboardShortcut(",", modifiers: .command)

        Button("Quit") {
            NSApp.terminate(nil)
        }
        .keyboardShortcut("q", modifiers: .command)
    }

    /// Health dot as an SF Symbol name. Green when the last push succeeded,
    /// amber when it failed/stale, hollow when never contacted.
    private func dotSymbol(_ peer: Peer) -> String {
        if peer.last_push_ok { return "circle.fill" }
        if let ts = peer.last_push_ts, ts > Date.distantPast { return "exclamationmark.circle.fill" }
        return "circle"
    }

    /// Human last-sync summary for the menu row.
    private func lastSync(_ peer: Peer) -> String {
        if !peer.last_push_ok, let ts = peer.last_push_ts, ts > Date.distantPast {
            return "offline"
        }
        let latest = [peer.last_push_ts, peer.last_recv_ts]
            .compactMap { $0 }
            .filter { $0 > Date.distantPast }
            .max()
        guard let latest else { return "never" }
        return relativeShort(latest)
    }

    private func relativeShort(_ date: Date) -> String {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .short
        return f.localizedString(for: date, relativeTo: Date())
    }
}
