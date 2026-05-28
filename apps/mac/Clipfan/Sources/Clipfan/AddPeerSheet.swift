import SwiftUI

struct AddPeerSheet: View {
    @Environment(\.dismiss) private var dismiss

    enum Mode: String, CaseIterable, Hashable {
        case manual = "Type address"
        case tailscale = "Pick from Tailnet"
    }

    @State private var mode: Mode = .manual

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Picker("", selection: $mode) {
                ForEach(Mode.allCases, id: \.self) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()

            switch mode {
            case .manual: ManualForm(onFinish: { dismiss() })
            case .tailscale: TailscalePickerView(onFinish: { dismiss() })
            }
        }
        .padding()
        .frame(width: 560, height: 440)
    }
}

struct ManualForm: View {
    @State private var user: String = NSUserName()
    @State private var host: String = ""
    @State private var port: Int = 22
    @State private var sshKey: String = ""
    @State private var status: String = ""
    @State private var installing = false
    let onFinish: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Form {
                TextField("Host", text: $host, prompt: Text("paradise-park or 192.168.1.42"))
                TextField("User", text: $user)
                TextField("SSH port", value: $port, format: .number)
                TextField("SSH key (optional)", text: $sshKey, prompt: Text("~/.ssh/id_ed25519"))
            }
            .formStyle(.grouped)

            HStack {
                Spacer()
                Button("Cancel") { onFinish() }
                Button(installing ? "Installing…" : "Install on \(host.isEmpty ? "…" : host)") {
                    install()
                }
                .keyboardShortcut(.return)
                .disabled(host.isEmpty || installing)
            }
            if !status.isEmpty {
                Text(status).foregroundStyle(.secondary).font(.callout)
            }
        }
    }

    func install() {
        installing = true
        status = ""
        Task {
            do {
                try await Installer.install(
                    user: user, host: host, port: port, sshKey: sshKey,
                    onProgress: { p in status = "\(p.step): \(p.detail)" }
                )
                await MainActor.run {
                    status = "Installed on \(host). Local daemon restarted."
                    installing = false
                    Task { try? await Task.sleep(nanoseconds: 1_200_000_000); onFinish() }
                }
            } catch {
                await MainActor.run {
                    status = "Failed: \(error.localizedDescription)"
                    installing = false
                }
            }
        }
    }
}

struct TailscalePickerView: View {
    @State private var peers: [TailscalePeer] = []
    @State private var selected: Set<String> = []
    @State private var loading = true
    @State private var loadError: String?
    @State private var installing = false
    @State private var statusByHost: [String: String] = [:]
    let onFinish: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if loading {
                ProgressView("Querying tailnet…").frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let loadError {
                Text("tailscale status failed:\n\(loadError)")
                    .foregroundStyle(.red)
                    .font(.callout)
                Button("Retry") { Task { await load() } }
            } else {
                Table(peers, selection: $selected) {
                    TableColumn("") { peer in
                        Circle().fill(peer.online ? Color.green : Color.gray)
                            .frame(width: 10, height: 10)
                    }.width(20)
                    TableColumn("Host", value: \.hostName)
                    TableColumn("DNS") { Text($0.dnsName).font(.caption).foregroundStyle(.secondary) }
                    TableColumn("IP") { Text($0.ip).font(.caption).foregroundStyle(.secondary) }.width(120)
                    TableColumn("OS", value: \.os).width(80)
                    TableColumn("Status") { peer in
                        Text(statusByHost[peer.hostName] ?? "")
                            .font(.caption)
                            .lineLimit(1)
                    }
                }
                .frame(minHeight: 240)
            }

            HStack {
                Spacer()
                Button("Cancel") { onFinish() }
                Button(installing ? "Installing…" : "Install on \(selected.count) host\(selected.count == 1 ? "" : "s")") {
                    install()
                }
                .keyboardShortcut(.return)
                .disabled(selected.isEmpty || installing)
            }
        }
        .task { await load() }
    }

    func load() async {
        loading = true
        loadError = nil
        do {
            peers = try await TailscaleClient.status()
            loading = false
        } catch {
            loadError = error.localizedDescription
            loading = false
        }
    }

    func install() {
        installing = true
        Task {
            let user = NSUserName()
            for peer in peers where selected.contains(peer.id) {
                do {
                    try await Installer.install(user: user, host: peer.hostName, port: 22, sshKey: "",
                                                onProgress: { p in
                        statusByHost[peer.hostName] = "\(p.step): \(p.detail)"
                    })
                    statusByHost[peer.hostName] = "ok"
                } catch {
                    statusByHost[peer.hostName] = "failed: \(error.localizedDescription)"
                }
            }
            installing = false
        }
    }
}
