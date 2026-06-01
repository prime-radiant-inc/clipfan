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

    init(peer: Peer) {
        self.peer = peer
        _host = State(initialValue: peer.hostname)
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
        .frame(width: 480, height: 360)
    }

    private func update() {
        let targetHost = host
        let targetUser = user
        let targetPort = sshPort
        let targetKey = sshKey
        updating = true
        progress = "\(targetHost): Connecting…"
        Task {
            do {
                let version = try await Installer.update(
                    user: targetUser,
                    host: targetHost,
                    port: targetPort,
                    sshKey: targetKey,
                    onProgress: { p in
                        let s = friendly(p, host: targetHost)
                        Task { @MainActor in progress = s }
                    }
                )
                await MainActor.run { progress = "\(targetHost): Updated to \(version)." }
                await DaemonClient.shared.refreshPeerVersions()
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                await MainActor.run {
                    updating = false
                    dismiss()
                }
            } catch {
                await MainActor.run {
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
}
