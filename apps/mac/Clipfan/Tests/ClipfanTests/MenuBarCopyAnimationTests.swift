import XCTest
@testable import Clipfan

final class MenuBarCopyAnimationTests: XCTestCase {
    func testInitialLoadedHistoryDoesNotAnimate() {
        var tracker = MenuBarCopyAnimationTracker()

        tracker.seedInitialHistory("existing")

        XCTAssertFalse(tracker.shouldAnimate(latestHistoryID: "existing"))
    }

    func testNewHistoryIDAnimatesAfterInitialLoad() {
        var tracker = MenuBarCopyAnimationTracker()

        tracker.seedInitialHistory("old")

        XCTAssertTrue(tracker.shouldAnimate(latestHistoryID: "new"))
        XCTAssertFalse(tracker.shouldAnimate(latestHistoryID: "new"))
    }

    func testFirstCopyAfterEmptyInitialHistoryAnimates() {
        var tracker = MenuBarCopyAnimationTracker()

        tracker.seedInitialHistory(nil)

        XCTAssertTrue(tracker.shouldAnimate(latestHistoryID: "first"))
    }

    func testUnprimedTrackerDoesNotAnimate() {
        var tracker = MenuBarCopyAnimationTracker()

        XCTAssertFalse(tracker.shouldAnimate(latestHistoryID: "loaded-later"))
    }
}

