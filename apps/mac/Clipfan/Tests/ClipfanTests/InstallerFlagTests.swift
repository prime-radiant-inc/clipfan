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

    func testRemoteObservedCallbackCommandExtractsFirstFieldUnderZsh() throws {
        let zsh = "/bin/zsh"
        guard FileManager.default.isExecutableFile(atPath: zsh) else {
            throw XCTSkip("zsh is not available")
        }

        let result = try runShell(zsh,
                                  Installer.privateDirectMeshObservedSSHClientHostCommand(),
                                  environment: ["SSH_CONNECTION": "100.92.23.74 51775 100.113.28.18 22"])

        XCTAssertEqual(result.status, 0)
        XCTAssertEqual(result.stdout, "100.92.23.74\n")
        XCTAssertEqual(result.stderr, "")
    }

    func testPrivateDirectMeshInstallConfigIsConfigV2IdentityOnly() {
        let body = Installer.privateDirectMeshInstallConfigJSON(hostID: "linux-b")

        XCTAssertTrue(body.contains(#""config_version": 2"#))
        XCTAssertTrue(body.contains(#""config_revision": 1"#))
        XCTAssertTrue(body.contains(#""hostname": "linux-b""#))
        XCTAssertTrue(body.contains(#""listen": "127.0.0.1:7853""#))
        XCTAssertTrue(body.contains(#""shared_key": """#))
        XCTAssertTrue(body.contains(#""static_peers": []"#))
        XCTAssertFalse(body.contains(#""static_peers": ["m4"]"#))
        XCTAssertFalse(body.contains("secret"))
    }

    func testPrivateDirectMeshInstallCommandPreservesConfigAndRunsSetupNoRestart() {
        let command = Installer.privateDirectMeshInstallCommand(stage: "/tmp/clipfan-install.ABC123",
                                                                configPath: "/home/jesse/Application Support/Clipfan/config.json",
                                                                installPath: "/home/jesse/.local/bin/clipfan",
                                                                withTmux: true)

        XCTAssertTrue(command.contains("stage='/tmp/clipfan-install.ABC123'"))
        XCTAssertTrue(command.contains("config_path='/home/jesse/Application Support/Clipfan/config.json'"))
        XCTAssertTrue(command.contains("install_path='/home/jesse/.local/bin/clipfan'"))
        XCTAssertTrue(command.contains("trap 'rm -rf \"$stage\"' EXIT"))
        XCTAssertTrue(command.contains("if [ ! -f \"$config_path\" ]; then"))
        XCTAssertTrue(command.contains("install -m 0600 \"$stage/config.json\" \"$config_path\""))
        XCTAssertTrue(command.contains("Keeping existing config: $config_path"))
        XCTAssertTrue(command.contains("install_dir=\"$(dirname \"$install_path\")\""))
        XCTAssertTrue(command.contains("cd \"$stage\" && DEST=\"$install_dir\" bash install.sh --with-tmux --no-restart >&2"))
        XCTAssertFalse(command.contains("shared_key"))
    }

    func testTrustedLocalProvisioningBinaryUsesBundledBinary() throws {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-trusted-helper-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let bundled = try writeVersionFixture(dir.appendingPathComponent("bundled"), version: "v0.3.21")

        let got = try Installer.trustedLocalProvisioningBinaryPath(
            bundledBinary: bundled
        )

        XCTAssertEqual(got, bundled.path)
    }

    func testTrustedLocalProvisioningBinaryRejectsMissingBundledBinary() throws {
        XCTAssertThrowsError(try Installer.trustedLocalProvisioningBinaryPath(bundledBinary: nil)) { error in
            XCTAssertTrue(String(describing: error).contains("current_local_provisioning_binary_required"))
        }
    }

    func testProvisionPrivateDirectMeshFailsClosedBeforeSSHWhenTrustedLocalProvisioningBinaryUnavailable() async throws {
        var called = false

        do {
            try await Installer.provisionPrivateDirectMesh(
                hostSpecs: [
                    "id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
                    "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
                ],
                regularKnownHosts: "/Users/jesse/.ssh/known_hosts",
                trustKeyscan: true,
                localProvisioningBinary: {
                    throw InstallError.configIO("current_local_provisioning_binary_required")
                },
                runCommand: { _, _ in
                    called = true
                    return ""
                },
                onProgress: { _ in }
            )
            XCTFail("expected current local provisioning binary failure")
        } catch {
            XCTAssertTrue(String(describing: error).contains("current_local_provisioning_binary_required"))
        }

        XCTAssertFalse(called)
    }

    func testPrivateDirectMeshHostSpecReplacingPlatformDefaultsUsesDetectedDarwinPaths() throws {
        let spec = try Installer.privateDirectMeshHostSpecReplacingPlatformDefaults(
            "id=magic,ssh=magic-kingdom,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
            goos: "darwin"
        )

        XCTAssertTrue(spec.contains("install=/Users/jesse/.local/bin/clipfan"))
        XCTAssertTrue(spec.contains("config=/Users/jesse/.config/clipfan/config.json"))
        XCTAssertTrue(spec.contains("known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts"))
        XCTAssertTrue(spec.contains("sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519"))
    }

    func testPrivateDirectMeshHostSpecReplacingPlatformDefaultsUsesDetectedLinuxPaths() throws {
        let spec = try Installer.privateDirectMeshHostSpecReplacingPlatformDefaults(
            "id=linux-b,ssh=linux-b,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519,callback_host=manual",
            goos: "linux"
        )

        XCTAssertTrue(spec.contains("install=/home/jesse/.local/bin/clipfan"))
        XCTAssertTrue(spec.contains("config=/home/jesse/.config/clipfan/config.json"))
        XCTAssertTrue(spec.contains("known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts"))
        XCTAssertTrue(spec.contains("sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"))
        XCTAssertTrue(spec.contains("callback_host=manual"))
    }

    func testPrivateDirectMeshHostSpecReplacingPlatformDefaultsPreservesCustomPaths() throws {
        let spec = try Installer.privateDirectMeshHostSpecReplacingPlatformDefaults(
            "id=linux-b,ssh=linux-b,user=jesse,port=22,install=/opt/clipfan/bin/clipfan,config=/srv/clipfan/config.json,known_hosts=/srv/clipfan/known_hosts,sync_key=/srv/clipfan/sync_ed25519",
            goos: "linux"
        )

        XCTAssertTrue(spec.contains("install=/opt/clipfan/bin/clipfan"))
        XCTAssertTrue(spec.contains("config=/srv/clipfan/config.json"))
        XCTAssertTrue(spec.contains("known_hosts=/srv/clipfan/known_hosts"))
        XCTAssertTrue(spec.contains("sync_key=/srv/clipfan/sync_ed25519"))
    }

    func testRegularSSHConnectionArgsUseStrictKnownHostsForSSHAndSCP() {
        let args = Installer.regularSSHConnectionArgs(
            port: 2200,
            knownHosts: "/Users/jesse/.ssh/clipfan_regular_known_hosts"
        )

        XCTAssertFalse(args.sshArgs.contains("-F"))
        XCTAssertFalse(args.sshArgs.contains("/dev/null"))
        XCTAssertTrue(args.sshArgs.contains("BatchMode=yes"))
        XCTAssertTrue(args.sshArgs.contains("StrictHostKeyChecking=yes"))
        XCTAssertTrue(args.sshArgs.contains("UserKnownHostsFile=/Users/jesse/.ssh/clipfan_regular_known_hosts"))
        XCTAssertTrue(args.sshArgs.contains("GlobalKnownHostsFile=/dev/null"))
        XCTAssertFalse(args.sshArgs.contains("ProxyCommand=none"))
        XCTAssertFalse(args.sshArgs.contains("ProxyJump=none"))
        XCTAssertTrue(args.sshArgs.contains("ClearAllForwardings=yes"))
        XCTAssertTrue(args.sshArgs.contains("-p"))
        XCTAssertTrue(args.sshArgs.contains("2200"))

        XCTAssertEqual(args.scpArgs.first, "-q")
        XCTAssertTrue(args.scpArgs.contains("BatchMode=yes"))
        XCTAssertTrue(args.scpArgs.contains("StrictHostKeyChecking=yes"))
        XCTAssertTrue(args.scpArgs.contains("UserKnownHostsFile=/Users/jesse/.ssh/clipfan_regular_known_hosts"))
        XCTAssertTrue(args.scpArgs.contains("GlobalKnownHostsFile=/dev/null"))
        XCTAssertTrue(args.scpArgs.contains("-P"))
        XCTAssertTrue(args.scpArgs.contains("2200"))

        let defaultPortArgs = Installer.regularSSHConnectionArgs(
            port: 22,
            knownHosts: "/Users/jesse/.ssh/clipfan_regular_known_hosts"
        )
        XCTAssertFalse(defaultPortArgs.sshArgs.contains("-p"))
        XCTAssertFalse(defaultPortArgs.scpArgs.contains("-P"))
    }

    func testRemoteSCPDestinationBracketsIPv6Hosts() {
        XCTAssertEqual(
            Installer.remoteSCPDestination(target: "jesse@fd7a:115c:a1e0::1234",
                                           remotePath: "/tmp/clipfan-install.ABC123/"),
            "jesse@[fd7a:115c:a1e0::1234]:/tmp/clipfan-install.ABC123/"
        )
        XCTAssertEqual(
            Installer.remoteSCPDestination(target: "fd7a:115c:a1e0::1234",
                                           remotePath: "/tmp/clipfan-install.ABC123/"),
            "[fd7a:115c:a1e0::1234]:/tmp/clipfan-install.ABC123/"
        )
        XCTAssertEqual(
            Installer.remoteSCPDestination(target: "jesse@linux-b.tailnet",
                                           remotePath: "/tmp/clipfan-install.ABC123/"),
            "jesse@linux-b.tailnet:/tmp/clipfan-install.ABC123/"
        )
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyWritesKeyscanLine() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        defer { try? FileManager.default.removeItem(at: root) }

        var commands: [(String, [String])] = []
        try await Installer.trustPrivateDirectMeshBootstrapHostKey(
            sshHost: "100.114.54.38",
            user: "jesse",
            port: 22,
            regularKnownHosts: knownHosts.path,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh" {
                    return """
                    hostname 100.114.54.38
                    port 22
                    """
                }
                return """
                # 100.114.54.38:22 SSH-2.0-OpenSSH
                100.114.54.38 ssh-rsa AAAATEST_RSA
                100.114.54.38 ssh-ed25519 AAAATEST_ED25519
                """
            }
        )

        XCTAssertEqual(commands.count, 2)
        XCTAssertEqual(commands[0].0, "/usr/bin/ssh")
        XCTAssertEqual(commands[0].1, ["-G", "-l", "jesse", "100.114.54.38"])
        XCTAssertEqual(commands[1].0, "/usr/bin/ssh-keyscan")
        XCTAssertEqual(commands[1].1, ["-T", "5", "-p", "22", "100.114.54.38"])
        XCTAssertEqual(
            try String(contentsOf: knownHosts, encoding: .utf8),
            "100.114.54.38 ssh-rsa AAAATEST_RSA\n100.114.54.38 ssh-ed25519 AAAATEST_ED25519\n"
        )
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyUsesSSHConfigHostNameForKeyscan() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        defer { try? FileManager.default.removeItem(at: root) }

        var commands: [(String, [String])] = []
        try await Installer.trustPrivateDirectMeshBootstrapHostKey(
            sshHost: "magic-kingdom",
            user: "jesse",
            port: 22,
            regularKnownHosts: knownHosts.path,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh" {
                    return """
                    hostname 100.114.54.38
                    port 22
                    """
                }
                return "100.114.54.38 ssh-ed25519 AAAARESOLVED_KEY\n"
            }
        )

        XCTAssertEqual(commands.map(\.0), ["/usr/bin/ssh", "/usr/bin/ssh-keyscan"])
        XCTAssertEqual(commands[0].1, ["-G", "-l", "jesse", "magic-kingdom"])
        XCTAssertEqual(commands[1].1, ["-T", "5", "-p", "22", "100.114.54.38"])
        XCTAssertEqual(
            try String(contentsOf: knownHosts, encoding: .utf8),
            "magic-kingdom ssh-ed25519 AAAARESOLVED_KEY\n"
        )
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyUsesSSHConfigIPv6HostNameForKeyscan() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        let resolvedIPv6 = "fd7a:115c:a1e0::1234"
        defer { try? FileManager.default.removeItem(at: root) }

        var commands: [(String, [String])] = []
        try await Installer.trustPrivateDirectMeshBootstrapHostKey(
            sshHost: "linux-v6",
            user: "jesse",
            port: 22,
            regularKnownHosts: knownHosts.path,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh" {
                    return """
                    hostname \(resolvedIPv6)
                    port 22
                    """
                }
                return "\(resolvedIPv6) ssh-ed25519 AAAAV6_KEY\n"
            }
        )

        XCTAssertEqual(commands.map(\.0), ["/usr/bin/ssh", "/usr/bin/ssh-keyscan"])
        XCTAssertEqual(commands[0].1, ["-G", "-l", "jesse", "linux-v6"])
        XCTAssertEqual(commands[1].1, ["-T", "5", "-p", "22", resolvedIPv6])
        XCTAssertEqual(
            try String(contentsOf: knownHosts, encoding: .utf8),
            "linux-v6 ssh-ed25519 AAAAV6_KEY\n"
        )
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyUsesSSHConfigPortForKeyscan() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        defer { try? FileManager.default.removeItem(at: root) }

        var commands: [(String, [String])] = []
        try await Installer.trustPrivateDirectMeshBootstrapHostKey(
            sshHost: "linux-b",
            user: "jesse",
            port: 22,
            regularKnownHosts: knownHosts.path,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh" {
                    return """
                    hostname linux-b
                    port 2222
                    """
                }
                return "[linux-b]:2222 ssh-ed25519 AAAAPORT_KEY\n"
            }
        )

        XCTAssertEqual(commands.map(\.0), ["/usr/bin/ssh", "/usr/bin/ssh-keyscan"])
        XCTAssertEqual(commands[0].1, ["-G", "-l", "jesse", "linux-b"])
        XCTAssertEqual(commands[1].1, ["-T", "5", "-p", "2222", "linux-b"])
        XCTAssertEqual(
            try String(contentsOf: knownHosts, encoding: .utf8),
            "[linux-b]:2222 ssh-ed25519 AAAAPORT_KEY\n"
        )
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyAcceptsSSHConfigHostNameCaseNormalization() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        defer { try? FileManager.default.removeItem(at: root) }

        var commands: [(String, [String])] = []
        try await Installer.trustPrivateDirectMeshBootstrapHostKey(
            sshHost: "linux-v6",
            user: "jesse",
            port: 22,
            regularKnownHosts: knownHosts.path,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh" {
                    return """
                    hostname linux-v6.
                    port 22
                    """
                }
                return "linux-v6. ssh-ed25519 AAAARESOLVED_KEY\n"
            }
        )

        XCTAssertEqual(commands.map(\.0), ["/usr/bin/ssh", "/usr/bin/ssh-keyscan"])
        XCTAssertEqual(commands[1].1, ["-T", "5", "-p", "22", "linux-v6."])
        XCTAssertEqual(
            try String(contentsOf: knownHosts, encoding: .utf8),
            "linux-v6 ssh-ed25519 AAAARESOLVED_KEY\n"
        )
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyRejectsUnsupportedSSHConfigProxyBeforeKeyscan() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        defer { try? FileManager.default.removeItem(at: root) }

        var commands: [(String, [String])] = []
        do {
            try await Installer.trustPrivateDirectMeshBootstrapHostKey(
                sshHost: "linux-b",
                user: "jesse",
                port: 22,
                regularKnownHosts: knownHosts.path,
                runCommand: { exe, args in
                    commands.append((exe, args))
                    return """
                    hostname linux-b
                    port 22
                    proxyjump bastion
                    """
                }
            )
            XCTFail("expected unsupported proxy config to fail")
        } catch {
            XCTAssertTrue(String(describing: error).contains("unsupported_ssh_config_for_keyscan"))
        }

        XCTAssertEqual(commands.map(\.0), ["/usr/bin/ssh"])
        XCTAssertFalse(FileManager.default.fileExists(atPath: knownHosts.path))
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyAllowsDefaultSSHConfigHostKeyAlias() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        defer { try? FileManager.default.removeItem(at: root) }

        var commands: [(String, [String])] = []
        try await Installer.trustPrivateDirectMeshBootstrapHostKey(
            sshHost: "linux-b",
            user: "jesse",
            port: 22,
            regularKnownHosts: knownHosts.path,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh" {
                    return """
                    hostname linux-b
                    port 22
                    hostkeyalias none
                    """
                }
                return "linux-b ssh-ed25519 AAAADEFAULT_ALIAS_KEY\n"
            }
        )

        XCTAssertEqual(commands.map(\.0), ["/usr/bin/ssh", "/usr/bin/ssh-keyscan"])
        XCTAssertEqual(
            try String(contentsOf: knownHosts, encoding: .utf8),
            "linux-b ssh-ed25519 AAAADEFAULT_ALIAS_KEY\n"
        )
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyRejectsSSHConfigHostKeyAliasBeforeKeyscan() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        defer { try? FileManager.default.removeItem(at: root) }

        var commands: [(String, [String])] = []
        do {
            try await Installer.trustPrivateDirectMeshBootstrapHostKey(
                sshHost: "linux-b",
                user: "jesse",
                port: 22,
                regularKnownHosts: knownHosts.path,
                runCommand: { exe, args in
                    commands.append((exe, args))
                    return """
                    hostname linux-b
                    port 22
                    hostkeyalias fleet-alias
                    """
                }
            )
            XCTFail("expected host key alias to fail")
        } catch {
            XCTAssertTrue(String(describing: error).contains("unsupported_ssh_config_for_keyscan"))
        }

        XCTAssertEqual(commands.map(\.0), ["/usr/bin/ssh"])
        XCTAssertFalse(FileManager.default.fileExists(atPath: knownHosts.path))
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyAcceptsNonED25519Key() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        defer { try? FileManager.default.removeItem(at: root) }

        try await Installer.trustPrivateDirectMeshBootstrapHostKey(
            sshHost: "linux-b.tailnet",
            user: "jesse",
                port: 22,
            regularKnownHosts: knownHosts.path,
            runCommand: { _, _ in
                "linux-b.tailnet ssh-rsa AAAATEST_RSA\n"
            }
        )

        XCTAssertEqual(
            try String(contentsOf: knownHosts, encoding: .utf8),
            "linux-b.tailnet ssh-rsa AAAATEST_RSA\n"
        )
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyRejectsNoMatchingHostKey() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        defer { try? FileManager.default.removeItem(at: root) }

        do {
            try await Installer.trustPrivateDirectMeshBootstrapHostKey(
                sshHost: "linux-b.tailnet",
                user: "jesse",
                port: 22,
                regularKnownHosts: knownHosts.path,
                runCommand: { _, _ in
                    "other-host.tailnet ssh-rsa AAAATEST_RSA\n"
                }
            )
            XCTFail("expected missing matching keyscan output to fail")
        } catch {
            XCTAssertTrue(String(describing: error).contains("ssh_keyscan_no_host_key"))
        }
        XCTAssertFalse(FileManager.default.fileExists(atPath: knownHosts.path))
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyRejectsConflictingED25519Entry() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try "100.114.54.38 ssh-ed25519 AAAAOLD_KEY\n"
            .write(to: knownHosts, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: root) }

        var commands: [(String, [String])] = []
        do {
            try await Installer.trustPrivateDirectMeshBootstrapHostKey(
                sshHost: "100.114.54.38",
                user: "jesse",
                port: 22,
                regularKnownHosts: knownHosts.path,
                runCommand: { exe, args in
                    commands.append((exe, args))
                    if exe == "/usr/bin/ssh-keygen" {
                        return """
                        # Host 100.114.54.38 found: line 1
                        |1|HASHED_HOST|HASHED_SALT ssh-ed25519 AAAAOLD_KEY
                        """
                    }
                    return "100.114.54.38 ssh-ed25519 AAAANEW_KEY\n"
                }
            )
            XCTFail("expected conflicting ED25519 known_hosts entry to fail")
        } catch {
            XCTAssertTrue(String(describing: error).contains("ssh_known_hosts_conflict"))
        }
        XCTAssertEqual(commands.map(\.0), ["/usr/bin/ssh", "/usr/bin/ssh-keyscan", "/usr/bin/ssh-keygen"])
        XCTAssertEqual(commands[2].1, ["-F", "100.114.54.38", "-f", knownHosts.path])
        XCTAssertEqual(
            try String(contentsOf: knownHosts, encoding: .utf8),
            "100.114.54.38 ssh-ed25519 AAAAOLD_KEY\n"
        )
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyTreatsNonDefaultPortAsDistinctHostToken() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try "linux-b.tailnet ssh-ed25519 AAAADEFAULT_PORT_KEY\n"
            .write(to: knownHosts, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: root) }

        try await Installer.trustPrivateDirectMeshBootstrapHostKey(
            sshHost: "linux-b.tailnet",
            user: "jesse",
                port: 2200,
            regularKnownHosts: knownHosts.path,
            runCommand: { exe, _ in
                if exe == "/usr/bin/ssh-keygen" {
                    throw InstallCommandFailure(executable: exe,
                                                arguments: [],
                                                exitStatus: 1,
                                                stdout: "",
                                                stderr: "")
                }
                return "[linux-b.tailnet]:2200 ssh-ed25519 AAAANONDEFAULT_PORT_KEY\n"
            }
        )

        XCTAssertEqual(
            try String(contentsOf: knownHosts, encoding: .utf8),
            "linux-b.tailnet ssh-ed25519 AAAADEFAULT_PORT_KEY\n[linux-b.tailnet]:2200 ssh-ed25519 AAAANONDEFAULT_PORT_KEY\n"
        )
    }

    func testTrustPrivateDirectMeshBootstrapHostKeyDoesNotOverwriteMalformedKnownHostsFile() async throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-known-hosts-test-\(UUID().uuidString)")
        let knownHosts = root.appendingPathComponent("known_hosts")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        let malformed = Data([0xff, 0xfe, 0xfd])
        try malformed.write(to: knownHosts)
        defer { try? FileManager.default.removeItem(at: root) }

        do {
            try await Installer.trustPrivateDirectMeshBootstrapHostKey(
                sshHost: "linux-b.tailnet",
                user: "jesse",
                port: 22,
                regularKnownHosts: knownHosts.path,
                runCommand: { _, _ in
                    "linux-b.tailnet ssh-ed25519 AAAATEST_ED25519\n"
                }
            )
            XCTFail("expected malformed known_hosts file to fail")
        } catch {
            XCTAssertFalse(String(describing: error).contains("ssh_keyscan_no_host_key"))
        }
        XCTAssertEqual(try Data(contentsOf: knownHosts), malformed)
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

    func testUploadAndInstallPrivateDirectMeshHostRunsSetupCommand() async throws {
        let stage = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-direct-install-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: stage) }

        var commands: [(String, [String])] = []
        try await Installer.uploadAndInstallPrivateDirectMeshHost(
            target: "jesse@linux-b.tailnet",
            sshArgs: ["-o", "ConnectTimeout=5"],
            scpArgs: ["-q"],
            stage: stage,
            stagedFiles: ["clipfan-linux-arm64", "install.sh", "clipfan.service", "tmux.conf.snippet", "config.json"],
            configPath: "/home/jesse/Application Support/Clipfan/config.json",
            installPath: "/home/jesse/.local/bin/clipfan",
            withTmux: true,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh", args.last == Installer.remoteStageCommand() {
                    return "/tmp/clipfan-install.ABC123\n"
                }
                return ""
            }
        )

        XCTAssertEqual(commands.count, 3)
        XCTAssertEqual(commands[0].0, "/usr/bin/ssh")
        XCTAssertEqual(commands[0].1.last, Installer.remoteStageCommand())
        XCTAssertEqual(commands[1].0, "/usr/bin/scp")
        XCTAssertEqual(commands[1].1.last, "jesse@linux-b.tailnet:/tmp/clipfan-install.ABC123/")
        XCTAssertEqual(commands[2].0, "/usr/bin/ssh")
        XCTAssertEqual(commands[2].1.last, Installer.privateDirectMeshInstallCommand(
            stage: "/tmp/clipfan-install.ABC123",
            configPath: "/home/jesse/Application Support/Clipfan/config.json",
            installPath: "/home/jesse/.local/bin/clipfan",
            withTmux: true
        ))
    }

    func testUploadAndInstallPrivateDirectMeshHostBracketsIPv6SCPDestination() async throws {
        let stage = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-direct-install-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: stage) }

        var commands: [(String, [String])] = []
        try await Installer.uploadAndInstallPrivateDirectMeshHost(
            target: "jesse@fd7a:115c:a1e0::1234",
            sshArgs: ["-o", "ConnectTimeout=5"],
            scpArgs: ["-q"],
            stage: stage,
            stagedFiles: ["clipfan-linux-arm64", "install.sh"],
            configPath: "/home/jesse/.config/clipfan/config.json",
            installPath: "/home/jesse/.local/bin/clipfan",
            withTmux: false,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh", args.last == Installer.remoteStageCommand() {
                    return "/tmp/clipfan-install.ABC123\n"
                }
                return ""
            }
        )

        let scpCommand = try XCTUnwrap(commands.first { $0.0 == "/usr/bin/scp" })
        XCTAssertEqual(scpCommand.1.last, "jesse@[fd7a:115c:a1e0::1234]:/tmp/clipfan-install.ABC123/")
    }

    func testProvisionPrivateDirectMeshBuildsHiddenCLIArgv() async throws {
        var commands: [(String, [String])] = []
        var localRestarts = 0
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-provision-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        try await Installer.provisionPrivateDirectMesh(
            hostSpecs: [
                " id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519 ",
                "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
            ],
            regularKnownHosts: regularKnownHosts,
            trustKeyscan: true,
            localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
            bootstrapInstall: false,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh-keyscan" {
                    return self.keyscanFixtureOutput(args)
                }
                return #"{"status":"ok"}"#
            },
            readLocalHostID: { nil },
            restartLocalDaemon: {
                localRestarts += 1
            },
            onProgress: { _ in }
        )

        XCTAssertEqual(commands.filter { $0.0 == "/usr/bin/ssh-keyscan" }.map { $0.1.last }, ["mac-a.tailnet", "linux-b.tailnet"])
        let command = try XCTUnwrap(commands.first { $0.0 == "/Users/jesse/.local/bin/clipfan" })
        XCTAssertEqual(command.0, "/Users/jesse/.local/bin/clipfan")
        XCTAssertEqual(Array(command.1[0...3]), ["ssh-provision-direct", "--trust-keyscan", "--regular-known-hosts", regularKnownHosts])
        XCTAssertEqual(command.1.filter { $0 == "--host" }.count, 2)
        XCTAssertTrue(command.1.contains("id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519"))

        let restartCommands = commands.filter { exe, args in exe == "/usr/bin/ssh" && args.first != "-G" }
        XCTAssertEqual(restartCommands.count, 2)
        XCTAssertEqual(restartCommands[0].1, [
            "-o", "BatchMode=yes",
            "-o", "StrictHostKeyChecking=yes",
            "-o", "UserKnownHostsFile=\(regularKnownHosts)",
            "-o", "GlobalKnownHostsFile=/dev/null",
            "-o", "PermitLocalCommand=no",
            "-o", "RequestTTY=no",
            "-o", "ClearAllForwardings=yes",
            "-o", "LogLevel=ERROR",
            "jesse@mac-a.tailnet",
            Installer.remoteRestartDaemonCommand(installPath: "/Users/jesse/.local/bin/clipfan")
        ])
        XCTAssertEqual(restartCommands[1].1, [
            "-o", "BatchMode=yes",
            "-o", "StrictHostKeyChecking=yes",
            "-o", "UserKnownHostsFile=\(regularKnownHosts)",
            "-o", "GlobalKnownHostsFile=/dev/null",
            "-o", "PermitLocalCommand=no",
            "-o", "RequestTTY=no",
            "-o", "ClearAllForwardings=yes",
            "-o", "LogLevel=ERROR",
            "jesse@linux-b.tailnet",
            Installer.remoteRestartDaemonCommand(installPath: "/home/jesse/.local/bin/clipfan")
        ])
        XCTAssertEqual(localRestarts, 1)
    }

    private func provisionAndHealFixtureSpecs() -> [String] {
        [
            "id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
            "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
        ]
    }

    func testProvisionAndHealRunsProvisionThenHealsFullMesh() async throws {
        var commands: [(String, [String])] = []
        var healedKnownHosts: String?
        var healRanAfterProvision = false
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-heal-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        let report = try await Installer.provisionPrivateDirectMeshAndHeal(
            hostSpecs: provisionAndHealFixtureSpecs(),
            regularKnownHosts: regularKnownHosts,
            trustKeyscan: true,
            localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
            bootstrapInstall: false,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh-keyscan" { return self.keyscanFixtureOutput(args) }
                return #"{"status":"ok"}"#
            },
            meshHeal: { knownHosts in
                healedKnownHosts = knownHosts
                healRanAfterProvision = commands.contains { $0.0 == "/Users/jesse/.local/bin/clipfan" && $0.1.first == "ssh-provision-direct" }
                return MeshHealReport(healed: ["mac-a<->linux-b"], restarted: ["mac-a"])
            },
            readLocalHostID: { nil },
            restartLocalDaemon: { },
            onProgress: { _ in }
        )

        XCTAssertTrue(healRanAfterProvision, "mesh-heal must run after ssh-provision-direct")
        XCTAssertEqual(healedKnownHosts, regularKnownHosts)
        XCTAssertEqual(report?.healed, ["mac-a<->linux-b"])
        XCTAssertEqual(report?.restarted, ["mac-a"])
    }

    func testProvisionAndHealIsNonFatalWhenHealFails() async throws {
        var provisioned = false
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-heal-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        let report = try await Installer.provisionPrivateDirectMeshAndHeal(
            hostSpecs: provisionAndHealFixtureSpecs(),
            regularKnownHosts: regularKnownHosts,
            trustKeyscan: true,
            localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
            bootstrapInstall: false,
            runCommand: { exe, args in
                if exe == "/usr/bin/ssh-keyscan" { return self.keyscanFixtureOutput(args) }
                if exe == "/Users/jesse/.local/bin/clipfan", args.first == "ssh-provision-direct" { provisioned = true }
                return #"{"status":"ok"}"#
            },
            meshHeal: { _ in throw InstallError.configIO("mesh_heal_boom") },
            readLocalHostID: { nil },
            restartLocalDaemon: { },
            onProgress: { _ in }
        )

        XCTAssertTrue(provisioned, "the explicit pair must still be provisioned")
        XCTAssertNil(report, "a heal failure is non-fatal and yields no report")
    }

    func testProvisionPrivateDirectMeshUsesRemoteObservedCallbackHostForLocalSpec() async throws {
        var commands: [(String, [String])] = []
        var localRestarts = 0
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-provision-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        try await Installer.provisionPrivateDirectMesh(
            hostSpecs: [
                "id=mac-a,ssh=mac-a.local,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519,callback_host=remote_observed",
                "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
            ],
            regularKnownHosts: regularKnownHosts,
            trustKeyscan: true,
            localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
            bootstrapInstall: false,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh-keyscan" {
                    return self.keyscanFixtureOutput(args)
                }
                if exe == "/usr/bin/ssh", args.last == Installer.privateDirectMeshObservedSSHClientHostCommand() {
                    return "100.64.10.20\n"
                }
                return #"{"status":"ok"}"#
            },
            readLocalHostID: { "mac-a" },
            restartLocalDaemon: {
                localRestarts += 1
            },
            onProgress: { _ in }
        )

        let observedCommand = try XCTUnwrap(commands.first { exe, args in
            exe == "/usr/bin/ssh" && args.last == Installer.privateDirectMeshObservedSSHClientHostCommand()
        })
        XCTAssertEqual(observedCommand.1.dropLast(), [
            "-o", "BatchMode=yes",
            "-o", "StrictHostKeyChecking=yes",
            "-o", "UserKnownHostsFile=\(regularKnownHosts)",
            "-o", "GlobalKnownHostsFile=/dev/null",
            "-o", "PermitLocalCommand=no",
            "-o", "RequestTTY=no",
            "-o", "ClearAllForwardings=yes",
            "-o", "LogLevel=ERROR",
            "jesse@linux-b.tailnet"
        ])
        XCTAssertEqual(commands.filter { $0.0 == "/usr/bin/ssh-keyscan" }.map { $0.1.last },
                       ["linux-b.tailnet", "100.64.10.20"])
        let command = try XCTUnwrap(commands.first { $0.0 == "/Users/jesse/.local/bin/clipfan" })
        XCTAssertTrue(command.1.contains("id=mac-a,ssh=100.64.10.20,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519,callback_host=remote_observed"))
        XCTAssertFalse(command.1.contains { $0.contains("ssh=mac-a.local") })
        XCTAssertEqual(localRestarts, 1)
    }

    func testProvisionPrivateDirectMeshKeepsManualCallbackHostOverride() async throws {
        var commands: [(String, [String])] = []
        var localRestarts = 0
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-provision-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        try await Installer.provisionPrivateDirectMesh(
            hostSpecs: [
                "id=mac-a,ssh=mac-override.example,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519,callback_host=manual",
                "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
            ],
            regularKnownHosts: regularKnownHosts,
            trustKeyscan: true,
            localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
            bootstrapInstall: false,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh-keyscan" {
                    return self.keyscanFixtureOutput(args)
                }
                return #"{"status":"ok"}"#
            },
            readLocalHostID: { "mac-a" },
            restartLocalDaemon: {
                localRestarts += 1
            },
            onProgress: { _ in }
        )

        XCTAssertFalse(commands.contains { exe, args in
            exe == "/usr/bin/ssh" && args.last == Installer.privateDirectMeshObservedSSHClientHostCommand()
        })
        XCTAssertEqual(commands.filter { $0.0 == "/usr/bin/ssh-keyscan" }.map { $0.1.last },
                       ["mac-override.example", "linux-b.tailnet"])
        let command = try XCTUnwrap(commands.first { $0.0 == "/Users/jesse/.local/bin/clipfan" })
        XCTAssertTrue(command.1.contains("id=mac-a,ssh=mac-override.example,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519,callback_host=manual"))
        XCTAssertEqual(localRestarts, 1)
    }

    func testProvisionPrivateDirectMeshRejectsInvalidRemoteObservedCallbackHost() async throws {
        var commands: [(String, [String])] = []
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-provision-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        do {
            try await Installer.provisionPrivateDirectMesh(
                hostSpecs: [
                    "id=mac-a,ssh=mac-a.local,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519,callback_host=remote_observed",
                    "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
                ],
                regularKnownHosts: regularKnownHosts,
                trustKeyscan: true,
                localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
                bootstrapInstall: false,
                runCommand: { exe, args in
                    commands.append((exe, args))
                    if exe == "/usr/bin/ssh-keyscan" {
                        return self.keyscanFixtureOutput(args)
                    }
                    if exe == "/usr/bin/ssh", args.last == Installer.privateDirectMeshObservedSSHClientHostCommand() {
                        return "100.64.10.20\nextra\n"
                    }
                    return #"{"status":"ok"}"#
                },
                readLocalHostID: { "mac-a" },
                onProgress: { _ in }
            )
            XCTFail("expected invalid remote observed callback host")
        } catch {
            XCTAssertTrue(String(describing: error).contains("invalid_remote_observed_callback_host"))
        }

        XCTAssertFalse(commands.contains { $0.0 == "/Users/jesse/.local/bin/clipfan" })
    }

    func testProvisionPrivateDirectMeshAcceptsIPv6RemoteObservedCallbackHost() async throws {
        var commands: [(String, [String])] = []
        let observedIPv6 = "fd7a:115c:a1e0::1234"
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-provision-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        try await Installer.provisionPrivateDirectMesh(
            hostSpecs: [
                "id=mac-a,ssh=mac-a.local,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519,callback_host=remote_observed",
                "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
            ],
            regularKnownHosts: regularKnownHosts,
            trustKeyscan: true,
            localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
            bootstrapInstall: false,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh-keyscan" {
                    return self.keyscanFixtureOutput(args)
                }
                if exe == "/usr/bin/ssh", args.last == Installer.privateDirectMeshObservedSSHClientHostCommand() {
                    return "\(observedIPv6)\n"
                }
                return #"{"status":"ok"}"#
            },
            readLocalHostID: { "mac-a" },
            restartLocalDaemon: {},
            onProgress: { _ in }
        )

        XCTAssertEqual(commands.filter { $0.0 == "/usr/bin/ssh-keyscan" }.map { $0.1.last },
                       ["linux-b.tailnet", observedIPv6])
        let command = try XCTUnwrap(commands.first { $0.0 == "/Users/jesse/.local/bin/clipfan" })
        XCTAssertTrue(command.1.contains("id=mac-a,ssh=\(observedIPv6),user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519,callback_host=remote_observed"))
    }

    func testProvisionPrivateDirectMeshRejectsMultipleRemoteObservedCallbackHostsBeforeSSH() async throws {
        var called = false

        do {
            try await Installer.provisionPrivateDirectMesh(
                hostSpecs: [
                    "id=mac-a,ssh=mac-a.local,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519,callback_host=remote_observed",
                    "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519,callback_host=remote_observed"
                ],
                regularKnownHosts: "/Users/jesse/.ssh/known_hosts",
                trustKeyscan: true,
                localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
                bootstrapInstall: false,
                runCommand: { _, _ in
                    called = true
                    return ""
                },
                readLocalHostID: { "mac-a" },
                onProgress: { _ in }
            )
            XCTFail("expected multiple remote observed callback hosts")
        } catch {
            XCTAssertTrue(String(describing: error).contains("multiple_remote_observed_callback_hosts"))
        }

        XCTAssertFalse(called)
    }

    func testProvisionPrivateDirectMeshBootstrapsNonLocalHostsBeforeProvision() async throws {
        var commands: [(String, [String])] = []
        var bootstraps: [(String, Bool)] = []
        var localRestarts = 0
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-provision-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        try await Installer.provisionPrivateDirectMesh(
            hostSpecs: [
                "id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
                "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/Application Support/Clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
            ],
            regularKnownHosts: regularKnownHosts,
            trustKeyscan: true,
            withTmux: true,
            localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
            bootstrapRemoteHost: { hostID, withTmux in
                bootstraps.append((hostID, withTmux))
            },
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh-keyscan" {
                    return self.keyscanFixtureOutput(args)
                }
                return #"{"status":"ok"}"#
            },
            readLocalHostID: { "mac-a" },
            restartLocalDaemon: {
                localRestarts += 1
            },
            onProgress: { _ in }
        )

        XCTAssertEqual(bootstraps.count, 1)
        XCTAssertEqual(bootstraps.first?.0, "linux-b")
        XCTAssertEqual(bootstraps.first?.1, true)
        XCTAssertEqual(commands.filter { $0.0 == "/usr/bin/ssh-keyscan" }.map { $0.1.last }, ["mac-a.tailnet", "linux-b.tailnet"])
        XCTAssertEqual(commands.filter { $0.0 == "/Users/jesse/.local/bin/clipfan" }.count, 1)
        let restartCommand = try XCTUnwrap(commands.first { exe, args in exe == "/usr/bin/ssh" && args.first != "-G" })
        XCTAssertEqual(restartCommand.1.last, Installer.remoteRestartDaemonCommand(installPath: "/home/jesse/.local/bin/clipfan"))
        let knownHostsBody = try String(contentsOfFile: regularKnownHosts, encoding: .utf8)
        XCTAssertTrue(knownHostsBody.contains("mac-a.tailnet ssh-ed25519"))
        XCTAssertTrue(knownHostsBody.contains("linux-b.tailnet ssh-ed25519"))
        XCTAssertEqual(localRestarts, 1)
    }

    func testProvisionPrivateDirectMeshSkipsRemoteRestartForLocalHostID() async throws {
        var commands: [(String, [String])] = []
        var localRestarts = 0
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-provision-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        try await Installer.provisionPrivateDirectMesh(
            hostSpecs: [
                "id=mac-a,ssh=mac-a.tailnet,user=jesse,port=2222,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
                "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
            ],
            regularKnownHosts: regularKnownHosts,
            trustKeyscan: true,
            localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
            bootstrapInstall: false,
            runCommand: { exe, args in
                commands.append((exe, args))
                if exe == "/usr/bin/ssh-keyscan" {
                    return self.keyscanFixtureOutput(args)
                }
                return ""
            },
            readLocalHostID: { "mac-a" },
            restartLocalDaemon: {
                localRestarts += 1
            },
            onProgress: { _ in }
        )

        XCTAssertEqual(commands.filter { $0.0 == "/usr/bin/ssh-keyscan" }.map { $0.1.last }, ["mac-a.tailnet", "linux-b.tailnet"])
        XCTAssertEqual(commands.filter { $0.0 == "/Users/jesse/.local/bin/clipfan" }.count, 1)
        let restartCommand = try XCTUnwrap(commands.first { exe, args in exe == "/usr/bin/ssh" && args.first != "-G" })
        XCTAssertEqual(restartCommand.1, [
            "-o", "BatchMode=yes",
            "-o", "StrictHostKeyChecking=yes",
            "-o", "UserKnownHostsFile=\(regularKnownHosts)",
            "-o", "GlobalKnownHostsFile=/dev/null",
            "-o", "PermitLocalCommand=no",
            "-o", "RequestTTY=no",
            "-o", "ClearAllForwardings=yes",
            "-o", "LogLevel=ERROR",
            "jesse@linux-b.tailnet",
            Installer.remoteRestartDaemonCommand(installPath: "/home/jesse/.local/bin/clipfan")
        ])
        XCTAssertFalse(commands.contains { _, args in args.contains("jesse@mac-a.tailnet") })
        XCTAssertEqual(localRestarts, 1)
    }

    func testProvisionPrivateDirectMeshRestartsLocalDaemonBeforePropagatingRemoteRestartFailure() async throws {
        enum RestartError: Error {
            case failed
        }
        var commands: [(String, [String])] = []
        var localRestarts = 0
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-provision-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        do {
            try await Installer.provisionPrivateDirectMesh(
                hostSpecs: [
                    "id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
                    "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
                ],
                regularKnownHosts: regularKnownHosts,
                trustKeyscan: true,
                localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
                bootstrapInstall: false,
                runCommand: { exe, args in
                    commands.append((exe, args))
                    if exe == "/usr/bin/ssh-keyscan" {
                        return self.keyscanFixtureOutput(args)
                    }
                    if exe == "/usr/bin/ssh", args.first != "-G" {
                        throw RestartError.failed
                    }
                    return ""
                },
                readLocalHostID: { nil },
                restartLocalDaemon: {
                    localRestarts += 1
                },
                onProgress: { _ in }
            )
            XCTFail("expected remote restart failure")
        } catch RestartError.failed {
        }

        XCTAssertEqual(commands.filter { $0.0 == "/usr/bin/ssh-keyscan" }.count, 2)
        XCTAssertEqual(commands.filter { exe, args in exe == "/usr/bin/ssh" && args.first != "-G" }.count, 2)
        XCTAssertEqual(localRestarts, 1)
    }

    func testProvisionPrivateDirectMeshPropagatesLocalRestartFailureAfterRemoteRestarts() async throws {
        enum RestartError: Error {
            case localFailed
        }
        var commands: [(String, [String])] = []
        var localRestarts = 0
        let knownHostsRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("clipfan-provision-known-hosts-\(UUID().uuidString)")
        let regularKnownHosts = knownHostsRoot.appendingPathComponent("known_hosts").path
        defer { try? FileManager.default.removeItem(at: knownHostsRoot) }

        do {
            try await Installer.provisionPrivateDirectMesh(
                hostSpecs: [
                    "id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
                    "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
                ],
                regularKnownHosts: regularKnownHosts,
                trustKeyscan: true,
                localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
                bootstrapInstall: false,
                runCommand: { exe, args in
                    commands.append((exe, args))
                    if exe == "/usr/bin/ssh-keyscan" {
                        return self.keyscanFixtureOutput(args)
                    }
                    return ""
                },
                readLocalHostID: { nil },
                restartLocalDaemon: {
                    localRestarts += 1
                    throw RestartError.localFailed
                },
                onProgress: { _ in }
            )
            XCTFail("expected local restart failure")
        } catch RestartError.localFailed {
        }

        XCTAssertEqual(commands.filter { $0.0 == "/usr/bin/ssh-keyscan" }.count, 2)
        XCTAssertEqual(commands.filter { exe, args in exe == "/usr/bin/ssh" && args.first != "-G" }.count, 2)
        XCTAssertEqual(localRestarts, 1)
    }

    func testRemoteRestartDaemonCommandUsesServiceManagersThenInstallPathFallback() {
        let command = Installer.remoteRestartDaemonCommand(installPath: "/Users/jesse/App's/bin/clipfan")

        XCTAssertTrue(command.contains("systemctl --user daemon-reload"))
        XCTAssertTrue(command.contains("systemctl --user enable clipfan.service"))
        XCTAssertTrue(command.contains("systemctl --user restart clipfan.service"))
        XCTAssertTrue(command.contains("plist=\"$HOME/Library/LaunchAgents/com.primeradiant.clipfan.plist\""))
        XCTAssertTrue(command.contains("launchctl enable \"gui/$user_uid/com.primeradiant.clipfan\""))
        XCTAssertTrue(command.contains("launchctl bootstrap \"gui/$user_uid\" \"$plist\""))
        XCTAssertTrue(command.contains("launchctl load \"$plist\""))
        XCTAssertTrue(command.contains("launchctl kickstart -k"))
        XCTAssertTrue(command.contains("nohup '/Users/jesse/App'\"'\"'s/bin/clipfan' daemon"))
    }

    func testProvisionPrivateDirectMeshFailsClosedBeforeCommandWithoutTrustKeyscan() async throws {
        var called = false

        do {
            try await Installer.provisionPrivateDirectMesh(
                hostSpecs: ["id=a", "id=b"],
                regularKnownHosts: "/Users/jesse/.ssh/known_hosts",
                trustKeyscan: false,
                localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
                runCommand: { _, _ in
                    called = true
                    return ""
                },
                onProgress: { _ in }
            )
            XCTFail("expected trust_keyscan_required")
        } catch {
            XCTAssertTrue(String(describing: error).contains("trust_keyscan_required"))
        }

        XCTAssertFalse(called)
    }

    func testProvisionPrivateDirectMeshRejectsUnsupportedFreshInstallPathBeforeCommand() async throws {
        var called = false

        do {
            try await Installer.provisionPrivateDirectMesh(
                hostSpecs: [
                    "id=mac-a,ssh=mac-a.tailnet,user=jesse,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
                    "id=linux-b,ssh=linux-b.tailnet,user=jesse,install=/home/jesse/.local/bin/clipfan-custom,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
                ],
                regularKnownHosts: "/Users/jesse/.ssh/known_hosts",
                trustKeyscan: true,
                localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
                runCommand: { _, _ in
                    called = true
                    return ""
                },
                onProgress: { _ in }
            )
            XCTFail("expected unsupported install basename")
        } catch {
            XCTAssertTrue(String(describing: error).contains("unsupported_private_direct_mesh_install_basename"))
        }

        XCTAssertFalse(called)
    }

    func testProvisionPrivateDirectMeshRejectsDistinctGatewayBeforeCommand() async throws {
        var called = false

        do {
            try await Installer.provisionPrivateDirectMesh(
                hostSpecs: [
                    "id=mac-a,ssh=mac-a.tailnet,user=jesse,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
                    "id=linux-b,ssh=linux-b.tailnet,user=jesse,install=/home/jesse/.local/bin/clipfan,gateway=/home/jesse/bin/clipfan-gateway,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
                ],
                regularKnownHosts: "/Users/jesse/.ssh/known_hosts",
                trustKeyscan: true,
                localProvisioningBinary: { "/Users/jesse/.local/bin/clipfan" },
                runCommand: { _, _ in
                    called = true
                    return ""
                },
                onProgress: { _ in }
            )
            XCTFail("expected unsupported gateway path")
        } catch {
            XCTAssertTrue(String(describing: error).contains("unsupported_private_direct_mesh_gateway_path"))
        }

        XCTAssertFalse(called)
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

    private func writeVersionFixture(_ url: URL, version: String) throws -> URL {
        try writeExecutableScript(url, """
        #!/usr/bin/env bash
        if [[ "$1" == "version" ]]; then
          echo "\(version)"
          exit 0
        fi
        exit 2
        """)
        return url
    }

    private func keyscanFixtureOutput(_ args: [String]) -> String {
        let host = args.last ?? "host"
        let port: String
        if let index = args.firstIndex(of: "-p"), args.indices.contains(args.index(after: index)) {
            port = args[args.index(after: index)]
        } else {
            port = "22"
        }
        let hostToken = port == "22" ? host : "[\(host)]:\(port)"
        let safeKeySuffix = "\(host)-\(port)"
            .map { $0.isLetter || $0.isNumber ? $0 : "_" }
            .map(String.init)
            .joined()
        return "\(hostToken) ssh-ed25519 AAAATEST_\(safeKeySuffix)\n"
    }

    private func runBash(_ command: String, environment: [String: String]) throws -> ShellResult {
        try runShell("/bin/bash", command, environment: environment)
    }

    private func runShell(_ shell: String, _ command: String, environment: [String: String]) throws -> ShellResult {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: shell)
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
