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
