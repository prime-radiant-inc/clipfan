import XCTest
@testable import Clipfan

final class HistoryFilterTests: XCTestCase {
    private func entry(_ id: String, _ kind: ClipKind, _ preview: String,
                       pinned: Bool = false, ts: TimeInterval = 0) -> HistoryEntry {
        HistoryEntry(id: id, kind: kind, preview: preview, text: preview,
                     imagePath: kind == .image ? "/x/\(id).png" : nil,
                     sizeBytes: 1, origin: "m4",
                     ts: Date(timeIntervalSince1970: ts), pinned: pinned)
    }

    func testSearchFiltersBySubstringCaseInsensitive() {
        let all = [entry("1", .text, "Hello World"), entry("2", .text, "goodbye")]
        let out = filteredHistory(all, search: "hello", typeFilter: .all)
        XCTAssertEqual(out.map(\.id), ["1"])
    }

    func testEmptySearchReturnsAll() {
        let all = [entry("1", .text, "a"), entry("2", .text, "b")]
        XCTAssertEqual(filteredHistory(all, search: "  ", typeFilter: .all).count, 2)
    }

    func testTypeFilter() {
        let all = [entry("1", .text, "t"), entry("2", .image, "i"), entry("3", .link, "https://x")]
        XCTAssertEqual(filteredHistory(all, search: "", typeFilter: .image).map(\.id), ["2"])
        XCTAssertEqual(filteredHistory(all, search: "", typeFilter: .link).map(\.id), ["3"])
        XCTAssertEqual(filteredHistory(all, search: "", typeFilter: .text).map(\.id), ["1"])
    }

    func testPinnedFloatToTopThenNewestFirst() {
        let all = [
            entry("old", .text, "a", pinned: false, ts: 10),
            entry("new", .text, "b", pinned: false, ts: 20),
            entry("pin", .text, "c", pinned: true, ts: 5),
        ]
        // pinned first (even though oldest), then unpinned newest-first
        XCTAssertEqual(filteredHistory(all, search: "", typeFilter: .all).map(\.id), ["pin", "new", "old"])
    }

    func testSearchMatchesTextNotJustPreview() {
        let e = HistoryEntry(id: "1", kind: .text, preview: "short", text: "a longer hidden body",
                             imagePath: nil, sizeBytes: 1, origin: "m4",
                             ts: Date(timeIntervalSince1970: 0), pinned: false)
        XCTAssertEqual(filteredHistory([e], search: "hidden", typeFilter: .all).map(\.id), ["1"])
    }

    func testTypeFilterLabels() {
        XCTAssertEqual(TypeFilter.allCases.map(\.label), ["All", "Text", "Image", "Link"])
    }
}
