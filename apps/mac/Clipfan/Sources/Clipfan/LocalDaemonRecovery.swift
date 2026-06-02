import Foundation

struct LocalDaemonRecoveryCapabilities: Equatable {
    let signedListenerRepairAvailable: Bool
    let offlineListenerRepairAvailable: Bool
    let localFleetResetAvailable: Bool

    init(signedListenerRepairAvailable: Bool = false,
         offlineListenerRepairAvailable: Bool = false,
         localFleetResetAvailable: Bool = false) {
        self.signedListenerRepairAvailable = signedListenerRepairAvailable
        self.offlineListenerRepairAvailable = offlineListenerRepairAvailable
        self.localFleetResetAvailable = localFleetResetAvailable
    }
}

enum LocalDaemonRecoveryDisposition: Equatable {
    case compatible
    case recoverable
    case blocked
}

enum LocalDaemonRecoveryPath: Equatable {
    case none
    case signedListenerRepair
    case offlineListenerRepair
    case confirmedLocalFleetReset
}

struct LocalDaemonRecoveryPlan: Equatable {
    let diagnostic: LocalDaemonStartupDiagnostic?
    let disposition: LocalDaemonRecoveryDisposition
    let recoveryPath: LocalDaemonRecoveryPath
    let permitsWholeConfigRawKeyWrites: Bool
    let requiresHKDFClient: Bool
}

enum LocalDaemonRecovery {
    static func plan(configData: Data?,
                     clientSupportsHKDF: Bool,
                     capabilities: LocalDaemonRecoveryCapabilities = LocalDaemonRecoveryCapabilities()) -> LocalDaemonRecoveryPlan {
        let config = parsedConfig(configData)
        let requiresHKDF = config.version >= 2

        guard requiresHKDF else {
            return LocalDaemonRecoveryPlan(
                diagnostic: nil,
                disposition: .compatible,
                recoveryPath: .none,
                permitsWholeConfigRawKeyWrites: true,
                requiresHKDFClient: false
            )
        }

        guard !clientSupportsHKDF else {
            return LocalDaemonRecoveryPlan(
                diagnostic: nil,
                disposition: .compatible,
                recoveryPath: .none,
                permitsWholeConfigRawKeyWrites: false,
                requiresHKDFClient: true
            )
        }

        if config.hasUsableSharedKey && capabilities.signedListenerRepairAvailable {
            return blockedRawConfigWritePlan(
                disposition: .recoverable,
                recoveryPath: .signedListenerRepair
            )
        }

        if config.unsafeListener && capabilities.offlineListenerRepairAvailable {
            return blockedRawConfigWritePlan(
                disposition: .recoverable,
                recoveryPath: .offlineListenerRepair
            )
        }

        if !config.hasUsableSharedKey && !config.unsafeListener && capabilities.localFleetResetAvailable {
            return blockedRawConfigWritePlan(
                disposition: .recoverable,
                recoveryPath: .confirmedLocalFleetReset
            )
        }

        return blockedRawConfigWritePlan(
            disposition: .blocked,
            recoveryPath: .none
        )
    }

    private static func blockedRawConfigWritePlan(disposition: LocalDaemonRecoveryDisposition,
                                                  recoveryPath: LocalDaemonRecoveryPath) -> LocalDaemonRecoveryPlan {
        LocalDaemonRecoveryPlan(
            diagnostic: .configV2RequiresHKDFClient,
            disposition: disposition,
            recoveryPath: recoveryPath,
            permitsWholeConfigRawKeyWrites: false,
            requiresHKDFClient: true
        )
    }

    private static func parsedConfig(_ data: Data?) -> (version: Int, hasUsableSharedKey: Bool, unsafeListener: Bool) {
        guard let data,
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return (version: 1, hasUsableSharedKey: false, unsafeListener: false)
        }

        let version = obj["config_version"] as? Int ?? 1
        let sharedKey = obj["shared_key"] as? String
        let listen = obj["listen"] as? String

        return (
            version: version,
            hasUsableSharedKey: isStandard32ByteBase64(sharedKey),
            unsafeListener: isUnsafeListener(listen)
        )
    }

    private static func isStandard32ByteBase64(_ value: String?) -> Bool {
        guard let value, !value.isEmpty,
              let decoded = Data(base64Encoded: value),
              decoded.count == 32 else {
            return false
        }
        return decoded.base64EncodedString() == value
    }

    private static func isUnsafeListener(_ listen: String?) -> Bool {
        guard let listen, !listen.isEmpty else {
            return false
        }
        if listen == ":7853" || listen == "0.0.0.0:7853" || listen == "[::]:7853" {
            return false
        }
        guard let parsed = listenerHostAndPort(listen) else {
            return true
        }
        let host = parsed.host
        return !(host == "127.0.0.1" || host == "::1" || host.lowercased() == "localhost")
    }

    private static func listenerHostAndPort(_ listen: String) -> (host: String, port: Int)? {
        if listen.hasPrefix("[") {
            guard let close = listen.firstIndex(of: "]"),
                  close < listen.index(before: listen.endIndex),
                  listen[listen.index(after: close)] == ":" else {
                return nil
            }
            let portStart = listen.index(close, offsetBy: 2)
            guard let port = validPort(String(listen[portStart...])) else {
                return nil
            }
            return (String(listen[listen.index(after: listen.startIndex)..<close]), port)
        }
        guard let colon = listen.lastIndex(of: ":") else {
            return nil
        }
        if listen[..<colon].contains(":") {
            return nil
        }
        let portStart = listen.index(after: colon)
        guard let port = validPort(String(listen[portStart...])) else {
            return nil
        }
        return (String(listen[..<colon]), port)
    }

    private static func validPort(_ value: String) -> Int? {
        guard let port = Int(value), port >= 1, port <= 65_535 else {
            return nil
        }
        return port
    }
}
