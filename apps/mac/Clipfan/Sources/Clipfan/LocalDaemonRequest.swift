import Foundation

struct LocalDaemonPreparedRequest: Equatable {
    let request: URLRequest
    let requestNonce: String
}

enum LocalDaemonRequestError: Error, Equatable {
    case healthOnlyEndpoint
    case invalidRequestURI(String)
    case missingRequestNonce
    case safeModeRepairUnavailable
}

enum LocalDaemonRequestBuilder {
    static let safeModeStatusPath = "/v1/status"
    static let safeModeLogPath = "/v1/ssh/logs?limit=50"
    static let listenerRepairPath = "/v1/config/listener"

    static func signedRequest(endpoint: LocalDaemonEndpoint,
                              method: String,
                              requestURI: String,
                              body: Data = Data(),
                              sharedKey: Data,
                              timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        guard endpoint.purpose != .healthOnly else {
            throw LocalDaemonRequestError.healthOnlyEndpoint
        }
        guard requestURI.hasPrefix("/"),
              let url = URL(string: endpoint.url.absoluteString + requestURI) else {
            throw LocalDaemonRequestError.invalidRequestURI(requestURI)
        }

        var request = URLRequest(url: url)
        request.httpMethod = method
        request.timeoutInterval = timeout
        if !body.isEmpty {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        let headers = clipfanVersionedSignatureHeaders(
            method: method,
            requestURI: requestURI,
            body: body,
            sharedKey: sharedKey
        )
        for (header, value) in headers {
            request.setValue(value, forHTTPHeaderField: header)
        }
        guard let requestNonce = headers["X-Clipfan-Nonce"] else {
            throw LocalDaemonRequestError.missingRequestNonce
        }

        return LocalDaemonPreparedRequest(request: request, requestNonce: requestNonce)
    }

    static func safeModeStatusRequest(endpoint: LocalDaemonEndpoint,
                                      sharedKey: Data,
                                      timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        try signedRequest(endpoint: endpoint,
                          method: "GET",
                          requestURI: safeModeStatusPath,
                          sharedKey: sharedKey,
                          timeout: timeout)
    }

    static func safeModeLogRequest(endpoint: LocalDaemonEndpoint,
                                   sharedKey: Data,
                                   timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        try signedRequest(endpoint: endpoint,
                          method: "GET",
                          requestURI: safeModeLogPath,
                          sharedKey: sharedKey,
                          timeout: timeout)
    }

    static func listenerRepairRequest(endpoint: LocalDaemonEndpoint,
                                      status: LocalDaemonListenerRepairStatus,
                                      sharedKey: Data,
                                      timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        guard status.safe_mode,
              !status.effectiveRepairListen.isEmpty else {
            throw LocalDaemonRequestError.safeModeRepairUnavailable
        }
        let payload: [String: Any] = [
            "listen": status.effectiveRepairListen,
            "port": port(fromListen: status.effectiveRepairListen) ?? status.port,
            "expected_revision_state": status.revisionState,
            "expected_config_revision": status.configRevision.map { NSNumber(value: $0) } ?? NSNull(),
        ]
        let body = try JSONSerialization.data(withJSONObject: payload)
        return try signedRequest(endpoint: endpoint,
                                 method: "PATCH",
                                 requestURI: listenerRepairPath,
                                 body: body,
                                 sharedKey: sharedKey,
                                 timeout: timeout)
    }

    static func listenerRepairStatusRequest(endpoint: LocalDaemonEndpoint,
                                            sharedKey: Data,
                                            timeout: TimeInterval = 2) throws -> LocalDaemonPreparedRequest {
        try signedRequest(endpoint: endpoint,
                          method: "GET",
                          requestURI: listenerRepairPath,
                          sharedKey: sharedKey,
                          timeout: timeout)
    }

    private static func port(fromListen listen: String) -> Int? {
        if listen.hasPrefix("[") {
            guard let close = listen.firstIndex(of: "]"),
                  close < listen.index(before: listen.endIndex),
                  listen[listen.index(after: close)] == ":" else {
                return nil
            }
            return validPort(String(listen[listen.index(close, offsetBy: 2)...]))
        }
        guard let colon = listen.lastIndex(of: ":"),
              !listen[..<colon].contains(":") else {
            return nil
        }
        return validPort(String(listen[listen.index(after: colon)...]))
    }

    private static func validPort(_ value: String) -> Int? {
        guard let port = Int(value), (1...65_535).contains(port) else {
            return nil
        }
        return port
    }
}
