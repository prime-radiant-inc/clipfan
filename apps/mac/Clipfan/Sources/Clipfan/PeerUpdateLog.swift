import Foundation

struct PeerUpdateLog: Equatable {
    private var lines: [String]

    init(host: String) {
        lines = ["[\(host)] starting peer update"]
    }

    var text: String {
        lines.joined(separator: "\n")
    }

    mutating func record(_ progress: InstallProgress) {
        lines.append("[\(progress.step)] \(progress.detail)")
    }

    mutating func recordSuccess(version: String) {
        lines.append("[Done] updated to \(version)")
    }

    mutating func recordFailure(_ error: Error) {
        if let commandFailure = error as? InstallCommandFailure {
            lines.append("[Error] \(commandFailure.logText)")
        } else {
            lines.append("[Error] \(error.localizedDescription)")
        }
    }
}

