import XCTest
@testable import Clipfan

final class LocalDaemonSSHPeerConfigTests: XCTestCase {
    private let rawKey = Data("0123456789abcdef0123456789abcdef".utf8)

    func testBuildsSSHPeerConfigReadRequest() throws {
        let endpoint = signedEndpoint()

        let prepared = try LocalDaemonRequestBuilder.sshPeerConfigReadRequest(
            endpoint: endpoint,
            peerID: "fsck",
            sharedKey: rawKey
        )

        XCTAssertEqual(prepared.request.url?.absoluteString, "http://127.0.0.1:7853/v1/config/ssh/peers/fsck")
        XCTAssertEqual(prepared.request.httpMethod, "GET")
        XCTAssertNil(prepared.request.httpBody)
        XCTAssertEqual(prepared.request.value(forHTTPHeaderField: "X-Clipfan-Auth-Version"), clipfanRequestAuthVersion)
        XCTAssertNoThrow(try verifyRequestSignature(prepared, method: "GET", requestURI: "/v1/config/ssh/peers/fsck", body: Data()))
    }

    func testRejectsUnsafeSSHPeerConfigPathID() throws {
        let endpoint = signedEndpoint()

        XCTAssertThrowsError(try LocalDaemonRequestBuilder.sshPeerConfigReadRequest(
            endpoint: endpoint,
            peerID: "fsck/disable",
            sharedKey: rawKey
        )) { error in
            XCTAssertEqual(error as? LocalDaemonRequestError, .invalidRequestURI("/v1/config/ssh/peers/fsck/disable"))
        }
        XCTAssertThrowsError(try LocalDaemonRequestBuilder.sshPeerConfigReadRequest(
            endpoint: endpoint,
            peerID: "..",
            sharedKey: rawKey
        )) { error in
            XCTAssertEqual(error as? LocalDaemonRequestError, .invalidRequestURI("/v1/config/ssh/peers/.."))
        }
    }

    func testBuildsSSHPeerConfigMutationRequests() throws {
        let endpoint = signedEndpoint()
        let upsert = LocalDaemonSSHPeerUpsertRequest(
            expectedConfigRevision: 7,
            peer: LocalDaemonSSHPeerUpsertFields(id: "fsck", enabled: true, accept: true, connect: false, migrationState: "loopback_unprovisioned")
        )
        let upsertRequest = try LocalDaemonRequestBuilder.sshPeerConfigUpsertRequest(
            endpoint: endpoint,
            peerID: "fsck",
            request: upsert,
            sharedKey: rawKey
        )
        try assertPreparedJSONRequest(upsertRequest, method: "PUT", requestURI: "/v1/config/ssh/peers/fsck") { body in
            XCTAssertEqual(body["expected_config_revision"] as? Int, 7)
            let peer = try XCTUnwrap(body["peer"] as? [String: Any])
            XCTAssertEqual(peer["id"] as? String, "fsck")
            XCTAssertEqual(peer["migration_state"] as? String, "loopback_unprovisioned")
        }

        let proof = LocalDaemonSSHPeerProofPatchRequest(
            expectedConfigRevision: 8,
            acceptProof: LocalDaemonSSHPeerDirectionalProofPatch(
                keyID: "accept-key-1",
                gatewayPath: "/home/jesse/.local/bin/clipfan",
                verifiedAt: "2026-06-02T12:34:56Z",
                verifiedBy: "local_file"
            )
        )
        let proofRequest = try LocalDaemonRequestBuilder.sshPeerConfigProofPatchRequest(
            endpoint: endpoint,
            peerID: "fsck",
            request: proof,
            sharedKey: rawKey
        )
        try assertPreparedJSONRequest(proofRequest, method: "PATCH", requestURI: "/v1/config/ssh/peers/fsck/proof") { body in
            XCTAssertEqual(body["expected_config_revision"] as? Int, 8)
            let accept = try XCTUnwrap(body["accept_proof"] as? [String: Any])
            XCTAssertEqual(accept["key_id"] as? String, "accept-key-1")
            XCTAssertNil(body["connect_proof"])
        }

        let transition = LocalDaemonSSHPeerTransitionRequest(
            expectedConfigRevision: 9,
            fromState: "loopback_unprovisioned",
            toState: "ssh_material_staged",
            reason: "material_staged",
            logID: "peer-log-1780257600"
        )
        let transitionRequest = try LocalDaemonRequestBuilder.sshPeerConfigTransitionRequest(
            endpoint: endpoint,
            peerID: "fsck",
            request: transition,
            sharedKey: rawKey
        )
        try assertPreparedJSONRequest(transitionRequest, method: "POST", requestURI: "/v1/config/ssh/peers/fsck/transition") { body in
            XCTAssertEqual(body["expected_config_revision"] as? Int, 9)
            XCTAssertEqual(body["from_state"] as? String, "loopback_unprovisioned")
            XCTAssertEqual(body["to_state"] as? String, "ssh_material_staged")
            XCTAssertEqual(body["log_id"] as? String, "peer-log-1780257600")
        }

        let disable = try LocalDaemonRequestBuilder.sshPeerConfigDisableRequest(
            endpoint: endpoint,
            peerID: "fsck",
            request: LocalDaemonSSHPeerDisableRequest(expectedConfigRevision: 10, reason: "user_disabled"),
            sharedKey: rawKey
        )
        try assertPreparedJSONRequest(disable, method: "POST", requestURI: "/v1/config/ssh/peers/fsck/disable") { body in
            XCTAssertEqual(body["expected_config_revision"] as? Int, 10)
            XCTAssertEqual(body["reason"] as? String, "user_disabled")
        }

        let delete = try LocalDaemonRequestBuilder.sshPeerConfigDeleteRequest(
            endpoint: endpoint,
            peerID: "fsck",
            request: LocalDaemonSSHPeerDeleteRequest(expectedConfigRevision: 11, reason: "user_deleted", logID: "peer-log-1780257600"),
            sharedKey: rawKey
        )
        try assertPreparedJSONRequest(delete, method: "DELETE", requestURI: "/v1/config/ssh/peers/fsck") { body in
            XCTAssertEqual(body["expected_config_revision"] as? Int, 11)
            XCTAssertEqual(body["reason"] as? String, "user_deleted")
            XCTAssertEqual(body["log_id"] as? String, "peer-log-1780257600")
        }
    }

    func testDecodesSSHPeerConfigResponseWithRedactedPeer() throws {
        let json = Data("""
        {
          "config_version": 2,
          "config_revision": 8,
          "revision_state": "versioned",
          "peer": {
            "id": "fsck",
            "enabled": true,
            "accept": true,
            "connect": false,
            "migration_state": "ssh_material_staged",
            "proof": {
              "accept_key_id": "accept-key-1",
              "accept_verified_by": "regular_ssh",
              "private_key": "should-not-decode"
            },
            "cleanup_status": {
              "cleanup_required": true,
              "auth_token": "should-not-decode"
            },
            "future_peer": {
              "keep": true,
              "auth_token": "should-not-decode",
              "children": [
                {
                  "keep": true,
                  "private_key": "should-not-decode"
                }
              ]
            }
          }
        }
        """.utf8)

        let response = try JSONDecoder.clipfan.decode(LocalDaemonSSHPeerConfigResponse.self, from: json)

        XCTAssertEqual(response.configVersion, 2)
        XCTAssertEqual(response.configRevision, 8)
        XCTAssertEqual(response.revisionState, "versioned")
        XCTAssertEqual(response.peer.id, "fsck")
        XCTAssertEqual(response.peer.migrationState, "ssh_material_staged")
        XCTAssertEqual(response.peer.proof?["accept_key_id"]?.stringValue, "accept-key-1")
        XCTAssertNil(response.peer.proof?["private_key"])
        XCTAssertEqual(response.peer.cleanupStatus?["cleanup_required"]?.boolValue, true)
        XCTAssertNil(response.peer.cleanupStatus?["auth_token"])
        let futurePeer = try XCTUnwrap(response.peer.additionalFields["future_peer"]?.objectValue)
        XCTAssertEqual(futurePeer["keep"]?.boolValue, true)
        XCTAssertNil(futurePeer["auth_token"])
        let children = try XCTUnwrap(futurePeer["children"])
        if case .array(let childValues) = children {
            let firstChild = try XCTUnwrap(childValues.first?.objectValue)
            XCTAssertEqual(firstChild["keep"]?.boolValue, true)
            XCTAssertNil(firstChild["private_key"])
        } else {
            XCTFail("future_peer.children did not decode as an array")
        }

        let roundTrip = try JSONEncoder.clipfan.encode(response.peer)
        let roundTripObject = try XCTUnwrap(JSONSerialization.jsonObject(with: roundTrip) as? [String: Any])
        let roundTripFuturePeer = try XCTUnwrap(roundTripObject["future_peer"] as? [String: Any])
        XCTAssertEqual(roundTripFuturePeer["keep"] as? Bool, true)
        XCTAssertNil(roundTripFuturePeer["auth_token"])
        XCTAssertNil(response.peer.sharedKey)
        XCTAssertNil(response.peer.privateKey)
    }

    func testSSHPeerConfigResponseIgnoresRedactedSecrets() throws {
        let json = Data("""
        {
          "config_revision": 8,
          "revision_state": "versioned",
          "peer": {
            "id": "fsck",
            "shared_key": "should-not-decode",
            "private_key": "should-not-decode",
            "auth_token": "should-not-decode"
          }
        }
        """.utf8)

        let response = try JSONDecoder.clipfan.decode(LocalDaemonSSHPeerConfigResponse.self, from: json)

        XCTAssertEqual(response.peer.id, "fsck")
        XCTAssertNil(response.peer.additionalFields["shared_key"])
        XCTAssertNil(response.peer.additionalFields["private_key"])
        XCTAssertNil(response.peer.additionalFields["auth_token"])
    }

    func testClipfanJSONEncoderEncodesDatesAsISO8601Strings() throws {
        struct DatePayload: Encodable {
            let verified_at: Date
        }

        let data = try JSONEncoder.clipfan.encode(DatePayload(verified_at: Date(timeIntervalSince1970: 0)))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["verified_at"] as? String, "1970-01-01T00:00:00.000Z")
    }

    func testClientRetriesStaleRevisionAfterRefreshingPeerConfig() async throws {
        let endpoint = signedEndpoint()
        var calls: [(String, String, Data)] = []
        let client = LocalDaemonSSHPeerConfigClient(endpoint: endpoint, sharedKey: rawKey) { prepared in
            let request = prepared.request
            let path = request.url!.path
            calls.append((request.httpMethod ?? "", path, request.httpBody ?? Data()))
            if calls.count == 1 {
                return Self.signedResponseBody(
                    for: prepared,
                    key: self.rawKey,
                    status: 409,
                    body: Self.errorResponseJSON(code: localDaemonConfigRevisionConflictCode)
                )
            }
            if calls.count == 2 {
                return Self.signedResponseBody(
                    for: prepared,
                    key: self.rawKey,
                    status: 200,
                    body: Self.peerConfigResponseJSON(revision: 12)
                )
            }
            return Self.signedResponseBody(
                for: prepared,
                key: self.rawKey,
                status: 200,
                body: Self.peerConfigResponseJSON(revision: 13, enabled: false)
            )
        }

        let response = try await client.disableWithRevisionRetry(
            peerID: "fsck",
            expectedConfigRevision: 7,
            reason: "user_disabled"
        )

        XCTAssertEqual(response.configRevision, 13)
        XCTAssertEqual(response.peer.enabled, false)
        XCTAssertEqual(calls.map(\.1), [
            "/v1/config/ssh/peers/fsck/disable",
            "/v1/config/ssh/peers/fsck",
            "/v1/config/ssh/peers/fsck/disable",
        ])
        let retriedBody = try XCTUnwrap(JSONSerialization.jsonObject(with: calls[2].2) as? [String: Any])
        XCTAssertEqual(retriedBody["expected_config_revision"] as? Int, 12)
    }

    func testClientRetriesTransitionOnlyWhenFreshStateStillMatchesFromState() async throws {
        let endpoint = signedEndpoint()
        var calls: [(String, String, Data)] = []
        let client = LocalDaemonSSHPeerConfigClient(endpoint: endpoint, sharedKey: rawKey) { prepared in
            let request = prepared.request
            calls.append((request.httpMethod ?? "", request.url!.path, request.httpBody ?? Data()))
            if calls.count == 1 {
                return Self.signedResponseBody(
                    for: prepared,
                    key: self.rawKey,
                    status: 409,
                    body: Self.errorResponseJSON(code: localDaemonConfigRevisionConflictCode)
                )
            }
            if calls.count == 2 {
                return Self.signedResponseBody(
                    for: prepared,
                    key: self.rawKey,
                    status: 200,
                    body: Self.peerConfigResponseJSON(revision: 12, migrationState: "loopback_unprovisioned")
                )
            }
            return Self.signedResponseBody(
                for: prepared,
                key: self.rawKey,
                status: 200,
                body: Self.peerConfigResponseJSON(revision: 13, migrationState: "ssh_material_staged")
            )
        }

        let response = try await client.transitionWithRevisionRetry(
            peerID: "fsck",
            request: LocalDaemonSSHPeerTransitionRequest(
                expectedConfigRevision: 7,
                fromState: "loopback_unprovisioned",
                toState: "ssh_material_staged",
                reason: "material_staged",
                logID: "peer-log-1780257600"
            )
        )

        XCTAssertEqual(response.configRevision, 13)
        XCTAssertEqual(calls.map(\.1), [
            "/v1/config/ssh/peers/fsck/transition",
            "/v1/config/ssh/peers/fsck",
            "/v1/config/ssh/peers/fsck/transition",
        ])
        let retriedBody = try XCTUnwrap(JSONSerialization.jsonObject(with: calls[2].2) as? [String: Any])
        XCTAssertEqual(retriedBody["expected_config_revision"] as? Int, 12)
        XCTAssertEqual(retriedBody["from_state"] as? String, "loopback_unprovisioned")
        XCTAssertEqual(retriedBody["to_state"] as? String, "ssh_material_staged")
    }

    func testClientDoesNotRetryUpsertWhenFreshStateChanged() async throws {
        let endpoint = signedEndpoint()
        var calls: [(String, String, Data)] = []
        let client = LocalDaemonSSHPeerConfigClient(endpoint: endpoint, sharedKey: rawKey) { prepared in
            let request = prepared.request
            calls.append((request.httpMethod ?? "", request.url!.path, request.httpBody ?? Data()))
            if calls.count == 1 {
                return Self.signedResponseBody(
                    for: prepared,
                    key: self.rawKey,
                    status: 409,
                    body: Self.errorResponseJSON(code: localDaemonConfigRevisionConflictCode)
                )
            }
            return Self.signedResponseBody(
                for: prepared,
                key: self.rawKey,
                status: 200,
                body: Self.peerConfigResponseJSON(revision: 12, migrationState: "provision_failed")
            )
        }

        do {
            _ = try await client.upsertWithRevisionRetry(
                peerID: "fsck",
                request: LocalDaemonSSHPeerUpsertRequest(
                    expectedConfigRevision: 7,
                    peer: LocalDaemonSSHPeerUpsertFields(
                        id: "fsck",
                        enabled: true,
                        migrationState: "loopback_unprovisioned"
                    )
                )
            )
            XCTFail("upsert unexpectedly succeeded")
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode) {
            XCTAssertEqual(code, localDaemonConfigRevisionConflictCode)
            XCTAssertEqual(statusCode, 409)
        }
        XCTAssertEqual(calls.map(\.1), [
            "/v1/config/ssh/peers/fsck",
            "/v1/config/ssh/peers/fsck",
        ])
    }

    func testClientDoesNotRetryTransitionWhenFreshStateChanged() async throws {
        let endpoint = signedEndpoint()
        var calls: [(String, String, Data)] = []
        let client = LocalDaemonSSHPeerConfigClient(endpoint: endpoint, sharedKey: rawKey) { prepared in
            let request = prepared.request
            calls.append((request.httpMethod ?? "", request.url!.path, request.httpBody ?? Data()))
            if calls.count == 1 {
                return Self.signedResponseBody(
                    for: prepared,
                    key: self.rawKey,
                    status: 409,
                    body: Self.errorResponseJSON(code: localDaemonConfigRevisionConflictCode)
                )
            }
            return Self.signedResponseBody(
                for: prepared,
                key: self.rawKey,
                status: 200,
                body: Self.peerConfigResponseJSON(revision: 12, migrationState: "provision_failed")
            )
        }

        do {
            _ = try await client.transitionWithRevisionRetry(
                peerID: "fsck",
                request: LocalDaemonSSHPeerTransitionRequest(
                    expectedConfigRevision: 7,
                    fromState: "loopback_unprovisioned",
                    toState: "ssh_material_staged",
                    reason: "material_staged",
                    logID: "peer-log-1780257600"
                )
            )
            XCTFail("transition unexpectedly succeeded")
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode) {
            XCTAssertEqual(code, localDaemonConfigRevisionConflictCode)
            XCTAssertEqual(statusCode, 409)
        }
        XCTAssertEqual(calls.map(\.1), [
            "/v1/config/ssh/peers/fsck/transition",
            "/v1/config/ssh/peers/fsck",
        ])
    }

    func testClientFailsRetryWhenRefreshOmitsConfigRevision() async throws {
        let endpoint = signedEndpoint()
        var calls: [(String, String, Data)] = []
        let client = LocalDaemonSSHPeerConfigClient(endpoint: endpoint, sharedKey: rawKey) { prepared in
            let request = prepared.request
            calls.append((request.httpMethod ?? "", request.url!.path, request.httpBody ?? Data()))
            if calls.count == 1 {
                return Self.signedResponseBody(
                    for: prepared,
                    key: self.rawKey,
                    status: 409,
                    body: Self.errorResponseJSON(code: localDaemonConfigRevisionConflictCode)
                )
            }
            return Self.signedResponseBody(
                for: prepared,
                key: self.rawKey,
                status: 200,
                body: Self.peerConfigResponseWithoutRevisionJSON()
            )
        }

        do {
            _ = try await client.disableWithRevisionRetry(
                peerID: "fsck",
                expectedConfigRevision: 7,
                reason: "user_disabled"
            )
            XCTFail("disable unexpectedly succeeded")
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode) {
            XCTAssertEqual(code, "missing_config_revision")
            XCTAssertEqual(statusCode, 409)
        }
        XCTAssertEqual(calls.map(\.1), [
            "/v1/config/ssh/peers/fsck/disable",
            "/v1/config/ssh/peers/fsck",
        ])
    }

    func testClientDoesNotRetryNonRevisionConflict() async throws {
        let endpoint = signedEndpoint()
        var count = 0
        let client = LocalDaemonSSHPeerConfigClient(endpoint: endpoint, sharedKey: rawKey) { prepared in
            count += 1
            return Self.signedResponseBody(
                for: prepared,
                key: self.rawKey,
                status: 400,
                body: Self.errorResponseJSON(code: "bad_request")
            )
        }

        do {
            _ = try await client.deleteWithRevisionRetry(
                peerID: "fsck",
                expectedConfigRevision: 7,
                reason: "user_deleted",
                logID: "peer-log-1780257600"
            )
            XCTFail("delete unexpectedly succeeded")
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode) {
            XCTAssertEqual(code, "bad_request")
            XCTAssertEqual(statusCode, 400)
        }
        XCTAssertEqual(count, 1)
    }

    func testClientRejectsMissingResponseSignature() async throws {
        let endpoint = signedEndpoint()
        let client = LocalDaemonSSHPeerConfigClient(endpoint: endpoint, sharedKey: rawKey) { prepared in
            let body = Self.peerConfigResponseJSON(revision: 12)
            let response = HTTPURLResponse(
                url: prepared.request.url!,
                statusCode: 200,
                httpVersion: nil,
                headerFields: [:]
            )!
            return (body, response)
        }

        do {
            _ = try await client.read(peerID: "fsck")
            XCTFail("read unexpectedly succeeded")
        } catch LocalDaemonSSHPeerConfigError.missingResponseSignature {
        }
    }

    func testClientRejectsBadResponseSignature() async throws {
        let endpoint = signedEndpoint()
        let client = LocalDaemonSSHPeerConfigClient(endpoint: endpoint, sharedKey: rawKey) { prepared in
            let signedBody = Self.peerConfigResponseJSON(revision: 12)
            let tamperedBody = Self.peerConfigResponseJSON(revision: 13)
            let signed = Self.signedResponseBody(
                for: prepared,
                key: self.rawKey,
                status: 200,
                body: signedBody
            )
            return (tamperedBody, signed.1)
        }

        do {
            _ = try await client.read(peerID: "fsck")
            XCTFail("read unexpectedly succeeded")
        } catch LocalDaemonSSHPeerConfigError.badResponseSignature {
        }
    }

    @MainActor
    func testDaemonClientBlocksSSHPeerConfigMutationsInSafeMode() async throws {
        let daemon = DaemonClient.shared
        let oldSafeModeStatus = daemon.safeModeStatus
        defer {
            daemon.safeModeStatus = oldSafeModeStatus
        }
        daemon.safeModeStatus = LocalDaemonSafeModeStatus(
            active: true,
            reason: "safe_mode_signed_repair",
            listen: "0.0.0.0:7853",
            effectiveRepairListen: "127.0.0.1:7853",
            expectedRevisionState: "versioned",
            expectedRevision: 7,
            port: 7853
        )

        do {
            _ = try await daemon.disableSSHPeer(
                peerID: "fsck",
                expectedConfigRevision: 7,
                reason: "user_disabled"
            )
            XCTFail("disable unexpectedly succeeded")
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode) {
            XCTAssertEqual(code, "safe_mode_active")
            XCTAssertEqual(statusCode, 503)
        }
    }

    @MainActor
    func testDaemonClientReportsMissingSharedKeyForSSHPeerConfigRead() async throws {
        let daemon = DaemonClient.shared
        let oldSafeModeStatus = daemon.safeModeStatus
        let oldXDG = getenv("XDG_CONFIG_HOME").map { String(cString: $0) }
        let temp = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-missing-shared-key-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: temp, withIntermediateDirectories: true)
        setenv("XDG_CONFIG_HOME", temp.path, 1)
        defer {
            daemon.safeModeStatus = oldSafeModeStatus
            if let oldXDG {
                setenv("XDG_CONFIG_HOME", oldXDG, 1)
            } else {
                unsetenv("XDG_CONFIG_HOME")
            }
            try? FileManager.default.removeItem(at: temp)
        }
        daemon.safeModeStatus = nil

        do {
            _ = try await daemon.readSSHPeerConfig(peerID: "fsck")
            XCTFail("read unexpectedly succeeded")
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode) {
            XCTAssertEqual(code, "missing_shared_key")
            XCTAssertEqual(statusCode, 503)
        }
    }

    private func signedEndpoint() -> LocalDaemonEndpoint {
        LocalDaemonEndpoint(url: URL(string: "http://127.0.0.1:7853")!, port: 7853, purpose: .signed)
    }

    private func assertPreparedJSONRequest(_ prepared: LocalDaemonPreparedRequest,
                                           method: String,
                                           requestURI: String,
                                           bodyAssertions: ([String: Any]) throws -> Void) throws {
        XCTAssertEqual(prepared.request.url?.absoluteString, "http://127.0.0.1:7853\(requestURI)")
        XCTAssertEqual(prepared.request.httpMethod, method)
        XCTAssertEqual(prepared.request.value(forHTTPHeaderField: "Content-Type"), "application/json")
        let body = try XCTUnwrap(prepared.request.httpBody)
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        try bodyAssertions(obj)
        try verifyRequestSignature(prepared, method: method, requestURI: requestURI, body: body)
    }

    private func verifyRequestSignature(_ prepared: LocalDaemonPreparedRequest,
                                        method: String,
                                        requestURI: String,
                                        body: Data) throws {
        let timestamp = try XCTUnwrap(prepared.request.value(forHTTPHeaderField: "X-Clipfan-Ts"))
        let signature = try XCTUnwrap(prepared.request.value(forHTTPHeaderField: "X-Clipfan-Sig"))
        XCTAssertEqual(
            signature,
            clipfanVersionedSign(
                method: method,
                requestURI: requestURI,
                timestamp: timestamp,
                nonce: prepared.requestNonce,
                body: body,
                sharedKey: rawKey
            )
        )
    }

    private static func signedResponseBody(for prepared: LocalDaemonPreparedRequest,
                                           key: Data,
                                           status: Int,
                                           body: Data) -> LocalDaemonSSHPeerConfigClient.HTTPResult {
        let response = HTTPURLResponse(
            url: prepared.request.url!,
            statusCode: status,
            httpVersion: nil,
            headerFields: [
                "X-Clipfan-Auth-Version": clipfanRequestAuthVersion,
                "X-Clipfan-Response-Sig": clipfanVersionedResponseSignature(
                    requestNonce: prepared.requestNonce,
                    body: body,
                    sharedKey: key
                ),
            ]
        )!
        return (body, response)
    }

    private static func peerConfigResponseJSON(revision: UInt64,
                                               enabled: Bool = true,
                                               migrationState: String = "ssh_material_staged") -> Data {
        Data("""
        {
          "config_version": 2,
          "config_revision": \(revision),
          "revision_state": "versioned",
          "peer": {
            "id": "fsck",
            "enabled": \(enabled),
            "migration_state": "\(migrationState)"
          }
        }
        """.utf8)
    }

    private static func peerConfigResponseWithoutRevisionJSON() -> Data {
        Data("""
        {
          "config_version": 2,
          "revision_state": "missing_revision",
          "peer": {
            "id": "fsck",
            "enabled": true,
            "migration_state": "ssh_material_staged"
          }
        }
        """.utf8)
    }

    private static func errorResponseJSON(code: String) -> Data {
        Data(#"{"type":"error","code":"\#(code)"}"#.utf8)
    }
}
