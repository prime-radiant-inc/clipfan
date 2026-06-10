import XCTest
@testable import Clipfan

final class StatusMenuTests: XCTestCase {
    func testMenuBarCommandsStayFocusedOnDailyActions() {
        XCTAssertEqual(statusMenuCommandRows(toggleShortcutLabel: "").map(\.title), [
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

    func testInstallUpdateCommandAppearsWhenAppUpdateIsAvailable() {
        XCTAssertEqual(statusMenuCommandRows(toggleShortcutLabel: "⌘⇧V", appUpdateAvailable: true), [
            StatusMenuCommandRow(command: .openClipboard, title: "Open Clipboard", shortcut: "⌘⇧V"),
            StatusMenuCommandRow(command: .installUpdate, title: "Install Update…", shortcut: ""),
            StatusMenuCommandRow(command: .settings, title: "Settings…", shortcut: "⌘,"),
            StatusMenuCommandRow(command: .quit, title: "Quit", shortcut: "⌘Q"),
        ])
    }

    func testCheckForUpdatesLivesInGeneralSettings() {
        XCTAssertEqual(GeneralSettingsAction.checkForUpdates.title, "Check for Updates…")
    }

    func testStartupUpdateRecommendationRequiresAnAvailableUpdate() {
        var state = AppUpdateAvailability()

        state.prepareStartupRecommendation()
        XCTAssertFalse(state.finishStartupProbeShouldRecommend())

        state.prepareStartupRecommendation()
        state.noteUpdateFound()
        XCTAssertTrue(state.finishStartupProbeShouldRecommend())
        XCTAssertFalse(state.finishStartupProbeShouldRecommend())
    }
}
