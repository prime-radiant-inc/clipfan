import XCTest
@testable import Clipfan

final class SSHTransportGatePolicyTests: XCTestCase {
    func testCurrentAddPeerProvisioningIsDisabledForGeneratedGates() {
        XCTAssertFalse(SSHTransportGatePolicy.current.addPeerProvisioningEnabled)
    }

    func testCurrentPrivateDirectMeshCanBeEnabledWhilePublicAddPeerStaysDisabled() {
        XCTAssertTrue(SSHTransportGatePolicy.current.privateDirectMeshProvisioningEnabled)
        XCTAssertFalse(SSHTransportGatePolicy.current.addPeerProvisioningEnabled)
    }

    func testRegularSSHUpdateIsEnabled() {
        XCTAssertTrue(SSHTransportGatePolicy.current.regularSSHUpdateEnabled)
    }

    func testAddPeerProvisioningRequiresEveryTransportAndRuntimeGate() {
        let enabledPolicy = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: true,
            publicAddPeerSuccessEnabled: true,
            receivePrimitiveEnabled: true,
            syncStreamEnabled: true,
            persistentCurrentEnabled: true,
            syncKeyRotationEnabled: true
        )

        XCTAssertTrue(enabledPolicy.addPeerProvisioningEnabled)

        let requiredGates: [(String, WritableKeyPath<SSHTransportGatePolicy, Bool>)] = [
            ("peerHTTPRuntimeDisabled", \.peerHTTPRuntimeDisabled),
            ("configV2WriteEnabled", \.configV2WriteEnabled),
            ("remoteSecretWriteReleaseEnabled", \.remoteSecretWriteReleaseEnabled),
            ("publicAddPeerSuccessEnabled", \.publicAddPeerSuccessEnabled),
            ("receivePrimitiveEnabled", \.receivePrimitiveEnabled),
            ("syncStreamEnabled", \.syncStreamEnabled),
            ("persistentCurrentEnabled", \.persistentCurrentEnabled),
            ("syncKeyRotationEnabled", \.syncKeyRotationEnabled),
        ]

        for (gateName, keyPath) in requiredGates {
            var policy = enabledPolicy
            policy[keyPath: keyPath] = false

            XCTAssertFalse(policy.addPeerProvisioningEnabled, "\(gateName) must be required")
        }
    }

    func testPeerHTTPVersionProbeFollowsRuntimeDisableGate() {
        XCTAssertTrue(SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: false,
            configV2WriteEnabled: false,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        ).peerHTTPVersionProbeEnabled)

        XCTAssertFalse(SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: false,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        ).peerHTTPVersionProbeEnabled)
    }

    func testAddPeerInstallButtonDisabledUntilPublicProvisioningGateIsReady() {
        let disabled = SSHTransportGatePolicy.current
        XCTAssertTrue(isAddPeerInstallDisabled(installCount: 1, installing: false, policy: disabled))

        let enabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: true,
            publicAddPeerSuccessEnabled: true,
            receivePrimitiveEnabled: true,
            syncStreamEnabled: true,
            persistentCurrentEnabled: true,
            syncKeyRotationEnabled: true
        )
        XCTAssertFalse(isAddPeerInstallDisabled(installCount: 1, installing: false, policy: enabled))
    }

    func testPrivateDirectMeshInstallButtonUsesPrivateGateAndTrustKeyscan() {
        let privateEnabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: true,
            syncStreamEnabled: true,
            persistentCurrentEnabled: true,
            syncKeyRotationEnabled: false
        )

        XCTAssertTrue(isAddPeerInstallDisabled(installCount: 2,
                                               installing: false,
                                               policy: privateEnabled,
                                               privateDirectMeshRequested: true,
                                               trustKeyscan: false))
        XCTAssertFalse(isAddPeerInstallDisabled(installCount: 2,
                                                installing: false,
                                                policy: privateEnabled,
                                                privateDirectMeshRequested: true,
                                                trustKeyscan: true))
        XCTAssertTrue(isAddPeerInstallDisabled(installCount: 1,
                                               installing: false,
                                               policy: privateEnabled,
                                               privateDirectMeshRequested: true,
                                               trustKeyscan: true))
    }

    func testPrivateDirectMeshInstallButtonAllowsOneRemotePlusLocalSpec() {
        let privateEnabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: true,
            syncStreamEnabled: true,
            persistentCurrentEnabled: true,
            syncKeyRotationEnabled: false
        )

        XCTAssertFalse(isAddPeerInstallDisabled(installCount: 2,
                                                installing: false,
                                                policy: privateEnabled,
                                                privateDirectMeshRequested: true,
                                                trustKeyscan: true))
    }

    func testAddPeerDerivedHostIDUsesShortSafeHostName() {
        XCTAssertEqual(addPeerDerivedHostID(from: "linux-b.tailnet.example."), "linux-b")
        XCTAssertEqual(addPeerDerivedHostID(from: "weird host.local"), "weird-host")
        XCTAssertEqual(addPeerDerivedHostID(from: "192.168.1.42"), "192-168-1-42")
    }

    func testAddPeerRemoteDirectMeshSpecUsesPlatformDefaults() {
        let linux = AddPeerRemoteHostDraft(
            sshHost: "linux-b.tailnet",
            hostID: "",
            user: "jesse",
            port: 2200,
            platform: .linux
        )
        let mac = AddPeerRemoteHostDraft(
            sshHost: "mac-b.tailnet",
            hostID: "mac-b",
            user: "jesse",
            port: 22,
            platform: .macOS
        )

        XCTAssertEqual(
            addPeerRemoteDirectMeshSpec(linux),
            "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=2200,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519"
        )
        XCTAssertEqual(
            addPeerRemoteDirectMeshSpec(mac),
            "id=mac-b,ssh=mac-b.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519"
        )
    }

    func testAddPeerPreferredTailnetSSHHostUsesDNSThenIPThenHostName() {
        XCTAssertEqual(addPeerPreferredTailnetSSHHost(TailscalePeer(
            hostName: "m4",
            dnsName: "m4.tailnet.example.",
            ip: "100.64.0.1",
            os: "darwin",
            online: true,
            user: "uid-1"
        )), "m4.tailnet.example")
        XCTAssertEqual(addPeerPreferredTailnetSSHHost(TailscalePeer(
            hostName: "m4",
            dnsName: "",
            ip: "100.64.0.1",
            os: "darwin",
            online: true,
            user: "uid-1"
        )), "100.64.0.1")
        XCTAssertEqual(addPeerPreferredTailnetSSHHost(TailscalePeer(
            hostName: "m4",
            dnsName: "",
            ip: "",
            os: "darwin",
            online: true,
            user: "uid-1"
        )), "m4")
    }

    func testAddPeerPreferredLocalTailnetSSHHostRequiresRoutableTailnetAddress() {
        XCTAssertEqual(addPeerPreferredLocalTailnetSSHHost(TailscalePeer(
            hostName: "m4",
            dnsName: "m4.tailnet.example.",
            ip: "100.64.0.1",
            os: "darwin",
            online: true,
            user: "uid-1"
        )), "m4.tailnet.example")
        XCTAssertEqual(addPeerPreferredLocalTailnetSSHHost(TailscalePeer(
            hostName: "m4",
            dnsName: "",
            ip: "100.64.0.1",
            os: "darwin",
            online: true,
            user: "uid-1"
        )), "100.64.0.1")
        XCTAssertEqual(addPeerPreferredLocalTailnetSSHHost(TailscalePeer(
            hostName: "m4",
            dnsName: "",
            ip: "",
            os: "darwin",
            online: true,
            user: "uid-1"
        )), "")
    }

    func testAddPeerDefaultLocalSSHHostDoesNotFallbackToShortLocalNames() {
        XCTAssertEqual(
            addPeerDefaultLocalSSHHost(tailnetSSHHost: " jesse-paradise-park.trout-rigel.ts.net "),
            "jesse-paradise-park.trout-rigel.ts.net"
        )
        XCTAssertEqual(
            addPeerDefaultLocalSSHHost(tailnetSSHHost: ""),
            ""
        )
    }

    func testTailscaleStatusSnapshotParsesSelfAndPeers() throws {
        let json = """
        {
          "Self": {
            "HostName": "m4",
            "DNSName": "m4.tailnet.example.",
            "TailscaleIPs": ["100.64.0.10"],
            "OS": "darwin",
            "Online": true,
            "UserID": 1001
          },
          "Peer": {
            "peer-2": {
              "HostName": "zed",
              "DNSName": "zed.tailnet.example.",
              "TailscaleIPs": ["100.64.0.12"],
              "OS": "linux",
              "Online": false,
              "UserID": 1001
            },
            "peer-1": {
              "HostName": "alpha",
              "DNSName": "alpha.tailnet.example.",
              "TailscaleIPs": ["100.64.0.11"],
              "OS": "linux",
              "Online": true,
              "UserID": 1001
            }
          }
        }
        """

        let snapshot = try TailscaleClient.parseStatusSnapshot(Data(json.utf8))

        XCTAssertEqual(snapshot.selfPeer?.hostName, "m4")
        XCTAssertEqual(snapshot.selfPeer.map(addPeerPreferredTailnetSSHHost), "m4.tailnet.example")
        XCTAssertEqual(snapshot.peers.map(\.hostName), ["alpha", "zed"])
    }

    func testTailscaleStatusCommandPrefersMacAppBinaryBeforePathLookup() {
        XCTAssertEqual(
            TailscaleClient.statusCommand { $0 == "/Applications/Tailscale.app/Contents/MacOS/Tailscale" },
            TailscaleStatusCommand(executablePath: "/Applications/Tailscale.app/Contents/MacOS/Tailscale",
                                   arguments: ["status", "--json"])
        )
        XCTAssertEqual(
            TailscaleClient.statusCommand { _ in false },
            TailscaleStatusCommand(executablePath: "/usr/bin/env",
                                   arguments: ["tailscale", "status", "--json"])
        )
    }

    func testPrivateDirectMeshRemoteSelectionUsesOnePeerAtATime() {
        let manualA = AddPeerRemoteHostDraft(
            sshHost: "linux-b.tailnet",
            hostID: "linux-b",
            user: "jesse",
            port: 22,
            platform: .linux
        )
        let manualB = AddPeerRemoteHostDraft(
            sshHost: "flower-garden.tailnet",
            hostID: "flower-garden",
            user: "jesse",
            port: 22,
            platform: .linux
        )
        let selectedTailnet = AddPeerRemoteHostDraft(
            sshHost: "magic-kingdom.tailnet",
            hostID: "magic-kingdom",
            user: "jesse",
            port: 22,
            platform: .macOS
        )

        XCTAssertEqual(
            addPeerPrivateDirectMeshRemoteDraftsForInstall(manualDrafts: [manualA, manualB],
                                                           selectedTailnetDrafts: []).map(\.sshHost),
            ["linux-b.tailnet"]
        )
        XCTAssertEqual(
            addPeerPrivateDirectMeshRemoteDraftsForInstall(manualDrafts: [manualA, manualB],
                                                           selectedTailnetDrafts: [selectedTailnet]).map(\.sshHost),
            ["magic-kingdom.tailnet"]
        )
    }

    func testRegularSSHUpdateButtonRemainsEnabledWhenPublicAddPeerIsDisabled() {
        XCTAssertFalse(isPeerUpdateButtonDisabled(host: "fsck.com", updating: false, policy: .current))
        XCTAssertTrue(isPeerUpdateButtonDisabled(host: "", updating: false, policy: .current))
        XCTAssertTrue(isPeerUpdateButtonDisabled(host: "fsck.com", updating: true, policy: .current))
    }

    func testPeerHTTPVersionProbeGateControlsDaemonClientPolicy() {
        XCTAssertFalse(shouldProbePeerHTTPVersions(policy: .current, localVersion: "v0.3.8", sharedKeyLoaded: true))
        let legacyHTTPEnabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: false,
            configV2WriteEnabled: false,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: true,
            syncStreamEnabled: false,
            persistentCurrentEnabled: true,
            syncKeyRotationEnabled: false
        )
        XCTAssertTrue(shouldProbePeerHTTPVersions(policy: legacyHTTPEnabled, localVersion: "v0.3.8", sharedKeyLoaded: true))
        XCTAssertTrue(shouldVerifyPeerHTTPVersionAfterUpdate(policy: legacyHTTPEnabled, expectedVersion: "v0.3.8"))
        let disabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        )
        XCTAssertFalse(shouldProbePeerHTTPVersions(policy: disabled, localVersion: "v0.3.8", sharedKeyLoaded: true))
        XCTAssertFalse(shouldVerifyPeerHTTPVersionAfterUpdate(policy: disabled, expectedVersion: "v0.3.8"))
    }

    func testVerifyPeerVersionResultIsSkippedWhenPeerHTTPRuntimeIsDisabled() {
        let result = skippedPeerHTTPVersionVerification(host: "fsck.com")
        XCTAssertNil(result.status)
        XCTAssertEqual(result.detail, "fsck.com peer HTTP version verification is disabled by SSH transport gates")
    }

    @MainActor
    func testDaemonClientVerifyPeerVersionSkipsHTTPFetchWhenDisabled() async {
        let daemon = DaemonClient.shared
        let oldPolicy = daemon.transportGatePolicy
        let oldFetch = daemon.peerVersionFetch
        let oldPeers = daemon.peers
        let oldVersions = daemon.peerVersions
        defer {
            daemon.transportGatePolicy = oldPolicy
            daemon.peerVersionFetch = oldFetch
            daemon.peers = oldPeers
            daemon.peerVersions = oldVersions
        }

        daemon.transportGatePolicy = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        )
        daemon.peers = [Peer(hostname: "fsck.com",
                             port: 7853,
                             last_push_ts: nil,
                             last_push_ok: false,
                             last_push_err: nil,
                             last_recv_ts: nil)]
        daemon.peerVersions = ["fsck.com": .current("v0.3.8")]
        var fetchCalled = false
        daemon.peerVersionFetch = { _, _, _ in
            fetchCalled = true
            return "v0.3.8"
        }

        let result = await daemon.verifyPeerVersion(hostname: "fsck.com",
                                                    expectedVersion: "v0.3.8")

        XCTAssertFalse(fetchCalled)
        XCTAssertNil(result?.status)
        XCTAssertNil(daemon.peerVersions["fsck.com"])
    }

    @MainActor
    func testDaemonClientRefreshPeerVersionsClearsWithoutHTTPFetchWhenDisabled() async {
        let daemon = DaemonClient.shared
        let oldPolicy = daemon.transportGatePolicy
        let oldFetch = daemon.peerVersionFetch
        let oldVersion = daemon.version
        let oldPeers = daemon.peers
        let oldVersions = daemon.peerVersions
        defer {
            daemon.transportGatePolicy = oldPolicy
            daemon.peerVersionFetch = oldFetch
            daemon.version = oldVersion
            daemon.peers = oldPeers
            daemon.peerVersions = oldVersions
        }

        daemon.transportGatePolicy = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        )
        daemon.version = "v0.3.8"
        daemon.peers = [Peer(hostname: "fsck.com",
                             port: 7853,
                             last_push_ts: nil,
                             last_push_ok: false,
                             last_push_err: nil,
                             last_recv_ts: nil)]
        daemon.peerVersions = ["fsck.com": .needsUpdate("v0.3.7")]
        var fetchCalled = false
        daemon.peerVersionFetch = { _, _, _ in
            fetchCalled = true
            return "v0.3.8"
        }

        await daemon.refreshPeerVersions()

        XCTAssertFalse(fetchCalled)
        XCTAssertTrue(daemon.peerVersions.isEmpty)
    }

    func testFleetRowsIgnoreHTTPVersionHealthWhenPeerHTTPRuntimeIsDisabled() {
        let disabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        )
        let peer = Peer(hostname: "fsck.com",
                        port: 7853,
                        last_push_ts: nil,
                        last_push_ok: false,
                        last_push_err: nil,
                        last_recv_ts: nil)

        let rows = fleetRows(origin: "m4",
                             connected: true,
                             peers: [peer],
                             peerVersions: ["fsck.com": .current("v0.3.8")],
                             policy: disabled)

        XCTAssertEqual(rows[1].health, .down)
        XCTAssertEqual(rows[1].subtitle, "port 7853")
    }
}
