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

/// Filesystem locations and side-effecting steps for first-run install. Pure
/// path helpers are static; the blocking installer subprocess runs off the main
/// actor so the Welcome window stays responsive.
enum Bootstrap {
    static var daemonBinary: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".local/bin/clipfan")
    }

    /// Whether the daemon binary is present on disk (the first-run signal).
    static var binaryInstalled: Bool {
        FileManager.default.fileExists(atPath: daemonBinary.path)
    }

    /// The bundled dist payload (all-arch binaries + install.sh) staged into the
    /// app by build-app.sh. nil if the app was built without it.
    static var bundledPayload: URL? {
        Bundle.main.resourceURL?.appendingPathComponent("dist")
    }

    static var installLog: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/clipfan-install.log")
    }

    /// Run the bundled install.sh, redirecting combined stdout+stderr to `log`.
    /// Returns true on a clean (exit 0) install. Blocking work runs off-main.
    ///
    /// No tmux flag is passed, so install.sh runs in its `auto` mode — wiring up
    /// tmux only if tmux is installed — matching a hand-run terminal install.
    static func runInstaller(script: URL, logTo log: URL) async -> Bool {
        await Task.detached(priority: .userInitiated) {
            FileManager.default.createFile(atPath: log.path, contents: nil)
            guard let handle = try? FileHandle(forWritingTo: log) else { return false }
            defer { try? handle.close() }
            let proc = Process()
            proc.executableURL = URL(fileURLWithPath: "/bin/bash")
            proc.arguments = [script.path]
            proc.currentDirectoryURL = script.deletingLastPathComponent()
            proc.standardOutput = handle
            proc.standardError = handle
            do {
                try proc.run()
                proc.waitUntilExit()
                return proc.terminationStatus == 0
            } catch {
                try? handle.write(contentsOf: Data("\nfailed to launch install.sh: \(error)\n".utf8))
                return false
            }
        }.value
    }
}

/// Drives the first-run install and publishes progress for the Welcome window.
/// Reusable for the Settings "Re-run setup" recovery path.
@MainActor
final class BootstrapController: ObservableObject {
    static let shared = BootstrapController()
    @Published var state: SetupState = .idle

    private init() {}

    var installLogPath: String { Bootstrap.installLog.path }

    /// Locate the bundled installer, clear quarantine, run install.sh, then wait
    /// for the daemon to come online. Reachable from first-run, the Settings
    /// "Re-run setup" button, and the Welcome "Retry" button, so guard against a
    /// second run landing on the same log file and `launchctl` reload.
    func install() async {
        if case .installing = state { return }
        state = .installing(progress: ["Preparing background service…"])
        guard let dist = Bootstrap.bundledPayload else {
            state = .failed(message: "Bundled installer not found in the app.",
                            logPath: installLogPath)
            return
        }
        let script = dist.appendingPathComponent("install.sh")
        guard FileManager.default.fileExists(atPath: script.path) else {
            state = .failed(message: "Missing installer at \(script.path).",
                            logPath: installLogPath)
            return
        }

        // Best-effort: clear quarantine so launchd will run the copied binaries
        // even if the app arrived via download or AirDrop.
        _ = try? await Installer.run("/usr/bin/xattr",
                                     ["-dr", "com.apple.quarantine", dist.path])

        state = state.appendingProgress("Installing…")
        guard await Bootstrap.runInstaller(script: script, logTo: Bootstrap.installLog) else {
            state = .failed(message: "Install failed. See the log for details.",
                            logPath: installLogPath)
            return
        }

        state = state.appendingProgress("Starting…")
        if await waitForDaemon() {
            await DaemonClient.shared.refresh()
            await DaemonClient.shared.refreshHistory()
            state = .success
        } else {
            state = .failed(message: "The service installed but didn't come online. See the log.",
                            logPath: installLogPath)
        }
    }

    /// Poll the local daemon until it answers, or give up after ~10s.
    private func waitForDaemon(attempts: Int = 20) async -> Bool {
        for _ in 0..<attempts {
            await DaemonClient.shared.refresh()
            if DaemonClient.shared.connected { return true }
            try? await Task.sleep(nanoseconds: 500_000_000)
        }
        return false
    }
}
