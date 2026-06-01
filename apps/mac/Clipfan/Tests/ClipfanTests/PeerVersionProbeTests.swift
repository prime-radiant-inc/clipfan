import XCTest
@testable import Clipfan

final class PeerVersionProbeTests: XCTestCase {
    private let rawKey = Data("0123456789abcdef0123456789abcdef".utf8)

    func testBuildsSignedRemoteVersionRequest() throws {
        let prepared = try PeerVersionProbe.request(host: "linux-box", port: 7853, key: rawKey)

        XCTAssertEqual(prepared.request.url?.absoluteString, "http://linux-box:7853/v1/version")
        XCTAssertEqual(prepared.request.httpMethod, "GET")
        XCTAssertNotNil(prepared.request.value(forHTTPHeaderField: "X-Clipfan-Ts"))
        XCTAssertEqual(prepared.request.value(forHTTPHeaderField: "X-Clipfan-Nonce"), prepared.requestNonce)
        XCTAssertNotNil(prepared.request.value(forHTTPHeaderField: "X-Clipfan-Sig"))
    }

    func testDecodesSignedVersionResponse() throws {
        let body = Data(#"{"version":"v0.3.4"}"#.utf8)
        let nonce = "request-nonce"
        let signature = clipfanResponseSignature(requestNonce: nonce, body: body, key: rawKey)
        let response = try XCTUnwrap(HTTPURLResponse(
            url: URL(string: "http://linux-box:7853/v1/version")!,
            statusCode: 200,
            httpVersion: nil,
            headerFields: ["X-Clipfan-Response-Sig": signature]
        ))

        let version = try PeerVersionProbe.decode(data: body, response: response, requestNonce: nonce, key: rawKey)

        XCTAssertEqual(version, "v0.3.4")
    }
}

