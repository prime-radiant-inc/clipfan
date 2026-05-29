import XCTest
@testable import Clipfan

final class PanelSelectionTests: XCTestCase {
    private func ids(_ xs: [String]) -> [HistoryEntry] {
        xs.map { HistoryEntry(id: $0, kind: .text, preview: $0, text: $0,
                              imagePath: nil, sizeBytes: 1, origin: "m4",
                              ts: Date(timeIntervalSince1970: 0), pinned: false) }
    }

    func testMoveSelectionDown() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(movedSelection(from: "a", in: list, delta: 1), "b")
        XCTAssertEqual(movedSelection(from: "b", in: list, delta: 1), "c")
    }

    func testMoveSelectionUp() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(movedSelection(from: "c", in: list, delta: -1), "b")
    }

    func testMoveClampsAtEnds() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(movedSelection(from: "c", in: list, delta: 1), "c")
        XCTAssertEqual(movedSelection(from: "a", in: list, delta: -1), "a")
    }

    func testMoveFromNilSelectsFirst() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(movedSelection(from: nil, in: list, delta: 1), "a")
        XCTAssertEqual(movedSelection(from: nil, in: list, delta: -1), "a")
    }

    func testMoveInEmptyListIsNil() {
        XCTAssertNil(movedSelection(from: nil, in: ids([]), delta: 1))
    }

    func testIdForNumber() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(idForNumber(1, in: list), "a")
        XCTAssertEqual(idForNumber(3, in: list), "c")
    }

    func testIdForNumberOutOfRange() {
        let list = ids(["a", "b"])
        XCTAssertNil(idForNumber(3, in: list))
        XCTAssertNil(idForNumber(0, in: list))
    }

    func testClampedSelectionKeepsValid() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(clampedSelection("b", in: list), "b")
    }

    func testClampedSelectionFallsBackToFirst() {
        let list = ids(["a", "b", "c"])
        XCTAssertEqual(clampedSelection("gone", in: list), "a")
        XCTAssertEqual(clampedSelection(nil, in: list), "a")
        XCTAssertNil(clampedSelection("x", in: ids([])))
    }
}
