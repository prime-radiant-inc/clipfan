import Foundation

struct VersionResponse: Codable {
    let version: String
}

enum PeerVersionStatus: Equatable {
    case current(String)
    case needsUpdate(String)
    case unknown

    var needsUpdate: Bool {
        switch self {
        case .current:
            return false
        case .needsUpdate, .unknown:
            return true
        }
    }

    var label: String {
        switch self {
        case .current(let version):
            return version
        case .needsUpdate(let version):
            return "\(version) · update available"
        case .unknown:
            return "version unknown · update recommended"
        }
    }
}

enum PeerUpdateAdvisor {
    static func status(remoteVersion: String, localVersion: String) -> PeerVersionStatus {
        if normalized(remoteVersion) == normalized(localVersion) {
            return .current(remoteVersion)
        }
        return .needsUpdate(remoteVersion)
    }

    static func status(forProbeError error: Error) -> PeerVersionStatus? {
        if case ClipfanAuthenticationError.badStatus(let status) = error,
           status == 404 {
            return .unknown
        }
        return nil
    }

    static func peersNeedingUpdate(peers: [Peer], statuses: [String: PeerVersionStatus]) -> [Peer] {
        peers.filter { statuses[$0.hostname]?.needsUpdate == true }
    }

    static func shouldOffer(localVersion: String?,
                            peers: [Peer],
                            statuses: [String: PeerVersionStatus],
                            lastOfferedVersion: String?) -> Bool {
        guard let local = normalized(localVersion) else { return false }
        if normalized(lastOfferedVersion) == local { return false }
        return !peersNeedingUpdate(peers: peers, statuses: statuses).isEmpty
    }

    private static func normalized(_ version: String?) -> String? {
        guard var version = version?.trimmingCharacters(in: .whitespacesAndNewlines),
              !version.isEmpty else { return nil }
        if version == "dev" { return nil }
        if version.first == "v" { version.removeFirst() }
        return version
    }
}

struct PeerUpdateVerificationResult: Equatable {
    let status: PeerVersionStatus?
    let detail: String
}

enum PeerUpdateVerifier {
    typealias Fetch = (String, Int, Data) async throws -> String

    static func verify(host: String,
                       port: Int,
                       key: Data,
                       expectedVersion: String,
                       attempts: Int,
                       delayNanoseconds: UInt64,
                       fetch: Fetch = PeerVersionProbe.fetch) async -> PeerUpdateVerificationResult {
        let totalAttempts = max(1, attempts)
        var lastResult = PeerUpdateVerificationResult(
            status: nil,
            detail: "\(host) did not answer /v1/version"
        )

        for attempt in 0..<totalAttempts {
            do {
                let remoteVersion = try await fetch(host, port, key)
                let status = PeerUpdateAdvisor.status(remoteVersion: remoteVersion,
                                                      localVersion: expectedVersion)
                let detail: String
                switch status {
                case .current:
                    detail = "\(host) is running \(remoteVersion)"
                case .needsUpdate:
                    detail = "\(host) answered with \(remoteVersion); expected \(expectedVersion)"
                case .unknown:
                    detail = "\(host) version is unknown"
                }
                lastResult = PeerUpdateVerificationResult(status: status, detail: detail)
                if case .current = status {
                    return lastResult
                }
            } catch {
                if let status = PeerUpdateAdvisor.status(forProbeError: error) {
                    lastResult = PeerUpdateVerificationResult(
                        status: status,
                        detail: "\(host) does not expose /v1/version yet"
                    )
                } else {
                    lastResult = PeerUpdateVerificationResult(
                        status: nil,
                        detail: "\(host) version probe failed: \(error.localizedDescription)"
                    )
                }
            }

            if attempt + 1 < totalAttempts, delayNanoseconds > 0 {
                try? await Task.sleep(nanoseconds: delayNanoseconds)
            }
        }

        return lastResult
    }
}

struct PreparedPeerVersionRequest {
    let request: URLRequest
    let requestNonce: String
}

enum PeerVersionProbe {
    static let requestURI = "/v1/version"

    static func request(host: String, port: Int, key: Data) throws -> PreparedPeerVersionRequest {
        var components = URLComponents()
        components.scheme = "http"
        components.host = host
        components.port = port
        components.path = requestURI
        guard let url = components.url else {
            throw URLError(.badURL)
        }

        let headers = clipfanSignatureHeaders(
            method: "GET",
            requestURI: requestURI,
            body: Data(),
            key: key
        )
        guard let requestNonce = headers["X-Clipfan-Nonce"] else {
            throw ClipfanAuthenticationError.missingRequestNonce
        }

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.timeoutInterval = 2
        for (header, value) in headers {
            request.setValue(value, forHTTPHeaderField: header)
        }
        return PreparedPeerVersionRequest(request: request, requestNonce: requestNonce)
    }

    static func decode(data: Data, response: URLResponse, requestNonce: String, key: Data) throws -> String {
        let body = try authenticatedClipfanData(data, response: response, requestNonce: requestNonce, key: key)
        return try JSONDecoder().decode(VersionResponse.self, from: body).version
    }

    static func fetch(host: String, port: Int, key: Data) async throws -> String {
        let prepared = try request(host: host, port: port, key: key)
        let (data, response) = try await URLSession.shared.data(for: prepared.request)
        return try decode(data: data, response: response, requestNonce: prepared.requestNonce, key: key)
    }
}
