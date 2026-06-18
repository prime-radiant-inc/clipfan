import KeyboardShortcuts
import ServiceManagement
import SwiftUI

/// Drives which Settings pane is shown so menu/AppKit call sites can open Settings
/// to a specific tab.
@MainActor
final class SettingsRoute: ObservableObject {
    static let shared = SettingsRoute()
    @Published var tab: SettingsView.Tab = .fleet
    init() {}
}

struct SettingsView: View {
    @EnvironmentObject var daemon: DaemonClient
    @ObservedObject private var route = SettingsRoute.shared

    enum Tab: String, CaseIterable, Hashable, Identifiable {
        case fleet = "Fleet"
        case general = "General"
        case diagnostics = "Diagnostics"
        case about = "About"
        var id: String { rawValue }

        var systemImage: String {
            switch self {
            case .fleet:       return "network"
            case .general:     return "gearshape"
            case .diagnostics: return "stethoscope"
            case .about:       return "info.circle"
            }
        }
    }

    var body: some View {
        NavigationSplitView {
            List(Tab.allCases, selection: $route.tab) { tab in
                Label(tab.rawValue, systemImage: tab.systemImage).tag(tab)
            }
            .navigationSplitViewColumnWidth(170)
        } detail: {
            switch route.tab {
            case .fleet:       FleetTab()
            case .general:     GeneralTab()
            case .diagnostics: DiagnosticsTab()
            case .about:       AboutView()
            }
        }
        .task { await daemon.refresh() }
    }
}

struct FleetTab: View {
    @EnvironmentObject var daemon: DaemonClient
    @State private var showAdd = false
    @State private var updatePeer: Peer?
    @State private var removePeer: Peer?
    @State private var removingHost: String?
    @State private var removeError: String?
    @State private var repairing = false
    @State private var repairResult: String?
    @State private var expandedMeshHost: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Fleet").font(.headline)
                if daemon.fleetMeshLoading {
                    ProgressView().controlSize(.small)
                }
                Spacer()
                if !daemon.peers.isEmpty {
                    Button(repairing ? "Repairing…" : "Repair mesh") {
                        repairMesh()
                    }
                    .disabled(repairing)
                }
                Button("Refresh") {
                    Task {
                        await daemon.refresh()
                        await daemon.refreshFleet()
                    }
                }
            }
            if let repairResult {
                Text(repairResult)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            if shouldPromptLocalNetwork(peers: daemon.peers) {
                LocalNetworkNudge()
            }
            if let safeMode = daemon.safeModeStatus, safeMode.active {
                SafeModeWarningPanel(
                    status: safeMode,
                    repairMessage: daemon.safeModeRepairMessage,
                    repairAction: {
                        Task { await daemon.moveDaemonListenerToLoopback() }
                    }
                )
            }
            ScrollView {
                VStack(spacing: 10) {
                    ForEach(fleetRows(origin: daemon.origin,
                                      connected: daemon.connected,
                                      peers: daemon.peers,
                                      safeMode: daemon.safeModeStatus)) { row in
                        VStack(alignment: .leading, spacing: 8) {
                            HStack(spacing: 8) {
                                FleetRow(model: row)
                                if let peer = row.peer {
                                    Button {
                                        updatePeer = peer
                                    } label: {
                                        Label("Update peer", systemImage: "arrow.triangle.2.circlepath")
                                            .labelStyle(.iconOnly)
                                    }
                                    .buttonStyle(.borderless)
                                    .help("Update clipfan on \(peer.hostname)")

                                    Button(role: .destructive) {
                                        removePeer = peer
                                    } label: {
                                        Label("Remove host", systemImage: "trash")
                                            .labelStyle(.iconOnly)
                                    }
                                    .buttonStyle(.borderless)
                                    .disabled(removingHost == peer.hostname)
                                    .help("Remove \(peer.hostname) from this Mac")
                                }
                            }
                            if let meshHost = meshHostRow(for: row.id, in: daemon.fleetMesh),
                               !meshHost.edges.isEmpty {
                                FleetRowMeshSection(
                                    host: meshHost,
                                    expanded: expandedMeshHost == row.id,
                                    toggle: {
                                        expandedMeshHost = expandedMeshHost == row.id ? nil : row.id
                                    }
                                )
                            }
                        }
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
        .padding(20)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .sheet(isPresented: $showAdd) { AddPeerSheet() }
        .sheet(item: $updatePeer) { peer in
            UpdatePeerSheet(peer: peer)
        }
        .confirmationDialog(
            removePeer.map { "Remove \($0.hostname)?" } ?? "Remove host?",
            isPresented: Binding(
                get: { removePeer != nil },
                set: { if !$0 { removePeer = nil } }
            ),
            presenting: removePeer
        ) { peer in
            Button("Remove Host", role: .destructive) {
                Task { await remove(peer) }
            }
            Button("Cancel", role: .cancel) {}
        } message: { peer in
            Text("Removes \(peer.hostname) from this Mac and restarts the daemon.")
        }
        .alert(
            "Remove host failed",
            isPresented: Binding(
                get: { removeError != nil },
                set: { if !$0 { removeError = nil } }
            )
        ) {
            Button("OK", role: .cancel) { removeError = nil }
        } message: {
            Text(removeError ?? "")
        }
        .alert(
            "Host removed",
            isPresented: Binding(
                get: { daemon.hostRemoveWarning != nil },
                set: { if !$0 { daemon.hostRemoveWarning = nil } }
            )
        ) {
            Button("OK", role: .cancel) { daemon.hostRemoveWarning = nil }
        } message: {
            Text(daemon.hostRemoveWarning ?? "")
        }
        .task { await daemon.refreshFleet() }
    }

    private func remove(_ peer: Peer) async {
        removePeer = nil
        removingHost = peer.hostname
        defer { removingHost = nil }
        do {
            _ = try await daemon.removeHost(hostID: peer.hostname)
        } catch {
            removeError = String(describing: error)
        }
    }

    private func repairMesh() {
        repairing = true
        repairResult = nil
        Task {
            defer { repairing = false }
            do {
                let report = try await MeshHealClient.heal(regularKnownHosts: "~/.ssh/known_hosts")
                repairResult = report.summary
                await daemon.refreshFleet()
            } catch {
                repairResult = "Repair failed: \(error.localizedDescription)"
            }
        }
    }
}

struct SafeModeWarningPanel: View {
    let status: LocalDaemonSafeModeStatus
    let repairMessage: String?
    let repairAction: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "exclamationmark.shield")
                .font(.system(size: 16))
                .foregroundStyle(.orange)
            VStack(alignment: .leading, spacing: 6) {
                Text("Daemon listener is in safe mode")
                    .font(.system(size: 12.5, weight: .semibold))
                Text(detail)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                Button("Move daemon listener to loopback") {
                    repairAction()
                }
                .disabled(!status.repairable)
                .font(.system(size: 11))
                .padding(.top, 2)
                if let repairMessage, !repairMessage.isEmpty {
                    Text(repairMessage)
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(Color.orange.opacity(0.08))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.orange.opacity(0.25)))
        .clipShape(RoundedRectangle(cornerRadius: 10))
    }

    private var detail: String {
        let listen = status.listen ?? "unknown listener"
        let repair = status.effectiveRepairListen ?? "loopback"
        return "The daemon reported \(listen). Repair will set the local listener to \(repair)."
    }
}


enum GeneralSettingsAction {
    case checkForUpdates

    var title: String {
        switch self {
        case .checkForUpdates: return "Check for Updates…"
        }
    }

    var systemImage: String {
        switch self {
        case .checkForUpdates: return "arrow.triangle.2.circlepath"
        }
    }
}

struct GeneralTab: View {
    @EnvironmentObject var daemon: DaemonClient
    @StateObject private var loginItem = LoginItemManager.shared
    @State private var historyLimit: Int = 200

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
                LabeledContent("History limit") {
                    HStack(spacing: 4) {
                        TextField("", value: $historyLimit, format: .number)
                            .labelsHidden()
                            .multilineTextAlignment(.trailing)
                            .frame(width: 64)
                        Stepper("", value: $historyLimit, in: 50...5000, step: 50)
                            .labelsHidden()
                    }
                }
                .onChange(of: historyLimit) { n in
                    Task { await daemon.setMaxHistory(n) }
                }
                KeyboardShortcuts.Recorder("Global shortcut", name: .toggleClipboard)
            }

            Section("Updates") {
                Button {
                    Updater.shared.checkForUpdates()
                } label: {
                    Label(GeneralSettingsAction.checkForUpdates.title,
                          systemImage: GeneralSettingsAction.checkForUpdates.systemImage)
                }
            }
        }
        .formStyle(.grouped)
        .onAppear { historyLimit = daemon.maxHistory }
        .onChange(of: daemon.maxHistory) { m in
            if m != historyLimit { historyLimit = m }
        }
    }
}

/// Shown in the Fleet tab when peer sends are failing — the usual cause on
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
    @EnvironmentObject var daemon: DaemonClient

    var body: some View {
        Form {
            Section {
                HStack(spacing: 12) {
                    HealthDot(health: daemon.connected ? .healthy : .down, size: 11)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(daemon.connected ? "Daemon running" : "Daemon not running")
                            .font(.system(size: 13, weight: .semibold))
                        Text("this Mac · \(daemon.origin) · \(daemonVersion)")
                            .font(.system(size: 11, design: .monospaced))
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Restart") {
                        daemon.restartDaemon()
                        Task { await daemon.refresh() }
                    }
                }
                .padding(.vertical, 4)
            }

            Section("Setup") {
                Button("Re-run setup…") {
                    WelcomeWindowController.shared.show(startInstall: true)
                }
                Text("Reinstalls and restarts the background service.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            if let safeMode = daemon.safeModeStatus, safeMode.active {
                Section("Safe Mode") {
                    LabeledContent("Reason") { Text(safeMode.reason ?? "—") }
                    LabeledContent("Listener") { Text(safeMode.listen ?? "—").font(.system(.caption, design: .monospaced)) }
                    LabeledContent("Repair listener") {
                        Text(safeMode.effectiveRepairListen ?? "—")
                            .font(.system(.caption, design: .monospaced))
                    }
                    if let state = safeMode.expectedRevisionState,
                       let revision = safeMode.expectedRevision {
                        LabeledContent("Expected revision") {
                            Text("\(state) \(revision)")
                                .font(.system(.caption, design: .monospaced))
                        }
                    }
                    Button("Move daemon listener to loopback") {
                        Task { await daemon.moveDaemonListenerToLoopback() }
                    }
                    .disabled(!safeMode.repairable || daemon.listenerRepairInProgress)
                    if !daemon.safeModeLog.isEmpty {
                        Button("Copy Log") {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(daemon.safeModeLog, forType: .string)
                        }
                        TextEditor(text: .constant(daemon.safeModeLog))
                            .font(.system(.caption, design: .monospaced))
                            .frame(minHeight: 120)
                    }
                }
            }

            Section("Developer") {
                LabeledContent("Config") { Text(configPath).font(.system(.caption, design: .monospaced)) }
                LabeledContent("Share dir") { Text(shareDirPath).font(.system(.caption, design: .monospaced)) }
                Button("Reveal config in Finder") {
                    NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: configPath)])
                }
                Button("Open daemon log") {
                    NSWorkspace.shared.open(URL(fileURLWithPath: logPath))
                }
            }
        }
        .formStyle(.grouped)
    }

    /// Daemon-reported version, falling back to the app bundle version.
    var daemonVersion: String {
        if let v = daemon.version, !v.isEmpty { return v }
        return Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "—"
    }

    var configPath: String {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/clipfan/config.json").path
    }
    var shareDirPath: String { Installer.shareDir.path }
    var logPath: String { "/tmp/clipfan-shell.log" }
}
