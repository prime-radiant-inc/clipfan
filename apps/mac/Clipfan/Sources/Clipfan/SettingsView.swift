import ServiceManagement
import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var daemon: DaemonClient

    enum Tab: String, CaseIterable, Hashable {
        case fleet = "Fleet"
        case general = "General"
    }

    @State private var selection: Tab = .fleet

    var body: some View {
        TabView(selection: $selection) {
            FleetTab()
                .tabItem { Label("Fleet", systemImage: "network") }
                .tag(Tab.fleet)
            GeneralTab()
                .tabItem { Label("General", systemImage: "gear") }
                .tag(Tab.general)
        }
        .padding()
    }
}

struct FleetTab: View {
    @EnvironmentObject var daemon: DaemonClient
    @State private var showAdd = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Fleet").font(.headline)
                Spacer()
                Button("Refresh") { Task { await daemon.refresh() } }
            }
            if daemon.peers.isEmpty {
                VStack(spacing: 6) {
                    Image(systemName: "network.slash").font(.system(size: 28)).foregroundStyle(.tertiary)
                    Text("No peers yet").foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    VStack(spacing: 10) {
                        ForEach(daemon.peers) { peer in PeerCard(peer: peer) }
                    }
                }
            }
            Button {
                showAdd = true
            } label: {
                Label("Add peer…", systemImage: "plus")
            }
            .buttonStyle(.borderedProminent)
        }
        .sheet(isPresented: $showAdd) { AddPeerSheet() }
    }
}

struct PeerCard: View {
    let peer: Peer

    var body: some View {
        HStack(spacing: 13) {
            Circle().fill(dotColor).frame(width: 9, height: 9)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(peer.hostname).font(.system(size: 13.5, weight: .semibold))
                }
                Text(stateLine).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 3) {
                Text("↑ \(peerTimeAgo(peer.last_push_ts))   ↓ \(peerTimeAgo(peer.last_recv_ts))")
                    .font(.system(size: 10)).foregroundStyle(.secondary)
                Text(peer.healthWord).font(.system(size: 10)).foregroundStyle(peer.healthColor)
            }
        }
        .padding(12)
        .background(Color.secondary.opacity(0.06))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.secondary.opacity(0.12)))
        .clipShape(RoundedRectangle(cornerRadius: 10))
    }

    private var dotColor: Color { peer.healthColor }

    private var stateLine: String {
        if peer.last_push_ok { return "port \(peer.port) · synced" }
        if let err = peer.last_push_err, !err.isEmpty { return "last error: \(err)" }
        return "port \(peer.port)"
    }
}

struct GeneralTab: View {
    @EnvironmentObject var daemon: DaemonClient
    @StateObject private var loginItem = LoginItemManager.shared
    @State private var showDeveloper = false

    var body: some View {
        Form {
            Section("Startup") {
                Toggle(isOn: Binding(get: { loginItem.isEnabled },
                                     set: { loginItem.setEnabled($0) })) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Launch clipfan at login")
                        Text("Start syncing automatically").font(.caption).foregroundStyle(.secondary)
                    }
                }
                if let err = loginItem.lastError {
                    Text(err).font(.caption).foregroundStyle(.red)
                }
                if loginItem.status == .requiresApproval {
                    Button("Open Login Items settings…") {
                        SMAppService.openSystemSettingsLoginItems()
                    }
                }
            }

            Section("Clipboard") {
                LabeledContent("History limit", value: "200 items")
                LabeledContent("Global shortcut", value: "⇧⌘V")
            }

            Section("Status") {
                LabeledContent("Daemon", value: daemon.connected ? "running" : "down")
            }

            Section {
                DisclosureGroup("Developer", isExpanded: $showDeveloper) {
                    LabeledContent("Config") { Text(configPath).font(.system(.caption, design: .monospaced)) }
                    LabeledContent("Share dir") { Text(shareDirPath).font(.system(.caption, design: .monospaced)) }
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
