import Foundation
import SwiftUI

@MainActor
final class DaemonClient: ObservableObject {
    static let shared = DaemonClient()

    @Published var origin: String = "—"
    @Published var version: String?
    @Published var maxHistory: Int = 200
    @Published var peers: [Peer] = []
    @Published var connected: Bool = false
    @Published var history: [HistoryEntry] = []

    private let base = URL(string: "http://127.0.0.1:7853")!
    private var timer: Timer?

    private init() {}

    func start() {
        timer?.invalidate()
        timer = Timer.scheduledTimer(withTimeInterval: 3.0, repeats: true) { [weak self] _ in
            Task { @MainActor in
                await self?.refresh()
                await self?.refreshHistory()
            }
        }
        Task {
            await refresh()
            await refreshHistory()
        }
    }

    func refresh() async {
        guard let key = loadSharedKey(),
              let url = URL(string: "\(base.absoluteString)/v1/peers") else {
            self.connected = false
            return
        }
        do {
            var req = URLRequest(url: url)
            req.httpMethod = "GET"
            req.timeoutInterval = 2
            for (header, value) in clipfanSignatureHeaders(method: "GET", requestURI: "/v1/peers", body: Data(), key: key) {
                req.setValue(value, forHTTPHeaderField: header)
            }
            let (data, _) = try await URLSession.shared.data(for: req)
            let resp = try JSONDecoder.clipfan.decode(PeersResponse.self, from: data)
            self.origin = resp.origin
            self.version = resp.version
            if let m = resp.max_history { self.maxHistory = m }
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

    func refreshHistory() async {
        let requestURI = "/v1/history?limit=\(maxHistory)"
        guard let key = loadSharedKey(),
              let url = URL(string: "\(base.absoluteString)\(requestURI)") else { return }
        var req = URLRequest(url: url)
        req.httpMethod = "GET"
        req.timeoutInterval = 2
        for (header, value) in clipfanSignatureHeaders(method: "GET", requestURI: requestURI, body: Data(), key: key) {
            req.setValue(value, forHTTPHeaderField: header)
        }
        do {
            let (data, _) = try await URLSession.shared.data(for: req)
            let resp = try JSONDecoder.clipfan.decode(HistoryResponse.self, from: data)
            // Only publish when the list actually changed so the 3s poll doesn't
            // re-render (and disturb selection/scroll) on every tick.
            if resp.entries != self.history {
                self.history = resp.entries
            }
        } catch {
            // leave history unchanged on transient failure
        }
    }

    func restore(_ id: String) async {
        await signedRequest(method: "POST", path: "/v1/restore", body: ["id": id])
        await refreshHistory()
    }

    func setPinned(_ id: String, _ pinned: Bool) async {
        await signedRequest(method: "POST", path: "/v1/history/pin", body: ["id": id, "pinned": pinned])
        await refreshHistory()
    }

    func deleteEntry(_ id: String) async {
        await signedRequest(method: "DELETE", path: "/v1/history", body: ["id": id])
        await refreshHistory()
    }

    func clearUnpinned() async {
        await signedRequest(method: "DELETE", path: "/v1/history", body: ["all_unpinned": true])
        await refreshHistory()
    }

    func setMaxHistory(_ n: Int) async {
        await signedRequest(method: "POST", path: "/v1/config", body: ["max_history": n])
        await refresh()
        await refreshHistory()
    }

    private func signedRequest(method: String, path: String, body: [String: Any]) async {
        guard let key = loadSharedKey(),
              let url = URL(string: "\(base.absoluteString)\(path)"),
              let payload = try? JSONSerialization.data(withJSONObject: body) else { return }
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.httpBody = payload
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        for (header, value) in clipfanSignatureHeaders(method: method, requestURI: path, body: payload, key: key) {
            req.setValue(value, forHTTPHeaderField: header)
        }
        _ = try? await URLSession.shared.data(for: req)
    }
}
