import Foundation

let localDaemonConfigRevisionConflictCode = "config_revision_conflict"

extension LocalDaemonRequestBuilder {
    static func sshPeerConfigReadRequest(endpoint: LocalDaemonEndpoint,
                                         peerID: String,
                                         sharedKey: Data,
                                         timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        let path = try sshPeerConfigPath(peerID: peerID)
        return try signedRequest(endpoint: endpoint,
                                 method: "GET",
                                 requestURI: path,
                                 sharedKey: sharedKey,
                                 timeout: timeout)
    }

    static func sshPeerConfigUpsertRequest(endpoint: LocalDaemonEndpoint,
                                           peerID: String,
                                           request: LocalDaemonSSHPeerUpsertRequest,
                                           sharedKey: Data,
                                           timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        let path = try sshPeerConfigPath(peerID: peerID)
        return try signedJSONRequest(endpoint: endpoint,
                                     method: "PUT",
                                     requestURI: path,
                                     request: request,
                                     sharedKey: sharedKey,
                                     timeout: timeout)
    }

    static func sshPeerConfigProofPatchRequest(endpoint: LocalDaemonEndpoint,
                                               peerID: String,
                                               request: LocalDaemonSSHPeerProofPatchRequest,
                                               sharedKey: Data,
                                               timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        let path = try sshPeerConfigPath(peerID: peerID)
        return try signedJSONRequest(endpoint: endpoint,
                                     method: "PATCH",
                                     requestURI: "\(path)/proof",
                                     request: request,
                                     sharedKey: sharedKey,
                                     timeout: timeout)
    }

    static func sshPeerConfigTransitionRequest(endpoint: LocalDaemonEndpoint,
                                               peerID: String,
                                               request: LocalDaemonSSHPeerTransitionRequest,
                                               sharedKey: Data,
                                               timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        let path = try sshPeerConfigPath(peerID: peerID)
        return try signedJSONRequest(endpoint: endpoint,
                                     method: "POST",
                                     requestURI: "\(path)/transition",
                                     request: request,
                                     sharedKey: sharedKey,
                                     timeout: timeout)
    }

    static func sshPeerConfigDisableRequest(endpoint: LocalDaemonEndpoint,
                                            peerID: String,
                                            request: LocalDaemonSSHPeerDisableRequest,
                                            sharedKey: Data,
                                            timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        let path = try sshPeerConfigPath(peerID: peerID)
        return try signedJSONRequest(endpoint: endpoint,
                                     method: "POST",
                                     requestURI: "\(path)/disable",
                                     request: request,
                                     sharedKey: sharedKey,
                                     timeout: timeout)
    }

    static func sshPeerConfigDeleteRequest(endpoint: LocalDaemonEndpoint,
                                           peerID: String,
                                           request: LocalDaemonSSHPeerDeleteRequest,
                                           sharedKey: Data,
                                           timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        let path = try sshPeerConfigPath(peerID: peerID)
        return try signedJSONRequest(endpoint: endpoint,
                                     method: "DELETE",
                                     requestURI: path,
                                     request: request,
                                     sharedKey: sharedKey,
                                     timeout: timeout)
    }

    private static func signedJSONRequest<T: Encodable>(endpoint: LocalDaemonEndpoint,
                                                        method: String,
                                                        requestURI: String,
                                                        request: T,
                                                        sharedKey: Data,
                                                        timeout: TimeInterval) throws -> LocalDaemonPreparedRequest {
        let body = try JSONEncoder.clipfan.encode(request)
        return try signedRequest(endpoint: endpoint,
                                 method: method,
                                 requestURI: requestURI,
                                 body: body,
                                 sharedKey: sharedKey,
                                 timeout: timeout)
    }

    private static func sshPeerConfigPath(peerID: String) throws -> String {
        let allowed = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-")
        guard !peerID.isEmpty,
              !peerID.allSatisfy({ $0 == "." }),
              peerID.unicodeScalars.allSatisfy({ allowed.contains($0) }) else {
            throw LocalDaemonRequestError.invalidRequestURI("/v1/config/ssh/peers/\(peerID)")
        }
        return "/v1/config/ssh/peers/\(peerID)"
    }
}

enum LocalDaemonSSHPeerConfigError: Error, Equatable {
    case api(code: String, statusCode: Int)
    case missingHTTPResponse
    case missingResponseSignature
    case badResponseSignature
}

struct LocalDaemonSSHPeerConfigClient {
    typealias HTTPResult = (Data, URLResponse)
    typealias Sender = (LocalDaemonPreparedRequest) async throws -> HTTPResult

    let endpoint: LocalDaemonEndpoint
    let sharedKey: Data
    let send: Sender

    init(endpoint: LocalDaemonEndpoint,
         sharedKey: Data,
         send: @escaping Sender) {
        self.endpoint = endpoint
        self.sharedKey = sharedKey
        self.send = send
    }

    init(endpoint: LocalDaemonEndpoint,
         sharedKey: Data) {
        self.init(endpoint: endpoint, sharedKey: sharedKey) { prepared in
            try await URLSession.shared.data(for: prepared.request)
        }
    }

    func read(peerID: String) async throws -> LocalDaemonSSHPeerConfigResponse {
        let request = try LocalDaemonRequestBuilder.sshPeerConfigReadRequest(endpoint: endpoint, peerID: peerID, sharedKey: sharedKey)
        return try await perform(request)
    }

    func upsert(peerID: String, request body: LocalDaemonSSHPeerUpsertRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        let request = try LocalDaemonRequestBuilder.sshPeerConfigUpsertRequest(endpoint: endpoint, peerID: peerID, request: body, sharedKey: sharedKey)
        return try await perform(request)
    }

    func patchProof(peerID: String, request body: LocalDaemonSSHPeerProofPatchRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        let request = try LocalDaemonRequestBuilder.sshPeerConfigProofPatchRequest(endpoint: endpoint, peerID: peerID, request: body, sharedKey: sharedKey)
        return try await perform(request)
    }

    func transition(peerID: String, request body: LocalDaemonSSHPeerTransitionRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        let request = try LocalDaemonRequestBuilder.sshPeerConfigTransitionRequest(endpoint: endpoint, peerID: peerID, request: body, sharedKey: sharedKey)
        return try await perform(request)
    }

    func disable(peerID: String, request body: LocalDaemonSSHPeerDisableRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        let request = try LocalDaemonRequestBuilder.sshPeerConfigDisableRequest(endpoint: endpoint, peerID: peerID, request: body, sharedKey: sharedKey)
        return try await perform(request)
    }

    func delete(peerID: String, request body: LocalDaemonSSHPeerDeleteRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        let request = try LocalDaemonRequestBuilder.sshPeerConfigDeleteRequest(endpoint: endpoint, peerID: peerID, request: body, sharedKey: sharedKey)
        return try await perform(request)
    }

    func upsertWithRevisionRetry(peerID: String,
                                 request body: LocalDaemonSSHPeerUpsertRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        try await retryingStaleRevision(peerID: peerID, makeRequest: { revision in
            LocalDaemonSSHPeerUpsertRequest(expectedConfigRevision: revision, peer: body.peer)
        }, call: { request in
            try await upsert(peerID: peerID, request: request)
        }, initialRevision: body.expected_config_revision, shouldRetry: { fresh in
            guard let requestedState = body.peer.migration_state else { return true }
            return fresh.peer.migrationState == requestedState
        })
    }

    func patchProofWithRevisionRetry(peerID: String,
                                     request body: LocalDaemonSSHPeerProofPatchRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        try await retryingStaleRevision(peerID: peerID, makeRequest: { revision in
            LocalDaemonSSHPeerProofPatchRequest(expectedConfigRevision: revision,
                                               acceptProof: body.accept_proof,
                                               connectProof: body.connect_proof)
        }, call: { request in
            try await patchProof(peerID: peerID, request: request)
        }, initialRevision: body.expected_config_revision, shouldRetry: { _ in
            true
        })
    }

    func transitionWithRevisionRetry(peerID: String,
                                     request body: LocalDaemonSSHPeerTransitionRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        try await retryingStaleRevision(peerID: peerID, makeRequest: { revision in
            LocalDaemonSSHPeerTransitionRequest(expectedConfigRevision: revision,
                                                fromState: body.from_state,
                                                toState: body.to_state,
                                                reason: body.reason,
                                                logID: body.log_id,
                                                failedPhase: body.failed_phase,
                                                remoteSecretAbsenceProof: body.remote_secret_absence_proof)
        }, call: { request in
            try await transition(peerID: peerID, request: request)
        }, initialRevision: body.expected_config_revision, shouldRetry: { fresh in
            fresh.peer.migrationState == body.from_state
        })
    }

    func disableWithRevisionRetry(peerID: String,
                                  expectedConfigRevision: UInt64,
                                  reason: String) async throws -> LocalDaemonSSHPeerConfigResponse {
        try await retryingStaleRevision(peerID: peerID, makeRequest: { revision in
            LocalDaemonSSHPeerDisableRequest(expectedConfigRevision: revision, reason: reason)
        }, call: { request in
            try await disable(peerID: peerID, request: request)
        }, initialRevision: expectedConfigRevision, shouldRetry: { _ in
            true
        })
    }

    func deleteWithRevisionRetry(peerID: String,
                                 expectedConfigRevision: UInt64,
                                 reason: String,
                                 logID: String) async throws -> LocalDaemonSSHPeerConfigResponse {
        try await retryingStaleRevision(peerID: peerID, makeRequest: { revision in
            LocalDaemonSSHPeerDeleteRequest(expectedConfigRevision: revision, reason: reason, logID: logID)
        }, call: { request in
            try await delete(peerID: peerID, request: request)
        }, initialRevision: expectedConfigRevision, shouldRetry: { _ in
            true
        })
    }

    private func retryingStaleRevision<Request>(peerID: String,
                                                makeRequest: (UInt64) -> Request,
                                                call: (Request) async throws -> LocalDaemonSSHPeerConfigResponse,
                                                initialRevision: UInt64? = nil,
                                                shouldRetry: (LocalDaemonSSHPeerConfigResponse) -> Bool) async throws -> LocalDaemonSSHPeerConfigResponse {
        let firstRevision = initialRevision ?? 0
        do {
            return try await call(makeRequest(firstRevision))
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode) where code == localDaemonConfigRevisionConflictCode {
            let fresh = try await read(peerID: peerID)
            guard let revision = fresh.configRevision else {
                throw LocalDaemonSSHPeerConfigError.api(code: "missing_config_revision", statusCode: 409)
            }
            guard shouldRetry(fresh) else {
                throw LocalDaemonSSHPeerConfigError.api(code: code, statusCode: statusCode)
            }
            return try await call(makeRequest(revision))
        }
    }

    private func perform<T: Decodable>(_ prepared: LocalDaemonPreparedRequest) async throws -> T {
        let (data, response) = try await send(prepared)
        let body = try authenticatedDataAllowingErrorStatus(data, response: response, requestNonce: prepared.requestNonce)
        return try JSONDecoder.clipfan.decode(T.self, from: body)
    }

    private func authenticatedDataAllowingErrorStatus(_ data: Data, response: URLResponse, requestNonce: String) throws -> Data {
        guard let http = response as? HTTPURLResponse else {
            throw LocalDaemonSSHPeerConfigError.missingHTTPResponse
        }
        try verifySignedResponse(data, http: http, requestNonce: requestNonce)
        guard (200..<300).contains(http.statusCode) else {
            let code = (try? JSONDecoder.clipfan.decode(LocalDaemonAPIErrorResponse.self, from: data).stableCode) ?? "http_\(http.statusCode)"
            throw LocalDaemonSSHPeerConfigError.api(code: code, statusCode: http.statusCode)
        }
        return data
    }

    private func verifySignedResponse(_ data: Data, http: HTTPURLResponse, requestNonce: String) throws {
        guard let signature = http.value(forHTTPHeaderField: "X-Clipfan-Response-Sig") else {
            throw LocalDaemonSSHPeerConfigError.missingResponseSignature
        }
        guard clipfanVerifyResponseSignatureHeader(signature,
                                                   authVersion: http.value(forHTTPHeaderField: "X-Clipfan-Auth-Version"),
                                                   requestNonce: requestNonce,
                                                   body: data,
                                                   key: sharedKey) else {
            throw LocalDaemonSSHPeerConfigError.badResponseSignature
        }
    }
}

extension JSONEncoder {
    static let clipfan: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .custom { date, encoder in
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            var container = encoder.singleValueContainer()
            try container.encode(formatter.string(from: date))
        }
        return encoder
    }()
}
