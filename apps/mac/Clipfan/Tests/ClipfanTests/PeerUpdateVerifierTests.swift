import XCTest
@testable import Clipfan

final class PeerUpdateVerifierTests: XCTestCase {
    func testRetriesStaleDaemonVersionUntilExpectedInstalledVersionAnswers() async {
        var replies = ["v0.3.4", "v0.3.5"]
        var attempts = 0

        let result = await PeerUpdateVerifier.verify(
            host: "fsck.com",
            port: 7853,
            key: Data("shared-key".utf8),
            expectedVersion: "v0.3.5",
            attempts: 3,
            delayNanoseconds: 0,
            fetch: { _, _, _ in
                attempts += 1
                return replies.removeFirst()
            }
        )

        XCTAssertEqual(result.status, .current("v0.3.5"))
        XCTAssertEqual(result.detail, "fsck.com is running v0.3.5")
        XCTAssertEqual(attempts, 2)
    }

    func testReportsLastObservedVersionWhenPeerNeverReachesExpectedVersion() async {
        let result = await PeerUpdateVerifier.verify(
            host: "fsck.com",
            port: 7853,
            key: Data("shared-key".utf8),
            expectedVersion: "v0.3.5",
            attempts: 2,
            delayNanoseconds: 0,
            fetch: { _, _, _ in "v0.3.4" }
        )

        XCTAssertEqual(result.status, .needsUpdate("v0.3.4"))
        XCTAssertEqual(result.detail, "fsck.com answered with v0.3.4; expected v0.3.5")
    }

    func testUpdateSheetOnlyDismissesAfterVerifiedCurrentDaemon() {
        XCTAssertTrue(shouldDismissPeerUpdateSheet(
            PeerUpdateVerificationResult(status: .current("v0.3.5"),
                                         detail: "fsck.com is running v0.3.5")
        ))
        XCTAssertFalse(shouldDismissPeerUpdateSheet(
            PeerUpdateVerificationResult(status: .needsUpdate("v0.3.4"),
                                         detail: "fsck.com answered with v0.3.4; expected v0.3.5")
        ))
        XCTAssertFalse(shouldDismissPeerUpdateSheet(nil))
    }
}
