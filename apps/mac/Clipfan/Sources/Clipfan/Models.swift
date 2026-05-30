import Foundation

struct Peer: Codable, Identifiable, Hashable {
    var id: String { hostname }
    let hostname: String
    let port: Int
    let last_push_ts: Date?
    let last_push_ok: Bool
    let last_push_err: String?
    let last_recv_ts: Date?
}

struct PeersResponse: Codable {
    let origin: String
    let peers: [Peer]
    let version: String?
    let max_history: Int?
}

struct TailscalePeer: Identifiable, Hashable {
    var id: String { hostName }
    let hostName: String
    let dnsName: String
    let ip: String
    let os: String
    let online: Bool
    let user: String
}

extension JSONDecoder {
    static let clipfan: JSONDecoder = {
        let d = JSONDecoder()
        let isoFractional = ISO8601DateFormatter()
        isoFractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime]
        d.dateDecodingStrategy = .custom { dec in
            let s = try dec.singleValueContainer().decode(String.self)
            if let date = isoFractional.date(from: s) ?? iso.date(from: s) {
                return date
            }
            // Fallback for the zero-value form Go emits when a timestamp
            // hasn't been set (0001-01-01T00:00:00Z).
            if s.hasPrefix("0001-") {
                return Date.distantPast
            }
            throw DecodingError.dataCorrupted(.init(codingPath: dec.codingPath, debugDescription: "bad date: \(s)"))
        }
        return d
    }()
}
