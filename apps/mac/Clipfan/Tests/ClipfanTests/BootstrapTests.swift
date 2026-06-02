import XCTest
@testable import Clipfan

final class BootstrapDecisionTests: XCTestCase {
    func testHealthyDaemonLaunchesNormally() {
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: true, daemonHealthy: true, installedBinaryCurrent: true), .normal)
        // Healthy wins even if we somehow think the binary is absent.
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: false, daemonHealthy: true, installedBinaryCurrent: false), .normal)
    }

    func testInstalledButDownRestartsExisting() {
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: true, daemonHealthy: false, installedBinaryCurrent: true), .restartExisting)
    }

    func testConfigV2BlocksRestartOfInstalledBinaryWithoutCapability() {
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: true,
                                             daemonHealthy: false,
                                             installedBinaryCurrent: true,
                                             configV2Present: true,
                                             installedBinarySupportsConfigV2: false),
                       .blockedDowngrade)
    }

    func testConfigV2CanUpgradeOutdatedBinaryBeforeRestart() {
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: true,
                                             daemonHealthy: false,
                                             installedBinaryCurrent: false,
                                             configV2Present: true,
                                             installedBinarySupportsConfigV2: false),
                       .upgradeExisting)
    }

    func testNotInstalledTriggersFirstRunInstall() {
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: false, daemonHealthy: false, installedBinaryCurrent: false), .firstRunInstall)
    }

    func testOutdatedInstalledDaemonUpgradesBeforeNormalLaunch() {
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: true, daemonHealthy: true, installedBinaryCurrent: false), .upgradeExisting)
    }

    func testOutdatedInstalledDaemonUpgradesBeforeRestart() {
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: true, daemonHealthy: false, installedBinaryCurrent: false), .upgradeExisting)
    }
}

final class BootstrapInstallTests: XCTestCase {
    func testUpgradeInstallDoesNotTouchTmuxConfig() {
        XCTAssertEqual(Bootstrap.installerArguments(mode: .upgradeExisting), ["--no-tmux"])
        XCTAssertEqual(Bootstrap.installerArguments(mode: .setup), [])
    }

    func testFileComparisonDetectsOutdatedInstalledBinary() throws {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-bootstrap-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }

        let installed = dir.appendingPathComponent("installed")
        let bundled = dir.appendingPathComponent("bundled")
        try Data("old".utf8).write(to: installed)
        try Data("new".utf8).write(to: bundled)

        XCTAssertFalse(Bootstrap.filesEqual(installed, bundled))
        try Data("new".utf8).write(to: installed)
        XCTAssertTrue(Bootstrap.filesEqual(installed, bundled))
    }

    func testMatchingReportedDaemonVersionIsCurrentEvenWhenBinariesDiffer() throws {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-bootstrap-version-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }

        let installed = try writeVersionScript(dir.appendingPathComponent("installed"),
                                               version: "v0.3.7",
                                               marker: "installed")
        let bundled = try writeVersionScript(dir.appendingPathComponent("bundled"),
                                             version: "v0.3.7",
                                             marker: "bundled")

        XCTAssertTrue(Bootstrap.installedBinaryCurrent(installed: installed, bundled: bundled))
        XCTAssertFalse(Bootstrap.filesEqual(installed, bundled))
    }

    func testInstalledBinarySupportsConfigV2ParsesVersionJSONCapability() throws {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-bootstrap-capability-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }

        let binary = try writeCapabilityScript(dir.appendingPathComponent("clipfan"), supportsConfigV2: true)

        XCTAssertTrue(Bootstrap.installedBinarySupportsConfigV2(binary: binary))
    }

    func testInstalledBinarySupportsConfigV2RejectsOldVersionCommand() throws {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-bootstrap-old-capability-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }

        let binary = try writeVersionScript(dir.appendingPathComponent("clipfan"),
                                            version: "v0.3.7",
                                            marker: "old")

        XCTAssertFalse(Bootstrap.installedBinarySupportsConfigV2(binary: binary))
    }

    func testConfigV2PresentDetectsVersionedConfig() throws {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-bootstrap-config-v2-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let configURL = dir.appendingPathComponent("config.json")
        try Data(#"{"config_version":2,"config_revision":1}"#.utf8).write(to: configURL)

        XCTAssertTrue(Bootstrap.configV2Present(configURL: configURL))
    }

    func testConfigV2CapabilityProbeOnlyRunsForCurrentBinaryAndV2Config() {
        XCTAssertFalse(Bootstrap.needsConfigV2CapabilityProbe(binaryInstalled: true,
                                                              installedBinaryCurrent: true,
                                                              configV2Present: false))
        XCTAssertFalse(Bootstrap.needsConfigV2CapabilityProbe(binaryInstalled: true,
                                                              installedBinaryCurrent: false,
                                                              configV2Present: true))
        XCTAssertFalse(Bootstrap.needsConfigV2CapabilityProbe(binaryInstalled: false,
                                                              installedBinaryCurrent: true,
                                                              configV2Present: true))
        XCTAssertTrue(Bootstrap.needsConfigV2CapabilityProbe(binaryInstalled: true,
                                                             installedBinaryCurrent: true,
                                                             configV2Present: true))
    }

    private func writeVersionScript(_ url: URL, version: String, marker: String) throws -> URL {
        let script = """
        #!/usr/bin/env bash
        if [[ "$1" == "version" ]]; then
          echo "\(version)"
        else
          echo "\(marker)"
        fi
        """
        try script.write(to: url, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755],
                                              ofItemAtPath: url.path)
        return url
    }

    private func writeCapabilityScript(_ url: URL, supportsConfigV2: Bool) throws -> URL {
        let configV2 = supportsConfigV2 ? "true" : "false"
        let script = """
        #!/usr/bin/env bash
        if [[ "$1" == "version" && "$2" == "--json" ]]; then
          echo '{"version":"v0.3.8","capabilities":{"config_v2":\(configV2)}}'
        elif [[ "$1" == "version" ]]; then
          echo "v0.3.8"
        else
          exit 2
        fi
        """
        try script.write(to: url, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755],
                                              ofItemAtPath: url.path)
        return url
    }
}

final class LocalNetworkNudgeTests: XCTestCase {
    private func peer(pushOK: Bool, pushTS: Date?, recvTS: Date?) -> Peer {
        Peer(hostname: "m4", port: 7853,
             last_push_ts: pushTS, last_push_ok: pushOK,
             last_push_err: pushOK ? nil : "dial tcp 192.168.1.9:7853: connect: no route",
             last_recv_ts: recvTS)
    }

    func testNoPeersNeverNudges() {
        XCTAssertFalse(shouldPromptLocalNetwork(peers: []))
    }

    func testHealthyPeerDoesNotNudge() {
        let p = peer(pushOK: true, pushTS: Date(), recvTS: Date())
        XCTAssertFalse(shouldPromptLocalNetwork(peers: [p]))
    }

    func testIdlePeerNeverPushedDoesNotNudge() {
        // No push has been attempted yet (idle/gray) — not evidence of a block.
        let p = peer(pushOK: false, pushTS: nil, recvTS: nil)
        XCTAssertFalse(shouldPromptLocalNetwork(peers: [p]))
        let sentinel = peer(pushOK: false, pushTS: .distantPast, recvTS: .distantPast)
        XCTAssertFalse(shouldPromptLocalNetwork(peers: [sentinel]))
    }

    func testPushAttemptedAndFailingNudges() {
        // A real push attempt that is failing — the Local Network symptom.
        let p = peer(pushOK: false, pushTS: Date(), recvTS: nil)
        XCTAssertTrue(shouldPromptLocalNetwork(peers: [p]))
    }

    func testAnyFailingPeerAmongHealthyNudges() {
        let healthy = peer(pushOK: true, pushTS: Date(), recvTS: Date())
        let failing = peer(pushOK: false, pushTS: Date(), recvTS: nil)
        XCTAssertTrue(shouldPromptLocalNetwork(peers: [healthy, failing]))
    }
}

final class SetupStateTests: XCTestCase {
    func testAppendingProgressAccumulatesLines() {
        var s = SetupState.installing(progress: ["Probe"])
        s = s.appendingProgress("Install")
        XCTAssertEqual(s, .installing(progress: ["Probe", "Install"]))
    }

    func testAppendingProgressFromIdleStartsInstalling() {
        let s = SetupState.idle.appendingProgress("Probe")
        XCTAssertEqual(s, .installing(progress: ["Probe"]))
    }

    func testAppendingProgressLeavesTerminalStatesUnchanged() {
        XCTAssertEqual(SetupState.success.appendingProgress("x"), .success)
        let failed = SetupState.failed(message: "boom", logPath: "/tmp/x.log")
        XCTAssertEqual(failed.appendingProgress("x"), failed)
    }
}
