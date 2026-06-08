import Foundation

/// What the app should do on launch, derived from whether the daemon binary is
/// installed, whether it matches the bundled payload, and whether the daemon is
/// currently answering on the local port.
enum LaunchDecision: Equatable {
    /// Daemon is answering — launch normally, no setup UI.
    case normal
    /// Installed daemon is older/different than the daemon bundled in this app.
    case upgradeExisting
    /// Binary is installed but the daemon is down — kickstart / child-launch it.
    case restartExisting
    /// No daemon installed — run the guided first-run install.
    case firstRunInstall
    /// Config v2 has been written and the installed binary cannot safely read it.
    case blockedDowngrade

    static func decide(binaryInstalled: Bool,
                       daemonHealthy: Bool,
                       installedBinaryCurrent: Bool,
                       configV2Present: Bool = false,
                       installedBinarySupportsConfigV2: Bool = true) -> LaunchDecision {
        if binaryInstalled && configV2Present && !installedBinarySupportsConfigV2 {
            return installedBinaryCurrent ? .blockedDowngrade : .upgradeExisting
        }
        if binaryInstalled && !installedBinaryCurrent { return .upgradeExisting }
        if daemonHealthy { return .normal }
        return binaryInstalled ? .restartExisting : .firstRunInstall
    }
}

enum BootstrapInstallMode {
    case setup
    case upgradeExisting
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

/// True when at least one peer shows a real, failing send attempt — the symptom
/// of macOS withholding Local Network access from the daemon. Idle peers (no send
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

    static var bundledDaemonBinary: URL? {
        bundledPayload?.appendingPathComponent("clipfan-darwin-\(currentGoArch)")
    }

    static var installedBinaryCurrent: Bool {
        installedBinaryCurrent(installed: daemonBinary, bundled: bundledDaemonBinary)
    }

    static func installedBinaryCurrent(installed: URL, bundled: URL?) -> Bool {
        guard FileManager.default.fileExists(atPath: installed.path) else { return false }
        guard let bundled,
              FileManager.default.fileExists(atPath: bundled.path) else { return true }
        if let installedVersion = binaryVersion(installed),
           let bundledVersion = binaryVersion(bundled) {
            return installedVersion == bundledVersion
        }
        return filesEqual(installed, bundled)
    }

    static func configV2Present(configURL: URL = Installer.localConfigURL()) -> Bool {
        guard let data = try? Data(contentsOf: configURL),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let version = object["config_version"] as? NSNumber else {
            return false
        }
        return version.intValue >= 2
    }

    static func needsConfigV2CapabilityProbe(binaryInstalled: Bool,
                                             installedBinaryCurrent: Bool,
                                             configV2Present: Bool) -> Bool {
        binaryInstalled && installedBinaryCurrent && configV2Present
    }

    static func installedBinarySupportsConfigV2(binary: URL = daemonBinary) -> Bool {
        guard FileManager.default.fileExists(atPath: binary.path) else { return false }
        let proc = Process()
        proc.executableURL = binary
        proc.arguments = ["version", "--json"]
        let out = Pipe()
        proc.standardOutput = out
        proc.standardError = Pipe()
        do {
            try proc.run()
            proc.waitUntilExit()
        } catch {
            return false
        }
        guard proc.terminationStatus == 0 else { return false }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let capabilities = object["capabilities"] as? [String: Any],
              let configV2 = capabilities["config_v2"] as? Bool else {
            return false
        }
        return configV2
    }

    static var installLog: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/clipfan-install.log")
    }

    static func installerArguments(mode: BootstrapInstallMode) -> [String] {
        switch mode {
        case .setup: return []
        case .upgradeExisting: return ["--no-tmux"]
        }
    }

    static func filesEqual(_ a: URL, _ b: URL) -> Bool {
        guard let aSize = try? FileManager.default.attributesOfItem(atPath: a.path)[.size] as? NSNumber,
              let bSize = try? FileManager.default.attributesOfItem(atPath: b.path)[.size] as? NSNumber,
              aSize == bSize else { return false }
        guard let aData = try? Data(contentsOf: a),
              let bData = try? Data(contentsOf: b) else { return false }
        return aData == bData
    }

    static func binaryVersion(_ binary: URL) -> String? {
        let proc = Process()
        proc.executableURL = binary
        proc.arguments = ["version"]
        let out = Pipe()
        proc.standardOutput = out
        proc.standardError = Pipe()
        do {
            try proc.run()
            proc.waitUntilExit()
        } catch {
            return nil
        }
        guard proc.terminationStatus == 0 else { return nil }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        let version = String(decoding: data, as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return version.isEmpty ? nil : version
    }

    private static var currentGoArch: String {
        #if arch(arm64)
        return "arm64"
        #elseif arch(x86_64)
        return "amd64"
        #else
        return ""
        #endif
    }

    /// Run the bundled install.sh, redirecting combined stdout+stderr to `log`.
    /// Returns true on a clean (exit 0) install. Blocking work runs off-main.
    ///
    /// Setup uses install.sh's default tmux behavior, while daemon upgrades pass
    /// `--no-tmux` so app updates do not alter shell configuration.
    static func runInstaller(script: URL, logTo log: URL, mode: BootstrapInstallMode = .setup) async -> Bool {
        await Task.detached(priority: .userInitiated) {
            FileManager.default.createFile(atPath: log.path, contents: nil)
            guard let handle = try? FileHandle(forWritingTo: log) else { return false }
            defer { try? handle.close() }
            let proc = Process()
            proc.executableURL = URL(fileURLWithPath: "/bin/bash")
            proc.arguments = [script.path] + installerArguments(mode: mode)
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

    static func storagePreflightRepairPrompt(binary: URL = daemonBinary) async -> LocalStorageRepairPrompt? {
        await Task.detached(priority: .userInitiated) {
            guard FileManager.default.fileExists(atPath: binary.path) else { return nil }
            let proc = Process()
            proc.executableURL = binary
            proc.arguments = ["storage-preflight"]
            let out = Pipe()
            proc.standardOutput = out
            proc.standardError = out
            do {
                try proc.run()
                proc.waitUntilExit()
            } catch {
                return nil
            }
            let data = out.fileHandleForReading.readDataToEndOfFile()
            let text = String(decoding: data, as: UTF8.self)
            return LocalStorageRepair.prompt(message: text)
        }.value
    }

    static func storageRepairFailureMessage(_ prompt: LocalStorageRepairPrompt) -> String {
        "\(prompt.code.rawValue): \(prompt.message)"
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
    func install(mode: BootstrapInstallMode = .setup) async {
        if case .installing = state { return }
        let firstStep = mode == .upgradeExisting ? "Updating background service…" : "Preparing background service…"
        state = .installing(progress: [firstStep])
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
        guard await Bootstrap.runInstaller(script: script, logTo: Bootstrap.installLog, mode: mode) else {
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
            if let repair = await Bootstrap.storagePreflightRepairPrompt() {
                state = .failed(message: Bootstrap.storageRepairFailureMessage(repair),
                                logPath: installLogPath)
                return
            }
            state = .failed(message: "The service installed but didn't come online. See the log.",
                            logPath: installLogPath)
        }
    }

    func presentStorageRepairIfAvailable() async -> Bool {
        guard let repair = await Bootstrap.storagePreflightRepairPrompt() else { return false }
        state = .failed(message: Bootstrap.storageRepairFailureMessage(repair),
                        logPath: installLogPath)
        return true
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
