import SwiftUI

struct AddPeerSheet: View {
    @Environment(\.dismiss) private var dismiss

    @State private var user: String = NSUserName()
    @State private var host: String = ""
    @State private var port: Int = 22
    @State private var sshKey: String = ""
    @State private var withTmux = false

    @State private var tailnet: [TailscalePeer] = []
    @State private var tailnetSelected: Set<String> = []
    @State private var tailnetAvailable = false

    @State private var installing = false
    @State private var progress: String = ""

    private var installCount: Int {
        tailnetSelected.count + (host.isEmpty ? 0 : 1)
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

            Toggle(isOn: $withTmux) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Set up tmux copy integration")
                    Text("Edits ~/.tmux.conf so copies inside tmux (incl. Claude Code) sync to the fleet.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }

            if !progress.isEmpty {
                Text(progress).font(.callout).foregroundStyle(.secondary)
            }

            Spacer()

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button(installing ? "Installing…" : installLabel) { install() }
                    .keyboardShortcut(.return)
                    .disabled(installCount == 0 || installing)
            }
        }
        .padding(20)
        .frame(width: 560, height: tailnetAvailable ? 560 : 420)
        .task { await loadTailnet() }
    }

    private var installLabel: String {
        installCount <= 1 ? "Install" : "Install on \(installCount) hosts"
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
            if !tailnetAvailable {
                TextField("SSH key (optional)", text: $sshKey, prompt: Text("~/.ssh/id_ed25519"))
            }
        }
        .formStyle(.grouped)
    }

    private func dividerLabel(_ text: String) -> some View {
        HStack {
            VStack { Divider() }
            Text(text).font(.caption).foregroundStyle(.secondary).fixedSize()
            VStack { Divider() }
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
        Task {
            var targets: [(user: String, host: String, port: Int, key: String)] = []
            for peer in tailnet where tailnetSelected.contains(peer.id) {
                targets.append((NSUserName(), peer.hostName, 22, ""))
            }
            if !host.isEmpty {
                targets.append((user, host, port, sshKey))
            }

            for t in targets {
                await MainActor.run { progress = "Installing on \(t.host)…" }
                do {
                    try await Installer.install(
                        user: t.user, host: t.host, port: t.port, sshKey: t.key,
                        withTmux: withTmux,
                        onProgress: { p in let s = friendly(p, host: t.host); Task { @MainActor in progress = s } }
                    )
                    await MainActor.run { progress = "Installed on \(t.host)." }
                } catch {
                    await MainActor.run { progress = "Failed on \(t.host): \(error.localizedDescription)" }
                    await MainActor.run { installing = false }
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
}
