import XCTest
@testable import Clipfan

final class AddPeerSheetButtonsTests: XCTestCase {
    func testSuccessShowsDoneAndAddAnother() {
        XCTAssertEqual(addPeerSheetButtons(installing: false, installSuccess: true, hasFailure: false), .success)
    }
    func testEditingByDefault() {
        XCTAssertEqual(addPeerSheetButtons(installing: false, installSuccess: false, hasFailure: false), .editing)
    }
    func testInstallingState() {
        XCTAssertEqual(addPeerSheetButtons(installing: true, installSuccess: false, hasFailure: false), .installing)
    }
    func testFailureState() {
        XCTAssertEqual(addPeerSheetButtons(installing: false, installSuccess: false, hasFailure: true), .failed)
    }
    func testSuccessWinsOverFailure() {
        XCTAssertEqual(addPeerSheetButtons(installing: false, installSuccess: true, hasFailure: true), .success)
    }
}
