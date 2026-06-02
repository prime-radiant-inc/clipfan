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
    let status: String?
    let hostname: String?
    let configured_listen: String?
    let effective_repair_listen: String?
    let parse_error: String?
    let safe_mode: Bool?
    let safe_mode_schema: String?
    let listener_repair_status: String?
    let last_failure_phase: String?
    let safe_mode_logs_available: Bool?
    let peer_sync_started: Bool?
    let config_version: Int?
    let config_revision: UInt64?
    let revision_state: String?
    let port: Int?

    var safeMode: LocalDaemonSafeModeStatus? {
        LocalDaemonSafeModeStatus.fromPayload(
            status: status,
            hostname: hostname,
            configuredListen: configured_listen,
            effectiveRepairListen: effective_repair_listen,
            parseError: parse_error,
            safeMode: safe_mode,
            safeModeSchema: safe_mode_schema,
            listenerRepairStatus: listener_repair_status,
            lastFailurePhase: last_failure_phase,
            safeModeLogsAvailable: safe_mode_logs_available,
            peerSyncStarted: peer_sync_started,
            configVersion: config_version,
            configRevision: config_revision,
            revisionState: revision_state,
            port: port
        )
    }
}

struct LocalDaemonStatusResponse: Codable, Equatable {
    let status: String?
    let hostname: String?
    let configured_listen: String?
    let effective_repair_listen: String?
    let parse_error: String?
    let safe_mode: Bool?
    let safe_mode_schema: String?
    let listener_repair_status: String?
    let last_failure_phase: String?
    let safe_mode_logs_available: Bool?
    let peer_sync_started: Bool?
    let config_version: Int?
    let config_revision: UInt64?
    let revision_state: String?
    let port: Int?

    var safeMode: LocalDaemonSafeModeStatus? {
        LocalDaemonSafeModeStatus.fromPayload(
            status: status,
            hostname: hostname,
            configuredListen: configured_listen,
            effectiveRepairListen: effective_repair_listen,
            parseError: parse_error,
            safeMode: safe_mode,
            safeModeSchema: safe_mode_schema,
            listenerRepairStatus: listener_repair_status,
            lastFailurePhase: last_failure_phase,
            safeModeLogsAvailable: safe_mode_logs_available,
            peerSyncStarted: peer_sync_started,
            configVersion: config_version,
            configRevision: config_revision,
            revisionState: revision_state,
            port: port
        )
    }
}

struct LocalDaemonSafeModeStatus: Equatable {
    let status: String?
    let hostname: String?
    let configuredListen: String?
    let effectiveRepairListen: String?
    let parseError: String?
    let safeMode: Bool
    let safeModeSchema: String?
    let listenerRepairStatus: String?
    let lastFailurePhase: String?
    let safeModeLogsAvailable: Bool
    let peerSyncStarted: Bool?
    let configVersion: Int?
    let configRevision: UInt64?
    let revisionState: String?
    let port: Int?

    init(status: String?,
         hostname: String?,
         configuredListen: String?,
         effectiveRepairListen: String?,
         parseError: String?,
         safeMode: Bool,
         safeModeSchema: String?,
         listenerRepairStatus: String?,
         lastFailurePhase: String?,
         safeModeLogsAvailable: Bool,
         peerSyncStarted: Bool?,
         configVersion: Int?,
         configRevision: UInt64?,
         revisionState: String?,
         port: Int?) {
        self.status = status
        self.hostname = hostname
        self.configuredListen = configuredListen
        self.effectiveRepairListen = effectiveRepairListen
        self.parseError = parseError
        self.safeMode = safeMode
        self.safeModeSchema = safeModeSchema
        self.listenerRepairStatus = listenerRepairStatus
        self.lastFailurePhase = lastFailurePhase
        self.safeModeLogsAvailable = safeModeLogsAvailable
        self.peerSyncStarted = peerSyncStarted
        self.configVersion = configVersion
        self.configRevision = configRevision
        self.revisionState = revisionState
        self.port = port
    }

    init(active: Bool,
         reason: String?,
         listen: String?,
         effectiveRepairListen: String?,
         expectedRevisionState: String?,
         expectedRevision: Int?,
         port: Int? = nil) {
        self.status = reason
        self.hostname = nil
        self.configuredListen = listen
        self.effectiveRepairListen = effectiveRepairListen
        self.parseError = reason
        self.safeMode = active
        self.safeModeSchema = active ? "safe_mode_v1" : nil
        self.listenerRepairStatus = active ? "needs_repair" : nil
        self.lastFailurePhase = active ? "listener_safe_mode" : nil
        self.safeModeLogsAvailable = active
        self.peerSyncStarted = false
        self.configVersion = nil
        self.configRevision = expectedRevision.map(UInt64.init)
        self.revisionState = expectedRevisionState
        self.port = port
    }

    var active: Bool {
        safeMode || safeModeSchema == "safe_mode_v1" || status == "safe_mode_signed_repair"
    }

    var repairable: Bool {
        active && peerSyncStarted == false && effectiveRepairListen?.isEmpty == false
    }

    var canRepairListener: Bool {
        repairable && revisionState?.isEmpty == false && configRevision != nil && port != nil
    }

    var reason: String? { parseError ?? status ?? lastFailurePhase }
    var listen: String? { configuredListen }
    var expectedRevisionState: String? { revisionState }
    var expectedRevision: Int? {
        guard let configRevision else { return nil }
        return Int(configRevision)
    }

    var listenerIsLoopback: Bool {
        guard let listen = configuredListen,
              let host = Self.host(fromListen: listen) else { return false }
        return Self.isLoopbackHost(host)
    }

    var permitsConnectedState: Bool {
        !active || listenerIsLoopback
    }

    static func fromPayload(status: String?,
                            hostname: String?,
                            configuredListen: String?,
                            effectiveRepairListen: String?,
                            parseError: String?,
                            safeMode: Bool?,
                            safeModeSchema: String?,
                            listenerRepairStatus: String?,
                            lastFailurePhase: String?,
                            safeModeLogsAvailable: Bool?,
                            peerSyncStarted: Bool?,
                            configVersion: Int?,
                            configRevision: UInt64?,
                            revisionState: String?,
                            port: Int?) -> LocalDaemonSafeModeStatus? {
        let isSafeMode = safeMode == true || safeModeSchema == "safe_mode_v1" || status == "safe_mode_signed_repair"
        guard isSafeMode else { return nil }
        return LocalDaemonSafeModeStatus(
            status: status,
            hostname: hostname,
            configuredListen: configuredListen,
            effectiveRepairListen: effectiveRepairListen,
            parseError: parseError,
            safeMode: safeMode ?? true,
            safeModeSchema: safeModeSchema,
            listenerRepairStatus: listenerRepairStatus,
            lastFailurePhase: lastFailurePhase,
            safeModeLogsAvailable: safeModeLogsAvailable ?? false,
            peerSyncStarted: peerSyncStarted,
            configVersion: configVersion,
            configRevision: configRevision,
            revisionState: revisionState,
            port: port
        )
    }

    private static func host(fromListen listen: String) -> String? {
        if listen.hasPrefix(":") { return "" }
        if listen.hasPrefix("[") {
            guard let close = listen.firstIndex(of: "]") else { return nil }
            return String(listen[listen.index(after: listen.startIndex)..<close])
        }
        guard let colon = listen.lastIndex(of: ":") else { return nil }
        return String(listen[..<colon])
    }

    private static func isLoopbackHost(_ host: String) -> Bool {
        let normalized = host.lowercased()
        if normalized == "localhost" || normalized == "::1" { return true }
        let octets = normalized.split(separator: ".")
        guard octets.count == 4, octets[0] == "127" else { return false }
        return octets.dropFirst().allSatisfy { part in
            guard let value = Int(part) else { return false }
            return (0...255).contains(value)
        }
    }
}

struct LocalDaemonListenerRepairStatus: Codable, Equatable {
    let listen: String
    let port: Int
    let previous_listen: String?
    let configured_listen: String
    let effective_repair_listen: String
    let parse_error: String?
    let safe_mode: Bool
    let config_version: Int?
    let config_revision: UInt64?
    let revision_state: String

    var effectiveRepairListen: String { effective_repair_listen }
    var configuredListen: String { configured_listen }
    var configRevision: UInt64? { config_revision }
    var revisionState: String { revision_state }
}

struct LocalDaemonSafeModeLogResponse: Codable, Equatable {
    let peer_id: String?
    let safe_mode: Bool?
    let safe_mode_schema: String?
    let listener_repair_status: String?
    let last_failure_phase: String?
    let safe_mode_logs_available: Bool?
    let entries: [LocalDaemonSafeModeLogEntry]
    let truncated: Bool?

    var formattedLog: String {
        entries.map(\.formattedLine).joined(separator: "\n")
    }
}

struct LocalDaemonSafeModeLogEntry: Codable, Equatable, Identifiable {
    var id: String { [ts, source, log_id, phase, code, message].compactMap { $0 }.joined(separator: "|") }
    let ts: String?
    let source: String?
    let durable: Bool?
    let log_id: String?
    let phase: String?
    let code: String?
    let message: String

    var formattedLine: String {
        let timestamp = ts.map { "\($0) " } ?? ""
        let scope = [source, phase].compactMap { value in
            guard let value, !value.isEmpty else { return nil }
            return value
        }.joined(separator: "/")
        let scopeText = scope.isEmpty ? "" : "[\(scope)] "
        let codeText = code.map { "\($0) " } ?? ""
        return "\(timestamp)\(scopeText)\(codeText)\(message)"
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }
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
        d.dateDecodingStrategy = .custom { dec in
            let s = try dec.singleValueContainer().decode(String.self)
            if let date = clipfanDate(s) {
                return date
            }
            throw DecodingError.dataCorrupted(.init(codingPath: dec.codingPath, debugDescription: "bad date: \(s)"))
        }
        return d
    }()

    fileprivate static func clipfanDate(_ s: String) -> Date? {
        let isoFractional = ISO8601DateFormatter()
        isoFractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime]
        if let date = isoFractional.date(from: s) ?? iso.date(from: s) {
            return date
        }
        // Fallback for the zero-value form Go emits when a timestamp
        // hasn't been set (0001-01-01T00:00:00Z).
        if s.hasPrefix("0001-") {
            return Date.distantPast
        }
        return nil
    }
}
