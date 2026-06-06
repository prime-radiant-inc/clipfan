import Foundation

struct Peer: Codable, Identifiable, Hashable {
    var id: String { hostname }
    let hostname: String
    let port: Int
    let last_push_ts: Date?
    let last_push_ok: Bool
    let last_push_err: String?
    let last_recv_ts: Date?
    let transport: String?
    let ssh_host: String?
    let ssh_port: Int?
    let ssh_user: String?
    let ssh_active: Bool?
    let ssh_pending: Bool?
    let ssh_status: String?
    let ssh_last_connect_ts: Date?
    let ssh_last_ack_ts: Date?
    let ssh_last_error: String?
    let ssh_last_error_ts: Date?

    init(hostname: String,
         port: Int,
         last_push_ts: Date?,
         last_push_ok: Bool,
         last_push_err: String?,
         last_recv_ts: Date?,
         transport: String? = nil,
         ssh_host: String? = nil,
         ssh_port: Int? = nil,
         ssh_user: String? = nil,
         ssh_active: Bool? = nil,
         ssh_pending: Bool? = nil,
         ssh_status: String? = nil,
         ssh_last_connect_ts: Date? = nil,
         ssh_last_ack_ts: Date? = nil,
         ssh_last_error: String? = nil,
         ssh_last_error_ts: Date? = nil) {
        self.hostname = hostname
        self.port = port
        self.last_push_ts = last_push_ts
        self.last_push_ok = last_push_ok
        self.last_push_err = last_push_err
        self.last_recv_ts = last_recv_ts
        self.transport = transport
        self.ssh_host = ssh_host
        self.ssh_port = ssh_port
        self.ssh_user = ssh_user
        self.ssh_active = ssh_active
        self.ssh_pending = ssh_pending
        self.ssh_status = ssh_status
        self.ssh_last_connect_ts = ssh_last_connect_ts
        self.ssh_last_ack_ts = ssh_last_ack_ts
        self.ssh_last_error = ssh_last_error
        self.ssh_last_error_ts = ssh_last_error_ts
    }

    var isSSHTransport: Bool {
        transport?.lowercased() == "ssh"
    }
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

struct LocalDaemonSSHPeerConfigResponse: Codable, Equatable {
    let peer: LocalDaemonSSHPeer
    let config_revision: UInt64?
    let revision_state: String
    let config_version: Int?

    var configRevision: UInt64? { config_revision }
    var revisionState: String { revision_state }
    var configVersion: Int? { config_version }
}

struct LocalDaemonHostRemoveResponse: Codable, Equatable {
    let host_id: String
    let removed_static_peer: Bool
    let removed_ssh_peer: Bool
    let config_revision: UInt64?
    let revision_state: String
    let config_version: Int?
    let ssh_cleanup_status: [String: LocalDaemonJSONValue]?

    var hostID: String { host_id }
    var removedStaticPeer: Bool { removed_static_peer }
    var removedSSHPeer: Bool { removed_ssh_peer }
    var configRevision: UInt64? { config_revision }
    var revisionState: String { revision_state }
    var configVersion: Int? { config_version }
    var sshCleanupStatus: [String: LocalDaemonJSONValue]? { ssh_cleanup_status }
}

struct LocalDaemonSSHPeer: Codable, Equatable {
    let id: String
    let enabled: Bool?
    let accept: Bool?
    let connect: Bool?
    let persistent: Bool?
    let on_demand: Bool?
    let ssh_host: String?
    let ssh_user: String?
    let ssh_port: Int?
    let install_path: String?
    let gateway_path: String?
    let migration_state: String?
    let proof: [String: LocalDaemonJSONValue]?
    let cleanup_status: [String: LocalDaemonJSONValue]?
    let additionalFields: [String: LocalDaemonJSONValue]

    var sshHost: String? { ssh_host }
    var sshUser: String? { ssh_user }
    var sshPort: Int? { ssh_port }
    var installPath: String? { install_path }
    var gatewayPath: String? { gateway_path }
    var migrationState: String? { migration_state }
    var cleanupStatus: [String: LocalDaemonJSONValue]? { cleanup_status }
    var sharedKey: String? { nil }
    var privateKey: String? { nil }

    private enum CodingKeys: String, CodingKey, CaseIterable {
        case id
        case enabled
        case accept
        case connect
        case persistent
        case on_demand
        case ssh_host
        case ssh_user
        case ssh_port
        case install_path
        case gateway_path
        case migration_state
        case proof
        case cleanup_status
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled)
        accept = try container.decodeIfPresent(Bool.self, forKey: .accept)
        connect = try container.decodeIfPresent(Bool.self, forKey: .connect)
        persistent = try container.decodeIfPresent(Bool.self, forKey: .persistent)
        on_demand = try container.decodeIfPresent(Bool.self, forKey: .on_demand)
        ssh_host = try container.decodeIfPresent(String.self, forKey: .ssh_host)
        ssh_user = try container.decodeIfPresent(String.self, forKey: .ssh_user)
        ssh_port = try container.decodeIfPresent(Int.self, forKey: .ssh_port)
        install_path = try container.decodeIfPresent(String.self, forKey: .install_path)
        gateway_path = try container.decodeIfPresent(String.self, forKey: .gateway_path)
        migration_state = try container.decodeIfPresent(String.self, forKey: .migration_state)
        proof = Self.redactingSecretLikeFields(in: try container.decodeIfPresent([String: LocalDaemonJSONValue].self, forKey: .proof))
        cleanup_status = Self.redactingSecretLikeFields(in: try container.decodeIfPresent([String: LocalDaemonJSONValue].self, forKey: .cleanup_status))

        let dynamic = try decoder.container(keyedBy: LocalDaemonDynamicCodingKey.self)
        var extras: [String: LocalDaemonJSONValue] = [:]
        let known = Set(CodingKeys.allCases.map(\.stringValue))
        for key in dynamic.allKeys where !known.contains(key.stringValue) && !LocalDaemonSSHPeer.secretLikeField(key.stringValue) {
            extras[key.stringValue] = try dynamic.decode(LocalDaemonJSONValue.self, forKey: key)
                .redactingSecretLikeFields(LocalDaemonSSHPeer.secretLikeField)
        }
        additionalFields = extras
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encodeIfPresent(enabled, forKey: .enabled)
        try container.encodeIfPresent(accept, forKey: .accept)
        try container.encodeIfPresent(connect, forKey: .connect)
        try container.encodeIfPresent(persistent, forKey: .persistent)
        try container.encodeIfPresent(on_demand, forKey: .on_demand)
        try container.encodeIfPresent(ssh_host, forKey: .ssh_host)
        try container.encodeIfPresent(ssh_user, forKey: .ssh_user)
        try container.encodeIfPresent(ssh_port, forKey: .ssh_port)
        try container.encodeIfPresent(install_path, forKey: .install_path)
        try container.encodeIfPresent(gateway_path, forKey: .gateway_path)
        try container.encodeIfPresent(migration_state, forKey: .migration_state)
        try container.encodeIfPresent(proof, forKey: .proof)
        try container.encodeIfPresent(cleanup_status, forKey: .cleanup_status)

        var dynamic = encoder.container(keyedBy: LocalDaemonDynamicCodingKey.self)
        let known = Set(CodingKeys.allCases.map(\.stringValue))
        for (key, value) in additionalFields where !known.contains(key) && !Self.secretLikeField(key) {
            try dynamic.encode(value, forKey: LocalDaemonDynamicCodingKey(stringValue: key))
        }
    }

    private static func secretLikeField(_ field: String) -> Bool {
        let lower = field.lowercased()
        return lower == "shared_key" ||
            lower == "private_key" ||
            lower == "private_key_path" ||
            lower == "sync_key" ||
            lower == "sync_key_path" ||
            lower == "accept_proof" ||
            lower == "connect_proof" ||
            lower.contains("shared_key") ||
            lower.contains("secret") ||
            lower.contains("token") ||
            lower.contains("seed") ||
            lower.contains("password") ||
            lower.contains("credential") ||
            lower.contains("private") ||
            lower.contains("hmac") ||
            lower.contains("nonce") ||
            lower.contains("encrypted") ||
            lower.contains("clipboard") ||
            lower.contains("signed_frame")
    }

    private static func redactingSecretLikeFields(in fields: [String: LocalDaemonJSONValue]?) -> [String: LocalDaemonJSONValue]? {
        guard let fields else { return nil }
        return LocalDaemonJSONValue.object(fields)
            .redactingSecretLikeFields(secretLikeField)
            .objectValue
    }
}

struct LocalDaemonSSHPeerUpsertRequest: Codable, Equatable {
    let expected_config_revision: UInt64
    let peer: LocalDaemonSSHPeerUpsertFields

    init(expectedConfigRevision: UInt64, peer: LocalDaemonSSHPeerUpsertFields) {
        self.expected_config_revision = expectedConfigRevision
        self.peer = peer
    }
}

struct LocalDaemonSSHPeerUpsertFields: Codable, Equatable {
    let id: String?
    let enabled: Bool?
    let accept: Bool?
    let connect: Bool?
    let persistent: Bool?
    let on_demand: Bool?
    let ssh_host: String?
    let ssh_user: String?
    let ssh_port: Int?
    let install_path: String?
    let gateway_path: String?
    let migration_state: String?

    init(id: String? = nil,
         enabled: Bool? = nil,
         accept: Bool? = nil,
         connect: Bool? = nil,
         persistent: Bool? = nil,
         onDemand: Bool? = nil,
         sshHost: String? = nil,
         sshUser: String? = nil,
         sshPort: Int? = nil,
         installPath: String? = nil,
         gatewayPath: String? = nil,
         migrationState: String? = nil) {
        self.id = id
        self.enabled = enabled
        self.accept = accept
        self.connect = connect
        self.persistent = persistent
        self.on_demand = onDemand
        self.ssh_host = sshHost
        self.ssh_user = sshUser
        self.ssh_port = sshPort
        self.install_path = installPath
        self.gateway_path = gatewayPath
        self.migration_state = migrationState
    }
}

struct LocalDaemonSSHPeerProofPatchRequest: Codable, Equatable {
    let expected_config_revision: UInt64
    let accept_proof: LocalDaemonSSHPeerDirectionalProofPatch?
    let connect_proof: LocalDaemonSSHPeerDirectionalProofPatch?

    init(expectedConfigRevision: UInt64,
         acceptProof: LocalDaemonSSHPeerDirectionalProofPatch? = nil,
         connectProof: LocalDaemonSSHPeerDirectionalProofPatch? = nil) {
        self.expected_config_revision = expectedConfigRevision
        self.accept_proof = acceptProof
        self.connect_proof = connectProof
    }
}

struct LocalDaemonSSHPeerDirectionalProofPatch: Codable, Equatable {
    let key_id: String
    let gateway_path: String
    let verified_at: String
    let verified_by: String

    init(keyID: String, gatewayPath: String, verifiedAt: String, verifiedBy: String) {
        self.key_id = keyID
        self.gateway_path = gatewayPath
        self.verified_at = verifiedAt
        self.verified_by = verifiedBy
    }
}

struct LocalDaemonSSHPeerTransitionRequest: Codable, Equatable {
    let expected_config_revision: UInt64
    let from_state: String
    let to_state: String
    let reason: String
    let log_id: String
    let failed_phase: String?
    let remote_secret_absence_proof: LocalDaemonSSHPeerRemoteSecretAbsenceProof?

    init(expectedConfigRevision: UInt64,
         fromState: String,
         toState: String,
         reason: String,
         logID: String,
         failedPhase: String? = nil,
         remoteSecretAbsenceProof: LocalDaemonSSHPeerRemoteSecretAbsenceProof? = nil) {
        self.expected_config_revision = expectedConfigRevision
        self.from_state = fromState
        self.to_state = toState
        self.reason = reason
        self.log_id = logID
        self.failed_phase = failedPhase
        self.remote_secret_absence_proof = remoteSecretAbsenceProof
    }
}

struct LocalDaemonSSHPeerRemoteSecretAbsenceProof: Codable, Equatable {
    let failed_phase: String
    let secret_write_command_spawned: Bool
    let absence_verified_by: String
    let verified_at: String
    let remote_config_revision: UInt64?
    let log_id: String

    init(failedPhase: String,
         secretWriteCommandSpawned: Bool,
         absenceVerifiedBy: String,
         verifiedAt: String,
         remoteConfigRevision: UInt64? = nil,
         logID: String) {
        self.failed_phase = failedPhase
        self.secret_write_command_spawned = secretWriteCommandSpawned
        self.absence_verified_by = absenceVerifiedBy
        self.verified_at = verifiedAt
        self.remote_config_revision = remoteConfigRevision
        self.log_id = logID
    }
}

struct LocalDaemonSSHPeerDisableRequest: Codable, Equatable {
    let expected_config_revision: UInt64
    let reason: String

    init(expectedConfigRevision: UInt64, reason: String) {
        self.expected_config_revision = expectedConfigRevision
        self.reason = reason
    }
}

struct LocalDaemonSSHPeerDeleteRequest: Codable, Equatable {
    let expected_config_revision: UInt64
    let reason: String
    let log_id: String

    init(expectedConfigRevision: UInt64, reason: String, logID: String) {
        self.expected_config_revision = expectedConfigRevision
        self.reason = reason
        self.log_id = logID
    }
}

struct LocalDaemonHostRemoveRequest: Codable, Equatable {
    let expected_revision_state: String
    let expected_config_revision: UInt64?
    let reason: String
    let log_id: String

    init(expectedRevisionState: String,
         expectedConfigRevision: UInt64?,
         reason: String,
         logID: String) {
        self.expected_revision_state = expectedRevisionState
        self.expected_config_revision = expectedConfigRevision
        self.reason = reason
        self.log_id = logID
    }
}

struct LocalDaemonAPIErrorResponse: Codable, Equatable {
    let error: String?
    let code: String?

    var stableCode: String? { code ?? error }
}

enum LocalDaemonJSONValue: Codable, Equatable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case object([String: LocalDaemonJSONValue])
    case array([LocalDaemonJSONValue])
    case null

    var stringValue: String? {
        if case .string(let value) = self { return value }
        return nil
    }

    var boolValue: Bool? {
        if case .bool(let value) = self { return value }
        return nil
    }

    var objectValue: [String: LocalDaemonJSONValue]? {
        if case .object(let value) = self { return value }
        return nil
    }

    func redactingSecretLikeFields(_ isSecretLike: (String) -> Bool) -> LocalDaemonJSONValue {
        switch self {
        case .object(let fields):
            var redacted: [String: LocalDaemonJSONValue] = [:]
            for (key, value) in fields where !isSecretLike(key) {
                redacted[key] = value.redactingSecretLikeFields(isSecretLike)
            }
            return .object(redacted)
        case .array(let values):
            return .array(values.map { $0.redactingSecretLikeFields(isSecretLike) })
        case .string, .number, .bool, .null:
            return self
        }
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([String: LocalDaemonJSONValue].self) {
            self = .object(value)
        } else {
            self = .array(try container.decode([LocalDaemonJSONValue].self))
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value):
            try container.encode(value)
        case .number(let value):
            try container.encode(value)
        case .bool(let value):
            try container.encode(value)
        case .object(let value):
            try container.encode(value)
        case .array(let value):
            try container.encode(value)
        case .null:
            try container.encodeNil()
        }
    }
}

private struct LocalDaemonDynamicCodingKey: CodingKey, Hashable {
    let stringValue: String
    let intValue: Int? = nil

    init(stringValue: String) {
        self.stringValue = stringValue
    }

    init?(intValue: Int) {
        nil
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
