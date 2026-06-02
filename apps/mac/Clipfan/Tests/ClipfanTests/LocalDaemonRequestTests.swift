import XCTest
@testable import Clipfan

final class LocalDaemonRequestTests: XCTestCase {
    private let rawKey = Data("0123456789abcdef0123456789abcdef".utf8)

    func testBuildsVersionedSignedRequestAndPreservesRequestURI() throws {
        let endpoint = LocalDaemonEndpoint(
            url: try XCTUnwrap(URL(string: "http://127.0.0.1:49123")),
            port: 49123,
            purpose: .signed
        )
        let body = Data(#"{"id":"abc"}"#.utf8)

        let prepared = try LocalDaemonRequestBuilder.signedRequest(
            endpoint: endpoint,
            method: "POST",
            requestURI: "/v1/restore?source=app",
            body: body,
            sharedKey: rawKey
        )

        XCTAssertEqual(prepared.request.url?.absoluteString, "http://127.0.0.1:49123/v1/restore?source=app")
        XCTAssertEqual(prepared.request.httpMethod, "POST")
        XCTAssertEqual(prepared.request.httpBody, body)
        XCTAssertEqual(prepared.request.value(forHTTPHeaderField: "Content-Type"), "application/json")
        XCTAssertEqual(prepared.request.value(forHTTPHeaderField: "X-Clipfan-Auth-Version"), clipfanRequestAuthVersion)
        XCTAssertFalse(prepared.requestNonce.isEmpty)

        let timestamp = try XCTUnwrap(prepared.request.value(forHTTPHeaderField: "X-Clipfan-Ts"))
        let signature = try XCTUnwrap(prepared.request.value(forHTTPHeaderField: "X-Clipfan-Sig"))
        XCTAssertEqual(
            signature,
            clipfanVersionedSign(
                method: "POST",
                requestURI: "/v1/restore?source=app",
                timestamp: timestamp,
                nonce: prepared.requestNonce,
                body: body,
                sharedKey: rawKey
            )
        )
    }

    func testSignedCompatibilityEndpointCanBuildVersionedRequest() throws {
        let endpoint = LocalDaemonEndpoint(
            url: try XCTUnwrap(URL(string: "http://127.0.0.1:7853")),
            port: 7853,
            purpose: .signedCompatibility
        )

        let prepared = try LocalDaemonRequestBuilder.signedRequest(
            endpoint: endpoint,
            method: "GET",
            requestURI: "/v1/peers",
            sharedKey: rawKey
        )

        XCTAssertEqual(prepared.request.url?.absoluteString, "http://127.0.0.1:7853/v1/peers")
        XCTAssertNil(prepared.request.httpBody)
        XCTAssertNil(prepared.request.value(forHTTPHeaderField: "Content-Type"))
        XCTAssertEqual(prepared.request.value(forHTTPHeaderField: "X-Clipfan-Auth-Version"), clipfanRequestAuthVersion)
    }

    func testRejectsHealthOnlyEndpointForSignedRequest() throws {
        let endpoint = LocalDaemonEndpoint(
            url: try XCTUnwrap(URL(string: "http://127.0.0.1:49123")),
            port: 49123,
            purpose: .healthOnly
        )

        XCTAssertThrowsError(
            try LocalDaemonRequestBuilder.signedRequest(
                endpoint: endpoint,
                method: "GET",
                requestURI: "/v1/peers",
                sharedKey: rawKey
            )
        ) { error in
            XCTAssertEqual(error as? LocalDaemonRequestError, .healthOnlyEndpoint)
        }
    }

    func testBuiltRequestNonceVerifiesVersionedResponse() throws {
        let endpoint = LocalDaemonEndpoint(
            url: try XCTUnwrap(URL(string: "http://127.0.0.1:49123")),
            port: 49123,
            purpose: .signed
        )
        let prepared = try LocalDaemonRequestBuilder.signedRequest(
            endpoint: endpoint,
            method: "GET",
            requestURI: "/v1/peers",
            sharedKey: rawKey
        )
        let body = Data(#"{"origin":"mac","peers":[]}"#.utf8)
        let response = try XCTUnwrap(HTTPURLResponse(
            url: try XCTUnwrap(prepared.request.url),
            statusCode: 200,
            httpVersion: nil,
            headerFields: [
                "X-Clipfan-Auth-Version": clipfanRequestAuthVersion,
                "X-Clipfan-Response-Sig": clipfanVersionedResponseSignature(
                    requestNonce: prepared.requestNonce,
                    body: body,
                    sharedKey: rawKey
                ),
            ]
        ))

        XCTAssertNoThrow(try authenticatedClipfanData(
            body,
            response: response,
            requestNonce: prepared.requestNonce,
            key: rawKey
        ))
    }

    func testBuiltRequestNonceKeepsRawResponseCompatibility() throws {
        let endpoint = LocalDaemonEndpoint(
            url: try XCTUnwrap(URL(string: "http://127.0.0.1:49123")),
            port: 49123,
            purpose: .signed
        )
        let prepared = try LocalDaemonRequestBuilder.signedRequest(
            endpoint: endpoint,
            method: "GET",
            requestURI: "/v1/peers",
            sharedKey: rawKey
        )
        let body = Data(#"{"origin":"mac","peers":[]}"#.utf8)
        let response = try XCTUnwrap(HTTPURLResponse(
            url: try XCTUnwrap(prepared.request.url),
            statusCode: 200,
            httpVersion: nil,
            headerFields: [
                "X-Clipfan-Response-Sig": clipfanResponseSignature(
                    requestNonce: prepared.requestNonce,
                    body: body,
                    key: rawKey
                ),
            ]
        ))

        XCTAssertNoThrow(try authenticatedClipfanData(
            body,
            response: response,
            requestNonce: prepared.requestNonce,
            key: rawKey
        ))
    }
}
