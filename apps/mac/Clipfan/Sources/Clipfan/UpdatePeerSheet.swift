import AppKit
import SwiftUI

struct UpdatePeerSheet: View {
    @Environment(\.dismiss) private var dismiss

    let peer: Peer

    @State private var user: String = NSUserName()
    @State private var host: String
    @State private var sshPort: Int = 22
    @State private var sshKey: String = ""
    @State private var updating = false
    @State private var progress: String = ""
    @State private var log: PeerUpdateLog

    init(peer: Peer) {
        self.peer = peer
        _host = State(initialValue: peer.hostname)
        _log = State(initialValue: PeerUpdateLog(host: peer.hostname))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Update peer").font(.title3).bold()
            Text("Refresh the clipfan binary on this host over SSH")
                .font(.callout).foregroundStyle(.secondary)

            Form {
                TextField("Host", text: $host)
                TextField("User", text: $user)
                TextField("SSH port", value: $sshPort, format: .number)
                TextField("SSH key (optional)", text: $sshKey, prompt: Text("~/.ssh/id_ed25519"))
            }
            .formStyle(.grouped)

            if !progress.isEmpty {
                Text(progress).font(.callout).foregroundStyle(.secondary)
            }

            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Text("Log")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Copy Log") { copyLog() }
                        .font(.caption)
                        .disabled(log.text.isEmpty)
                }
                ScrollView {
                    Text(log.text)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(8)
                }
                .frame(minHeight: 120)
                .background(Color.secondary.opacity(0.06))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.secondary.opacity(0.12)))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            }

            Spacer()

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .disabled(updating)
                Button(updating ? "Updating…" : "Update") { update() }
                    .keyboardShortcut(.return)
                    .disabled(host.isEmpty || updating)
            }
        }
        .padding(20)
        .frame(width: 560, height: 520)
    }

    private func update() {
        let targetHost = host
        let targetUser = user
        let targetPort = sshPort
        let targetKey = sshKey
        updating = true
        progress = "\(targetHost): Connecting…"
        log = PeerUpdateLog(host: targetHost)
        Task {
            do {
                let version = try await Installer.update(
                    user: targetUser,
                    host: targetHost,
                    port: targetPort,
                    sshKey: targetKey,
                    onProgress: { p in
                        let s = friendly(p, host: targetHost)
                        log.record(p)
                        progress = s
                    }
                )
                await MainActor.run {
                    log.recordSuccess(version: version)
                    progress = "\(targetHost): Updated to \(version). Verifying daemon…"
                    log.record(.init(step: "Verify", detail: "probing signed /v1/version on \(peer.hostname)"))
                }
                await DaemonClient.shared.refresh()
                let status = await DaemonClient.shared.refreshPeerVersion(hostname: peer.hostname,
                                                                          attempts: 6,
                                                                          delayNanoseconds: 1_000_000_000)
                await MainActor.run {
                    if case .current = status {
                        log.record(.init(step: "Verify", detail: "\(peer.hostname) is running \(version)"))
                        progress = "\(targetHost): Updated to \(version)."
                    } else {
                        log.record(.init(step: "Verify", detail: "\(peer.hostname) did not answer with the current daemon version yet"))
                        progress = "\(targetHost): Updated to \(version); daemon verification is still pending."
                    }
                }
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                await MainActor.run {
                    updating = false
                    dismiss()
                }
            } catch {
                await MainActor.run {
                    log.recordFailure(error)
                    progress = "Failed on \(targetHost): \(error.localizedDescription)"
                    updating = false
                }
            }
        }
    }

    private func friendly(_ p: InstallProgress, host: String) -> String {
        let phrase: String
        switch p.step {
        case "Probe":   phrase = "Connecting"
        case "Upload":  phrase = "Copying clipfan"
        case "Install": phrase = "Installing"
        default:        phrase = "Working"
        }
        return "\(host): \(phrase)…"
    }

    private func copyLog() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(log.text, forType: .string)
    }
}
