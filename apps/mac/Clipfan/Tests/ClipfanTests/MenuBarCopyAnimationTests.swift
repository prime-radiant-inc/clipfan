import XCTest
@testable import Clipfan

final class MenuBarCopyAnimationTests: XCTestCase {
    func testMenuBarArtworkIsTemplateImage() {
        let image = ClipfanMenuBarIconArtwork.stackImage()

        XCTAssertTrue(image.isTemplate)
        XCTAssertGreaterThan(image.size.width, 0)
        XCTAssertGreaterThan(image.size.height, 0)
    }

    func testAppIconArtworkUsesMenuBarFanCardSlots() {
        XCTAssertEqual(ClipfanMenuBarIconArtwork.appIconCardSlots, MenuBarFanCardSlot.steady)
    }

    func testAppIconArtworkFlipsRotationForCoreGraphicsCoordinates() {
        XCTAssertEqual(ClipfanMenuBarIconArtwork.coreGraphicsRotation(for: .back), -MenuBarFanCardSlot.back.rotation)
        XCTAssertEqual(ClipfanMenuBarIconArtwork.coreGraphicsRotation(for: .middle), -MenuBarFanCardSlot.middle.rotation)
        XCTAssertEqual(ClipfanMenuBarIconArtwork.coreGraphicsRotation(for: .front), -MenuBarFanCardSlot.front.rotation)
    }

    func testAppIconArtworkIsNonTemplateImage() {
        let image = ClipfanMenuBarIconArtwork.appIconImage(size: 128)

        XCTAssertFalse(image.isTemplate)
        XCTAssertEqual(image.size.width, 128)
        XCTAssertEqual(image.size.height, 128)
    }

    func testFanInsertAnimationUsesQuickMenuBarTiming() {
        let timing = MenuBarFanInsertTiming.quickMenuBar

        XCTAssertEqual(timing.duration, 0.82, accuracy: 0.001)
        XCTAssertEqual(timing.frontDelay, 0.13, accuracy: 0.001)
        XCTAssertEqual(timing.middleDelay, 0.26, accuracy: 0.001)
        XCTAssertEqual(timing.backDelay, 0.43, accuracy: 0.001)
    }

    func testFanInsertCardsShareTopEdge() {
        let topEdges = [
            MenuBarFanCardSlot.incoming,
            MenuBarFanCardSlot.back,
            MenuBarFanCardSlot.middle,
            MenuBarFanCardSlot.front,
            MenuBarFanCardSlot.discarded,
        ].map(\.topY)

        XCTAssertEqual(Set(topEdges).count, 1)
        XCTAssertEqual(topEdges.first, MenuBarFanCardSlot.topY)
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
