import XCTest
import Network
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

    func testFetchUsesClosablePlainHTTPConnectionForSignedRemoteVersionProbe() async throws {
        let server = try OneShotVersionServer(key: rawKey, version: "v0.3.7")
        defer { server.cancel() }

        let version = try await PeerVersionProbe.fetch(host: "127.0.0.1", port: server.port, key: rawKey)

        XCTAssertEqual(version, "v0.3.7")
        XCTAssertTrue(server.sawConnectionClose)
    }
}

private final class OneShotVersionServer {
    private let key: Data
    private let version: String
    private let listener: NWListener
    private let queue = DispatchQueue(label: "clipfan-version-test-server")
    private let ready = DispatchSemaphore(value: 0)
    private let lock = NSLock()
    private var connectionClose = false

    private(set) var port: Int = 0

    var sawConnectionClose: Bool {
        lock.lock()
        defer { lock.unlock() }
        return connectionClose
    }

    init(key: Data, version: String) throws {
        self.key = key
        self.version = version
        listener = try NWListener(using: .tcp, on: 0)
        listener.newConnectionHandler = { [weak self] connection in
            self?.handle(connection)
        }
        listener.stateUpdateHandler = { [ready] state in
            if case .ready = state {
                ready.signal()
            }
        }
        listener.start(queue: queue)
        _ = ready.wait(timeout: .now() + 2)
        guard let rawPort = listener.port?.rawValue else {
            throw URLError(.cannotConnectToHost)
        }
        port = Int(rawPort)
    }

    func cancel() {
        listener.cancel()
    }

    private func handle(_ connection: NWConnection) {
        var request = Data()
        connection.start(queue: queue)

        func receive() {
            connection.receive(minimumIncompleteLength: 1, maximumLength: 8192) { data, _, _, error in
                if error != nil {
                    connection.cancel()
                    return
                }
                if let data {
                    request.append(data)
                }
                guard request.range(of: Data("\r\n\r\n".utf8)) != nil else {
                    receive()
                    return
                }

                let text = String(decoding: request, as: UTF8.self)
                if text.contains("\r\nConnection: close\r\n"),
                   let nonce = Self.header("X-Clipfan-Nonce", in: text) {
                    self.lock.lock()
                    self.connectionClose = true
                    self.lock.unlock()
                    self.sendSignedVersion(nonce: nonce, on: connection)
                } else {
                    self.sendBadRequest(on: connection)
                }
            }
        }

        receive()
    }

    private func sendSignedVersion(nonce: String, on connection: NWConnection) {
        let body = Data("{\"version\":\"\(version)\"}".utf8)
        let signature = clipfanResponseSignature(requestNonce: nonce, body: body, key: key)
        let header = Data("""
        HTTP/1.1 200 OK\r
        Content-Type: application/json\r
        Content-Length: \(body.count)\r
        X-Clipfan-Response-Sig: \(signature)\r
        Connection: close\r
        \r

        """.utf8)
        var response = Data()
        response.append(header)
        response.append(body)
        connection.send(content: response, completion: .contentProcessed { _ in
            connection.cancel()
        })
    }

    private func sendBadRequest(on connection: NWConnection) {
        connection.send(content: Data("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".utf8),
                        completion: .contentProcessed { _ in
            connection.cancel()
        })
    }

    private static func header(_ name: String, in request: String) -> String? {
        for line in request.components(separatedBy: "\r\n") {
            let parts = line.split(separator: ":", maxSplits: 1)
            if parts.count == 2, parts[0].caseInsensitiveCompare(name) == .orderedSame {
                return parts[1].trimmingCharacters(in: .whitespaces)
            }
        }
        return nil
    }
}
