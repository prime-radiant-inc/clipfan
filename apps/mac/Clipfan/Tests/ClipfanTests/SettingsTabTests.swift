import XCTest
@testable import Clipfan

final class SettingsTabTests: XCTestCase {
    func testAboutTabIsPresentWithIcon() {
        XCTAssertTrue(SettingsView.Tab.allCases.contains(.about),
                      "About should be a Settings pane")
        for tab in SettingsView.Tab.allCases {
            XCTAssertFalse(tab.systemImage.isEmpty, "\(tab) is missing a systemImage")
        }
    }

    @MainActor
    func testSettingsRouteDefaultsToFleet() {
        XCTAssertEqual(SettingsRoute().tab, .fleet)
    }
}
