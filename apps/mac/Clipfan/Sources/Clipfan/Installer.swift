import Foundation

enum InstallError: LocalizedError {
    case sshFailed(String, String)
    case scpFailed(String, String)
    case unsupportedHost(String)
    case missingPayload(String)
    case configIO(String)

    var errorDescription: String? {
        switch self {
        case .sshFailed(let host, let msg): return "ssh \(host) failed: \(msg)"
        case .scpFailed(let host, let msg): return "scp to \(host) failed: \(msg)"
        case .unsupportedHost(let s): return "unsupported host: \(s)"
        case .missingPayload(let s): return "missing install payload: \(s)"
        case .configIO(let s): return "config IO: \(s)"
        }
    }
}

struct InstallProgress {
    var step: String
    var detail: String
}

struct InstallCommandFailure: LocalizedError {
    let executable: String
    let arguments: [String]
    let exitStatus: Int32
    let stdout: String
    let stderr: String

    var commandLine: String {
        ([executable] + arguments).joined(separator: " ")
    }

    var errorDescription: String? {
        "\(commandLine) failed with exit \(exitStatus)"
    }

    var logText: String {
        var parts = ["\(commandLine) failed with exit \(exitStatus)"]
        if !stdout.isEmpty {
            parts.append("stdout:\n\(stdout)")
        }
        if !stderr.isEmpty {
            parts.append("stderr:\n\(stderr)")
        }
        return parts.joined(separator: "\n\n")
    }
}

/// Drives the same scp + install.sh playbook used by `cc-clip`-style remote
/// installs. Source binaries are read out of $HOME/.local/share/clipfan
/// (staged by `dist/install.sh` on the host running the menubar app).
actor Installer {
    typealias CommandRunner = (String, [String]) async throws -> String

    static let shareDir: URL = {
        if let xdg = ProcessInfo.processInfo.environment["XDG_DATA_HOME"] {
            return URL(fileURLWithPath: xdg).appendingPathComponent("clipfan")
        }
        return FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".local/share/clipfan")
    }()

    static func localConfigURL(environment: [String: String] = ProcessInfo.processInfo.environment,
                               homeDirectory: URL = FileManager.default.homeDirectoryForCurrentUser) -> URL {
        if let xdg = environment["XDG_CONFIG_HOME"], !xdg.isEmpty {
            return URL(fileURLWithPath: xdg).appendingPathComponent("clipfan/config.json")
        }
        return homeDirectory.appendingPathComponent(".config/clipfan/config.json")
    }

    /// tmuxFlag maps the Add-Peer tmux checkbox to the install.sh flag. The GUI
    /// always passes an explicit flag so installs are never subject to auto-detect.
    static func tmuxFlag(_ withTmux: Bool) -> String {
        withTmux ? "--with-tmux" : "--no-tmux"
    }

    static func remoteStageCommand() -> String {
        "set -e; stage=$(mktemp -d /tmp/clipfan-install.XXXXXX); chmod 700 \"$stage\"; printf '%s\\n' \"$stage\""
    }

    static func remoteInstallCommand(stage: String, withTmux: Bool) -> String {
        let quotedStage = shellSingleQuote(stage)
        return """
        set -e
        stage=\(quotedStage)
        trap 'rm -rf "$stage"' EXIT
        mkdir -p ~/.config/clipfan
        install -m 0600 "$stage/config.json" ~/.config/clipfan/config.json
        cd "$stage" && bash install.sh \(tmuxFlag(withTmux))
        """
    }

    static func remoteUpdateCommand(stage: String) -> String {
        remoteUpdateCommand(stage: stage,
                            payloadBinaryName: nil,
                            enforceStorageAbort: GeneratedSSHTransportGates.peerHTTPRuntimeDisabled ||
                                GeneratedSSHTransportGates.configV2WriteEnabled)
    }

    static func remoteUpdateCommand(stage: String,
                                    payloadBinaryName: String?,
                                    enforceStorageAbort: Bool) -> String {
        let quotedStage = shellSingleQuote(stage)
        if enforceStorageAbort, payloadBinaryName == nil {
            return """
            set -e
            stage=\(quotedStage)
            trap 'rm -rf "$stage"' EXIT
            printf '%s\\n' 'storage_check_inconclusive: missing staged storage preflight binary' >&2
            exit 1
            """
        }

        let storageAbortPrelude: String
        if enforceStorageAbort, let payloadBinaryName {
            storageAbortPrelude = """
            preflight_bin="$stage/\(payloadBinaryName)"
            if [ ! -x "$preflight_bin" ]; then
                chmod 700 "$preflight_bin" 2>/dev/null || true
            fi
            if [ ! -x "$preflight_bin" ]; then
                printf '%s\\n' 'storage_check_inconclusive: staged storage preflight binary is not executable' >&2
                exit 1
            fi
            preflight_status=0
            preflight_output="$("$preflight_bin" storage-preflight 2>&1)" || preflight_status=$?
            if [ "$preflight_status" -ne 0 ]; then
                printf '%s\\n' "$preflight_output" >&2
                if printf '%s\\n' "$preflight_output" | grep -Eq 'unsupported_runtime_storage|storage_check_inconclusive'; then
                    service_still_active=0
                    user_uid="$(id -u 2>/dev/null || printf '%s' "${UID:-}")"
                    if command -v launchctl >/dev/null 2>&1; then
                        plist="$HOME/Library/LaunchAgents/com.primeradiant.clipfan.plist"
                        launchctl bootout "gui/$user_uid/com.primeradiant.clipfan" >/dev/null 2>&1 || \
                            launchctl bootout "gui/$user_uid" "$plist" >/dev/null 2>&1 || \
                            launchctl unload "$plist" >/dev/null 2>&1 || true
                        launchctl disable "gui/$user_uid/com.primeradiant.clipfan" >/dev/null 2>&1 || true
                        if launchctl print "gui/$user_uid/com.primeradiant.clipfan" >/dev/null 2>&1; then
                            service_still_active=1
                        fi
                    fi
                    if command -v systemctl >/dev/null 2>&1; then
                        systemctl --user stop clipfan.service >/dev/null 2>&1 || true
                        systemctl --user disable clipfan.service >/dev/null 2>&1 || true
                        if systemctl --user is-active --quiet clipfan.service; then
                            service_still_active=1
                        fi
                    fi
                    if [ "$service_still_active" -ne 0 ]; then
                        printf '%s\\n' 'public_listener_service_still_active' >&2
                        exit 1
                    fi
                fi
                exit "$preflight_status"
            fi
            """
        } else {
            storageAbortPrelude = ""
        }

        return """
        set -e
        stage=\(quotedStage)
        trap 'rm -rf "$stage"' EXIT
        \(storageAbortPrelude)
        cd "$stage" && bash install.sh --no-tmux >&2
        bin="${DEST:-$HOME/.local/bin}/clipfan"
        "$bin" version
        """
    }

    static func remoteCleanupCommand(stage: String) -> String {
        let quotedStage = shellSingleQuote(stage)
        return "stage=\(quotedStage); rm -rf \"$stage\""
    }

    static func validatedRemoteStagePath(_ output: String) throws -> String {
        let path: String
        if output.hasSuffix("\n") {
            path = String(output.dropLast())
        } else {
            path = output
        }

        let prefix = "/tmp/clipfan-install."
        guard path.hasPrefix(prefix) else {
            throw InstallError.configIO("unexpected remote stage path: \(output)")
        }

        let suffix = String(path.dropFirst(prefix.count))
        let allowedCharacters = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-")
        guard !suffix.isEmpty,
              suffix.rangeOfCharacter(from: allowedCharacters.inverted) == nil,
              !suffix.contains("..") else {
            throw InstallError.configIO("unexpected remote stage path: \(output)")
        }

        return path
    }

    static func shellSingleQuote(_ s: String) -> String {
        "'" + s.replacingOccurrences(of: "'", with: "'\"'\"'") + "'"
    }

    static func install(user: String, host: String, port: Int, sshKey: String,
                        withTmux: Bool,
                        onProgress: @MainActor @escaping (InstallProgress) -> Void) async throws {
        let invocation = sshInvocation(user: user, host: host, port: port, sshKey: sshKey)

        await MainActor.run { onProgress(.init(step: "Probe", detail: "running uname on \(invocation.target)")) }
        let probe = try await run("/usr/bin/ssh", invocation.sshArgs + [invocation.target, "uname -s; uname -m"])
        let platform = try remotePlatform(from: probe)

        // Resolve our shared key from the local daemon's config.
        await MainActor.run { onProgress(.init(step: "Config", detail: "reading shared key")) }
        let localCfg = try await readLocalConfig()
        let selfShort = shortName(Host.current().localizedName ?? Host.current().name ?? "")
        let remoteConfigJSON = """
        {
          "listen": ":7853",
          "shared_key": \(jsonString(localCfg["shared_key"] as? String ?? "")),
          "discovery": "static",
          "static_peers": [\(jsonString(selfShort))],
          "port": 7853
        }
        """

        // Stage payload in a tmpdir for scp.
        let stage = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-install-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: stage) }

        var stagedFiles = try stageInstallPayload(goos: platform.goos, goarch: platform.goarch, in: stage)
        try remoteConfigJSON.write(to: stage.appendingPathComponent("config.json"),
                                   atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o600],
                                              ofItemAtPath: stage.appendingPathComponent("config.json").path)
        stagedFiles.append("config.json")

        let fileCount = stagedFiles.count
        await MainActor.run { onProgress(.init(step: "Upload", detail: "scp \(fileCount) files")) }
        try await uploadAndInstallRemoteStage(target: invocation.target,
                                              sshArgs: invocation.sshArgs,
                                              scpArgs: invocation.scpArgs,
                                              stage: stage,
                                              stagedFiles: stagedFiles,
                                              withTmux: withTmux,
                                              runCommand: run,
                                              onInstall: {
                                                  onProgress(.init(step: "Install",
                                                                   detail: "running install.sh on \(invocation.target)"))
                                              })

        // Add this host to our own static_peers, save, kick the daemon.
        await MainActor.run { onProgress(.init(step: "Local", detail: "adding peer to local config")) }
        try await addPeerToLocalConfig(host)
        await MainActor.run { onProgress(.init(step: "Restart", detail: "kickstarting local daemon")) }
        await DaemonClient.shared.restartDaemon()
    }

    static func update(user: String, host: String, port: Int, sshKey: String,
                       onProgress: @MainActor @escaping (InstallProgress) -> Void) async throws -> String {
        let invocation = sshInvocation(user: user, host: host, port: port, sshKey: sshKey)

        await MainActor.run { onProgress(.init(step: "Probe", detail: "running uname on \(invocation.target)")) }
        let probe = try await run("/usr/bin/ssh", invocation.sshArgs + [invocation.target, "uname -s; uname -m"])
        let platform = try remotePlatform(from: probe)

        let stage = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-update-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: stage) }

        let stagedFiles = try stageInstallPayload(goos: platform.goos, goarch: platform.goarch, in: stage)
        await MainActor.run { onProgress(.init(step: "Upload", detail: "scp \(stagedFiles.count) files")) }
        let version = try await uploadAndUpdateRemoteStage(target: invocation.target,
                                                           sshArgs: invocation.sshArgs,
                                                           scpArgs: invocation.scpArgs,
                                                           stage: stage,
                                                           stagedFiles: stagedFiles,
                                                           runCommand: run,
                                                           onInstall: {
                                                               onProgress(.init(step: "Install",
                                                                                detail: "updating clipfan on \(invocation.target)"))
                                                           })
        return version
    }

    private static func sshInvocation(user: String, host: String, port: Int, sshKey: String)
        -> (target: String, sshArgs: [String], scpArgs: [String]) {
        let target = user.isEmpty ? host : "\(user)@\(host)"
        var sshArgs: [String] = ["-o", "ConnectTimeout=5"]
        var scpArgs: [String] = ["-q"]
        if !sshKey.isEmpty {
            sshArgs += ["-i", sshKey]
            scpArgs += ["-i", sshKey]
        }
        if port != 22 {
            sshArgs += ["-p", "\(port)"]
            scpArgs += ["-P", "\(port)"]
        }
        return (target, sshArgs, scpArgs)
    }

    private static func remotePlatform(from probe: String) throws -> (goos: String, goarch: String) {
        let lines = probe.trimmingCharacters(in: .whitespacesAndNewlines).split(separator: "\n")
        guard lines.count >= 2 else { throw InstallError.unsupportedHost(probe) }
        let goos: String = {
            switch String(lines[0]).lowercased() {
            case "linux": return "linux"
            case "darwin": return "darwin"
            default: return ""
            }
        }()
        let goarch: String = {
            switch String(lines[1]).trimmingCharacters(in: .whitespaces) {
            case "x86_64", "amd64": return "amd64"
            case "arm64", "aarch64": return "arm64"
            default: return ""
            }
        }()
        guard !goos.isEmpty, !goarch.isEmpty else {
            throw InstallError.unsupportedHost("\(lines[0]) \(lines[1])")
        }
        return (goos, goarch)
    }

    private static func stageInstallPayload(goos: String, goarch: String, in stage: URL) throws -> [String] {
        let binName = "clipfan-\(goos)-\(goarch)"
        let binPath = shareDir.appendingPathComponent(binName)
        guard FileManager.default.fileExists(atPath: binPath.path) else {
            throw InstallError.missingPayload(binPath.path)
        }

        var stagedFiles: [String] = []
        try FileManager.default.copyItem(at: binPath, to: stage.appendingPathComponent(binName))
        stagedFiles.append(binName)

        if goos == "linux" {
            let shimName = "clipfan-shim-\(goos)-\(goarch)"
            let shim = shareDir.appendingPathComponent(shimName)
            if FileManager.default.fileExists(atPath: shim.path) {
                try FileManager.default.copyItem(at: shim, to: stage.appendingPathComponent(shimName))
                stagedFiles.append(shimName)
            }
        }
        if goos == "darwin" {
            let helperName = "clipfan-pasteboard-helper-\(goos)-\(goarch)"
            let helper = shareDir.appendingPathComponent(helperName)
            if FileManager.default.fileExists(atPath: helper.path) {
                try FileManager.default.copyItem(at: helper, to: stage.appendingPathComponent(helperName))
                stagedFiles.append(helperName)
            }
        }
        for ancillary in ["install.sh", "clipfan.service", "com.primeradiant.clipfan.plist", "tmux.conf.snippet"] {
            let src = shareDir.appendingPathComponent(ancillary)
            if FileManager.default.fileExists(atPath: src.path) {
                try FileManager.default.copyItem(at: src, to: stage.appendingPathComponent(ancillary))
                stagedFiles.append(ancillary)
            }
        }
        return stagedFiles
    }

    static func uploadAndInstallRemoteStage(target: String, sshArgs: [String], scpArgs: [String],
                                            stage: URL, stagedFiles: [String], withTmux: Bool,
                                            runCommand: CommandRunner,
                                            onInstall: @MainActor @escaping () -> Void = {}) async throws {
        let remoteStageOutput = try await runCommand("/usr/bin/ssh", sshArgs + [target, remoteStageCommand()])
        let remoteStage = try validatedRemoteStagePath(remoteStageOutput)
        let scpFull: [String] = scpArgs +
            stagedFiles.map { stage.appendingPathComponent($0).path } +
            ["\(target):\(remoteStage)/"]
        do {
            _ = try await runCommand("/usr/bin/scp", scpFull)

            await onInstall()
            let cmd = remoteInstallCommand(stage: remoteStage, withTmux: withTmux)
            _ = try await runCommand("/usr/bin/ssh", sshArgs + [target, cmd])
        } catch {
            _ = try? await runCommand("/usr/bin/ssh", sshArgs + [target, remoteCleanupCommand(stage: remoteStage)])
            throw error
        }
    }

    static func uploadAndUpdateRemoteStage(target: String, sshArgs: [String], scpArgs: [String],
                                           stage: URL, stagedFiles: [String],
                                           enforceStorageAbort: Bool = GeneratedSSHTransportGates.peerHTTPRuntimeDisabled ||
                                               GeneratedSSHTransportGates.configV2WriteEnabled,
                                           runCommand: CommandRunner,
                                           onInstall: @MainActor @escaping () -> Void = {}) async throws -> String {
        let remoteStageOutput = try await runCommand("/usr/bin/ssh", sshArgs + [target, remoteStageCommand()])
        let remoteStage = try validatedRemoteStagePath(remoteStageOutput)
        let scpFull: [String] = scpArgs +
            stagedFiles.map { stage.appendingPathComponent($0).path } +
            ["\(target):\(remoteStage)/"]
        do {
            _ = try await runCommand("/usr/bin/scp", scpFull)

            await onInstall()
            let cmd = remoteUpdateCommand(stage: remoteStage,
                                          payloadBinaryName: updatePayloadBinaryName(from: stagedFiles),
                                          enforceStorageAbort: enforceStorageAbort)
            let output = try await runCommand("/usr/bin/ssh", sshArgs + [target, cmd])
            let remoteVersion = output.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !remoteVersion.isEmpty else {
                throw InstallError.configIO("remote version check returned empty output")
            }
            return remoteVersion
        } catch {
            _ = try? await runCommand("/usr/bin/ssh", sshArgs + [target, remoteCleanupCommand(stage: remoteStage)])
            throw error
        }
    }

    static func updatePayloadBinaryName(from stagedFiles: [String]) -> String? {
        stagedFiles.first { name in
            let parts = name.split(separator: "-").map(String.init)
            guard parts.count == 3, parts[0] == "clipfan" else { return false }
            return (parts[1] == "darwin" || parts[1] == "linux") &&
                (parts[2] == "amd64" || parts[2] == "arm64")
        }
    }

    // MARK: - helpers

    static func readLocalConfig(configURL: URL? = nil) async throws -> [String: Any] {
        let p = configURL ?? localConfigURL()
        let data = try Data(contentsOf: p)
        return (try JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:]
    }

    static func addPeerToLocalConfig(_ host: String, configURL: URL? = nil) async throws {
        let p = configURL ?? localConfigURL()
        var cfg = (try? await readLocalConfig(configURL: p)) ?? [:]
        if cfg.keys.contains("config_version"),
           !GeneratedSSHTransportGates.configV2WriteEnabled {
            throw InstallError.configIO("config_v2_writes_disabled")
        }
        var peers = (cfg["static_peers"] as? [String]) ?? []
        if !peers.contains(host) { peers.append(host) }
        cfg["static_peers"] = peers
        let data = try JSONSerialization.data(withJSONObject: cfg, options: [.prettyPrinted])
        let dir = p.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try FileManager.default.setAttributes([.posixPermissions: 0o700],
                                              ofItemAtPath: dir.path)
        try data.write(to: p, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600],
                                              ofItemAtPath: p.path)
    }

    static func run(_ exe: String, _ args: [String]) async throws -> String {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: exe)
        proc.arguments = args
        let out = Pipe(), err = Pipe()
        proc.standardOutput = out
        proc.standardError = err
        try proc.run()
        proc.waitUntilExit()
        let stdout = String(data: out.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        let stderr = String(data: err.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        if proc.terminationStatus != 0 {
            throw InstallCommandFailure(executable: exe,
                                        arguments: args,
                                        exitStatus: proc.terminationStatus,
                                        stdout: stdout,
                                        stderr: stderr)
        }
        return stdout
    }

    static func shortName(_ h: String) -> String {
        var s = h
        if s.hasSuffix(".local") { s = String(s.dropLast(".local".count)) }
        return s.split(separator: ".").first.map(String.init) ?? s
    }

    static func jsonString(_ s: String) -> String {
        let escaped = s
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
        return "\"\(escaped)\""
    }
}
