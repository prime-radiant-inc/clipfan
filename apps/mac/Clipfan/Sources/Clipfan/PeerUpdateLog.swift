import Foundation

enum PeerOperationLogRedactor {
    private struct Rule {
        let regex: NSRegularExpression
        let template: String
    }

    private static let rules: [Rule] = {
        let patterns: [(String, String)] = [
            (#"(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----"#, "[redacted-private-key]"),
            (#"(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*$"#, "[redacted-private-key]"),
            (#"(?i)(-i\s+)(\S+)"#, "$1[redacted-path]"),
            (#"(?i)("(?:shared_key|private_key|private_key_path|sync_key|sync_key_path|token|hmac|nonce|password|credential)"\s*:\s*)("[^"]*"|[^,\s}]+)"#, "$1\"[redacted]\""),
            (#"(?i)((?:shared_key|private_key|private_key_path|sync_key|sync_key_path|token|hmac|nonce|password|credential)\s*[=:]\s*)([^\s,}]+)"#, "$1[redacted]"),
            (#"(?:/Users/[^/\s]+|~)/\.ssh/[^\s,;:]+"#, "[redacted-ssh-path]")
        ]
        return patterns.compactMap { pattern, template in
            guard let regex = try? NSRegularExpression(pattern: pattern) else { return nil }
            return Rule(regex: regex, template: template)
        }
    }()

    static func redact(_ text: String, maxCharacters: Int = 8_000) -> String {
        var redacted = text
        for rule in rules {
            redacted = replacing(rule: rule, in: redacted)
        }
        guard redacted.count > maxCharacters else { return redacted }
        let omitted = redacted.count - maxCharacters
        return "\(redacted.prefix(maxCharacters))\n[truncated \(omitted) characters]"
    }

    private static func replacing(rule: Rule, in text: String) -> String {
        let range = NSRange(text.startIndex..<text.endIndex, in: text)
        return rule.regex.stringByReplacingMatches(in: text, options: [], range: range, withTemplate: rule.template)
    }
}

struct AddPeerOperationFailure: Equatable {
    let message: String
    let logText: String

    init(host: String, error: Error, log: AddPeerOperationLog) {
        self.message = Self.message(host: host, error: error)
        self.logText = log.text
    }

    private static func message(host: String, error: Error) -> String {
        if let scopedError = error as? LocalDaemonSSHPeerConfigError {
            return "\(host): \(scopedPeerConfigMessage(scopedError))"
        }
        return "Failed on \(host): \(PeerOperationLogRedactor.redact(error.localizedDescription))"
    }

    private static func scopedPeerConfigMessage(_ error: LocalDaemonSSHPeerConfigError) -> String {
        switch error {
        case .api(let code, _):
            switch code {
            case localDaemonConfigRevisionConflictCode,
                 localDaemonSSHPeerMigrationStateChangeNotAllowedCode:
                return "Local peer config changed; retry."
            case "missing_config_revision":
                return "Local peer config is missing a revision; reload and retry."
            case "safe_mode_active":
                return "Local daemon is in safe mode; repair the listener and retry."
            default:
                return "Scoped peer config failed (\(code)); retry."
            }
        case .missingHTTPResponse:
            return "Local daemon did not return an HTTP response; retry."
        case .missingResponseSignature:
            return "Local daemon response was unsigned; retry."
        case .badResponseSignature:
            return "Local daemon response signature was invalid; retry."
        }
    }
}

private struct PeerOperationLogLines: Equatable {
    private var lines: [String]

    init(startLine: String) {
        lines = [startLine]
    }

    var text: String {
        lines.joined(separator: "\n")
    }

    mutating func record(_ progress: InstallProgress) {
        lines.append("[\(progress.step)] \(PeerOperationLogRedactor.redact(progress.detail))")
    }

    mutating func recordFailure(_ error: Error) {
        lines.append("[Error] \(operationFailureText(error))")
    }

    mutating func append(_ line: String) {
        lines.append(line)
    }
}

struct AddPeerOperationLog: Equatable {
    private var storage: PeerOperationLogLines

    init(host: String) {
        storage = PeerOperationLogLines(startLine: "[\(host)] starting add peer")
    }

    var text: String { storage.text }

    mutating func record(_ progress: InstallProgress) {
        storage.record(progress)
    }

    mutating func recordFailure(_ error: Error) {
        storage.recordFailure(error)
    }
}

struct PeerUpdateLog: Equatable {
    private var storage: PeerOperationLogLines

    init(host: String) {
        storage = PeerOperationLogLines(startLine: "[\(host)] starting peer update")
    }

    var text: String { storage.text }

    mutating func record(_ progress: InstallProgress) {
        storage.record(progress)
    }

    mutating func recordSuccess(version: String) {
        storage.append("[Done] updated to \(version)")
    }

    mutating func recordFailure(_ error: Error) {
        storage.recordFailure(error)
    }
}

private func operationFailureText(_ error: Error) -> String {
    if let commandFailure = error as? InstallCommandFailure {
        return PeerOperationLogRedactor.redact(commandFailure.logText)
    }
    if let scopedError = error as? LocalDaemonSSHPeerConfigError {
        switch scopedError {
        case .api(let code, let statusCode):
            return "scoped peer config failed: code=\(code) status=\(statusCode)"
        case .missingHTTPResponse:
            return "scoped peer config failed: missing_http_response"
        case .missingResponseSignature:
            return "scoped peer config failed: missing_response_signature"
        case .badResponseSignature:
            return "scoped peer config failed: bad_response_signature"
        }
    }
    return PeerOperationLogRedactor.redact(error.localizedDescription)
}
