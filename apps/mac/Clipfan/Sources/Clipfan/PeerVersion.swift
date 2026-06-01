import Foundation
import Network

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
        let (data, response) = try await PlainHTTPClient.data(for: prepared.request)
        return try decode(data: data, response: response, requestNonce: prepared.requestNonce, key: key)
    }
}

private enum PlainHTTPClient {
    private static let queue = DispatchQueue(label: "clipfan-peer-version-http")
    private static let maxResponseBytes = 1 << 20

    static func data(for request: URLRequest) async throws -> (Data, URLResponse) {
        try await withCheckedThrowingContinuation { continuation in
            let exchange = PlainHTTPExchange(continuation: continuation)
            let connection: NWConnection
            let bytes: Data
            do {
                let prepared = try prepare(request)
                connection = prepared.connection
                bytes = prepared.bytes
            } catch {
                continuation.resume(throwing: error)
                return
            }

            exchange.connection = connection
            let timeout = request.timeoutInterval > 0 ? request.timeoutInterval : 2
            queue.asyncAfter(deadline: .now() + timeout) {
                exchange.finish(.failure(URLError(.timedOut)))
            }

            connection.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    connection.send(content: bytes, completion: .contentProcessed { error in
                        if let error {
                            exchange.finish(.failure(error))
                            return
                        }
                        receive(from: connection, into: Data(), for: request, exchange: exchange)
                    })
                case .failed(let error):
                    exchange.finish(.failure(error))
                default:
                    break
                }
            }
            connection.start(queue: queue)
        }
    }

    private static func prepare(_ request: URLRequest) throws -> (connection: NWConnection, bytes: Data) {
        guard let url = request.url,
              url.scheme == "http",
              let host = url.host else {
            throw URLError(.unsupportedURL)
        }
        guard (request.httpMethod ?? "GET") == "GET" else {
            throw URLError(.unsupportedURL)
        }
        let rawPort = url.port ?? 80
        guard (1...65535).contains(rawPort),
              let port = NWEndpoint.Port(rawValue: UInt16(rawPort)) else {
            throw URLError(.badURL)
        }
        let connection = NWConnection(host: NWEndpoint.Host(host), port: port, using: .tcp)
        return (connection, try requestBytes(for: request, host: host, port: rawPort))
    }

    private static func requestBytes(for request: URLRequest, host: String, port: Int) throws -> Data {
        guard let url = request.url else { throw URLError(.badURL) }
        let path = url.query.map { "\(url.path)?\($0)" } ?? url.path
        let hostForHeader = host.contains(":") ? "[\(host)]" : host
        let hostHeader = port == 80 ? hostForHeader : "\(hostForHeader):\(port)"
        var lines = [
            "GET \(path.isEmpty ? "/" : path) HTTP/1.1",
            "Host: \(hostHeader)",
            "Connection: close",
        ]

        for (header, value) in (request.allHTTPHeaderFields ?? [:]).sorted(by: { $0.key < $1.key }) {
            let lower = header.lowercased()
            if lower == "host" || lower == "connection" { continue }
            lines.append("\(header): \(value)")
        }
        lines.append("")
        lines.append("")
        return Data(lines.joined(separator: "\r\n").utf8)
    }

    private static func receive(from connection: NWConnection,
                                into received: Data,
                                for request: URLRequest,
                                exchange: PlainHTTPExchange) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { data, _, isComplete, error in
            if let error {
                exchange.finish(.failure(error))
                return
            }

            var buffer = received
            if let data {
                buffer.append(data)
            }
            if buffer.count > maxResponseBytes {
                exchange.finish(.failure(URLError(.dataLengthExceedsMaximum)))
                return
            }

            if isComplete {
                do {
                    exchange.finish(.success(try parseResponse(buffer, url: request.url)))
                } catch {
                    exchange.finish(.failure(error))
                }
                return
            }

            receive(from: connection, into: buffer, for: request, exchange: exchange)
        }
    }

    private static func parseResponse(_ data: Data, url: URL?) throws -> (Data, URLResponse) {
        guard let url,
              let headerRange = data.range(of: Data("\r\n\r\n".utf8)) else {
            throw URLError(.badServerResponse)
        }
        let headerData = data[..<headerRange.lowerBound]
        let bodyStart = headerRange.upperBound
        let headerText = String(decoding: headerData, as: UTF8.self)
        var lines = headerText.components(separatedBy: "\r\n")
        guard !lines.isEmpty else { throw URLError(.badServerResponse) }
        let statusParts = lines.removeFirst().split(separator: " ", maxSplits: 2)
        guard statusParts.count >= 2,
              let status = Int(statusParts[1]) else {
            throw URLError(.badServerResponse)
        }

        var headers: [String: String] = [:]
        for line in lines {
            let parts = line.split(separator: ":", maxSplits: 1)
            if parts.count == 2 {
                headers[String(parts[0])] = parts[1].trimmingCharacters(in: .whitespaces)
            }
        }

        let bodyData = data[bodyStart...]
        let body: Data
        if let lengthText = headers.first(where: { $0.key.caseInsensitiveCompare("Content-Length") == .orderedSame })?.value,
           let length = Int(lengthText),
           length <= bodyData.count {
            body = Data(bodyData.prefix(length))
        } else {
            body = Data(bodyData)
        }

        guard let response = HTTPURLResponse(url: url,
                                             statusCode: status,
                                             httpVersion: "HTTP/1.1",
                                             headerFields: headers) else {
            throw URLError(.badServerResponse)
        }
        return (body, response)
    }
}

private final class PlainHTTPExchange: @unchecked Sendable {
    private let lock = NSLock()
    private var completed = false
    private var continuation: CheckedContinuation<(Data, URLResponse), Error>?

    var connection: NWConnection?

    init(continuation: CheckedContinuation<(Data, URLResponse), Error>) {
        self.continuation = continuation
    }

    func finish(_ result: Result<(Data, URLResponse), Error>) {
        lock.lock()
        guard !completed else {
            lock.unlock()
            return
        }
        completed = true
        let continuation = continuation
        self.continuation = nil
        let connection = connection
        self.connection = nil
        lock.unlock()

        connection?.cancel()
        switch result {
        case .success(let value):
            continuation?.resume(returning: value)
        case .failure(let error):
            continuation?.resume(throwing: error)
        }
    }
}
