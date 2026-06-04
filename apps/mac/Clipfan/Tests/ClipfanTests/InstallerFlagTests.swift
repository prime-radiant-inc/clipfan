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

    func testRemoteInstallConfigUsesCurrentGeneratedLoopbackDefault() {
        XCTAssertTrue(GeneratedSSHTransportGates.peerHTTPRuntimeDisabled)
        XCTAssertTrue(GeneratedSSHTransportGates.configV2WriteEnabled)
        XCTAssertFalse(GeneratedSSHTransportGates.remoteSecretWriteReleaseEnabled)
        XCTAssertFalse(GeneratedSSHTransportGates.publicAddPeerSuccessEnabled)

        let body = Installer.remoteInstallConfigJSON(sharedKey: "secret", selfShort: "m4")

        XCTAssertTrue(body.contains(#""listen": "127.0.0.1:7853""#))
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
        let command = Installer.remoteUpdateCommand(stage: "/tmp/clipfan-install.ABC123",
                                                    payloadBinaryName: nil,
                                                    enforceStorageAbort: false,
                                                    enforceDowngradeBlock: false)

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
                                                    enforceStorageAbort: true,
                                                    enforceDowngradeBlock: false)

        let preflight = try! XCTUnwrap(command.range(of: "\"$preflight_bin\" storage-preflight"))
        let install = try! XCTUnwrap(command.range(of: "cd \"$stage\" && bash install.sh --no-tmux >&2"))

        XCTAssertLessThan(preflight.lowerBound, install.lowerBound)
        XCTAssertTrue(command.contains("payload_bin=\"$stage/clipfan-linux-arm64\""))
        XCTAssertTrue(command.contains("preflight_bin=\"$payload_bin\""))
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

    func testRemoteUpdateCommandDowngradeBlockInstallsWithoutRestartThenStopsService() {
        let command = Installer.remoteUpdateCommand(stage: "/tmp/clipfan-install.ABC123",
                                                    payloadBinaryName: "clipfan-linux-arm64",
                                                    enforceStorageAbort: false,
                                                    enforceDowngradeBlock: true)

        let stagedVersion = try! XCTUnwrap(command.range(of: "\"$payload_bin\" version --json"))
        let install = try! XCTUnwrap(command.range(of: "cd \"$stage\" && bash install.sh --no-tmux --no-restart >&2"))
        let stop = try! XCTUnwrap(command.range(of: "systemctl --user stop clipfan.service"))
        let installedVersion = try! XCTUnwrap(command.range(of: "\"$bin\" version --json"))
        let restart = try! XCTUnwrap(command.range(of: "systemctl --user restart clipfan.service"))
        let finalVersion = try! XCTUnwrap(command.range(of: "\"$bin\" version", options: .backwards))

        XCTAssertLessThan(stagedVersion.lowerBound, install.lowerBound)
        XCTAssertLessThan(install.lowerBound, stop.lowerBound)
        XCTAssertLessThan(stop.lowerBound, installedVersion.lowerBound)
        XCTAssertLessThan(installedVersion.lowerBound, restart.lowerBound)
        XCTAssertLessThan(restart.lowerBound, finalVersion.lowerBound)
        XCTAssertTrue(command.contains("pre_ssh_binary_unsupported"))
        XCTAssertTrue(command.contains("public_listener_service_still_active"))
        XCTAssertFalse(command.contains("config.json"))
        XCTAssertFalse(command.contains("config_version"))
    }

    func testGeneratedConfigV2WriteGateEnablesDefaultDowngradeBlock() throws {
        guard GeneratedSSHTransportGates.configV2WriteEnabled else {
            throw XCTSkip("requires internal/test generated config v2 write gate")
        }
        XCTAssertTrue(GeneratedSSHTransportGates.peerHTTPRuntimeDisabled)
        XCTAssertFalse(GeneratedSSHTransportGates.remoteSecretWriteReleaseEnabled)
        XCTAssertFalse(GeneratedSSHTransportGates.publicAddPeerSuccessEnabled)

        let command = Installer.remoteUpdateCommand(
            stage: "/tmp/clipfan-install.ABC123",
            payloadBinaryName: "clipfan-linux-arm64",
            enforceStorageAbort: GeneratedSSHTransportGates.peerHTTPRuntimeDisabled ||
                GeneratedSSHTransportGates.configV2WriteEnabled
        )

        let storagePreflight = try XCTUnwrap(command.range(of: "\"$preflight_bin\" storage-preflight"))
        let stagedVersion = try XCTUnwrap(command.range(of: "\"$payload_bin\" version --json"))
        let install = try XCTUnwrap(command.range(of: "cd \"$stage\" && bash install.sh --no-tmux --no-restart >&2"))
        let stop = try XCTUnwrap(command.range(of: "systemctl --user stop clipfan.service",
                                               range: install.upperBound..<command.endIndex))
        let installedVersion = try XCTUnwrap(command.range(of: "\"$bin\" version --json"))
        let restart = try XCTUnwrap(command.range(of: "systemctl --user restart clipfan.service"))

        XCTAssertLessThan(storagePreflight.lowerBound, install.lowerBound)
        XCTAssertLessThan(stagedVersion.lowerBound, install.lowerBound)
        XCTAssertLessThan(install.lowerBound, stop.lowerBound)
        XCTAssertLessThan(stop.lowerBound, installedVersion.lowerBound)
        XCTAssertLessThan(installedVersion.lowerBound, restart.lowerBound)
        XCTAssertTrue(command.contains("pre_ssh_binary_unsupported"))
        XCTAssertTrue(command.contains("public_listener_service_still_active"))
        XCTAssertFalse(command.contains("config.json"))
        XCTAssertFalse(command.contains("config_version"))
    }

    func testRemoteUpdateCommandDowngradeBlockRejectsPreSSHStagedBinaryBeforeInstall() throws {
        let fixture = try makeRemoteUpdateShellFixture(systemctlIsActive: false)
        defer { try? FileManager.default.removeItem(at: fixture.root) }
        try writeExecutableScript(fixture.stage.appendingPathComponent("clipfan-linux-arm64"), """
        #!/usr/bin/env bash
        if [[ "$1" == "version" && "$2" == "--json" ]]; then
          echo "old binary has no json capability" >&2
          exit 42
        fi
        exit 42
        """)

        let command = Installer.remoteUpdateCommand(stage: fixture.stage.path,
                                                    payloadBinaryName: "clipfan-linux-arm64",
                                                    enforceStorageAbort: false,
                                                    enforceDowngradeBlock: true)
        let result = try runBash(command, environment: fixture.environment)

        XCTAssertEqual(result.status, 1)
        XCTAssertTrue(result.stderr.contains("pre_ssh_binary_unsupported"))
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.installMarker.path))
    }

    func testRemoteUpdateCommandDowngradeBlockFailsIfOldServiceStillActive() throws {
        let fixture = try makeRemoteUpdateShellFixture(systemctlIsActive: true)
        defer { try? FileManager.default.removeItem(at: fixture.root) }

        let command = Installer.remoteUpdateCommand(stage: fixture.stage.path,
                                                    payloadBinaryName: "clipfan-linux-arm64",
                                                    enforceStorageAbort: false,
                                                    enforceDowngradeBlock: true)
        let result = try runBash(command, environment: fixture.environment)

        XCTAssertEqual(result.status, 1)
        XCTAssertTrue(result.stderr.contains("public_listener_service_still_active"))
        XCTAssertTrue(FileManager.default.fileExists(atPath: fixture.installMarker.path))
    }

    func testRemoteUpdateCommandDowngradeBlockRestartsServiceAfterCapabilityCheck() throws {
        let fixture = try makeRemoteUpdateShellFixture(systemctlIsActive: false)
        defer { try? FileManager.default.removeItem(at: fixture.root) }

        let command = Installer.remoteUpdateCommand(stage: fixture.stage.path,
                                                    payloadBinaryName: "clipfan-linux-arm64",
                                                    enforceStorageAbort: false,
                                                    enforceDowngradeBlock: true)
        let result = try runBash(command, environment: fixture.environment)

        XCTAssertEqual(result.status, 0)
        XCTAssertEqual(result.stdout.trimmingCharacters(in: .whitespacesAndNewlines), "v-fixture")
        XCTAssertTrue(FileManager.default.fileExists(atPath: fixture.installMarker.path))

        let systemctlLog = try String(contentsOf: fixture.systemctlLog, encoding: .utf8)
        let stop = try XCTUnwrap(systemctlLog.range(of: "--user stop clipfan.service"))
        let disable = try XCTUnwrap(systemctlLog.range(of: "--user disable clipfan.service"))
        let enable = try XCTUnwrap(systemctlLog.range(of: "--user enable clipfan.service"))
        let restart = try XCTUnwrap(systemctlLog.range(of: "--user restart clipfan.service"))
        XCTAssertLessThan(stop.lowerBound, disable.lowerBound)
        XCTAssertLessThan(disable.lowerBound, enable.lowerBound)
        XCTAssertLessThan(enable.lowerBound, restart.lowerBound)
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
        let expectedUpdateCommand = Installer.remoteUpdateCommand(
            stage: "/tmp/clipfan-install.ABC123",
            payloadBinaryName: "clipfan-linux-arm64",
            enforceStorageAbort: GeneratedSSHTransportGates.peerHTTPRuntimeDisabled ||
                GeneratedSSHTransportGates.configV2WriteEnabled,
            enforceDowngradeBlock: GeneratedSSHTransportGates.configV2WriteEnabled
        )
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
                if exe == "/usr/bin/ssh", args.last == expectedUpdateCommand {
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
        XCTAssertEqual(commands[2].1.last, expectedUpdateCommand)
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
                try await Installer.addPeerToLocalConfig("new-peer",
                                                         configURL: configURL,
                                                         configV2WriteEnabled: false)
                XCTFail("expected config_v2_writes_disabled for \(body)")
            } catch {
                XCTAssertTrue(String(describing: error).contains("config_v2_writes_disabled"),
                              "error \(error) should include stable code")
            }

            let after = try Data(contentsOf: configURL)
            XCTAssertEqual(after, before)
        }
    }

    func testAddPeerToLocalConfigUsesScopedWriterForConfigV2AndDoesNotRewriteFile() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-config-v2-scoped-\(UUID().uuidString)")
        let configURL = root.appendingPathComponent("clipfan/config.json")
        defer { try? FileManager.default.removeItem(at: root) }

        try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let before = """
        {"config_version":2,"config_revision":1,"shared_key":"secret","static_peers":["existing"],"ssh":{"future":{"keep":true},"peers":{"existing":{"future_peer":"keep"}}},"future":{"keep":true}}
        """.data(using: .utf8)!
        try before.write(to: configURL)

        var capturedPeerID: String?
        var capturedRequest: LocalDaemonSSHPeerUpsertRequest?
        let writer = ScopedSSHPeerConfigWriter { peerID, request in
            capturedPeerID = peerID
            capturedRequest = request
            return try JSONDecoder.clipfan.decode(LocalDaemonSSHPeerConfigResponse.self, from: """
            {"peer":{"id":"new-peer","enabled":true,"accept":true,"connect":false,"migration_state":"loopback_unprovisioned"},"config_revision":8,"revision_state":"current","config_version":2}
            """.data(using: .utf8)!)
        }

        try await Installer.addPeerToLocalConfig("new-peer",
                                                 configURL: configURL,
                                                 scopedWriter: writer,
                                                 configV2WriteEnabled: true)

        XCTAssertEqual(try Data(contentsOf: configURL), before)
        XCTAssertEqual(capturedPeerID, "new-peer")
        XCTAssertEqual(capturedRequest?.expected_config_revision, 1)
        XCTAssertEqual(capturedRequest?.peer.id, "new-peer")
        XCTAssertEqual(capturedRequest?.peer.enabled, true)
        XCTAssertEqual(capturedRequest?.peer.accept, true)
        XCTAssertEqual(capturedRequest?.peer.connect, false)
        XCTAssertEqual(capturedRequest?.peer.migration_state, "loopback_unprovisioned")
    }

    func testAddPeerToLocalConfigRequiresScopedWriterForConfigV2WhenGateEnabled() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-config-v2-no-writer-\(UUID().uuidString)")
        let configURL = root.appendingPathComponent("clipfan/config.json")
        defer { try? FileManager.default.removeItem(at: root) }

        try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let before = #"{"config_version":2,"config_revision":9,"shared_key":"secret","static_peers":["existing"],"future":{"keep":true}}"#
            .data(using: .utf8)!
        try before.write(to: configURL)

        do {
            try await Installer.addPeerToLocalConfig("new-peer",
                                                     configURL: configURL,
                                                     configV2WriteEnabled: true)
            XCTFail("expected missing scoped writer failure")
        } catch {
            XCTAssertTrue(String(describing: error).contains("config_v2_scoped_writer_unavailable"),
                          "error \(error) should include stable code")
        }

        XCTAssertEqual(try Data(contentsOf: configURL), before)
    }

    func testAddPeerToLocalConfigRejectsUnsupportedConfigVersionWhenGateEnabled() async throws {
        let bodies = [
            #"{"config_version":3,"config_revision":9,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":"2","config_revision":9,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":2.1,"config_revision":9,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":null,"config_revision":9,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":0,"config_revision":9,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":true,"config_revision":9,"shared_key":"secret","static_peers":["existing"]}"#
        ]

        for body in bodies {
            let root = FileManager.default.temporaryDirectory
                .appendingPathComponent("clipfan-config-v2-bad-version-\(UUID().uuidString)")
            let configURL = root.appendingPathComponent("clipfan/config.json")
            defer { try? FileManager.default.removeItem(at: root) }

            try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                    withIntermediateDirectories: true)
            let before = body.data(using: .utf8)!
            try before.write(to: configURL)

            var calledWriter = false
            let writer = ScopedSSHPeerConfigWriter { _, _ in
                calledWriter = true
                return try JSONDecoder.clipfan.decode(LocalDaemonSSHPeerConfigResponse.self, from: """
                {"peer":{"id":"new-peer"},"config_revision":10,"revision_state":"current","config_version":2}
                """.data(using: .utf8)!)
            }

            do {
                try await Installer.addPeerToLocalConfig("new-peer",
                                                         configURL: configURL,
                                                         scopedWriter: writer,
                                                         configV2WriteEnabled: true)
                XCTFail("expected unsupported_config_version for \(body)")
            } catch {
                XCTAssertTrue(String(describing: error).contains("unsupported_config_version"),
                              "error \(error) should include stable code")
            }

            XCTAssertFalse(calledWriter)
            XCTAssertEqual(try Data(contentsOf: configURL), before)
        }
    }

    func testAddPeerToLocalConfigTreatsAlreadyAdvancedConfigV2PeerAsPresent() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-config-v2-existing-\(UUID().uuidString)")
        let configURL = root.appendingPathComponent("clipfan/config.json")
        defer { try? FileManager.default.removeItem(at: root) }

        try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let before = #"{"config_version":2,"config_revision":12,"shared_key":"secret","ssh":{"peers":[{"id":"new-peer","migration_state":"ssh_material_staged","future_peer":"keep"}]},"future":{"keep":true}}"#
            .data(using: .utf8)!
        try before.write(to: configURL)

        var upsertCalls = 0
        var readCalls = 0
        let writer = ScopedSSHPeerConfigWriter(
            readPeer: { peerID in
                readCalls += 1
                XCTAssertEqual(peerID, "new-peer")
                return try JSONDecoder.clipfan.decode(LocalDaemonSSHPeerConfigResponse.self, from: """
                {"peer":{"id":"new-peer","enabled":true,"accept":true,"connect":false,"migration_state":"ssh_material_staged"},"config_revision":12,"revision_state":"current","config_version":2}
                """.data(using: .utf8)!)
            },
            upsertPeer: { peerID, request in
                upsertCalls += 1
                XCTAssertEqual(peerID, "new-peer")
                XCTAssertEqual(request.expected_config_revision, 12)
                throw LocalDaemonSSHPeerConfigError.api(code: localDaemonSSHPeerMigrationStateChangeNotAllowedCode,
                                                        statusCode: 409)
            }
        )

        try await Installer.addPeerToLocalConfig("new-peer",
                                                 configURL: configURL,
                                                 scopedWriter: writer,
                                                 configV2WriteEnabled: true)

        XCTAssertEqual(upsertCalls, 1)
        XCTAssertEqual(readCalls, 1)
        XCTAssertEqual(try Data(contentsOf: configURL), before)
    }

    func testAddPeerToLocalConfigPropagatesStateMismatchWhenScopedReadFails() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-config-v2-read-fails-\(UUID().uuidString)")
        let configURL = root.appendingPathComponent("clipfan/config.json")
        defer { try? FileManager.default.removeItem(at: root) }

        try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let before = #"{"config_version":2,"config_revision":12,"shared_key":"secret","ssh":{"peers":[{"id":"new-peer","migration_state":"ssh_material_staged"}]}}"#
            .data(using: .utf8)!
        try before.write(to: configURL)

        let writer = ScopedSSHPeerConfigWriter(
            readPeer: { _ in
                throw LocalDaemonSSHPeerConfigError.api(code: "missing_config_revision", statusCode: 409)
            },
            upsertPeer: { _, _ in
                throw LocalDaemonSSHPeerConfigError.api(code: localDaemonSSHPeerMigrationStateChangeNotAllowedCode,
                                                        statusCode: 409)
            }
        )

        do {
            try await Installer.addPeerToLocalConfig("new-peer",
                                                     configURL: configURL,
                                                     scopedWriter: writer,
                                                     configV2WriteEnabled: true)
            XCTFail("expected original migration-state error")
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode) {
            XCTAssertEqual(code, localDaemonSSHPeerMigrationStateChangeNotAllowedCode)
            XCTAssertEqual(statusCode, 409)
        } catch {
            XCTFail("unexpected error \(error)")
        }

        XCTAssertEqual(try Data(contentsOf: configURL), before)
    }

    func testAddPeerToLocalConfigPropagatesStateMismatchWhenScopedWriterHasNoReader() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-config-v2-no-reader-\(UUID().uuidString)")
        let configURL = root.appendingPathComponent("clipfan/config.json")
        defer { try? FileManager.default.removeItem(at: root) }

        try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let before = #"{"config_version":2,"config_revision":12,"shared_key":"secret","ssh":{"peers":[{"id":"new-peer","enabled":true,"accept":true,"migration_state":"ssh_material_staged"}]}}"#
            .data(using: .utf8)!
        try before.write(to: configURL)

        let writer = ScopedSSHPeerConfigWriter { _, _ in
            throw LocalDaemonSSHPeerConfigError.api(code: localDaemonSSHPeerMigrationStateChangeNotAllowedCode,
                                                    statusCode: 409)
        }

        do {
            try await Installer.addPeerToLocalConfig("new-peer",
                                                     configURL: configURL,
                                                     scopedWriter: writer,
                                                     configV2WriteEnabled: true)
            XCTFail("expected original migration-state error")
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode) {
            XCTAssertEqual(code, localDaemonSSHPeerMigrationStateChangeNotAllowedCode)
            XCTAssertEqual(statusCode, 409)
        } catch {
            XCTFail("unexpected error \(error)")
        }

        XCTAssertEqual(try Data(contentsOf: configURL), before)
    }

    func testAddPeerToLocalConfigDoesNotTreatDisabledAdvancedConfigV2PeerAsPresent() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-config-v2-disabled-\(UUID().uuidString)")
        let configURL = root.appendingPathComponent("clipfan/config.json")
        defer { try? FileManager.default.removeItem(at: root) }

        try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let before = #"{"config_version":2,"config_revision":12,"shared_key":"secret","ssh":{"peers":[{"id":"new-peer","enabled":false,"accept":true,"migration_state":"ssh_material_staged"}]}}"#
            .data(using: .utf8)!
        try before.write(to: configURL)

        let writer = ScopedSSHPeerConfigWriter(
            readPeer: { _ in
                try JSONDecoder.clipfan.decode(LocalDaemonSSHPeerConfigResponse.self, from: """
                {"peer":{"id":"new-peer","enabled":false,"accept":true,"connect":false,"migration_state":"ssh_material_staged"},"config_revision":12,"revision_state":"current","config_version":2}
                """.data(using: .utf8)!)
            },
            upsertPeer: { _, _ in
                throw LocalDaemonSSHPeerConfigError.api(code: localDaemonSSHPeerMigrationStateChangeNotAllowedCode,
                                                        statusCode: 409)
            }
        )

        do {
            try await Installer.addPeerToLocalConfig("new-peer",
                                                     configURL: configURL,
                                                     scopedWriter: writer,
                                                     configV2WriteEnabled: true)
            XCTFail("expected original migration-state error")
        } catch LocalDaemonSSHPeerConfigError.api(let code, let statusCode) {
            XCTAssertEqual(code, localDaemonSSHPeerMigrationStateChangeNotAllowedCode)
            XCTAssertEqual(statusCode, 409)
        } catch {
            XCTFail("unexpected error \(error)")
        }

        XCTAssertEqual(try Data(contentsOf: configURL), before)
    }

    func testAddPeerToLocalConfigTreatsRevisionConflictWithExistingConnectPeerAsPresent() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-config-v2-existing-connect-\(UUID().uuidString)")
        let configURL = root.appendingPathComponent("clipfan/config.json")
        defer { try? FileManager.default.removeItem(at: root) }

        try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let before = #"{"config_version":2,"config_revision":12,"shared_key":"secret","ssh":{"peers":[{"id":"new-peer","enabled":true,"accept":true,"connect":true,"migration_state":"ssh_keys_ready"}]}}"#
            .data(using: .utf8)!
        try before.write(to: configURL)

        var readCalls = 0
        let writer = ScopedSSHPeerConfigWriter(
            readPeer: { peerID in
                readCalls += 1
                XCTAssertEqual(peerID, "new-peer")
                return try JSONDecoder.clipfan.decode(LocalDaemonSSHPeerConfigResponse.self, from: """
                {"peer":{"id":"new-peer","enabled":true,"accept":true,"connect":true,"migration_state":"ssh_keys_ready"},"config_revision":13,"revision_state":"current","config_version":2}
                """.data(using: .utf8)!)
            },
            upsertPeer: { _, _ in
                throw LocalDaemonSSHPeerConfigError.api(code: localDaemonConfigRevisionConflictCode,
                                                        statusCode: 409)
            }
        )

        try await Installer.addPeerToLocalConfig("new-peer",
                                                 configURL: configURL,
                                                 scopedWriter: writer,
                                                 configV2WriteEnabled: true)

        XCTAssertEqual(readCalls, 1)
        XCTAssertEqual(try Data(contentsOf: configURL), before)
    }

    func testAddPeerToLocalConfigRejectsConfigV2WithoutValidRevisionBeforeScopedWrite() async throws {
        let bodies = [
            #"{"config_version":2,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":2,"config_revision":0,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":2,"config_revision":-1,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":2,"config_revision":true,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":2,"config_revision":"7","shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":2,"config_revision":7.5,"shared_key":"secret","static_peers":["existing"]}"#,
            #"{"config_version":2,"config_revision":18446744073709551616,"shared_key":"secret","static_peers":["existing"]}"#
        ]

        for body in bodies {
            let root = FileManager.default.temporaryDirectory
                .appendingPathComponent("clipfan-config-v2-bad-revision-\(UUID().uuidString)")
            let configURL = root.appendingPathComponent("clipfan/config.json")
            defer { try? FileManager.default.removeItem(at: root) }

            try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                    withIntermediateDirectories: true)
            let before = body.data(using: .utf8)!
            try before.write(to: configURL)

            var calledWriter = false
            let writer = ScopedSSHPeerConfigWriter { _, _ in
                calledWriter = true
                return try JSONDecoder.clipfan.decode(LocalDaemonSSHPeerConfigResponse.self, from: """
                {"peer":{"id":"new-peer"},"config_revision":8,"revision_state":"current","config_version":2}
                """.data(using: .utf8)!)
            }

            do {
                try await Installer.addPeerToLocalConfig("new-peer",
                                                         configURL: configURL,
                                                         scopedWriter: writer,
                                                         configV2WriteEnabled: true)
                XCTFail("expected invalid_config_revision for \(body)")
            } catch {
                XCTAssertTrue(String(describing: error).contains("invalid_config_revision"),
                              "error \(error) should include stable code")
            }

            XCTAssertFalse(calledWriter)
            XCTAssertEqual(try Data(contentsOf: configURL), before)
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
        if [[ "$1" == "version" && "$2" == "--json" ]]; then
          echo '{"version":"v-fixture","capabilities":{"config_v2":true}}'
          exit 0
        fi
        if [[ "$1" == "version" ]]; then
          echo "v-fixture"
          exit 0
        fi
        echo "unexpected staged binary command: $*" >&2
        exit 99
        """)
        try writeExecutableScript(stage.appendingPathComponent("install.sh"), """
        #!/usr/bin/env bash
        touch "$INSTALL_MARKER"
        mkdir -p "$HOME/.local/bin"
        cat > "$HOME/.local/bin/clipfan" <<'BIN'
        #!/usr/bin/env bash
        if [[ "$1" == "version" && "$2" == "--json" ]]; then
          echo '{"version":"v-fixture","capabilities":{"config_v2":true}}'
          exit 0
        fi
        if [[ "$1" == "version" ]]; then
          echo "v-fixture"
          exit 0
        fi
        exit 99
        BIN
        chmod 755 "$HOME/.local/bin/clipfan"
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
