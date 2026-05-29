import XCTest
@testable import Clipfan

final class HistoryEntryTests: XCTestCase {
    func testDecodeEntries() throws {
        let json = """
        {"entries":[
          {"id":"a1","kind":"image","preview":"shot.png","image_path":"/s/a1.png","size_bytes":1234,"origin":"magic-kingdom","ts":"2026-05-28T10:00:00Z","pinned":false},
          {"id":"b2","kind":"link","preview":"https://x.com","text":"https://x.com","size_bytes":13,"origin":"m4","ts":"2026-05-28T10:01:00Z","pinned":true}
        ]}
        """.data(using: .utf8)!
        let resp = try JSONDecoder.clipfan.decode(HistoryResponse.self, from: json)
        XCTAssertEqual(resp.entries.count, 2)
        XCTAssertEqual(resp.entries[0].kind, .image)
        XCTAssertEqual(resp.entries[0].origin, "magic-kingdom")
        XCTAssertEqual(resp.entries[0].imagePath, "/s/a1.png")
        XCTAssertNil(resp.entries[0].text)
        XCTAssertEqual(resp.entries[1].kind, .link)
        XCTAssertEqual(resp.entries[1].text, "https://x.com")
        XCTAssertTrue(resp.entries[1].pinned)
    }
}
