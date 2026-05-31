import Foundation
import Sparkle

/// Owns the Sparkle updater for the app's lifetime. `startingUpdater: true` wires
/// up automatic background checks (governed by SUFeedURL / SUPublicEDKey in
/// Info.plist); `checkForUpdates()` is the manual entry point from the menu.
@MainActor
final class Updater {
    static let shared = Updater()

    private let controller: SPUStandardUpdaterController

    private init() {
        controller = SPUStandardUpdaterController(
            startingUpdater: true,
            updaterDelegate: nil,
            userDriverDelegate: nil
        )
    }

    func checkForUpdates() {
        controller.checkForUpdates(nil)
    }
}
