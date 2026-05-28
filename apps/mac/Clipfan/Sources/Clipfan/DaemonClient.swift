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
}
