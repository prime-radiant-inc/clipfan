import XCTest
@testable import Clipfan

final class LocalStorageRepairTests: XCTestCase {
    func testUnsupportedStoragePromptRequiresNoDaemonEndpoint() throws {
        let prompt = try XCTUnwrap(LocalStorageRepair.prompt(
            code: "unsupported_runtime_storage",
            roots: [
                LocalStorageRepairRoot(role: "state",
                                       normalizedPath: "/Users/me/Dropbox/clipfan",
                                       storageClass: "cloud_sync",
                                       reason: "cloud_sync_root")
            ]
        ))

        XCTAssertEqual(prompt.code, .unsupportedRuntimeStorage)
        XCTAssertFalse(prompt.sshTransportEnabled)
        XCTAssertFalse(prompt.requiresDaemonEndpoint)
        XCTAssertTrue(prompt.message.contains("local storage"))
        XCTAssertEqual(prompt.roots[0].normalizedPath, "/Users/me/Dropbox/clipfan")
    }

    func testInconclusivePromptCanBeDerivedFromDaemonFailureText() throws {
        let prompt = try XCTUnwrap(LocalStorageRepair.prompt(
            message: """
            code: storage_check_inconclusive
            ssh_transport_enabled: false
            daemon_endpoint_required: false
            - state: /Users/me/.local/state/clipfan (inconclusive) reason=unknown_filesystem
            """
        ))

        XCTAssertEqual(prompt.code, .storageCheckInconclusive)
        XCTAssertFalse(prompt.sshTransportEnabled)
        XCTAssertFalse(prompt.requiresDaemonEndpoint)
        XCTAssertTrue(prompt.message.contains("offline storage check"))
        XCTAssertEqual(prompt.roots, [
            LocalStorageRepairRoot(role: "state",
                                   normalizedPath: "/Users/me/.local/state/clipfan",
                                   storageClass: "inconclusive",
                                   reason: "unknown_filesystem")
        ])
    }

    func testUnknownFailureTextDoesNotCreateStorageRepairPrompt() {
        XCTAssertNil(LocalStorageRepair.prompt(message: "launchd failed"))
    }

    func testBootstrapStoragePreflightRepairPromptRunsOfflineCommand() async throws {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-storage-preflight-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let script = dir.appendingPathComponent("clipfan")
        try """
        #!/usr/bin/env bash
        if [[ "$1" != "storage-preflight" ]]; then
          exit 7
        fi
        echo "code: unsupported_runtime_storage" >&2
        echo "ssh_transport_enabled: false" >&2
        echo "daemon_endpoint_required: false" >&2
        echo "- config: /Users/me/Dropbox/clipfan (cloud_sync) reason=cloud_sync_root" >&2
        exit 1
        """.write(to: script, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755],
                                              ofItemAtPath: script.path)

        let maybePrompt = await Bootstrap.storagePreflightRepairPrompt(binary: script)
        let prompt = try XCTUnwrap(maybePrompt)
        XCTAssertEqual(prompt.code, .unsupportedRuntimeStorage)
        XCTAssertFalse(prompt.requiresDaemonEndpoint)
        XCTAssertEqual(prompt.roots, [
            LocalStorageRepairRoot(role: "config",
                                   normalizedPath: "/Users/me/Dropbox/clipfan",
                                   storageClass: "cloud_sync",
                                   reason: "cloud_sync_root")
        ])
        XCTAssertTrue(Bootstrap.storageRepairFailureMessage(prompt).contains("unsupported_runtime_storage"))
    }
}
