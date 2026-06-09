import AppKit
import XCTest
@testable import Clipfan

final class ApplicationActivationControllerTests: XCTestCase {
    func testAccessoryWithoutVisibleClipfanWindow() {
        XCTAssertEqual(ApplicationActivationController.activationPolicy(hasVisibleClipfanWindow: false),
                       .accessory)
    }

    func testRegularWithVisibleClipfanWindow() {
        XCTAssertEqual(ApplicationActivationController.activationPolicy(hasVisibleClipfanWindow: true),
                       .regular)
    }

    func testVisibleRegularWindowCountsAsClipfanWindow() {
        XCTAssertTrue(ApplicationActivationController.isVisibleClipfanWindow(
            visible: true,
            miniaturized: false,
            panel: false,
            identifier: nil
        ))
    }

    func testVisibleCommandPanelCountsAsClipfanWindow() {
        XCTAssertTrue(ApplicationActivationController.isVisibleClipfanWindow(
            visible: true,
            miniaturized: false,
            panel: true,
            identifier: ApplicationActivationController.commandPanelWindowIdentifier
        ))
    }

    func testUnidentifiedPanelDoesNotCountAsClipfanWindow() {
        XCTAssertFalse(ApplicationActivationController.isVisibleClipfanWindow(
            visible: true,
            miniaturized: false,
            panel: true,
            identifier: nil
        ))
    }

    func testHiddenWindowDoesNotCountAsClipfanWindow() {
        XCTAssertFalse(ApplicationActivationController.isVisibleClipfanWindow(
            visible: false,
            miniaturized: false,
            panel: false,
            identifier: nil
        ))
    }
}
