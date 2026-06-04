import AppKit
import SwiftUI

func isAddPeerInstallDisabled(installCount: Int,
                              installing: Bool,
                              policy: SSHTransportGatePolicy = .current,
                              privateDirectMeshRequested: Bool = false,
                              trustKeyscan: Bool = false) -> Bool {
    if installing || installCount == 0 { return true }
    if privateDirectMeshRequested {
        return !policy.privateDirectMeshProvisioningEnabled || !trustKeyscan || installCount < 2
    }
    return !policy.addPeerProvisioningEnabled
}

func addPeerInstallButtonTitle(installing: Bool, installCount: Int, failure: AddPeerOperationFailure?) -> String {
    if installing { return "Installing…" }
    if failure != nil { return "Retry" }
    return installCount <= 1 ? "Install" : "Install on \(installCount) hosts"
}

struct AddPeerSheet: View {
    @Environment(\.dismiss) private var dismiss

    @State private var user: String = NSUserName()
    @State private var host: String = ""
    @State private var port: Int = 22
    @State private var sshKey: String = ""
    @State private var withTmux = false
    @State private var directMeshHostSpecs: String = ""
    @State private var directMeshRegularKnownHosts: String = "~/.ssh/known_hosts"
    @State private var trustDirectMeshKeyscan = false

    @State private var tailnet: [TailscalePeer] = []
    @State private var tailnetSelected: Set<String> = []
    @State private var tailnetAvailable = false

    @State private var installing = false
    @State private var progress: String = ""
    @State private var log: AddPeerOperationLog?
    @State private var failure: AddPeerOperationFailure?

    private var installCount: Int {
        let directSpecs = directMeshHostSpecLines.count
        if directSpecs > 0 { return directSpecs }
        return tailnetSelected.count + (host.isEmpty ? 0 : 1)
    }

    private var directMeshHostSpecLines: [String] {
        directMeshHostSpecs
            .split(whereSeparator: \.isNewline)
            .map { String($0).trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Add a peer").font(.title3).bold()
            Text("Install clipfan on another host over SSH")
                .font(.callout).foregroundStyle(.secondary)

            if tailnetAvailable {
                tailnetSection
                dividerLabel("or add manually")
            }

            manualSection
            if SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled {
                privateDirectMeshSection
            }

            Toggle(isOn: $withTmux) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Set up tmux copy integration")
                    Text("Edits ~/.tmux.conf so copies inside tmux (incl. Claude Code) sync to the fleet.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }

            if let failure {
                failureSection(failure)
            } else if !progress.isEmpty {
                Text(progress).font(.callout).foregroundStyle(.secondary)
            }

            Spacer()

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button(addPeerInstallButtonTitle(installing: installing,
                                                 installCount: installCount,
                                                 failure: failure)) { install() }
                    .keyboardShortcut(.return)
                    .disabled(isAddPeerInstallDisabled(installCount: installCount,
                                                       installing: installing,
                                                       privateDirectMeshRequested: !directMeshHostSpecLines.isEmpty,
                                                       trustKeyscan: trustDirectMeshKeyscan))
            }
        }
        .padding(20)
        .frame(width: 560)
        .frame(minHeight: tailnetAvailable ? 620 : 500)
        .task { await loadTailnet() }
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
        if tailnetSelected.contains(id) { tailnetSelected.remove(id) } else { tailnetSelected.insert(id) }
    }

    // MARK: manual

    private var manualSection: some View {
        Form {
            TextField("Host", text: $host, prompt: Text("host.local or 192.168.1.42"))
            TextField("User", text: $user)
            TextField("SSH port", value: $port, format: .number)
            if !tailnetAvailable || !host.isEmpty {
                TextField("SSH key (optional)", text: $sshKey, prompt: Text("~/.ssh/id_ed25519"))
            }
        }
        .formStyle(.grouped)
    }

    private var privateDirectMeshSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("Private SSH mesh", systemImage: "point.3.connected.trianglepath.dotted")
                .font(.headline)
            TextEditor(text: $directMeshHostSpecs)
                .font(.system(.caption, design: .monospaced))
                .frame(minHeight: 96)
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.secondary.opacity(0.25)))
            TextField("known_hosts", text: $directMeshRegularKnownHosts)
                .textFieldStyle(.roundedBorder)
            Toggle("Trust ssh-keyscan host keys", isOn: $trustDirectMeshKeyscan)
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
        if let peers = try? await TailscaleClient.status(), !peers.isEmpty {
            tailnet = peers
            tailnetAvailable = true
        } else {
            tailnetAvailable = false
        }
    }

    private func install() {
        installing = true
        progress = ""
        failure = nil
        log = nil
        Task {
            var targets: [(user: String, host: String, port: Int, key: String)] = []
            let directSpecs = directMeshHostSpecLines
            if !directSpecs.isEmpty {
                await MainActor.run {
                    progress = "Provisioning private SSH mesh…"
                    log = AddPeerOperationLog(host: "private-ssh-mesh")
                }
                do {
                    try await Installer.provisionPrivateDirectMesh(
                        hostSpecs: directSpecs,
                        regularKnownHosts: directMeshRegularKnownHosts,
                        trustKeyscan: trustDirectMeshKeyscan,
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
                        progress = "Provisioned private SSH mesh."
                        installing = false
                        Task { try? await Task.sleep(nanoseconds: 1_000_000_000); dismiss() }
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
            for peer in tailnet where tailnetSelected.contains(peer.id) {
                targets.append((NSUserName(), peer.hostName, 22, ""))
            }
            if !host.isEmpty {
                targets.append((user, host, port, sshKey))
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
                    await MainActor.run { progress = "Installed on \(t.host)." }
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
                Task { try? await Task.sleep(nanoseconds: 1_000_000_000); dismiss() }
            }
        }
    }

    /// friendly maps Installer's internal step names to user-facing phrases,
    /// so raw playbook strings never reach the UI.
    private func friendly(_ p: InstallProgress, host: String) -> String {
        let phrase: String
        switch p.step {
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
