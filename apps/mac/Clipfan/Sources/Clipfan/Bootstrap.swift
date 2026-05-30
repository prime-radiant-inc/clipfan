import Foundation

/// What the app should do on launch, derived from whether the daemon binary is
/// installed and whether the daemon is currently answering on the local port.
enum LaunchDecision: Equatable {
    /// Daemon is answering — launch normally, no setup UI.
    case normal
    /// Binary is installed but the daemon is down — kickstart / child-launch it.
    case restartExisting
    /// No daemon installed — run the guided first-run install.
    case firstRunInstall

    static func decide(binaryInstalled: Bool, daemonHealthy: Bool) -> LaunchDecision {
        if daemonHealthy { return .normal }
        return binaryInstalled ? .restartExisting : .firstRunInstall
    }
}

/// State of the first-run setup, rendered by the Welcome window.
enum SetupState: Equatable {
    case idle
    case installing(progress: [String])
    case success
    case failed(message: String, logPath: String)

    /// Append a progress line. Starting from `.idle` enters `.installing`;
    /// terminal states (`.success` / `.failed`) are left unchanged.
    func appendingProgress(_ line: String) -> SetupState {
        switch self {
        case .idle: return .installing(progress: [line])
        case .installing(let lines): return .installing(progress: lines + [line])
        case .success, .failed: return self
        }
    }
}

/// True when at least one peer shows a real, failing push attempt — the symptom
/// of macOS withholding Local Network access from the daemon. Idle peers (no push
/// attempted yet) and the unset-timestamp sentinel are not evidence of a block, so
/// solo users and freshly-added peers don't see the nudge.
func shouldPromptLocalNetwork(peers: [Peer]) -> Bool {
    peers.contains { peer in
        guard !peer.last_push_ok else { return false }
        guard let ts = peer.last_push_ts, ts > .distantPast else { return false }
        return true
    }
}
