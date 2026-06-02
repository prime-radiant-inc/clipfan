import Foundation

struct LocalDaemonRecoveryCapabilities: Equatable {
    let signedListenerRepairAvailable: Bool
    let offlineListenerRepairAvailable: Bool

    init(signedListenerRepairAvailable: Bool = false,
         offlineListenerRepairAvailable: Bool = false) {
        self.signedListenerRepairAvailable = signedListenerRepairAvailable
        self.offlineListenerRepairAvailable = offlineListenerRepairAvailable
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

        if capabilities.offlineListenerRepairAvailable {
            return blockedRawConfigWritePlan(
                disposition: .recoverable,
                recoveryPath: .offlineListenerRepair
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

    private static func parsedConfig(_ data: Data?) -> (version: Int, hasUsableSharedKey: Bool) {
        guard let data,
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return (version: 1, hasUsableSharedKey: false)
        }

        let version = obj["config_version"] as? Int ?? 1
        let sharedKey = obj["shared_key"] as? String

        return (
            version: version,
            hasUsableSharedKey: sharedKey?.isEmpty == false
        )
    }
}
