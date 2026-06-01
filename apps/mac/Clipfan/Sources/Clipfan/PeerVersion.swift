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
