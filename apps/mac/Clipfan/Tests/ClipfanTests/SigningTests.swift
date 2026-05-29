import XCTest
import CryptoKit
@testable import Clipfan

final class SigningTests: XCTestCase {
    // Raw key = ASCII "0123456789abcdef0123456789abcdef" (32 bytes).
    private let rawKey = Data("0123456789abcdef0123456789abcdef".utf8)

    func testSignEmptyBodyMatchesGo() {
        let sig = clipfanSign(body: Data(), key: rawKey)
        XCTAssertEqual(sig, "796cd3078af14636753d26b3b5555422ff55a3e261cf847b48e95371b9bd0aa2")
    }

    func testSignBodyMatchesGo() {
        let sig = clipfanSign(body: Data(#"{"id":"abc"}"#.utf8), key: rawKey)
        XCTAssertEqual(sig, "45e60cb68a96381d8441f775dbdc19811bbfcf853d036a33599da3e55841df72")
    }

    func testLoadKeyDecodesBase64() throws {
        // Write a temp config with the base64 of rawKey and confirm it decodes to rawKey.
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let cfg = dir.appendingPathComponent("config.json")
        try #"{"shared_key":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}"#.data(using: .utf8)!.write(to: cfg)
        let key = try XCTUnwrap(loadSharedKey(configPath: cfg))
        XCTAssertEqual(key, rawKey)
    }
}
