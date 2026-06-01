import XCTest
@testable import Clipfan

final class PeerUpdateAdvisorTests: XCTestCase {
    private func peer(_ hostname: String) -> Peer {
        Peer(hostname: hostname, port: 7853,
             last_push_ts: nil, last_push_ok: true,
             last_push_err: nil, last_recv_ts: nil)
    }

    func testClassifiesMatchingVersionAsCurrent() {
        XCTAssertEqual(
            PeerUpdateAdvisor.status(remoteVersion: "v0.3.4", localVersion: "0.3.4"),
            .current("v0.3.4")
        )
    }

    func testClassifiesDifferentVersionAsNeedsUpdate() {
        XCTAssertEqual(
            PeerUpdateAdvisor.status(remoteVersion: "v0.3.3", localVersion: "v0.3.4"),
            .needsUpdate("v0.3.3")
        )
    }

    func testUnknownPeerStatusNeedsUpdateBecauseOlderPeersHaveNoEndpoint() {
        let peers = [peer("old-box"), peer("current-box")]
        let needingUpdate = PeerUpdateAdvisor.peersNeedingUpdate(
            peers: peers,
            statuses: [
                "old-box": .unknown,
                "current-box": .current("v0.3.4"),
            ]
        )

        XCTAssertEqual(needingUpdate, [peers[0]])
    }

    func testProbeFailuresOnlyRecommendUpdateForMissingVersionEndpoint() {
        XCTAssertEqual(
            PeerUpdateAdvisor.status(forProbeError: ClipfanAuthenticationError.badStatus(404)),
            .unknown
        )
        XCTAssertNil(PeerUpdateAdvisor.status(forProbeError: URLError(.cannotConnectToHost)))
    }

    func testOfferIsOnlyShownOncePerLocalReleaseVersion() {
        let peers = [peer("old-box")]
        let statuses: [String: PeerVersionStatus] = ["old-box": .needsUpdate("v0.3.3")]

        XCTAssertTrue(PeerUpdateAdvisor.shouldOffer(
            localVersion: "v0.3.4",
            peers: peers,
            statuses: statuses,
            lastOfferedVersion: "v0.3.3"
        ))
        XCTAssertFalse(PeerUpdateAdvisor.shouldOffer(
            localVersion: "v0.3.4",
            peers: peers,
            statuses: statuses,
            lastOfferedVersion: "v0.3.4"
        ))
    }

    func testDevBuildsDoNotOfferPeerUpdates() {
        XCTAssertFalse(PeerUpdateAdvisor.shouldOffer(
            localVersion: "dev",
            peers: [peer("old-box")],
            statuses: ["old-box": .unknown],
            lastOfferedVersion: nil
        ))
    }
}
