import Foundation
import SwiftUI

@MainActor
final class DaemonClient: ObservableObject {
    static let shared = DaemonClient()

    @Published var origin: String = "—"
    @Published var peers: [Peer] = []
    @Published var connected: Bool = false

    private let base = URL(string: "http://127.0.0.1:7853")!
    private var timer: Timer?

    private init() {}

    func start() {
        timer?.invalidate()
        timer = Timer.scheduledTimer(withTimeInterval: 3.0, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.refresh() }
        }
        Task { await refresh() }
    }

    func refresh() async {
        do {
            var req = URLRequest(url: base.appendingPathComponent("/v1/peers"))
            req.timeoutInterval = 2
            let (data, _) = try await URLSession.shared.data(for: req)
            let resp = try JSONDecoder.clipfan.decode(PeersResponse.self, from: data)
            self.origin = resp.origin
            self.peers = resp.peers
            self.connected = true
        } catch {
            self.connected = false
        }
    }

    func restartDaemon() {
        // Try launchd kickstart first; fall back to shell relaunch.
        let uid = "\(getuid())"
        let kick = Process()
        kick.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        kick.arguments = ["kickstart", "-k", "gui/\(uid)/com.primeradiant.clipfan"]
        try? kick.run()
        kick.waitUntilExit()
    }

    /// Shell-launch the daemon if it isn't already answering on the local port.
    /// Spawning it as a child of this GUI app makes it inherit the app's Local
    /// Network grant, sidestepping the Sequoia gate that silently breaks
    /// launchd-spawned daemons. Mirrors the `nohup ~/.local/bin/clipfan` hack
    /// in dist/install.sh.
    func ensureDaemonRunning() async {
        await refresh()
        if connected { return }

        let bin = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".local/bin/clipfan")
        guard FileManager.default.fileExists(atPath: bin.path) else { return }

        let log = "/tmp/clipfan-shell.log"
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/bin/sh")
        proc.arguments = ["-c", "nohup \(bin.path) >\(log) 2>&1 &"]
        try? proc.run()
        proc.waitUntilExit()
    }
}
