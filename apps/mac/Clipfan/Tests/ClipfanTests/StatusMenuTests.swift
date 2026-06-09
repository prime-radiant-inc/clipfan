import XCTest
@testable import Clipfan

final class StatusMenuTests: XCTestCase {
    func testMenuBarCommandsStayFocusedOnDailyActions() {
        XCTAssertEqual(StatusMenuCommand.allCases.map(\.title), [
            "Open Clipboard",
            "Settings…",
            "Quit",
        ])
    }

    func testCheckForUpdatesLivesInGeneralSettings() {
        XCTAssertEqual(GeneralSettingsAction.checkForUpdates.title, "Check for Updates…")
    }
}
