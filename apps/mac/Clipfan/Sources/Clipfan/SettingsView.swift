import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var daemon: DaemonClient

    enum Tab: String, CaseIterable, Hashable {
        case peers = "Peers"
        case general = "General"
    }

    @State private var selection: Tab = .peers

    var body: some View {
        TabView(selection: $selection) {
            PeersTab()
                .tabItem { Label("Peers", systemImage: "network") }
                .tag(Tab.peers)
            GeneralTab()
                .tabItem { Label("General", systemImage: "gear") }
                .tag(Tab.general)
        }
        .padding()
    }
}

struct PeersTab: View {
    @EnvironmentObject var daemon: DaemonClient
    @State private var showAdd = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Connected peers")
                    .font(.headline)
                Spacer()
                Button("Add peer…") { showAdd = true }
                Button("Refresh") {
                    Task { await daemon.refresh() }
                }
            }
            if daemon.peers.isEmpty {
                Text("No peers configured yet.")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                Table(daemon.peers) {
                    TableColumn("") { peer in
                        Circle()
                            .fill(color(for: peer))
                            .frame(width: 10, height: 10)
                    }.width(20)
                    TableColumn("Host", value: \.hostname)
                    TableColumn("Port") { peer in Text("\(peer.port)") }.width(50)
                    TableColumn("Last push") { peer in Text(timeAgo(peer.last_push_ts)) }
                    TableColumn("Last recv") { peer in Text(timeAgo(peer.last_recv_ts)) }
                    TableColumn("Status") { peer in
                        Text(peer.last_push_ok ? "ok" : (peer.last_push_err ?? "—"))
                            .lineLimit(1)
                            .truncationMode(.tail)
                            .help(peer.last_push_err ?? "")
                    }
                }
                .frame(minHeight: 200)
            }
        }
        .sheet(isPresented: $showAdd) {
            AddPeerSheet()
        }
    }

    func color(for peer: Peer) -> Color {
        if peer.last_push_ok { return .green }
        if let ts = peer.last_push_ts, ts > Date.distantPast { return .red }
        return .gray
    }

    func timeAgo(_ date: Date?) -> String {
        guard let date, date > Date.distantPast else { return "—" }
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .short
        return f.localizedString(for: date, relativeTo: Date())
    }
}

struct GeneralTab: View {
    @EnvironmentObject var daemon: DaemonClient

    var body: some View {
        Form {
            LabeledContent("Origin", value: daemon.origin)
            LabeledContent("Daemon", value: daemon.connected ? "running" : "down")
            LabeledContent("Config") { Text(configPath).font(.system(.body, design: .monospaced)) }
            LabeledContent("Share dir") { Text(shareDirPath).font(.system(.body, design: .monospaced)) }

            Section("Actions") {
                HStack {
                    Button("Reveal config in Finder") {
                        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: configPath)])
                    }
                    Button("Open daemon log") {
                        NSWorkspace.shared.open(URL(fileURLWithPath: logPath))
                    }
                    Button("Restart daemon") {
                        daemon.restartDaemon()
                        Task { await daemon.refresh() }
                    }
                }
            }
        }
        .formStyle(.grouped)
    }

    var configPath: String {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/clipfan/config.json").path
    }
    var shareDirPath: String { Installer.shareDir.path }
    var logPath: String { "/tmp/clipfan-shell.log" }
}
