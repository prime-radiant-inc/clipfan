import XCTest
@testable import Clipfan

final class SafeModeStatusTests: XCTestCase {
    func testDecodeStatusResponseSafeModePayload() throws {
        let json = """
        {
          "status":"safe_mode_signed_repair",
          "hostname":"paradise-park",
          "configured_listen":"0.0.0.0:49123",
          "effective_repair_listen":"127.0.0.1:49123",
          "parse_error":"public_listen_requires_confirmation",
          "safe_mode":true,
          "safe_mode_schema":"safe_mode_v1",
          "listener_repair_status":"needs_repair",
          "last_failure_phase":"listener_safe_mode",
          "safe_mode_logs_available":true,
          "peer_sync_started":false,
          "config_version":2,
          "config_revision":42
        }
        """.data(using: .utf8)!

        let status = try JSONDecoder.clipfan.decode(LocalDaemonStatusResponse.self, from: json)

        XCTAssertTrue(status.safeMode?.active == true)
        XCTAssertEqual(status.safeMode?.hostname, "paradise-park")
        XCTAssertEqual(status.safeMode?.configuredListen, "0.0.0.0:49123")
        XCTAssertEqual(status.safeMode?.effectiveRepairListen, "127.0.0.1:49123")
        XCTAssertEqual(status.safeMode?.listenerRepairStatus, "needs_repair")
        XCTAssertEqual(status.safeMode?.lastFailurePhase, "listener_safe_mode")
        XCTAssertEqual(status.safeMode?.safeModeLogsAvailable, true)
        XCTAssertEqual(status.safeMode?.peerSyncStarted, false)
        XCTAssertEqual(status.safeMode?.configRevision, 42)
        XCTAssertEqual(status.safeMode?.listenerIsLoopback, false)
    }

    func testSafeModeLogFormattingIsStableAndCopyable() throws {
        let json = """
        {
          "peer_id":"local",
          "safe_mode":true,
          "safe_mode_schema":"safe_mode_v1",
          "listener_repair_status":"needs_repair",
          "last_failure_phase":"listener_safe_mode",
          "safe_mode_logs_available":true,
          "entries":[
            {"ts":"2026-06-02T12:34:56Z","source":"listener_repair","durable":false,"log_id":"safe-mode-listener","phase":"listener_safe_mode","code":"public_listen_requires_confirmation","message":"Configured listener requires local repair before peer sync can start."},
            {"source":"remediation","durable":false,"log_id":"legacy-static-peer-0","phase":"legacy_static_peer","code":"ssh_setup_required","message":"Static peer requires SSH setup before sync."}
          ],
          "truncated":false
        }
        """.data(using: .utf8)!

        let response = try JSONDecoder.clipfan.decode(LocalDaemonSafeModeLogResponse.self, from: json)

        XCTAssertEqual(response.formattedLog, """
        2026-06-02T12:34:56Z [listener_repair/listener_safe_mode] public_listen_requires_confirmation Configured listener requires local repair before peer sync can start.
        [remediation/legacy_static_peer] ssh_setup_required Static peer requires SSH setup before sync.
        """)
    }

    func testSafeModeStatusIgnoresUnknownSSHRuntimeFields() throws {
        let json = """
        {
          "status":"safe_mode_signed_repair",
          "safe_mode":true,
          "safe_mode_schema":"safe_mode_v1",
          "peer_sync_started":false,
          "ssh":{"peers":[{"id":"not-yet"}]},
          "runtime_health":{"state":"not-yet"},
          "migration_state":"not-yet"
        }
        """.data(using: .utf8)!

        let status = try JSONDecoder.clipfan.decode(LocalDaemonStatusResponse.self, from: json)

        XCTAssertEqual(status.safeMode?.safeModeSchema, "safe_mode_v1")
        XCTAssertEqual(status.safeMode?.peerSyncStarted, false)
    }
}
