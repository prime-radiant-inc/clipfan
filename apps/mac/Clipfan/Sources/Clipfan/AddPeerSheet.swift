import AppKit
import SwiftUI

func isAddPeerInstallDisabled(installCount: Int,
                              installing: Bool,
                              policy: SSHTransportGatePolicy = .current,
                              privateDirectMeshRequested: Bool = false) -> Bool {
    if installing || installCount == 0 { return true }
    if privateDirectMeshRequested {
        return !policy.privateDirectMeshProvisioningEnabled || installCount < 2
    }
    return !policy.addPeerProvisioningEnabled
}

func addPeerInstallButtonTitle(installing: Bool, installCount: Int, failure: AddPeerOperationFailure?) -> String {
    if installing { return "Installing…" }
    if failure != nil { return "Retry" }
    return installCount <= 1 ? "Install" : "Install on \(installCount) hosts"
}

enum AddPeerSheetButtons: Equatable {
    case editing      // Cancel + Install
    case installing   // Cancel(disabled) + Installing…
    case success      // Done + Add another
    case failed       // Cancel + Retry
}

/// Which buttons the sheet shows. Success takes precedence so a retry that
/// finally succeeds shows Done, not Retry.
func addPeerSheetButtons(installing: Bool, installSuccess: Bool, hasFailure: Bool) -> AddPeerSheetButtons {
    if installSuccess { return .success }
    if installing { return .installing }
    if hasFailure { return .failed }
    return .editing
}

enum AddPeerHostPlatform: String, CaseIterable, Identifiable {
    case linux = "Linux"
    case macOS = "macOS"

    var id: String { rawValue }

    static func fromTailnetOS(_ value: String) -> AddPeerHostPlatform {
        value.lowercased().contains("darwin") || value.lowercased().contains("mac") ? .macOS : .linux
    }

    func homeDirectory(for user: String) -> String {
        switch self {
        case .linux: return "/home/\(user)"
        case .macOS: return "/Users/\(user)"
        }
    }
}

struct AddPeerRemoteHostDraft: Identifiable, Equatable {
    let id = UUID()
    var sshHost: String = ""
    var hostID: String = ""
    var user: String = NSUserName()
    var port: Int = 22
    var platform: AddPeerHostPlatform = .linux
}

func addPeerDerivedHostID(from host: String) -> String {
    let trimmed = host.trimmingCharacters(in: .whitespacesAndNewlines)
        .trimmingCharacters(in: CharacterSet(charactersIn: "."))
    if addPeerLooksLikeIPv4Address(trimmed) {
        return trimmed.replacingOccurrences(of: ".", with: "-")
    }
    let short = trimmed.split(separator: ".", maxSplits: 1).first.map(String.init) ?? trimmed
    let allowed = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-")
    let scalars = short.unicodeScalars.map { allowed.contains($0) ? Character($0) : "-" }
    let normalized = String(scalars).trimmingCharacters(in: CharacterSet(charactersIn: "-"))
    return normalized.isEmpty ? trimmed : normalized
}

func addPeerLooksLikeIPv4Address(_ host: String) -> Bool {
    let parts = host.split(separator: ".", omittingEmptySubsequences: false)
    guard parts.count == 4 else { return false }
    return parts.allSatisfy { part in
        guard !part.isEmpty, part.allSatisfy(\.isNumber),
              let value = Int(part) else { return false }
        return (0...255).contains(value)
    }
}

func addPeerPreferredTailnetSSHHost(_ peer: TailscalePeer) -> String {
    let dnsName = peer.dnsName.trimmingCharacters(in: CharacterSet(charactersIn: "."))
        .trimmingCharacters(in: .whitespacesAndNewlines)
    if !dnsName.isEmpty {
        return dnsName
    }
    if AddPeerHostPlatform.fromTailnetOS(peer.os) == .macOS {
        let localName = addPeerLocalSSHHostCandidate(peer.hostName)
        if !localName.isEmpty {
            return localName
        }
    }
    let ip = peer.ip.trimmingCharacters(in: .whitespacesAndNewlines)
    if !ip.isEmpty {
        return ip
    }
    return peer.hostName.trimmingCharacters(in: .whitespacesAndNewlines)
}

func addPeerPreferredLocalTailnetSSHHost(_ peer: TailscalePeer) -> String {
    let dnsName = peer.dnsName.trimmingCharacters(in: CharacterSet(charactersIn: "."))
        .trimmingCharacters(in: .whitespacesAndNewlines)
    if !dnsName.isEmpty {
        return dnsName
    }
    return peer.ip.trimmingCharacters(in: .whitespacesAndNewlines)
}

func addPeerDefaultLocalSSHHost(systemHostName: String = addPeerLiveSystemLocalHostName(),
                                tailnetSSHHost: String) -> String {
    let localName = addPeerLocalSSHHostCandidate(systemHostName)
    if !localName.isEmpty {
        return localName
    }
    return tailnetSSHHost.trimmingCharacters(in: .whitespacesAndNewlines)
}

func addPeerEffectiveLocalSSHHost(override: String,
                                  systemHostName: String = addPeerLiveSystemLocalHostName(),
                                  tailnetSSHHost: String) -> String {
    let trimmedOverride = override.trimmingCharacters(in: .whitespacesAndNewlines)
    if !trimmedOverride.isEmpty {
        return trimmedOverride
    }
    return addPeerDefaultLocalSSHHost(systemHostName: systemHostName,
                                      tailnetSSHHost: tailnetSSHHost)
}

func addPeerSystemLocalHostName(scutilLocalHostName: String?, processHostName: String) -> String {
    let localHostName = (scutilLocalHostName ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    if !localHostName.isEmpty {
        return localHostName
    }
    return processHostName.trimmingCharacters(in: .whitespacesAndNewlines)
}

func addPeerLiveSystemLocalHostName() -> String {
    addPeerSystemLocalHostName(scutilLocalHostName: addPeerReadScutilLocalHostName(),
                               processHostName: ProcessInfo.processInfo.hostName)
}

private func addPeerReadScutilLocalHostName() -> String? {
    let proc = Process()
    proc.executableURL = URL(fileURLWithPath: "/usr/sbin/scutil")
    proc.arguments = ["--get", "LocalHostName"]
    let out = Pipe()
    proc.standardOutput = out
    do {
        try proc.run()
        proc.waitUntilExit()
    } catch {
        return nil
    }
    guard proc.terminationStatus == 0 else { return nil }
    let data = out.fileHandleForReading.readDataToEndOfFile()
    return String(decoding: data, as: UTF8.self)
}

func addPeerLocalSSHHostCandidate(_ hostName: String) -> String {
    let localName = hostName
        .trimmingCharacters(in: .whitespacesAndNewlines)
        .trimmingCharacters(in: CharacterSet(charactersIn: "."))
    if localName.isEmpty || localName.lowercased() == "localhost" {
        return ""
    }
    if localName.contains(".") {
        return localName
    }
    return "\(localName).local"
}

func addPeerDirectMeshSpec(hostID: String,
                           sshHost: String,
                           user: String,
                           port: Int,
                           installPath: String,
                           configPath: String,
                           knownHostsPath: String,
                           syncKeyPath: String,
                           callbackHostSource: String? = nil) -> String {
    var fields = [
        "id=\(hostID)",
        "ssh=\(sshHost)",
        "user=\(user)",
        "port=\(port)",
        "install=\(installPath)",
        "config=\(configPath)",
        "known_hosts=\(knownHostsPath)",
        "sync_key=\(syncKeyPath)"
    ]
    let callbackSource = (callbackHostSource ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    if !callbackSource.isEmpty {
        fields.append("callback_host=\(callbackSource)")
    }
    return fields.joined(separator: ",")
}

func addPeerRemoteDirectMeshSpec(_ draft: AddPeerRemoteHostDraft) -> String? {
    let sshHost = draft.sshHost.trimmingCharacters(in: .whitespacesAndNewlines)
    let user = draft.user.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !sshHost.isEmpty, !user.isEmpty else { return nil }
    let hostID = draft.hostID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        ? addPeerDerivedHostID(from: sshHost)
        : draft.hostID.trimmingCharacters(in: .whitespacesAndNewlines)
    let home = draft.platform.homeDirectory(for: user)
    return addPeerDirectMeshSpec(
        hostID: hostID,
        sshHost: sshHost,
        user: user,
        port: draft.port,
        installPath: "\(home)/.local/bin/clipfan",
        configPath: "\(home)/.config/clipfan/config.json",
        knownHostsPath: "\(home)/.config/clipfan/ssh/known_hosts",
        syncKeyPath: "\(home)/.config/clipfan/ssh/sync_ed25519"
    )
}

func addPeerPrivateDirectMeshRemoteDraftsForInstall(manualDrafts: [AddPeerRemoteHostDraft],
                                                    selectedTailnetDrafts: [AddPeerRemoteHostDraft]) -> [AddPeerRemoteHostDraft] {
    if let selected = selectedTailnetDrafts.first {
        return [selected]
    }
    guard let manual = manualDrafts.first(where: {
        !$0.sshHost.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }) else {
        return []
    }
    return [manual]
}

struct AddPeerSheet: View {
    @Environment(\.dismiss) private var dismiss
    @EnvironmentObject private var daemon: DaemonClient

    @State private var sshKey: String = ""
    @State private var withTmux = false
    @State private var remoteDrafts: [AddPeerRemoteHostDraft] = [AddPeerRemoteHostDraft()]
    @State private var localTailnetSSHHost: String = ""
    @State private var localSystemHostName: String = addPeerLiveSystemLocalHostName()
    @State private var localSSHHostOverride: String = ""
    @State private var localSSHUser: String = NSUserName()
    @State private var localSSHPort: Int = 22
    @State private var directMeshRegularKnownHosts: String = "~/.ssh/known_hosts"
    @State private var showingAdvancedSSH = false

    @State private var tailnet: [TailscalePeer] = []
    @State private var tailnetSelected: Set<String> = []
    @State private var tailnetAvailable = false

    @State private var installing = false
    @State private var installSuccess = false
    @State private var lastInstalledHost = ""
    @State private var progress: String = ""
    @State private var log: AddPeerOperationLog?
    @State private var failure: AddPeerOperationFailure?
    @State private var confirmingDirectMeshKeyscanTrust = false

    private var installCount: Int {
        if SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled {
            return directMeshHostSpecLines.count
        }
        return remoteHostDraftsForInstall.count
    }

    private var directMeshHostSpecLines: [String] {
        guard SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled,
              !remoteHostDraftsForInstall.isEmpty,
              let localSpec = localDirectMeshSpec else {
            return []
        }
        let remoteSpecs = remoteHostDraftsForInstall.compactMap(addPeerRemoteDirectMeshSpec)
        guard !remoteSpecs.isEmpty else { return [] }
        return [localSpec] + remoteSpecs
    }

    private var remoteHostDraftsForInstall: [AddPeerRemoteHostDraft] {
        if SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled {
            return addPeerPrivateDirectMeshRemoteDraftsForInstall(manualDrafts: remoteDrafts,
                                                                 selectedTailnetDrafts: selectedTailnetDrafts)
        }
        var seenIDs = Set<String>()
        return (remoteDrafts.filter { !$0.sshHost.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty } +
                selectedTailnetDrafts).filter { draft in
            let key = draft.hostID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                ? addPeerDerivedHostID(from: draft.sshHost)
                : draft.hostID.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !seenIDs.contains(key) else { return false }
            seenIDs.insert(key)
            return true
        }
    }

    private var selectedTailnetDrafts: [AddPeerRemoteHostDraft] {
        tailnet.filter { tailnetSelected.contains($0.id) }.map { peer in
            let host = addPeerPreferredTailnetSSHHost(peer)
            return AddPeerRemoteHostDraft(sshHost: host,
                                          hostID: addPeerDerivedHostID(from: peer.hostName),
                                          user: NSUserName(),
                                          port: 22,
                                          platform: AddPeerHostPlatform.fromTailnetOS(peer.os))
        }
    }

    private var localHostID: String {
        let origin = daemon.origin.trimmingCharacters(in: .whitespacesAndNewlines)
        if !origin.isEmpty, origin != "—" { return origin }
        return addPeerDerivedHostID(from: localSSHHost)
    }

    private var localSSHHost: String {
        addPeerEffectiveLocalSSHHost(override: localSSHHostOverride,
                                     systemHostName: localSystemHostName,
                                     tailnetSSHHost: localTailnetSSHHost)
    }

    private var localDirectMeshSpec: String? {
        let sshHost = localSSHHost.trimmingCharacters(in: .whitespacesAndNewlines)
        let user = localSSHUser.trimmingCharacters(in: .whitespacesAndNewlines)
        let hostID = localHostID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !sshHost.isEmpty, !user.isEmpty, !hostID.isEmpty else { return nil }
        let home = FileManager.default.homeDirectoryForCurrentUser
        let sshDir = home.appendingPathComponent(".config/clipfan/ssh").path
        return addPeerDirectMeshSpec(
            hostID: hostID,
            sshHost: sshHost,
            user: user,
            port: localSSHPort,
            installPath: Installer.localClipfanBinaryPath(),
            configPath: Installer.localConfigURL().path,
            knownHostsPath: "\(sshDir)/known_hosts",
            syncKeyPath: "\(sshDir)/sync_ed25519",
            callbackHostSource: localSSHHostOverride.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                ? "remote_observed"
                : "manual"
        )
    }

    private var installButtonTitle: String {
        if installing || failure != nil {
            return addPeerInstallButtonTitle(installing: installing, installCount: installCount, failure: failure)
        }
        if SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled {
            return "Add peer"
        }
        return addPeerInstallButtonTitle(installing: installing, installCount: installCount, failure: failure)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Add a peer").font(.title3).bold()
            Text("Connect another host to this Mac over SSH. Clipfan detects macOS or Linux during setup.")
                .font(.callout).foregroundStyle(.secondary)

        ScrollView {
        VStack(alignment: .leading, spacing: 14) {
            if tailnetAvailable {
                tailnetSection
                dividerLabel("or add manually")
            }

            manualSection
            if SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled {
                directMeshOptionsSection
            }

            Toggle(isOn: $withTmux) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Set up tmux copy integration")
                    Text("Edits ~/.tmux.conf so tmux copy-mode yanks sync to the fleet.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }

            if let failure {
                failureSection(failure)
            } else if !progress.isEmpty {
                Text(progress).font(.callout).foregroundStyle(.secondary)
            }

            if installSuccess {
                HStack(spacing: 10) {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                    Text("Added \(lastInstalledHost).").font(.callout)
                }
            }
        }
        }

            HStack {
                Spacer()
                switch addPeerSheetButtons(installing: installing, installSuccess: installSuccess, hasFailure: failure != nil) {
                case .success:
                    Button("Add another") { resetForAnother() }
                    Button("Done") { dismiss() }
                        .keyboardShortcut(.defaultAction)
                default:
                    Button("Cancel") { dismiss() }
                    Button(installButtonTitle) { requestInstall() }
                        .keyboardShortcut(.return)
                        .disabled(isAddPeerInstallDisabled(installCount: installCount,
                                                           installing: installing,
                                                           privateDirectMeshRequested: SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled))
                }
            }
        }
        .padding(20)
        .frame(width: 620)
        .frame(height: tailnetAvailable ? 660 : 520)
        .task {
            localSystemHostName = addPeerLiveSystemLocalHostName()
            await daemon.refresh()
            await loadTailnet()
        }
        .alert("Trust SSH host keys?", isPresented: $confirmingDirectMeshKeyscanTrust) {
            Button("Trust and add peer") {
                install(trustKeyscanConfirmed: true)
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Clipfan will pin SSH host keys returned by ssh-keyscan for the selected hosts.")
        }
    }

    // MARK: tailnet

    private var tailnetSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("From your tailnet", systemImage: "network").font(.headline)
            ForEach(tailnet) { peer in
                Button {
                    toggle(peer.id)
                } label: {
                    HStack(spacing: 10) {
                        Image(systemName: tailnetSelected.contains(peer.id) ? "checkmark.square.fill" : "square")
                            .foregroundStyle(tailnetSelected.contains(peer.id) ? Color.accentColor : .secondary)
                        Circle().fill(peer.online ? Color.green : Color.gray).frame(width: 8, height: 8)
                        Text(peer.hostName)
                        Text(peer.os).font(.caption).foregroundStyle(.secondary)
                        Spacer()
                        Text(peer.ip).font(.caption).foregroundStyle(.secondary)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .padding(.vertical, 4).padding(.horizontal, 8)
                .background(tailnetSelected.contains(peer.id) ? Color.accentColor.opacity(0.12) : Color.clear)
                .clipShape(RoundedRectangle(cornerRadius: 7))
            }
        }
    }

    private func toggle(_ id: String) {
        if tailnetSelected.contains(id) {
            tailnetSelected.remove(id)
        } else {
            tailnetSelected = [id]
        }
    }

    // MARK: manual

    private var manualSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("Host to add", systemImage: "server.rack")
                .font(.headline)
            ForEach($remoteDrafts) { $draft in
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        TextField("Host", text: $draft.sshHost, prompt: Text("linux-b.tailnet.ts.net"))
                        TextField("Name", text: $draft.hostID, prompt: Text(addPeerDerivedHostID(from: draft.sshHost)))
                            .frame(width: 120)
                    }
                    HStack {
                        TextField("User", text: $draft.user)
                        TextField("SSH port", value: $draft.port, format: .number)
                            .frame(width: 80)
                        if remoteDrafts.count > 1 {
                            Button {
                                removeRemoteDraft(draft.id)
                            } label: {
                                Label("Remove host", systemImage: "minus.circle")
                                    .labelStyle(.iconOnly)
                            }
                            .buttonStyle(.borderless)
                        }
                    }
                }
                .padding(10)
                .background(Color.secondary.opacity(0.05))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.secondary.opacity(0.12)))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            }
            HStack {
                if !SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled,
                   (!tailnetAvailable || remoteDrafts.contains(where: { !$0.sshHost.isEmpty })) {
                    TextField("SSH key (optional)", text: $sshKey, prompt: Text("~/.ssh/id_ed25519"))
                }
            }
        }
    }

    private var directMeshOptionsSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            TextField("SSH known hosts", text: $directMeshRegularKnownHosts)
                .textFieldStyle(.roundedBorder)
            DisclosureGroup("Advanced callback address", isExpanded: $showingAdvancedSSH) {
                VStack(alignment: .leading, spacing: 8) {
                    TextField("Host override", text: $localSSHHostOverride, prompt: Text("Automatic"))
                    TextField("User", text: $localSSHUser)
                    HStack {
                        TextField("Port", value: $localSSHPort, format: .number)
                            .frame(width: 80)
                        Spacer()
                    }
                }
                .padding(.top, 4)
            }
        }
    }

    private func dividerLabel(_ text: String) -> some View {
        HStack {
            VStack { Divider() }
            Text(text).font(.caption).foregroundStyle(.secondary).fixedSize()
            VStack { Divider() }
        }
    }

    private func failureSection(_ failure: AddPeerOperationFailure) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(failure.message)
                .font(.callout)
                .foregroundStyle(.orange)
            HStack {
                Text("Log")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                Spacer()
                Button("Copy Log") { copyFailureLog(failure) }
                    .font(.caption)
                    .disabled(failure.logText.isEmpty)
            }
            ScrollView {
                Text(failure.logText)
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8)
            }
            .frame(minHeight: 110)
            .background(Color.orange.opacity(0.06))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.orange.opacity(0.20)))
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }
    }

    // MARK: actions

    private func loadTailnet() async {
        if let snapshot = try? await TailscaleClient.statusSnapshot() {
            tailnet = snapshot.peers
            tailnetAvailable = !snapshot.peers.isEmpty
            if let selfPeer = snapshot.selfPeer {
                localTailnetSSHHost = addPeerPreferredLocalTailnetSSHHost(selfPeer)
            }
        } else {
            tailnetAvailable = false
        }
    }

    private func removeRemoteDraft(_ id: UUID) {
        remoteDrafts.removeAll { $0.id == id }
        if remoteDrafts.isEmpty {
            remoteDrafts = [AddPeerRemoteHostDraft(user: NSUserName())]
        }
    }

    private func resetForAnother() {
        installSuccess = false
        installing = false
        progress = ""
        failure = nil
        log = nil
        lastInstalledHost = ""
        remoteDrafts = [AddPeerRemoteHostDraft(user: NSUserName())]
        tailnetSelected = []
    }

    private func requestInstall() {
        if SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled {
            confirmingDirectMeshKeyscanTrust = true
            return
        }
        install(trustKeyscanConfirmed: false)
    }

    private func install(trustKeyscanConfirmed: Bool) {
        installing = true
        progress = ""
        failure = nil
        log = nil
        Task {
            var targets: [(user: String, host: String, port: Int, key: String)] = []
            let directSpecs = directMeshHostSpecLines
            if SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled {
                guard !directSpecs.isEmpty else {
                    await MainActor.run {
                        installing = false
                    }
                    return
                }
                await MainActor.run {
                    progress = "Provisioning private SSH mesh…"
                    log = AddPeerOperationLog(host: "private-ssh-mesh")
                }
                do {
                    try await ensureCurrentLocalHelperForPrivateMesh()
                    let healReport = try await Installer.provisionPrivateDirectMeshAndHeal(
                        hostSpecs: directSpecs,
                        regularKnownHosts: directMeshRegularKnownHosts,
                        trustKeyscan: trustKeyscanConfirmed,
                        withTmux: withTmux,
                        onProgress: { @MainActor p in
                            let s = friendly(p, host: "private-ssh-mesh")
                            progress = s
                            if var currentLog = log {
                                currentLog.record(p)
                                log = currentLog
                            }
                        }
                    )
                    await MainActor.run {
                        progress = healReport.map { "Provisioned private SSH mesh · \($0.summary)." }
                            ?? "Provisioned private SSH mesh."
                        lastInstalledHost = "private SSH mesh"
                        installing = false
                        installSuccess = true
                    }
                } catch {
                    await MainActor.run {
                        var currentLog = log ?? AddPeerOperationLog(host: "private-ssh-mesh")
                        currentLog.recordFailure(error)
                        let operationFailure = AddPeerOperationFailure(host: "private-ssh-mesh", error: error, log: currentLog)
                        log = currentLog
                        failure = operationFailure
                        progress = operationFailure.message
                        installing = false
                    }
                }
                return
            }
            for draft in remoteHostDraftsForInstall {
                targets.append((draft.user, draft.sshHost, draft.port, sshKey))
            }

            for t in targets {
                await MainActor.run {
                    progress = "Installing on \(t.host)…"
                    log = AddPeerOperationLog(host: t.host)
                }
                do {
                    try await Installer.install(
                        user: t.user, host: t.host, port: t.port, sshKey: t.key,
                        withTmux: withTmux,
                        onProgress: { @MainActor p in
                            let s = friendly(p, host: t.host)
                            progress = s
                            if var currentLog = log {
                                currentLog.record(p)
                                log = currentLog
                            }
                        }
                    )
                    await MainActor.run {
                        progress = "Installed on \(t.host)."
                        lastInstalledHost = t.host
                    }
                } catch {
                    await MainActor.run {
                        var currentLog = log ?? AddPeerOperationLog(host: t.host)
                        currentLog.recordFailure(error)
                        let operationFailure = AddPeerOperationFailure(host: t.host, error: error, log: currentLog)
                        log = currentLog
                        failure = operationFailure
                        progress = operationFailure.message
                        installing = false
                    }
                    return
                }
            }
            await MainActor.run {
                installing = false
                installSuccess = true
            }
        }
    }

    private func ensureCurrentLocalHelperForPrivateMesh() async throws {
        guard !Bootstrap.installedBinaryCurrent else { return }
        await MainActor.run {
            let update = InstallProgress(step: "Local", detail: "updating local clipfan helper")
            progress = friendly(update, host: "private-ssh-mesh")
            if var currentLog = log {
                currentLog.record(update)
                log = currentLog
            }
        }
        guard let dist = Bootstrap.bundledPayload else {
            throw InstallError.configIO("current_local_provisioning_binary_required")
        }
        let script = dist.appendingPathComponent("install.sh")
        guard await Bootstrap.runInstaller(script: script, logTo: Bootstrap.installLog, mode: .upgradeExisting),
              Bootstrap.installedBinaryCurrent else {
            throw InstallError.configIO("current_local_provisioning_binary_required")
        }
    }

    /// friendly maps Installer's internal step names to user-facing phrases,
    /// so raw playbook strings never reach the UI.
    private func friendly(_ p: InstallProgress, host: String) -> String {
        let phrase: String
        switch p.step {
        case "Keyscan": phrase = "Trusting host key"
        case "Probe":   phrase = "Connecting"
        case "Config":  phrase = "Preparing keys"
        case "Upload":  phrase = "Copying clipfan"
        case "Install": phrase = "Installing"
        case "Local", "Restart": phrase = "Finishing up"
        default:        phrase = "Working"
        }
        return "\(host): \(phrase)…"
    }

    private func copyFailureLog(_ failure: AddPeerOperationFailure) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(failure.logText, forType: .string)
    }
}
