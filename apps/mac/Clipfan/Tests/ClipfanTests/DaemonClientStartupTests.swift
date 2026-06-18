import XCTest
@testable import Clipfan

@MainActor
final class DaemonClientStartupTests: XCTestCase {
    func testStartDoesNotScheduleBackgroundPolling() {
        DaemonClient.shared.start()

        let timer = Mirror(reflecting: DaemonClient.shared).descendant("timer")
        XCTAssertNil(timer)
    }
}
