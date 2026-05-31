import XCTest
import CryptoKit
@testable import Clipfan

final class SigningTests: XCTestCase {
    // Raw key = ASCII "0123456789abcdef0123456789abcdef" (32 bytes).
    private let rawKey = Data("0123456789abcdef0123456789abcdef".utf8)

    func testSignHistoryRequestMatchesGo() {
        let sig = clipfanSign(
            method: "GET",
            requestURI: "/v1/history?limit=10",
            timestamp: "1780257600",
            nonce: "nonce-1",
            body: Data(),
            key: rawKey
        )
        XCTAssertEqual(sig, "4653ff5ab124b2cc35fcee57709494f8a9b09c0981389654893d513a84191c40")
    }

    func testSignRestoreRequestMatchesGo() {
        let sig = clipfanSign(
            method: "POST",
            requestURI: "/v1/restore",
            timestamp: "1780257600",
            nonce: "nonce-2",
            body: Data(#"{"id":"abc"}"#.utf8),
            key: rawKey
        )
        XCTAssertEqual(sig, "bc45df25bb616a886c831c26136fcfd38957c355d800179cfa39e53cc5c15086")
    }

    func testSignatureHeadersIncludeFreshnessFields() {
        let headers = clipfanSignatureHeaders(
            method: "GET",
            requestURI: "/v1/peers",
            body: Data(),
            key: rawKey
        )
        XCTAssertNotNil(headers["X-Clipfan-Ts"])
        XCTAssertNotNil(headers["X-Clipfan-Nonce"])
        XCTAssertNotNil(headers["X-Clipfan-Sig"])
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
