import Foundation

struct LocalDaemonPreparedRequest: Equatable {
    let request: URLRequest
    let requestNonce: String
}

enum LocalDaemonRequestError: Error, Equatable {
    case healthOnlyEndpoint
    case invalidRequestURI(String)
    case missingRequestNonce
}

enum LocalDaemonRequestBuilder {
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
}
