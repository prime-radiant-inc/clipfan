import ServiceManagement
import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var daemon: DaemonClient

    enum Tab: String, CaseIterable, Hashable, Identifiable {
        case fleet = "Fleet"
        case general = "General"
        case diagnostics = "Diagnostics"
        var id: String { rawValue }

        var systemImage: String {
            switch self {
            case .fleet:       return "network"
            case .general:     return "gearshape"
            case .diagnostics: return "stethoscope"
            }
        }
    }

    @State private var selection: Tab = .fleet

    var body: some View {
        NavigationSplitView {
            List(Tab.allCases, selection: $selection) { tab in
                Label(tab.rawValue, systemImage: tab.systemImage).tag(tab)
            }
            .navigationSplitViewColumnWidth(170)
        } detail: {
            switch selection {
            case .fleet:       FleetTab()
            case .general:     GeneralTab()
            case .diagnostics: DiagnosticsTab()
            }
        }
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
            if shouldPromptLocalNetwork(peers: daemon.peers) {
                LocalNetworkNudge()
            }
            ScrollView {
                VStack(spacing: 10) {
                    ForEach(fleetRows(origin: daemon.origin,
                                      connected: daemon.connected,
                                      peers: daemon.peers)) { row in
                        FleetRow(model: row)
                            .padding(12)
                            .background(Color.secondary.opacity(0.06))
                            .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.secondary.opacity(0.12)))
                            .clipShape(RoundedRectangle(cornerRadius: 10))
                    }
                    if daemon.peers.isEmpty {
                        Text("No peers yet — add one in Settings")
                            .font(.system(size: 11)).foregroundStyle(.secondary)
                            .frame(maxWidth: .infinity)
                            .padding(.top, 4)
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
                Button("Re-run setup…") {
                    WelcomeWindowController.shared.show(startInstall: true)
                }
                Text("Reinstalls and restarts the background service.")
                    .font(.caption).foregroundStyle(.secondary)
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

/// Shown in the Fleet tab when a peer's pushes are failing — the usual cause on
/// macOS Sequoia is the daemon lacking Local Network permission. Heuristic (no
/// public API reports the grant), so it's worded as a likely cause, not a verdict.
struct LocalNetworkNudge: View {
    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "wifi.exclamationmark")
                .font(.system(size: 16))
                .foregroundStyle(.orange)
            VStack(alignment: .leading, spacing: 4) {
                Text("Sync may be blocked").font(.system(size: 12.5, weight: .semibold))
                Text("A peer isn't reachable. macOS needs **Local Network** permission for clipfan to sync over your LAN.")
                    .font(.system(size: 11)).foregroundStyle(.secondary)
                Button("Open Local Network settings…") { openLocalNetworkSettings() }
                    .font(.system(size: 11))
                    .padding(.top, 2)
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(Color.orange.opacity(0.08))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.orange.opacity(0.25)))
        .clipShape(RoundedRectangle(cornerRadius: 10))
    }
}

/// Open System Settings to the Local Network privacy pane, falling back to the
/// general Privacy & Security pane if the deep link can't be resolved.
func openLocalNetworkSettings() {
    let local = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork")!
    let privacy = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy")!
    if !NSWorkspace.shared.open(local) {
        NSWorkspace.shared.open(privacy)
    }
}

struct DiagnosticsTab: View {
    var body: some View { Text("Diagnostics").padding() }
}
