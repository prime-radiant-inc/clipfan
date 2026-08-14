import XCTest
@testable import Clipfan

@MainActor
final class DaemonClientTests: XCTestCase {
    func testLaunchdRestartArgumentsRepairAndRestartTheUserService() {
        XCTAssertEqual(
            DaemonClient.launchdRestartArguments(uid: "501", plistPath: "/tmp/com.primeradiant.clipfan.plist"),
            [
                ["enable", "gui/501/com.primeradiant.clipfan"],
                ["bootstrap", "gui/501", "/tmp/com.primeradiant.clipfan.plist"],
                ["load", "/tmp/com.primeradiant.clipfan.plist"],
                ["kickstart", "-k", "gui/501/com.primeradiant.clipfan"]
            ]
        )
    }
}
