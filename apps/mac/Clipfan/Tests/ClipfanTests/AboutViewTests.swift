import XCTest
@testable import Clipfan

final class AboutViewTests: XCTestCase {
    func testAboutLinksToGitHub() {
        XCTAssertEqual(aboutGitHubURL.absoluteString, "https://github.com/prime-radiant-inc/clipfan")
    }

    func testAboutUsesBundledAppIconName() {
        XCTAssertEqual(aboutAppIconResourceName, "AppIcon")
    }

    func testVersionSummaryWithBoth() {
        XCTAssertEqual(aboutVersionSummary(appVersion: "0.3.29", daemonVersion: "0.3.29"),
                       "App 0.3.29 · Daemon 0.3.29")
    }
    func testVersionSummaryMissingDaemon() {
        XCTAssertEqual(aboutVersionSummary(appVersion: "0.3.29", daemonVersion: nil),
                       "App 0.3.29 · Daemon —")
    }
    func testVersionSummaryMissingApp() {
        XCTAssertEqual(aboutVersionSummary(appVersion: nil, daemonVersion: "0.3.29"),
                       "App — · Daemon 0.3.29")
    }
}
