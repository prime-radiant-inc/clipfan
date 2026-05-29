import XCTest
@testable import Clipfan

final class ClipMetadataTests: XCTestCase {
    func testHumanSizeBytes() {
        XCTAssertEqual(humanSize(0), "0 B")
        XCTAssertEqual(humanSize(13), "13 B")
        XCTAssertEqual(humanSize(1023), "1023 B")
    }

    func testHumanSizeKBandMB() {
        XCTAssertEqual(humanSize(1234), "1 KB")        // 1234/1024 = 1.2 -> 1
        XCTAssertEqual(humanSize(248_213), "242 KB")   // 248213/1024 = 242.4 -> 242
        XCTAssertEqual(humanSize(5 * 1024 * 1024), "5 MB")
    }

    func testFormatDimensions() {
        XCTAssertEqual(formatDimensions(width: 1920, height: 1080), "1920×1080")
        XCTAssertEqual(formatDimensions(width: 0, height: 0), "0×0")
    }

    func testMonospacePreferredForPaths() {
        XCTAssertTrue(isMonospacePreferred("/Users/jesse/.config/clipfan/config.json"))
        XCTAssertTrue(isMonospacePreferred("~/.ssh/id_ed25519"))
    }

    func testMonospacePreferredForWindowsPath() {
        XCTAssertTrue(isMonospacePreferred(#"C:\Users\jesse\Documents"#))
    }

    func testMonospacePreferredForCode() {
        XCTAssertTrue(isMonospacePreferred("func chooseBackend(wayland string) {"))
        XCTAssertTrue(isMonospacePreferred("brew install --cask clipfan"))
    }

    func testMonospaceNotPreferredForProse() {
        XCTAssertFalse(isMonospacePreferred("Remember to call the dentist tomorrow"))
        XCTAssertFalse(isMonospacePreferred("jesse@fsck.com"))
    }
}
