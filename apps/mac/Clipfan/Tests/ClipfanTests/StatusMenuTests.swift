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

    func testMenuBarCommandRowsCarryTextAndShortcutOnly() {
        XCTAssertEqual(statusMenuCommandRows(toggleShortcutLabel: "⌘⇧V"), [
            StatusMenuCommandRow(command: .openClipboard, title: "Open Clipboard", shortcut: "⌘⇧V"),
            StatusMenuCommandRow(command: .settings, title: "Settings…", shortcut: "⌘,"),
            StatusMenuCommandRow(command: .quit, title: "Quit", shortcut: "⌘Q"),
        ])
    }

    func testCheckForUpdatesLivesInGeneralSettings() {
        XCTAssertEqual(GeneralSettingsAction.checkForUpdates.title, "Check for Updates…")
    }
}
