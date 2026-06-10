import Combine
import Foundation
import Sparkle

/// Owns the Sparkle updater for the app's lifetime. `startingUpdater: true` wires
/// up automatic background checks (governed by SUFeedURL / SUPublicEDKey in
/// Info.plist); `checkForUpdates()` is the manual entry point from the menu.
@MainActor
final class Updater: NSObject, ObservableObject {
    static let shared = Updater()

    @Published private(set) var isUpdateAvailable = false

    private var availability = AppUpdateAvailability()
    private var controller: SPUStandardUpdaterController!

    private override init() {
        super.init()
        controller = SPUStandardUpdaterController(
            startingUpdater: true,
            updaterDelegate: self,
            userDriverDelegate: self
        )
    }

    func checkForUpdates() {
        controller.checkForUpdates(nil)
    }

    func presentAvailableUpdate() {
        controller.checkForUpdates(nil)
    }

    func recommendUpdateAtStartup() {
        updateAvailability { $0.prepareStartupRecommendation() }
        controller.updater.checkForUpdateInformation()
    }

    private func updateAvailability(_ update: (inout AppUpdateAvailability) -> Void) {
        update(&availability)
        isUpdateAvailable = availability.isUpdateAvailable
    }
}

extension Updater: SPUUpdaterDelegate {
    func updater(_ updater: SPUUpdater, didFindValidUpdate item: SUAppcastItem) {
        updateAvailability { $0.noteUpdateFound() }
    }

    func updaterDidNotFindUpdate(_ updater: SPUUpdater) {
        updateAvailability { $0.noteUpdateNotFound() }
    }

    func updater(_ updater: SPUUpdater, didFinishUpdateCycleFor updateCheck: SPUUpdateCheck, error: Error?) {
        guard updateCheck == .updateInformation else { return }

        let shouldRecommend = availability.finishStartupProbeShouldRecommend()
        isUpdateAvailable = availability.isUpdateAvailable
        if shouldRecommend {
            presentAvailableUpdate()
        }
    }
}

extension Updater: SPUStandardUserDriverDelegate {}
