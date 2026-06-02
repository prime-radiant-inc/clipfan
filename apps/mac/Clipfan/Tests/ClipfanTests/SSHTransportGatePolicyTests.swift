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
        var policy = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: true,
            publicAddPeerSuccessEnabled: true,
            receivePrimitiveEnabled: true,
            syncStreamEnabled: true,
            persistentCurrentEnabled: true,
            syncKeyRotationEnabled: false
        )

        XCTAssertFalse(policy.addPeerProvisioningEnabled)

        policy.syncKeyRotationEnabled = true

        XCTAssertTrue(policy.addPeerProvisioningEnabled)
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
