import Foundation

struct LocalDaemonIdentityProof: Equatable {
    let configPath: String
    let stateDirectory: String
    let authVersion: String
    let hostID: String
}

struct LocalDaemonEndpoint: Equatable {
    enum Purpose: Equatable {
        case signed
        case signedCompatibility
        case healthOnly
    }

    let url: URL
    let port: Int
    let purpose: Purpose
}

struct LocalDaemonDiscoveryPlan: Equatable {
    let signedEndpoints: [LocalDaemonEndpoint]
    let healthOnlyEndpoints: [LocalDaemonEndpoint]
}

enum LocalDaemonDiscovery {
    static let defaultPort = 7853

    static func plan(configData: Data?, identityProof: LocalDaemonIdentityProof? = nil) -> LocalDaemonDiscoveryPlan {
        let config = parseConfig(configData)
        let listen = (config?["listen"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines)
        let portValue = config?["port"] as? Int
        let listenEndpoint = parseListen(listen)
        let derivedPort = listenEndpoint?.port ?? validPort(portValue) ?? defaultPort

        var signed: [LocalDaemonEndpoint] = []
        if let listenEndpoint, listenEndpoint.isLoopback {
            signed.append(endpoint(host: listenEndpoint.host, port: derivedPort, purpose: .signed))
        }
        if derivedPort != defaultPort, identityProof != nil {
            signed.append(endpoint(port: defaultPort, purpose: .signedCompatibility))
        }

        var healthOnly = [endpoint(port: derivedPort, purpose: .healthOnly)]
        if derivedPort != defaultPort {
            healthOnly.append(endpoint(port: defaultPort, purpose: .healthOnly))
        }

        return LocalDaemonDiscoveryPlan(signedEndpoints: signed, healthOnlyEndpoints: healthOnly)
    }

    private static func parseConfig(_ data: Data?) -> [String: Any]? {
        guard let data else { return nil }
        return (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
    }

    private static func endpoint(port: Int, purpose: LocalDaemonEndpoint.Purpose) -> LocalDaemonEndpoint {
        endpoint(host: "127.0.0.1", port: port, purpose: purpose)
    }

    private static func endpoint(host: String, port: Int, purpose: LocalDaemonEndpoint.Purpose) -> LocalDaemonEndpoint {
        let url: URL
        if host.contains(":") {
            url = URL(string: "http://[\(host)]:\(port)")!
        } else {
            var components = URLComponents()
            components.scheme = "http"
            components.host = host
            components.port = port
            url = components.url!
        }
        return LocalDaemonEndpoint(url: url, port: port, purpose: purpose)
    }

    private static func validPort(_ port: Int?) -> Int? {
        guard let port, (1...65535).contains(port) else { return nil }
        return port
    }

    private static func parseListen(_ listen: String?) -> (host: String, port: Int, isLoopback: Bool)? {
        guard let listen, !listen.isEmpty else { return nil }
        if listen.hasPrefix(":") {
            return validPort(Int(listen.dropFirst())).map { ("", $0, false) }
        }
        if listen.hasPrefix("[") {
            guard let close = listen.firstIndex(of: "]") else { return nil }
            let host = String(listen[listen.index(after: listen.startIndex)..<close])
            let remainder = listen[listen.index(after: close)...]
            guard remainder.hasPrefix(":") else { return nil }
            let portText = remainder.dropFirst()
            return validPort(Int(portText)).map { (host, $0, isLoopbackHost(host)) }
        }
        guard let colon = listen.lastIndex(of: ":") else { return nil }
        let host = String(listen[..<colon])
        let portText = listen[listen.index(after: colon)...]
        return validPort(Int(portText)).map { (host, $0, isLoopbackHost(host)) }
    }

    private static func isLoopbackHost(_ host: String) -> Bool {
        let normalized = host.lowercased()
        if normalized == "localhost" || normalized == "::1" {
            return true
        }
        let octets = normalized.split(separator: ".")
        guard octets.count == 4, octets[0] == "127" else {
            return false
        }
        return octets.dropFirst().allSatisfy { part in
            guard let value = Int(part) else { return false }
            return (0...255).contains(value)
        }
    }
}
