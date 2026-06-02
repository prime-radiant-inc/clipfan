import XCTest
@testable import Clipfan

final class SSHTransportGatePolicyTests: XCTestCase {
    func testCurrentAddPeerProvisioningIsDisabledForGeneratedGates() {
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
}
