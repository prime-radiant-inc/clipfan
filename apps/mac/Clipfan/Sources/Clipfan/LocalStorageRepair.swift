import Foundation

enum LocalStorageRepairCode: String, Equatable {
    case unsupportedRuntimeStorage = "unsupported_runtime_storage"
    case storageCheckInconclusive = "storage_check_inconclusive"
}

struct LocalStorageRepairRoot: Equatable {
    let role: String
    let normalizedPath: String
    let storageClass: String?
    let reason: String?
}

struct LocalStorageRepairPrompt: Equatable {
    let code: LocalStorageRepairCode
    let title: String
    let message: String
    let roots: [LocalStorageRepairRoot]
    let sshTransportEnabled: Bool
    let requiresDaemonEndpoint: Bool
}

enum LocalStorageRepair {
    static func prompt(code: String, roots: [LocalStorageRepairRoot] = []) -> LocalStorageRepairPrompt? {
        guard let repairCode = LocalStorageRepairCode(rawValue: code) else { return nil }
        switch repairCode {
        case .unsupportedRuntimeStorage:
            return LocalStorageRepairPrompt(
                code: repairCode,
                title: "Unsupported clipfan storage",
                message: "Move clipfan config and state to local storage on this Mac, then restart clipfan. Network homes, shared homes, and cloud-synced folders are not supported for SSH transport.",
                roots: roots,
                sshTransportEnabled: false,
                requiresDaemonEndpoint: false
            )
        case .storageCheckInconclusive:
            return LocalStorageRepairPrompt(
                code: repairCode,
                title: "Storage check needs repair",
                message: "Move clipfan config and state to local storage or fix the storage permissions, then restart clipfan. SSH transport stays disabled until the offline storage check passes.",
                roots: roots,
                sshTransportEnabled: false,
                requiresDaemonEndpoint: false
            )
        }
    }

    static func prompt(message: String, roots: [LocalStorageRepairRoot] = []) -> LocalStorageRepairPrompt? {
        let parsedRoots = roots.isEmpty ? parseRoots(message) : roots
        if message.contains(LocalStorageRepairCode.unsupportedRuntimeStorage.rawValue) {
            return prompt(code: LocalStorageRepairCode.unsupportedRuntimeStorage.rawValue, roots: parsedRoots)
        }
        if message.contains(LocalStorageRepairCode.storageCheckInconclusive.rawValue) {
            return prompt(code: LocalStorageRepairCode.storageCheckInconclusive.rawValue, roots: parsedRoots)
        }
        return nil
    }

    private static func parseRoots(_ message: String) -> [LocalStorageRepairRoot] {
        message.split(separator: "\n").compactMap { line in
            parseRootLine(String(line))
        }
    }

    private static func parseRootLine(_ line: String) -> LocalStorageRepairRoot? {
        guard line.hasPrefix("- ") else { return nil }
        let body = String(line.dropFirst(2))
        guard let colon = body.firstIndex(of: ":") else { return nil }
        let role = String(body[..<colon]).trimmingCharacters(in: .whitespaces)
        var rest = body[body.index(after: colon)...].trimmingCharacters(in: .whitespaces)
        var reason: String?
        if let reasonRange = rest.range(of: " reason=") {
            reason = String(rest[reasonRange.upperBound...]).trimmingCharacters(in: .whitespaces)
            rest = String(rest[..<reasonRange.lowerBound]).trimmingCharacters(in: .whitespaces)
        }
        var storageClass: String?
        if rest.hasSuffix(")"), let open = rest.lastIndex(of: "("), open > rest.startIndex {
            storageClass = String(rest[rest.index(after: open)..<rest.index(before: rest.endIndex)])
            rest = String(rest[..<open]).trimmingCharacters(in: .whitespaces)
        }
        guard !role.isEmpty, !rest.isEmpty else { return nil }
        return LocalStorageRepairRoot(role: role,
                                      normalizedPath: rest,
                                      storageClass: storageClass,
                                      reason: reason)
    }
}
