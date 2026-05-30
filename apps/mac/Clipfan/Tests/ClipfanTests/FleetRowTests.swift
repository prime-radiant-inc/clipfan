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

    func testPeerRowsCarryPeer() {
        let p = peer("flower-garden")
        let rows = fleetRows(origin: "me", connected: true, peers: [p])
        XCTAssertEqual(rows[1].peer, p)
        XCTAssertFalse(rows[1].isSelf)
    }
}
