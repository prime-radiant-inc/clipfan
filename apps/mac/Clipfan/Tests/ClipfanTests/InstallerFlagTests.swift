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

    func testRemoteUpdateCommandPreservesConfigAndSkipsTmux() {
        let command = Installer.remoteUpdateCommand(stage: "/tmp/clipfan-install.ABC123")

        XCTAssertTrue(command.contains("stage='/tmp/clipfan-install.ABC123'"))
        XCTAssertTrue(command.contains("trap 'rm -rf \"$stage\"' EXIT"))
        XCTAssertTrue(command.contains("cd \"$stage\" && bash install.sh --no-tmux >&2"))
        XCTAssertTrue(command.contains("\"$bin\" version"))
        XCTAssertFalse(command.contains("config.json"))
        XCTAssertFalse(command.contains("~/.config/clipfan"))
        XCTAssertFalse(command.contains("--with-tmux"))
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

        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-config-v2-\(UUID().uuidString)")
        let configURL = root.appendingPathComponent("clipfan/config.json")
        defer { try? FileManager.default.removeItem(at: root) }

        try FileManager.default.createDirectory(at: configURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let before = #"{"config_version":2,"config_revision":1,"shared_key":"secret","static_peers":["existing"],"future":{"keep":true}}"#
            .data(using: .utf8)!
        try before.write(to: configURL)
        try FileManager.default.setAttributes([.posixPermissions: 0o600],
                                              ofItemAtPath: configURL.path)

        do {
            try await Installer.addPeerToLocalConfig("new-peer", configURL: configURL)
            XCTFail("expected config_v2_writes_disabled")
        } catch {
            XCTAssertTrue(String(describing: error).contains("config_v2_writes_disabled"),
                          "error \(error) should include stable code")
        }

        let after = try Data(contentsOf: configURL)
        XCTAssertEqual(after, before)
    }

    private func posixMode(_ url: URL) throws -> Int {
        let attrs = try FileManager.default.attributesOfItem(atPath: url.path)
        return (attrs[.posixPermissions] as? NSNumber)?.intValue ?? 0
    }
}
