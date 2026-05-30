import XCTest
@testable import Clipfan

final class BootstrapDecisionTests: XCTestCase {
    func testHealthyDaemonLaunchesNormally() {
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: true, daemonHealthy: true), .normal)
        // Healthy wins even if we somehow think the binary is absent.
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: false, daemonHealthy: true), .normal)
    }

    func testInstalledButDownRestartsExisting() {
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: true, daemonHealthy: false), .restartExisting)
    }

    func testNotInstalledTriggersFirstRunInstall() {
        XCTAssertEqual(LaunchDecision.decide(binaryInstalled: false, daemonHealthy: false), .firstRunInstall)
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
