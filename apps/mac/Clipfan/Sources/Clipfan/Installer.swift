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

struct ScopedSSHPeerConfigWriter {
    typealias ReadPeer = (String) async throws -> LocalDaemonSSHPeerConfigResponse
    typealias UpsertPeer = (String, LocalDaemonSSHPeerUpsertRequest) async throws -> LocalDaemonSSHPeerConfigResponse

    let readPeer: ReadPeer?
    let upsertPeer: UpsertPeer

    init(readPeer: ReadPeer? = nil, upsertPeer: @escaping UpsertPeer) {
        self.readPeer = readPeer
        self.upsertPeer = upsertPeer
    }

    func read(peerID: String) async throws -> LocalDaemonSSHPeerConfigResponse {
        guard let readPeer else {
            throw InstallError.configIO("config_v2_scoped_reader_unavailable")
        }
        return try await readPeer(peerID)
    }

    func upsert(peerID: String, request: LocalDaemonSSHPeerUpsertRequest) async throws -> LocalDaemonSSHPeerConfigResponse {
        try await upsertPeer(peerID, request)
    }

    static let daemon = ScopedSSHPeerConfigWriter(
        readPeer: { peerID in
            try await DaemonClient.shared.readSSHPeerConfig(peerID: peerID)
        },
        upsertPeer: { peerID, request in
            try await DaemonClient.shared.upsertSSHPeerConfig(peerID: peerID, request: request)
        }
    )
}

/// Drives the same scp + install.sh playbook used by `cc-clip`-style remote
/// installs. Source binaries are read out of $HOME/.local/share/clipfan
/// (staged by `dist/install.sh` on the host running the menubar app).
actor Installer {
    typealias CommandRunner = (String, [String]) async throws -> String
    typealias LocalHostIDReader = () async -> String?
    typealias LocalDaemonRestarter = () async throws -> Void

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

    static func localClipfanBinaryPath(homeDirectory: URL = FileManager.default.homeDirectoryForCurrentUser) -> String {
        homeDirectory.appendingPathComponent(".local/bin/clipfan").path
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

    static func generatedListenDefault(loopbackDefault: Bool = GeneratedSSHTransportGates.peerHTTPRuntimeDisabled &&
                                       GeneratedSSHTransportGates.configV2WriteEnabled) -> String {
        loopbackDefault ? "127.0.0.1:7853" : ":7853"
    }

    static func remoteInstallConfigJSON(sharedKey: String,
                                        selfShort: String,
                                        loopbackDefault: Bool = GeneratedSSHTransportGates.peerHTTPRuntimeDisabled &&
                                            GeneratedSSHTransportGates.configV2WriteEnabled) -> String {
        """
        {
          "listen": \(jsonString(generatedListenDefault(loopbackDefault: loopbackDefault))),
          "shared_key": \(jsonString(sharedKey)),
          "discovery": "static",
          "static_peers": [\(jsonString(selfShort))],
          "port": 7853
        }
        """
    }

    static func remoteUpdateCommand(stage: String) -> String {
        remoteUpdateCommand(stage: stage,
                            payloadBinaryName: nil,
                            enforceStorageAbort: GeneratedSSHTransportGates.peerHTTPRuntimeDisabled ||
                                GeneratedSSHTransportGates.configV2WriteEnabled,
                            enforceDowngradeBlock: GeneratedSSHTransportGates.configV2WriteEnabled)
    }

    static func remoteUpdateCommand(stage: String,
                                    payloadBinaryName: String?,
                                    enforceStorageAbort: Bool,
                                    enforceDowngradeBlock: Bool = GeneratedSSHTransportGates.configV2WriteEnabled) -> String {
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
        if enforceDowngradeBlock, payloadBinaryName == nil {
            return """
            set -e
            stage=\(quotedStage)
            trap 'rm -rf "$stage"' EXIT
            printf '%s\\n' 'pre_ssh_binary_unsupported: missing staged update binary' >&2
            exit 1
            """
        }

        let payloadExecutablePrelude: String
        if (enforceStorageAbort || enforceDowngradeBlock), let payloadBinaryName {
            let notExecutable = enforceDowngradeBlock
                ? "pre_ssh_binary_unsupported: staged update binary is not executable"
                : "storage_check_inconclusive: staged storage preflight binary is not executable"
            payloadExecutablePrelude = """
            payload_bin="$stage/\(payloadBinaryName)"
            if [ ! -x "$payload_bin" ]; then
                chmod 700 "$payload_bin" 2>/dev/null || true
            fi
            if [ ! -x "$payload_bin" ]; then
                printf '%s\\n' '\(notExecutable)' >&2
                exit 1
            fi
            """
        } else {
            payloadExecutablePrelude = ""
        }

        let storageAbortPrelude: String
        if enforceStorageAbort, payloadBinaryName != nil {
            storageAbortPrelude = """
            preflight_bin="$payload_bin"
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

        let downgradeCapabilityPrelude: String
        let serviceStopAfterInstall: String
        let installedCapabilityCheck: String
        let serviceRestartAfterCapabilityCheck: String
        if enforceDowngradeBlock {
            downgradeCapabilityPrelude = """
            staged_version_json="$("$payload_bin" version --json 2>&1)" || {
                printf '%s\\n' "$staged_version_json" >&2
                printf '%s\\n' 'pre_ssh_binary_unsupported: staged binary lacks config v2 capability' >&2
                exit 1
            }
            if ! printf '%s\\n' "$staged_version_json" | grep -Eq '"config_v2"[[:space:]]*:[[:space:]]*true'; then
                printf '%s\\n' 'pre_ssh_binary_unsupported: staged binary lacks config v2 capability' >&2
                exit 1
            fi
            """
            serviceStopAfterInstall = """
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
            """
            installedCapabilityCheck = """
            installed_version_json="$("$bin" version --json 2>&1)" || {
                printf '%s\\n' "$installed_version_json" >&2
                printf '%s\\n' 'pre_ssh_binary_unsupported: installed binary lacks config v2 capability' >&2
                exit 1
            }
            if ! printf '%s\\n' "$installed_version_json" | grep -Eq '"config_v2"[[:space:]]*:[[:space:]]*true'; then
                printf '%s\\n' 'pre_ssh_binary_unsupported: installed binary lacks config v2 capability' >&2
                exit 1
            fi
            """
            serviceRestartAfterCapabilityCheck = """
            user_uid="$(id -u 2>/dev/null || printf '%s' "${UID:-}")"
            if command -v launchctl >/dev/null 2>&1; then
                plist="$HOME/Library/LaunchAgents/com.primeradiant.clipfan.plist"
                launchctl enable "gui/$user_uid/com.primeradiant.clipfan" >/dev/null 2>&1 || true
                launchctl load "$plist" >/dev/null 2>&1 || \
                    launchctl kickstart -k "gui/$user_uid/com.primeradiant.clipfan" >/dev/null 2>&1
            fi
            if command -v systemctl >/dev/null 2>&1; then
                systemctl --user enable clipfan.service >/dev/null 2>&1
                systemctl --user restart clipfan.service >/dev/null 2>&1
            fi
            """
        } else {
            downgradeCapabilityPrelude = ""
            serviceStopAfterInstall = ""
            installedCapabilityCheck = ""
            serviceRestartAfterCapabilityCheck = ""
        }

        let installFlags = enforceDowngradeBlock ? "--no-tmux --no-restart" : "--no-tmux"

        return """
        set -e
        stage=\(quotedStage)
        trap 'rm -rf "$stage"' EXIT
        \(payloadExecutablePrelude)
        \(storageAbortPrelude)
        \(downgradeCapabilityPrelude)
        cd "$stage" && bash install.sh \(installFlags) >&2
        \(serviceStopAfterInstall)
        bin="${DEST:-$HOME/.local/bin}/clipfan"
        \(installedCapabilityCheck)
        \(serviceRestartAfterCapabilityCheck)
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
        let remoteConfigJSON = remoteInstallConfigJSON(sharedKey: localCfg["shared_key"] as? String ?? "",
                                                       selfShort: selfShort)

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
        try await addPeerToLocalConfig(host, scopedWriter: .daemon)
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

    static func provisionPrivateDirectMesh(hostSpecs: [String],
                                           regularKnownHosts: String,
                                           trustKeyscan: Bool,
                                           clipfanBinary: String = localClipfanBinaryPath(),
                                           runCommand: CommandRunner = run,
                                           readLocalHostID: @escaping LocalHostIDReader = {
                                               let config = try? await readLocalConfig()
                                               return config?["hostname"] as? String
                                           },
                                           restartLocalDaemon: @escaping LocalDaemonRestarter = {
                                               try await MainActor.run {
                                                   guard DaemonClient.shared.restartDaemon() else {
                                                       throw InstallError.configIO("local_daemon_restart_failed")
                                                   }
                                               }
                                           },
                                           onProgress: @MainActor @escaping (InstallProgress) -> Void) async throws {
        let specs = hostSpecs
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        guard specs.count >= 2 else {
            throw InstallError.configIO("private_direct_mesh_requires_at_least_two_hosts")
        }
        guard trustKeyscan else {
            throw InstallError.configIO("trust_keyscan_required")
        }
        let knownHosts = expandHome(regularKnownHosts.trimmingCharacters(in: .whitespacesAndNewlines))
        guard !knownHosts.isEmpty else {
            throw InstallError.configIO("regular_known_hosts_required")
        }
        let hosts = try specs.map { try privateDirectMeshHost(from: $0) }

        await MainActor.run { onProgress(.init(step: "Provision", detail: "running ssh-provision-direct")) }
        var args = ["ssh-provision-direct", "--trust-keyscan", "--regular-known-hosts", knownHosts]
        for spec in specs {
            args += ["--host", spec]
        }
        _ = try await runCommand(clipfanBinary, args)

        await MainActor.run { onProgress(.init(step: "Restart", detail: "restarting affected daemons")) }
        let localHostID = await readLocalHostID() ?? ""
        var firstRestartError: Error?
        for host in hosts {
            if !localHostID.isEmpty && host.id == localHostID {
                continue
            }
            let restartSSHArgs = regularSSHRemoteCommandArgs(user: host.user,
                                                             host: host.sshHost,
                                                             port: host.port,
                                                             knownHosts: knownHosts,
                                                             remoteCommand: remoteRestartDaemonCommand(installPath: host.installPath))
            do {
                _ = try await runCommand("/usr/bin/ssh", restartSSHArgs)
            } catch {
                if firstRestartError == nil {
                    firstRestartError = error
                }
            }
        }
        try await restartLocalDaemon()
        if let firstRestartError {
            throw firstRestartError
        }
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

    private static func expandHome(_ path: String,
                                   homeDirectory: URL = FileManager.default.homeDirectoryForCurrentUser) -> String {
        if path == "~" {
            return homeDirectory.path
        }
        if path.hasPrefix("~/") {
            return homeDirectory.appendingPathComponent(String(path.dropFirst(2))).path
        }
        return path
    }

    private struct PrivateDirectMeshHost {
        let id: String
        let sshHost: String
        let user: String
        let port: Int
        let installPath: String
    }

    private static func privateDirectMeshHost(from spec: String) throws -> PrivateDirectMeshHost {
        var fields: [String: String] = [:]
        for item in spec.split(separator: ",") {
            let parts = item.split(separator: "=", maxSplits: 1).map(String.init)
            guard parts.count == 2 else {
                throw InstallError.configIO("invalid_private_direct_mesh_host_spec")
            }
            let key = parts[0].trimmingCharacters(in: .whitespacesAndNewlines)
                .replacingOccurrences(of: "-", with: "_")
            let value = parts[1].trimmingCharacters(in: .whitespacesAndNewlines)
            guard !key.isEmpty, !value.isEmpty else {
                throw InstallError.configIO("invalid_private_direct_mesh_host_spec")
            }
            fields[key] = value
        }
        let port: Int
        if let rawPort = fields["port"], !rawPort.isEmpty {
            guard let parsed = Int(rawPort), parsed > 0, parsed <= 65535 else {
                throw InstallError.configIO("invalid_private_direct_mesh_port")
            }
            port = parsed
        } else {
            port = 22
        }
        guard let id = fields["id"], !id.isEmpty,
              let sshHost = fields["ssh"] ?? fields["host"], !sshHost.isEmpty,
              let user = fields["user"], !user.isEmpty,
              let installPath = fields["install"], !installPath.isEmpty else {
            throw InstallError.configIO("missing_private_direct_mesh_host_field")
        }
        return PrivateDirectMeshHost(id: id, sshHost: sshHost, user: user, port: port, installPath: installPath)
    }

    private static func regularSSHRemoteCommandArgs(user: String,
                                                    host: String,
                                                    port: Int,
                                                    knownHosts: String,
                                                    remoteCommand: String) -> [String] {
        [
            "-F", "/dev/null",
            "-o", "BatchMode=yes",
            "-o", "StrictHostKeyChecking=yes",
            "-o", "UserKnownHostsFile=\(knownHosts)",
            "-o", "GlobalKnownHostsFile=/dev/null",
            "-o", "ProxyCommand=none",
            "-o", "ProxyJump=none",
            "-o", "PermitLocalCommand=no",
            "-o", "RequestTTY=no",
            "-o", "ClearAllForwardings=yes",
            "-o", "LogLevel=ERROR",
            "-p", "\(port)",
            "\(user)@\(host)",
            remoteCommand
        ]
    }

    static func remoteRestartDaemonCommand(installPath: String) -> String {
        let quotedInstallPath = shellSingleQuote(installPath)
        return """
        if command -v systemctl >/dev/null 2>&1; then
            systemctl --user restart clipfan.service >/dev/null 2>&1 && exit 0
        fi
        if command -v launchctl >/dev/null 2>&1; then
            user_uid="$(id -u 2>/dev/null || printf '%s' "${UID:-}")"
            launchctl kickstart -k "gui/$user_uid/com.primeradiant.clipfan" >/dev/null 2>&1 && exit 0
        fi
        nohup \(quotedInstallPath) daemon >/tmp/clipfan-daemon.log 2>&1 &
        """
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
                                           enforceDowngradeBlock: Bool = GeneratedSSHTransportGates.configV2WriteEnabled,
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
                                          enforceStorageAbort: enforceStorageAbort,
                                          enforceDowngradeBlock: enforceDowngradeBlock)
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

    static func addPeerToLocalConfig(_ host: String,
                                     configURL: URL? = nil,
                                     scopedWriter: ScopedSSHPeerConfigWriter? = nil,
                                     configV2WriteEnabled: Bool = GeneratedSSHTransportGates.configV2WriteEnabled) async throws {
        let p = configURL ?? localConfigURL()
        var cfg = (try? await readLocalConfig(configURL: p)) ?? [:]
        if cfg.keys.contains("config_version") {
            guard configV2WriteEnabled else {
                throw InstallError.configIO("config_v2_writes_disabled")
            }
            try validateConfigV2Marker(cfg)
            guard let scopedWriter else {
                throw InstallError.configIO("config_v2_scoped_writer_unavailable")
            }
            let revision = try configRevision(from: cfg)
            let request = LocalDaemonSSHPeerUpsertRequest(
                expectedConfigRevision: revision,
                peer: LocalDaemonSSHPeerUpsertFields(
                    id: host,
                    enabled: true,
                    accept: true,
                    connect: false,
                    migrationState: "loopback_unprovisioned"
                )
            )
            try await upsertAddPeerConfigV2(host, request: request, scopedWriter: scopedWriter)
            return
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

    private static func validateConfigV2Marker(_ cfg: [String: Any]) throws {
        let version = try positiveInteger(from: cfg["config_version"],
                                          stableCode: "unsupported_config_version")
        guard version == 2 else {
            throw InstallError.configIO("unsupported_config_version")
        }
    }

    private static func configRevision(from cfg: [String: Any]) throws -> UInt64 {
        try positiveInteger(from: cfg["config_revision"], stableCode: "invalid_config_revision")
    }

    private static func positiveInteger(from rawValue: Any?, stableCode: String) throws -> UInt64 {
        guard let rawValue, !(rawValue is NSNull),
              let number = rawValue as? NSNumber,
              CFGetTypeID(number) != CFBooleanGetTypeID() else {
            throw InstallError.configIO(stableCode)
        }
        let decimal = number.decimalValue
        guard decimal > Decimal(0),
              decimal <= Decimal(UInt64.max),
              decimal == Decimal(number.uint64Value) else {
            throw InstallError.configIO(stableCode)
        }
        return number.uint64Value
    }

    private static func upsertAddPeerConfigV2(_ host: String,
                                              request: LocalDaemonSSHPeerUpsertRequest,
                                              scopedWriter: ScopedSSHPeerConfigWriter) async throws {
        do {
            _ = try await scopedWriter.upsert(peerID: host, request: request)
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode)
            where code == localDaemonSSHPeerMigrationStateChangeNotAllowedCode ||
            code == localDaemonConfigRevisionConflictCode {
            if let existing = try? await scopedWriter.read(peerID: host),
               existingAddPeerMatches(existing.peer, host: host) {
                return
            }
            throw LocalDaemonSSHPeerConfigError.api(code: code, statusCode: statusCode)
        }
    }

    private static func existingAddPeerMatches(_ peer: LocalDaemonSSHPeer, host: String) -> Bool {
        peer.id == host && peer.enabled == true && peer.accept == true
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
