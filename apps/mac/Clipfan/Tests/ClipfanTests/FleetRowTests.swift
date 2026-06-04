import XCTest
@testable import Clipfan

final class FleetRowTests: XCTestCase {
    func testDecodePeersResponseVersion() throws {
        let json = """
        {"origin":"paradise-park","version":"v1.2.3","peers":[]}
        """.data(using: .utf8)!
        let resp = try JSONDecoder.clipfan.decode(PeersResponse.self, from: json)
        XCTAssertEqual(resp.origin, "paradise-park")
        XCTAssertEqual(resp.version, "v1.2.3")
        XCTAssertTrue(resp.peers.isEmpty)
    }

    func testDecodePeersResponseWithoutVersion() throws {
        let json = """
        {"origin":"paradise-park","peers":[]}
        """.data(using: .utf8)!
        let resp = try JSONDecoder.clipfan.decode(PeersResponse.self, from: json)
        XCTAssertNil(resp.version)
    }
}

extension FleetRowTests {
    func testDecodePeersResponseMaxHistory() throws {
        let json = """
        {"origin":"p","peers":[],"max_history":350}
        """.data(using: .utf8)!
        let resp = try JSONDecoder.clipfan.decode(PeersResponse.self, from: json)
        XCTAssertEqual(resp.max_history, 350)
    }

    func testDecodePeersResponseSafeModeCompatibilityPayload() throws {
        let json = """
        {
          "origin":"paradise-park",
          "version":"v1.2.3",
          "peers":[],
          "status":"safe_mode_signed_repair",
          "safe_mode":true,
          "safe_mode_schema":"safe_mode_v1",
          "listener_repair_status":"needs_repair",
          "last_failure_phase":"listener_safe_mode",
          "safe_mode_logs_available":true,
          "configured_listen":"0.0.0.0:49123",
          "effective_repair_listen":"127.0.0.1:49123",
          "peer_sync_started":false,
          "config_revision":17
        }
        """.data(using: .utf8)!

        let resp = try JSONDecoder.clipfan.decode(PeersResponse.self, from: json)

        XCTAssertTrue(resp.safeMode?.active == true)
        XCTAssertEqual(resp.safeMode?.listenerRepairStatus, "needs_repair")
        XCTAssertEqual(resp.safeMode?.effectiveRepairListen, "127.0.0.1:49123")
        XCTAssertEqual(resp.safeMode?.configRevision, 17)
    }
}

extension FleetRowTests {
    private func peer(_ name: String) -> Peer {
        Peer(hostname: name, port: 7853,
             last_push_ts: nil, last_push_ok: true,
             last_push_err: nil, last_recv_ts: nil)
    }

    func testSelfRowIsFirst() {
        let rows = fleetRows(origin: "paradise-park", connected: true,
                             peers: [peer("flower-garden"), peer("linux-box")])
        XCTAssertEqual(rows.count, 3)
        XCTAssertTrue(rows[0].isSelf)
        XCTAssertEqual(rows[0].name, "paradise-park")
        XCTAssertFalse(rows[1].isSelf)
        XCTAssertEqual(rows[1].name, "flower-garden")
        XCTAssertEqual(rows[2].name, "linux-box")
    }

    func testSelfRowHasNoSyncTimesAndReflectsConnected() {
        let up = fleetRows(origin: "me", connected: true, peers: [])
        XCTAssertNil(up[0].pushTS)
        XCTAssertNil(up[0].recvTS)
        XCTAssertEqual(up[0].health, .healthy)
        XCTAssertEqual(up[0].subtitle, "this Mac · running")

        let down = fleetRows(origin: "me", connected: false, peers: [])
        XCTAssertEqual(down[0].health, .down)
        XCTAssertEqual(down[0].subtitle, "this Mac · daemon not running")
    }

    func testSafeModeCompatibilityDoesNotGreenSelfRow() {
        let safeMode = LocalDaemonSafeModeStatus.fromPayload(
            status: "safe_mode_signed_repair",
            hostname: "me",
            configuredListen: "0.0.0.0:49123",
            effectiveRepairListen: "127.0.0.1:49123",
            parseError: nil,
            safeMode: true,
            safeModeSchema: "safe_mode_v1",
            listenerRepairStatus: "needs_repair",
            lastFailurePhase: "listener_safe_mode",
            safeModeLogsAvailable: true,
            peerSyncStarted: false,
            configVersion: 2,
            configRevision: 17,
            revisionState: "versioned",
            port: 49123
        )

        let rows = fleetRows(origin: "me", connected: true, peers: [peer("remote")], safeMode: safeMode)

        XCTAssertEqual(rows[0].health, .attention)
        XCTAssertEqual(rows[0].subtitle, "this Mac · listener repair required")
        XCTAssertEqual(rows.count, 1)
    }

    func testPeerRowsCarryPeer() {
        let p = peer("flower-garden")
        let rows = fleetRows(origin: "me", connected: true, peers: [p])
        XCTAssertEqual(rows[1].peer, p)
        XCTAssertFalse(rows[1].isSelf)
    }

    func testCurrentVersionProbeMakesPeerHealthy() {
        let stalePush = Peer(hostname: "flower-garden", port: 7853,
                             last_push_ts: Date(), last_push_ok: false,
                             last_push_err: "service restarted",
                             last_recv_ts: Date.distantPast)

        let rows = fleetRows(origin: "me",
                             connected: true,
                             peers: [stalePush],
                             peerVersions: ["flower-garden": .current("v0.3.5")],
                             policy: legacyPeerHTTPProbePolicy())

        XCTAssertEqual(rows[1].health, .healthy)
        XCTAssertEqual(rows[1].subtitle, "port 7853 · current")
    }

    func testVersionNeedingUpdateMakesPeerAttention() {
        let pushedOK = peer("old-box")

        let rows = fleetRows(origin: "me",
                             connected: true,
                             peers: [pushedOK],
                             peerVersions: ["old-box": .needsUpdate("v0.3.4")],
                             policy: legacyPeerHTTPProbePolicy())

        XCTAssertEqual(rows[1].health, .attention)
        XCTAssertEqual(rows[1].subtitle, "port 7853 · update available")
    }

    private func legacyPeerHTTPProbePolicy() -> SSHTransportGatePolicy {
        SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: false,
            configV2WriteEnabled: false,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: true,
            syncStreamEnabled: false,
            persistentCurrentEnabled: true,
            syncKeyRotationEnabled: false
        )
    }
}
