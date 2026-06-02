import XCTest
@testable import Clipfan

final class InstallerFlagTests: XCTestCase {
    func testTmuxFlagOn() {
        XCTAssertEqual(Installer.tmuxFlag(true), "--with-tmux")
    }
    func testTmuxFlagOff() {
        XCTAssertEqual(Installer.tmuxFlag(false), "--no-tmux")
    }

    func testRemoteStageCommandUsesPrivateMktempDirectory() {
        let command = Installer.remoteStageCommand()

        XCTAssertTrue(command.contains("mktemp -d /tmp/clipfan-install.XXXXXX"))
        XCTAssertTrue(command.contains("chmod 700"))
        XCTAssertTrue(command.contains("printf '%s\\n'"))
        XCTAssertFalse(command.contains("mkdir -p /tmp/clipfan-install"))
    }

    func testRemoteInstallCommandUsesStagePathAndCleansUp() {
        let command = Installer.remoteInstallCommand(stage: "/tmp/clipfan-install.ABC123", withTmux: true)

        XCTAssertTrue(command.contains("stage='/tmp/clipfan-install.ABC123'"))
        XCTAssertTrue(command.contains("trap 'rm -rf \"$stage\"' EXIT"))
        XCTAssertTrue(command.contains("install -m 0600 \"$stage/config.json\" ~/.config/clipfan/config.json"))
        XCTAssertTrue(command.contains("cd \"$stage\" && bash install.sh --with-tmux"))
    }

    func testRemoteInstallCommandShellQuotesStagePath() {
        let command = Installer.remoteInstallCommand(stage: "/tmp/clipfan-install.a'b", withTmux: false)

        XCTAssertTrue(command.contains("stage='/tmp/clipfan-install.a'\"'\"'b'"))
        XCTAssertTrue(command.contains("cd \"$stage\" && bash install.sh --no-tmux"))
    }

    func testRemoteInstallConfigUsesLegacyPublicListenDefault() {
        XCTAssertFalse(GeneratedSSHTransportGates.peerHTTPRuntimeDisabled)
        XCTAssertFalse(GeneratedSSHTransportGates.configV2WriteEnabled)

        let body = Installer.remoteInstallConfigJSON(sharedKey: "secret", selfShort: "m4")

        XCTAssertTrue(body.contains(#""listen": ":7853""#))
        XCTAssertTrue(body.contains(#""shared_key": "secret""#))
        XCTAssertTrue(body.contains(#""static_peers": ["m4"]"#))
        XCTAssertFalse(body.contains("config_version"))
        XCTAssertFalse(body.contains("config_revision"))
    }

    func testRemoteInstallConfigCanUseLoopbackGeneratedListen() {
        let body = Installer.remoteInstallConfigJSON(sharedKey: "secret",
                                                     selfShort: "m4",
                                                     loopbackDefault: true)

        XCTAssertTrue(body.contains(#""listen": "127.0.0.1:7853""#))
        XCTAssertFalse(body.contains("config_version"))
        XCTAssertFalse(body.contains("config_revision"))
    }

    func testRemoteUpdateCommandPreservesConfigAndSkipsTmux() {
        let command = Installer.remoteUpdateCommand(stage: "/tmp/clipfan-install.ABC123")

        XCTAssertTrue(command.contains("stage='/tmp/clipfan-install.ABC123'"))
        XCTAssertTrue(command.contains("trap 'rm -rf \"$stage\"' EXIT"))
        XCTAssertTrue(command.contains("cd \"$stage\" && bash install.sh --no-tmux >&2"))
        XCTAssertTrue(command.contains("\"$bin\" version"))
        XCTAssertFalse(command.contains("storage-preflight"))
        XCTAssertFalse(command.contains("config.json"))
        XCTAssertFalse(command.contains("~/.config/clipfan"))
        XCTAssertFalse(command.contains("config_version"))
        XCTAssertFalse(command.contains("--with-tmux"))
    }

    func testRemoteUpdateCommandStorageAbortPreflightsBeforeInstall() {
        let command = Installer.remoteUpdateCommand(stage: "/tmp/clipfan-install.ABC123",
                                                    payloadBinaryName: "clipfan-linux-arm64",
                                                    enforceStorageAbort: true)

        let preflight = try! XCTUnwrap(command.range(of: "\"$preflight_bin\" storage-preflight"))
        let install = try! XCTUnwrap(command.range(of: "cd \"$stage\" && bash install.sh --no-tmux >&2"))

        XCTAssertLessThan(preflight.lowerBound, install.lowerBound)
        XCTAssertTrue(command.contains("preflight_bin=\"$stage/clipfan-linux-arm64\""))
        XCTAssertTrue(command.contains("unsupported_runtime_storage"))
        XCTAssertTrue(command.contains("storage_check_inconclusive"))
        XCTAssertTrue(command.contains("systemctl --user stop clipfan.service"))
        XCTAssertTrue(command.contains("systemctl --user disable clipfan.service"))
        XCTAssertTrue(command.contains("user_uid=\"$(id -u 2>/dev/null || printf '%s' \"${UID:-}\")\""))
        XCTAssertTrue(command.contains("launchctl disable \"gui/$user_uid/com.primeradiant.clipfan\""))
        XCTAssertTrue(command.contains("public_listener_service_still_active"))
        XCTAssertTrue(command.contains("exit \"$preflight_status\""))
        XCTAssertFalse(command.contains("config.json"))
        XCTAssertFalse(command.contains("~/.config/clipfan"))
        XCTAssertFalse(command.contains("config_version"))
    }

    func testRemoteUpdateCommandStorageAbortFailsClosedWithoutPayloadBinary() {
        let command = Installer.remoteUpdateCommand(stage: "/tmp/clipfan-install.ABC123",
                                                    payloadBinaryName: nil,
                                                    enforceStorageAbort: true)

        XCTAssertTrue(command.contains("storage_check_inconclusive"))
        XCTAssertTrue(command.contains("missing staged storage preflight binary"))
        XCTAssertFalse(command.contains("bash install.sh"))
        XCTAssertFalse(command.contains("\"$bin\" version"))
    }

    func testRemoteUpdateCommandStorageAbortRunsStopDisableAndSkipsInstall() throws {
        let fixture = try makeRemoteUpdateShellFixture(systemctlIsActive: false)
        defer { try? FileManager.default.removeItem(at: fixture.root) }

        let command = Installer.remoteUpdateCommand(stage: fixture.stage.path,
                                                    payloadBinaryName: "clipfan-linux-arm64",
                                                    enforceStorageAbort: true)
        let result = try runBash(command, environment: fixture.environment)

        XCTAssertEqual(result.status, 37)
        XCTAssertTrue(result.stderr.contains("unsupported_runtime_storage"))
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.installMarker.path))

        let systemctlLog = try String(contentsOf: fixture.systemctlLog, encoding: .utf8)
        XCTAssertTrue(systemctlLog.contains("--user stop clipfan.service"))
        XCTAssertTrue(systemctlLog.contains("--user disable clipfan.service"))
        XCTAssertTrue(systemctlLog.contains("--user is-active --quiet clipfan.service"))
    }

    func testRemoteUpdateCommandStorageAbortFailsIfServiceStillActive() throws {
        let fixture = try makeRemoteUpdateShellFixture(systemctlIsActive: true)
        defer { try? FileManager.default.removeItem(at: fixture.root) }

        let command = Installer.remoteUpdateCommand(stage: fixture.stage.path,
                                                    payloadBinaryName: "clipfan-linux-arm64",
                                                    enforceStorageAbort: true)
        let result = try runBash(command, environment: fixture.environment)

        XCTAssertEqual(result.status, 1)
        XCTAssertTrue(result.stderr.contains("unsupported_runtime_storage"))
        XCTAssertTrue(result.stderr.contains("public_listener_service_still_active"))
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.installMarker.path))
    }

    func testValidatedRemoteStagePathAcceptsPrivateMktempOutput() throws {
        let path = try Installer.validatedRemoteStagePath("/tmp/clipfan-install.ABC123\n")

        XCTAssertEqual(path, "/tmp/clipfan-install.ABC123")
    }

    func testValidatedRemoteStagePathRejectsUnsafeOutput() {
        let invalidOutputs = [
            "/tmp/clipfan-install.ABC123\nextra",
            "debug\n/tmp/clipfan-install.ABC123\n",
            "/tmp/clipfan-install.ABC 123\n",
            "/tmp/clipfan-install.ABC;rm -rf ~\n",
            "/tmp/clipfan-install.ABC'123\n",
            "/tmp/clipfan-install.ABC/123\n",
            "/tmp/clipfan-install../ABC123\n",
            "/tmp/clipfan-install.ABC..\n",
            "/tmp/other-install.ABC123\n",
            "/tmp/clipfan-install.\n"
        ]

        for output in invalidOutputs {
            XCTAssertThrowsError(try Installer.validatedRemoteStagePath(output), output)
        }
    }

    func testRemoteCleanupCommandUsesValidatedStagePath() {
        let command = Installer.remoteCleanupCommand(stage: "/tmp/clipfan-install.ABC123")

        XCTAssertTrue(command.contains("stage='/tmp/clipfan-install.ABC123'"))
        XCTAssertTrue(command.contains("rm -rf \"$stage\""))
    }

    func testUploadFailureCleansRemoteStageBeforeRethrowing() async throws {
        enum TestError: Error {
            case scpFailed
        }

        let stage = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-installer-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: stage) }

        var commands: [(String, [String])] = []
        do {
            try await Installer.uploadAndInstallRemoteStage(
                target: "remote.example",
                sshArgs: ["-o", "ConnectTimeout=5"],
                scpArgs: ["-q"],
                stage: stage,
                stagedFiles: ["config.json"],
                withTmux: false,
                runCommand: { exe, args in
                    commands.append((exe, args))
                    if exe == "/usr/bin/ssh", args.last == Installer.remoteStageCommand() {
                        return "/tmp/clipfan-install.ABC123\n"
                    }
                    if exe == "/usr/bin/scp" {
                        throw TestError.scpFailed
                    }
                    return ""
                })
            XCTFail("expected upload failure")
        } catch TestError.scpFailed {
        }

        XCTAssertEqual(commands.count, 3)
        XCTAssertEqual(commands[1].0, "/usr/bin/scp")
        XCTAssertEqual(commands[1].1.last, "remote.example:/tmp/clipfan-install.ABC123/")
        XCTAssertEqual(commands[2].0, "/usr/bin/ssh")
        XCTAssertEqual(commands[2].1.last, Installer.remoteCleanupCommand(stage: "/tmp/clipfan-install.ABC123"))
    }

    func testUploadAndUpdateRemoteStageRunsUpdateCommandAndReturnsVersion() async throws {
        let stage = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-update-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: stage) }

        var commands: [(String, [String])] = []
        let version = try await Installer.uploadAndUpdateRemoteStage(
            target: "remote.example",
            sshArgs: ["-o", "ConnectTimeout=5"],
            scpArgs: ["-q"],
            stage: stage,
            stagedFiles: ["clipfan-linux-arm64", "install.sh", "clipfan.service"],
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh", args.last == Installer.remoteStageCommand() {
                    return "/tmp/clipfan-install.ABC123\n"
                }
                if exe == "/usr/bin/ssh", args.last == Installer.remoteUpdateCommand(stage: "/tmp/clipfan-install.ABC123") {
                    return "v0.3.2\n"
                }
                return ""
            }
        )

        XCTAssertEqual(version, "v0.3.2")
        XCTAssertEqual(commands.count, 3)
        XCTAssertEqual(commands[1].0, "/usr/bin/scp")
        XCTAssertEqual(commands[1].1.last, "remote.example:/tmp/clipfan-install.ABC123/")
        XCTAssertEqual(commands[2].0, "/usr/bin/ssh")
        XCTAssertEqual(commands[2].1.last, Installer.remoteUpdateCommand(stage: "/tmp/clipfan-install.ABC123"))
    }

    func testUploadAndUpdateRemoteStagePropagatesStorageAbortAndCleansUp() async throws {
        let stage = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-update-abort-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: stage) }

        var commands: [(String, [String])] = []
        do {
            _ = try await Installer.uploadAndUpdateRemoteStage(
                target: "remote.example",
                sshArgs: ["-o", "ConnectTimeout=5"],
                scpArgs: ["-q"],
                stage: stage,
                stagedFiles: ["clipfan-linux-arm64", "install.sh", "clipfan.service"],
                enforceStorageAbort: true,
                runCommand: { exe, args in
                    commands.append((exe, args))
                    if exe == "/usr/bin/ssh", args.last == Installer.remoteStageCommand() {
                        return "/tmp/clipfan-install.ABC123\n"
                    }
                    if exe == "/usr/bin/ssh",
                       args.last == Installer.remoteUpdateCommand(stage: "/tmp/clipfan-install.ABC123",
                                                                   payloadBinaryName: "clipfan-linux-arm64",
                                                                   enforceStorageAbort: true) {
                        throw InstallCommandFailure(executable: exe,
                                                    arguments: args,
                                                    exitStatus: 1,
                                                    stdout: "",
                                                    stderr: "code: unsupported_runtime_storage\n")
                    }
                    return ""
                })
            XCTFail("expected storage abort")
        } catch let failure as InstallCommandFailure {
            XCTAssertEqual(failure.exitStatus, 1)
            XCTAssertTrue(failure.stderr.contains("unsupported_runtime_storage"))
        }

        XCTAssertEqual(commands.count, 4)
        XCTAssertEqual(commands[1].0, "/usr/bin/scp")
        XCTAssertEqual(commands[2].0, "/usr/bin/ssh")
        XCTAssertEqual(commands[2].1.last, Installer.remoteUpdateCommand(stage: "/tmp/clipfan-install.ABC123",
                                                                         payloadBinaryName: "clipfan-linux-arm64",
                                                                         enforceStorageAbort: true))
        XCTAssertEqual(commands[3].1.last, Installer.remoteCleanupCommand(stage: "/tmp/clipfan-install.ABC123"))
    }

    func testLocalConfigURLHonorsXDGConfigHome() {
        let home = URL(fileURLWithPath: "/tmp/home")
        let xdg = URL(fileURLWithPath: "/tmp/xdg-config")

        let got = Installer.localConfigURL(
            environment: ["XDG_CONFIG_HOME": xdg.path],
            homeDirectory: home
        )

        XCTAssertEqual(got.path, xdg.appendingPathComponent("clipfan/config.json").path)
    }

    func testLocalConfigURLFallsBackToHomeConfig() {
        let home = URL(fileURLWithPath: "/tmp/home")

        let got = Installer.localConfigURL(environment: [:], homeDirectory: home)

        XCTAssertEqual(got.path, home.appendingPathComponent(".config/clipfan/config.json").path)
    }

    func testAddPeerToLocalConfigWritesPrivateFileAndDirectoryModes() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-config-\(UUID().uuidString)")
        let configURL = root.appendingPathComponent("clipfan/config.json")
        defer { try? FileManager.default.removeItem(at: root) }

        try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        try #"{"shared_key":"secret","static_peers":["existing"]}"#.data(using: .utf8)!
            .write(to: configURL)
        try FileManager.default.setAttributes([.posixPermissions: 0o755],
                                              ofItemAtPath: configURL.deletingLastPathComponent().path)
        try FileManager.default.setAttributes([.posixPermissions: 0o644],
                                              ofItemAtPath: configURL.path)

        try await Installer.addPeerToLocalConfig("new-peer", configURL: configURL)

        let data = try Data(contentsOf: configURL)
        let cfg = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(cfg["static_peers"] as? [String], ["existing", "new-peer"])

        XCTAssertEqual(try posixMode(configURL.deletingLastPathComponent()), 0o700)
        XCTAssertEqual(try posixMode(configURL), 0o600)
    }

    func testAddPeerToLocalConfigRejectsConfigV2WhenGateDisabled() async throws {
        XCTAssertFalse(GeneratedSSHTransportGates.configV2WriteEnabled)

        let bodies = [
            #"{"config_version":2,"config_revision":1,"shared_key":"secret","static_peers":["existing"],"future":{"keep":true}}"#,
            #"{"config_version":3,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":"2","shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":2.1,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":null,"shared_key":"secret","static_peers":["existing"]}"#
        ]

        for body in bodies {
            let root = FileManager.default.temporaryDirectory
                .appendingPathComponent("clipfan-config-v2-\(UUID().uuidString)")
            let configURL = root.appendingPathComponent("clipfan/config.json")
            defer { try? FileManager.default.removeItem(at: root) }

            try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                    withIntermediateDirectories: true)
            let before = body.data(using: .utf8)!
            try before.write(to: configURL)
            try FileManager.default.setAttributes([.posixPermissions: 0o600],
                                                  ofItemAtPath: configURL.path)

            do {
                try await Installer.addPeerToLocalConfig("new-peer", configURL: configURL)
                XCTFail("expected config_v2_writes_disabled for \(body)")
            } catch {
                XCTAssertTrue(String(describing: error).contains("config_v2_writes_disabled"),
                              "error \(error) should include stable code")
            }

            let after = try Data(contentsOf: configURL)
            XCTAssertEqual(after, before)
        }
    }

    private func posixMode(_ url: URL) throws -> Int {
        let attrs = try FileManager.default.attributesOfItem(atPath: url.path)
        return (attrs[.posixPermissions] as? NSNumber)?.intValue ?? 0
    }

    private struct ShellResult {
        let status: Int32
        let stdout: String
        let stderr: String
    }

    private struct RemoteUpdateShellFixture {
        let root: URL
        let stage: URL
        let systemctlLog: URL
        let installMarker: URL
        let environment: [String: String]
    }

    private func makeRemoteUpdateShellFixture(systemctlIsActive: Bool) throws -> RemoteUpdateShellFixture {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-update-shell-\(UUID().uuidString)")
        let stage = root.appendingPathComponent("stage")
        let fakebin = root.appendingPathComponent("fakebin")
        let home = root.appendingPathComponent("home")
        let systemctlLog = root.appendingPathComponent("systemctl.log")
        let launchctlLog = root.appendingPathComponent("launchctl.log")
        let installMarker = root.appendingPathComponent("install-ran")
        try FileManager.default.createDirectory(at: stage, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: fakebin, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)

        try writeExecutableScript(stage.appendingPathComponent("clipfan-linux-arm64"), """
        #!/usr/bin/env bash
        if [[ "$1" == "storage-preflight" ]]; then
          echo "code: unsupported_runtime_storage" >&2
          exit 37
        fi
        echo "unexpected staged binary command: $*" >&2
        exit 99
        """)
        try writeExecutableScript(stage.appendingPathComponent("install.sh"), """
        #!/usr/bin/env bash
        touch "$INSTALL_MARKER"
        exit 0
        """)
        try writeExecutableScript(fakebin.appendingPathComponent("launchctl"), """
        #!/usr/bin/env bash
        printf '%s\\n' "$*" >> "$LAUNCHCTL_LOG"
        exit 1
        """)
        try writeExecutableScript(fakebin.appendingPathComponent("systemctl"), """
        #!/usr/bin/env bash
        printf '%s\\n' "$*" >> "$SYSTEMCTL_LOG"
        if [[ "$1" == "--user" && "$2" == "is-active" && "$3" == "--quiet" ]]; then
          \(systemctlIsActive ? "exit 0" : "exit 1")
        fi
        exit 0
        """)

        return RemoteUpdateShellFixture(
            root: root,
            stage: stage,
            systemctlLog: systemctlLog,
            installMarker: installMarker,
            environment: [
                "HOME": home.path,
                "PATH": "\(fakebin.path):/usr/bin:/bin:/usr/sbin:/sbin",
                "SYSTEMCTL_LOG": systemctlLog.path,
                "LAUNCHCTL_LOG": launchctlLog.path,
                "INSTALL_MARKER": installMarker.path
            ]
        )
    }

    private func writeExecutableScript(_ url: URL, _ body: String) throws {
        try body.write(to: url, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755],
                                              ofItemAtPath: url.path)
    }

    private func runBash(_ command: String, environment: [String: String]) throws -> ShellResult {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/bin/bash")
        proc.arguments = ["-lc", command]
        proc.environment = environment
        let out = Pipe()
        let err = Pipe()
        proc.standardOutput = out
        proc.standardError = err
        try proc.run()
        proc.waitUntilExit()
        return ShellResult(
            status: proc.terminationStatus,
            stdout: String(decoding: out.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self),
            stderr: String(decoding: err.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
        )
    }
}
