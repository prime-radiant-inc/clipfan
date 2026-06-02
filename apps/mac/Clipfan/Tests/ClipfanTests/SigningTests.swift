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

    func testSignResponseMatchesGo() {
        let sig = clipfanResponseSignature(
            requestNonce: "nonce-1",
            body: Data(#"{"origin":"m4"}"#.utf8),
            key: rawKey
        )
        XCTAssertEqual(sig, "42c761090fce0e3702d231663a20b8e7b05e0cd8c3bd2e357d9f4e85420a1d86")
    }

    func testRequestHMACKeyMatchesDesignVector() {
        XCTAssertEqual(
            hex(clipfanRequestHMACKey(sharedKey: rawKey)),
            "1c23ce9e76df9696c06b04fa7f16dabd200d87043e31f592a91344899161a132"
        )
    }

    func testVersionedSignHistoryRequestMatchesGo() {
        let sig = clipfanVersionedSign(
            method: "GET",
            requestURI: "/v1/history?limit=10",
            timestamp: "1780257600",
            nonce: "nonce-1",
            body: Data(),
            sharedKey: rawKey
        )
        XCTAssertEqual(sig, "8443d8072bd22b9ab5752160293eb8a2b9f48e225adbf3b8d6d8ca5439123d24")
    }

    func testVersionedSignRestoreRequestMatchesGo() {
        let sig = clipfanVersionedSign(
            method: "POST",
            requestURI: "/v1/restore",
            timestamp: "1780257600",
            nonce: "nonce-2",
            body: Data(#"{"id":"abc"}"#.utf8),
            sharedKey: rawKey
        )
        XCTAssertEqual(sig, "b035d02ef665e8a86b0a763e8f47e3a2492f2fa7cf39f9baa830f260206835aa")
    }

    func testVersionedSignResponseMatchesGo() {
        let sig = clipfanVersionedResponseSignature(
            requestNonce: "nonce-1",
            body: Data(#"{"origin":"m4"}"#.utf8),
            sharedKey: rawKey
        )
        XCTAssertEqual(sig, "71ba47e6d02000e8852c4a7017f2ac1f6e0559d9ccf058e63265243859a2f6b4")
    }

    func testAuthenticatedResponseRejectsWrongSignature() throws {
        let url = URL(string: "http://127.0.0.1:7853/v1/peers")!
        let body = Data(#"{"origin":"m4"}"#.utf8)
        let sig = clipfanResponseSignature(requestNonce: "nonce-1", body: body, key: rawKey)
        let response = try XCTUnwrap(HTTPURLResponse(
            url: url,
            statusCode: 200,
            httpVersion: nil,
            headerFields: ["X-Clipfan-Response-Sig": sig]
        ))

        XCTAssertNoThrow(try authenticatedClipfanData(body, response: response, requestNonce: "nonce-1", key: rawKey))
        XCTAssertThrowsError(try authenticatedClipfanData(body, response: response, requestNonce: "nonce-2", key: rawKey))
        XCTAssertThrowsError(try authenticatedClipfanData(Data(#"{"origin":"evil"}"#.utf8), response: response, requestNonce: "nonce-1", key: rawKey))
    }

    func testAuthenticatedResponseAcceptsVersionedSignature() throws {
        let url = URL(string: "http://127.0.0.1:7853/v1/peers")!
        let body = Data(#"{"origin":"m4"}"#.utf8)
        let sig = clipfanVersionedResponseSignature(requestNonce: "nonce-1", body: body, sharedKey: rawKey)
        let response = try XCTUnwrap(HTTPURLResponse(
            url: url,
            statusCode: 200,
            httpVersion: nil,
            headerFields: [
                "X-Clipfan-Auth-Version": clipfanRequestAuthVersion,
                "X-Clipfan-Response-Sig": sig,
            ]
        ))

        XCTAssertNoThrow(try authenticatedClipfanData(body, response: response, requestNonce: "nonce-1", key: rawKey))
        XCTAssertThrowsError(try authenticatedClipfanData(body, response: response, requestNonce: "nonce-2", key: rawKey))
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

    func testVersionedSignatureHeadersIncludeAuthVersion() {
        let headers = clipfanVersionedSignatureHeaders(
            method: "GET",
            requestURI: "/v1/peers",
            body: Data(),
            sharedKey: rawKey
        )
        XCTAssertEqual(headers["X-Clipfan-Auth-Version"], clipfanRequestAuthVersion)
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

    private func hex(_ data: Data) -> String {
        data.map { String(format: "%02x", $0) }.joined()
    }
}
