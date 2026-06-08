import Foundation
import SwiftUI

func shouldProbePeerHTTPVersions(policy: SSHTransportGatePolicy = .current,
                                 localVersion: String?,
                                 sharedKeyLoaded: Bool) -> Bool {
    policy.peerHTTPVersionProbeEnabled && localVersion != nil && sharedKeyLoaded
}

func shouldVerifyPeerHTTPVersionAfterUpdate(policy: SSHTransportGatePolicy = .current,
                                            expectedVersion: String?) -> Bool {
    policy.peerHTTPVersionProbeEnabled && expectedVersion != nil
}

func skippedPeerHTTPVersionVerification(host: String) -> PeerUpdateVerificationResult {
    PeerUpdateVerificationResult(
        status: nil,
        detail: "\(host) peer HTTP version verification is disabled by SSH transport gates"
    )
}

func localDaemonSignatureHeaders(method: String, requestURI: String, body: Data, sharedKey: Data) -> [String: String] {
    clipfanVersionedSignatureHeaders(method: method, requestURI: requestURI, body: body, sharedKey: sharedKey)
}

@MainActor
final class DaemonClient: ObservableObject {
    static let shared = DaemonClient()

    @Published var origin: String = "—"
    @Published var version: String?
    @Published var maxHistory: Int = 200
    @Published var peers: [Peer] = []
    @Published var peerVersions: [String: PeerVersionStatus] = [:]
    @Published var connected: Bool = false
    @Published var history: [HistoryEntry] = []
    @Published var historyLoaded: Bool = false
    @Published var safeModeStatus: LocalDaemonSafeModeStatus?
    @Published var safeModeLog: String = ""
    @Published var listenerRepairInProgress: Bool = false
    @Published var safeModeRepairMessage: String?
    @Published var configRevision: UInt64?
    @Published var revisionState: String?
    @Published var hostRemoveWarning: String?
    @Published var fleetMesh: FleetMesh?
    @Published var fleetMeshLoading: Bool = false

    var transportGatePolicy: SSHTransportGatePolicy = .current
    var peerVersionFetch: PeerUpdateVerifier.Fetch = PeerVersionProbe.fetch

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
            await refreshPeerVersions()
            await refreshHistory()
        }
    }

    func refresh() async {
        guard let key = loadSharedKey() else {
            self.connected = false
            return
        }
        do {
            let resp = try await fetchPeers(key: key)
            applyPeers(resp)
            if safeModeStatus?.active == true {
                await refreshSafeModeStatus(key: key)
                await refreshSafeModeLog(key: key)
            } else {
                safeModeLog = ""
            }
            self.connected = true
        } catch {
            self.connected = false
        }
    }

    @discardableResult
    func restartDaemon() -> Bool {
        // Try launchd kickstart first; fall back to shell relaunch.
        let uid = "\(getuid())"
        let kick = Process()
        kick.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        kick.arguments = ["kickstart", "-k", "gui/\(uid)/com.primeradiant.clipfan"]
        do {
            try kick.run()
        } catch {
            return false
        }
        kick.waitUntilExit()
        return kick.terminationStatus == 0
    }

    func moveDaemonListenerToLoopback() async {
        guard let key = loadSharedKey(),
              safeModeStatus?.repairable == true else {
            safeModeRepairMessage = "Safe mode listener repair is unavailable."
            return
        }

        listenerRepairInProgress = true
        defer { listenerRepairInProgress = false }
        do {
            let endpoint = LocalDaemonEndpoint(url: base, port: base.port ?? LocalDaemonDiscovery.defaultPort, purpose: .signedCompatibility)
            let statusRequest = try LocalDaemonRequestBuilder.listenerRepairStatusRequest(endpoint: endpoint, sharedKey: key)
            let statusData = try await authenticatedData(for: statusRequest, key: key)
            let repairStatus = try JSONDecoder.clipfan.decode(LocalDaemonListenerRepairStatus.self, from: statusData)
            let repairRequest = try LocalDaemonRequestBuilder.listenerRepairRequest(endpoint: endpoint,
                                                                                    status: repairStatus,
                                                                                    sharedKey: key)
            _ = try await authenticatedData(for: repairRequest, key: key)
            restartDaemon()
            if await verifyListenerRepairCleared() {
                safeModeRepairMessage = "Listener repair applied."
            } else {
                safeModeRepairMessage = "Listener repair applied, but safe mode is still active."
            }
        } catch {
            connected = false
            safeModeRepairMessage = "Listener repair failed: \(error.localizedDescription)"
        }
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
        guard safeModeStatus?.active != true else { return }
        guard let key = loadSharedKey(),
              await verifyDaemon(key: key) else { return }
        guard safeModeStatus?.active != true else { return }
        let requestURI = "/v1/history?limit=\(maxHistory)"
        do {
            let data = try await signedData(method: "GET", path: requestURI, body: Data(), key: key)
            let resp = try JSONDecoder.clipfan.decode(HistoryResponse.self, from: data)
            // Only publish when the list actually changed so the 3s poll doesn't
            // re-render (and disturb selection/scroll) on every tick.
            if resp.entries != self.history {
                self.history = resp.entries
            }
        } catch {
            // leave history unchanged on transient failure
        }
        historyLoaded = true
    }

    func refreshPeerVersions() async {
        guard safeModeStatus?.active != true else {
            peerVersions = [:]
            return
        }
        guard transportGatePolicy.peerHTTPVersionProbeEnabled else {
            peerVersions = [:]
            return
        }
        guard let key = loadSharedKey(), let localVersion = version else {
            peerVersions = [:]
            return
        }
        let currentPeers = peers
        if currentPeers.isEmpty {
            peerVersions = [:]
            return
        }

        var statuses: [String: PeerVersionStatus] = [:]
        let fetch = peerVersionFetch
        await withTaskGroup(of: (String, PeerVersionStatus?).self) { group in
            for peer in currentPeers {
                group.addTask {
                    do {
                        let remoteVersion = try await fetch(peer.hostname, peer.port, key)
                        return (peer.hostname, PeerUpdateAdvisor.status(remoteVersion: remoteVersion,
                                                                        localVersion: localVersion))
                    } catch {
                        return (peer.hostname, PeerUpdateAdvisor.status(forProbeError: error))
                    }
                }
            }
            for await (hostname, status) in group {
                if let status {
                    statuses[hostname] = status
                }
            }
        }
        peerVersions = statuses
    }

    func refreshPeerVersion(hostname: String,
                            attempts: Int = 1,
                            delayNanoseconds: UInt64 = 0) async -> PeerVersionStatus? {
        guard safeModeStatus?.active != true else { return nil }
        guard transportGatePolicy.peerHTTPVersionProbeEnabled else { return nil }
        guard let localVersion = version else { return nil }
        return await verifyPeerVersion(hostname: hostname,
                                       expectedVersion: localVersion,
                                       attempts: attempts,
                                       delayNanoseconds: delayNanoseconds)?.status
    }

    func verifyPeerVersion(hostname: String,
                           expectedVersion: String,
                           attempts: Int = 1,
                           delayNanoseconds: UInt64 = 0) async -> PeerUpdateVerificationResult? {
        guard safeModeStatus?.active != true else { return nil }
        guard transportGatePolicy.peerHTTPVersionProbeEnabled else {
            peerVersions.removeValue(forKey: hostname)
            return skippedPeerHTTPVersionVerification(host: hostname)
        }
        guard let key = loadSharedKey(),
              let peer = peers.first(where: { $0.hostname == hostname }) else { return nil }

        let result = await PeerUpdateVerifier.verify(host: peer.hostname,
                                                     port: peer.port,
                                                     key: key,
                                                     expectedVersion: expectedVersion,
                                                     attempts: attempts,
                                                     delayNanoseconds: delayNanoseconds,
                                                     fetch: peerVersionFetch)
        if let status = result.status {
            peerVersions[peer.hostname] = status
        } else {
            peerVersions.removeValue(forKey: peer.hostname)
        }
        return result
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

    func readSSHPeerConfig(peerID: String) async throws -> LocalDaemonSSHPeerConfigResponse {
        try await sshPeerConfigClient().read(peerID: peerID)
    }

    func upsertSSHPeerConfig(peerID: String,
                             request: LocalDaemonSSHPeerUpsertRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        try requireSSHPeerConfigMutationAvailable()
        return try await sshPeerConfigClient().upsertWithRevisionRetry(peerID: peerID, request: request)
    }

    func patchSSHPeerProof(peerID: String,
                           request: LocalDaemonSSHPeerProofPatchRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        try requireSSHPeerConfigMutationAvailable()
        return try await sshPeerConfigClient().patchProofWithRevisionRetry(peerID: peerID, request: request)
    }

    func transitionSSHPeer(peerID: String,
                           request: LocalDaemonSSHPeerTransitionRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        try requireSSHPeerConfigMutationAvailable()
        return try await sshPeerConfigClient().transitionWithRevisionRetry(peerID: peerID, request: request)
    }

    func disableSSHPeer(peerID: String,
                        expectedConfigRevision: UInt64,
                        reason: String) async throws -> LocalDaemonSSHPeerConfigResponse {
        try requireSSHPeerConfigMutationAvailable()
        return try await sshPeerConfigClient().disableWithRevisionRetry(
            peerID: peerID,
            expectedConfigRevision: expectedConfigRevision,
            reason: reason
        )
    }

    func deleteSSHPeer(peerID: String,
                       expectedConfigRevision: UInt64,
                       reason: String,
                       logID: String) async throws -> LocalDaemonSSHPeerConfigResponse {
        try requireSSHPeerConfigMutationAvailable()
        return try await sshPeerConfigClient().deleteWithRevisionRetry(
            peerID: peerID,
            expectedConfigRevision: expectedConfigRevision,
            reason: reason,
            logID: logID
        )
    }

    func removeHost(hostID: String) async throws -> LocalDaemonHostRemoveResponse {
        try requireSSHPeerConfigMutationAvailable()
        hostRemoveWarning = nil
        if revisionState?.isEmpty != false {
            await refresh()
        }
        let client = try sshPeerConfigClient()
        do {
            let response = try await client.removeHost(hostID: hostID, request: makeHostRemoveRequest())
            await completeHostRemove(hostID: hostID)
            return response
        } catch LocalDaemonSSHPeerConfigError.api(let code, _) where code == localDaemonConfigRevisionConflictCode {
            await refresh()
            let response = try await client.removeHost(hostID: hostID, request: makeHostRemoveRequest())
            await completeHostRemove(hostID: hostID)
            return response
        }
    }

    private func signedRequest(method: String, path: String, body: [String: Any]) async {
        guard safeModeStatus?.active != true else { return }
        guard let key = loadSharedKey(),
              await verifyDaemon(key: key),
              let payload = try? JSONSerialization.data(withJSONObject: body) else { return }
        _ = try? await signedData(method: method, path: path, body: payload, key: key)
    }

    private func fetchPeers(key: Data) async throws -> PeersResponse {
        let data = try await signedData(method: "GET", path: "/v1/peers", body: Data(), key: key)
        return try JSONDecoder.clipfan.decode(PeersResponse.self, from: data)
    }

    /// refreshFleet fetches the daemon's aggregated mesh view (GET /v1/fleet), which
    /// the daemon gathers from each peer over SSH. It is on-demand — the Fleet view
    /// triggers it on open / refresh — rather than part of the 3s status poll, because
    /// a single refresh can SSH the entire fleet. A failed refresh leaves the prior
    /// mesh visible (the daemon may be momentarily between restarts).
    func refreshFleet() async {
        guard let key = loadSharedKey() else { return }
        fleetMeshLoading = true
        defer { fleetMeshLoading = false }
        do {
            let data = try await signedData(method: "GET", path: "/v1/fleet", body: Data(), key: key)
            fleetMesh = try buildFleetMesh(from: data)
        } catch {
            // leave the prior mesh visible on a transient refresh failure
        }
    }

    private func refreshSafeModeStatus(key: Data) async {
        do {
            let data = try await signedData(method: "GET", path: LocalDaemonRequestBuilder.safeModeStatusPath, body: Data(), key: key)
            let resp = try JSONDecoder.clipfan.decode(LocalDaemonStatusResponse.self, from: data)
            self.safeModeStatus = resp.safeMode
        } catch {
            // Compatibility daemons may only expose safe_mode_v1 on /v1/peers.
        }
    }

    private func refreshSafeModeLog(key: Data) async {
        guard safeModeStatus?.active == true else {
            safeModeLog = ""
            return
        }
        do {
            let data = try await signedData(method: "GET", path: LocalDaemonRequestBuilder.safeModeLogPath, body: Data(), key: key)
            safeModeLog = try JSONDecoder.clipfan.decode(LocalDaemonSafeModeLogResponse.self, from: data).formattedLog
        } catch {
            // Leave the last log visible if the daemon is between restarts.
        }
    }

    private func verifyDaemon(key: Data) async -> Bool {
        do {
            let resp = try await fetchPeers(key: key)
            applyPeers(resp)
            self.connected = true
            return true
        } catch {
            self.connected = false
            return false
        }
    }

    private func applyPeers(_ resp: PeersResponse) {
        self.origin = resp.origin
        self.version = resp.version
        if let m = resp.max_history { self.maxHistory = m }
        self.configRevision = resp.config_revision
        self.revisionState = resp.revision_state
        self.safeModeStatus = resp.safeMode
        if safeModeStatus?.active == true {
            self.peers = []
            self.peerVersions = [:]
        } else {
            self.peers = resp.peers
        }
    }

    private func verifyListenerRepairCleared() async -> Bool {
        for _ in 0..<5 {
            try? await Task.sleep(nanoseconds: 500_000_000)
            await refresh()
            if safeModeStatus?.active != true {
                return true
            }
            if safeModeStatus?.listenerIsLoopback == true {
                return true
            }
        }
        return false
    }

    private func signedData(method: String, path: String, body: Data, key: Data) async throws -> Data {
        let endpoint = LocalDaemonEndpoint(url: base, port: base.port ?? LocalDaemonDiscovery.defaultPort, purpose: .signed)
        let prepared = try LocalDaemonRequestBuilder.signedRequest(endpoint: endpoint,
                                                                   method: method,
                                                                   requestURI: path,
                                                                   body: body,
                                                                   sharedKey: key)
        return try await authenticatedData(for: prepared, key: key)
    }

    private func authenticatedData(for prepared: LocalDaemonPreparedRequest, key: Data) async throws -> Data {
        let (data, response) = try await URLSession.shared.data(for: prepared.request)
        return try authenticatedClipfanData(data, response: response, requestNonce: prepared.requestNonce, key: key)
    }

    private func requireSSHPeerConfigMutationAvailable() throws {
        guard safeModeStatus?.active != true else {
            throw LocalDaemonSSHPeerConfigError.api(code: "safe_mode_active", statusCode: 503)
        }
    }

    private func makeHostRemoveRequest() throws -> LocalDaemonHostRemoveRequest {
        guard let revisionState, !revisionState.isEmpty else {
            throw LocalDaemonSSHPeerConfigError.api(code: "missing_revision_state", statusCode: 409)
        }
        let expectedRevision: UInt64?
        if revisionState == "versioned" {
            guard let configRevision, configRevision > 0 else {
                throw LocalDaemonSSHPeerConfigError.api(code: localDaemonConfigRevisionConflictCode, statusCode: 409)
            }
            expectedRevision = configRevision
        } else {
            expectedRevision = nil
        }
        return LocalDaemonHostRemoveRequest(
            expectedRevisionState: revisionState,
            expectedConfigRevision: expectedRevision,
            reason: "user_deleted",
            logID: "host-remove-\(Int(Date().timeIntervalSince1970))"
        )
    }

    private func completeHostRemove(hostID: String) async {
        peerVersions.removeValue(forKey: hostID)
        guard restartDaemon() else {
            hostRemoveWarning = "Removed \(hostID) from config, but Clipfan could not restart the daemon. Restart Clipfan to apply the change."
            connected = false
            return
        }
        try? await Task.sleep(nanoseconds: 500_000_000)
        await refresh()
        await refreshPeerVersions()
        if peers.contains(where: { $0.hostname == hostID }) {
            hostRemoveWarning = "Removed \(hostID) from config, but the daemon has not refreshed its peer list yet."
        }
    }

    private func sshPeerConfigClient() throws -> LocalDaemonSSHPeerConfigClient {
        guard let key = loadSharedKey() else {
            throw LocalDaemonSSHPeerConfigError.api(code: "missing_shared_key", statusCode: 503)
        }
        let endpoint = LocalDaemonEndpoint(url: base, port: base.port ?? LocalDaemonDiscovery.defaultPort, purpose: .signed)
        return LocalDaemonSSHPeerConfigClient(endpoint: endpoint, sharedKey: key)
    }
}
