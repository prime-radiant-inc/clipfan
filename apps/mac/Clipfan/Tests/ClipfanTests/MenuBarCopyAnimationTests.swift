import XCTest
@testable import Clipfan

final class MenuBarCopyAnimationTests: XCTestCase {
    func testMenuBarArtworkIsTemplateImage() {
        let image = ClipfanMenuBarIconArtwork.stackImage()

        XCTAssertTrue(image.isTemplate)
        XCTAssertGreaterThan(image.size.width, 0)
        XCTAssertGreaterThan(image.size.height, 0)
    }

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
