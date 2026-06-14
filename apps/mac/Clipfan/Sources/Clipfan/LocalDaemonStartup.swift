import Foundation

enum LocalDaemonStartupDiagnostic: String, Equatable {
    case configV2RequiresHKDFClient = "config_v2_requires_hkdf_client"
}

struct LocalDaemonStartupPreparation: Equatable {
    let diagnostic: LocalDaemonStartupDiagnostic?
    let permitsWholeConfigRawKeyWrites: Bool
    let requiresHKDFClient: Bool
}

enum LocalDaemonStartup {
    static func childLaunchEnvironment(from environment: [String: String] = ProcessInfo.processInfo.environment) -> [String: String] {
        var result = environment
        let home = environment["HOME"] ?? FileManager.default.homeDirectoryForCurrentUser.path
        let existingPath = environment["PATH"] ?? "/usr/bin:/bin:/usr/sbin:/sbin"
        let preferredPaths = [
            "\(home)/.local/bin",
            "/opt/homebrew/bin",
            "/usr/local/bin",
        ]
        var pathComponents: [String] = []
        for path in preferredPaths + existingPath.split(separator: ":").map(String.init) where !pathComponents.contains(path) {
            pathComponents.append(path)
        }
        result["PATH"] = pathComponents.joined(separator: ":")
        return result
    }

    static func prepare(configData: Data?, clientSupportsHKDF: Bool) -> LocalDaemonStartupPreparation {
        let requiresHKDF = configVersion(configData) >= 2
        if requiresHKDF && !clientSupportsHKDF {
            return LocalDaemonStartupPreparation(
                diagnostic: .configV2RequiresHKDFClient,
                permitsWholeConfigRawKeyWrites: false,
                requiresHKDFClient: true
            )
        }

        return LocalDaemonStartupPreparation(
            diagnostic: nil,
            permitsWholeConfigRawKeyWrites: !requiresHKDF,
            requiresHKDFClient: requiresHKDF
        )
    }

    private static func configVersion(_ data: Data?) -> Int {
        guard let data,
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let version = obj["config_version"] as? Int else {
            return 1
        }
        return version
    }
}
