struct SSHTransportGatePolicy {
    var peerHTTPRuntimeDisabled: Bool
    var configV2WriteEnabled: Bool
    var remoteSecretWriteReleaseEnabled: Bool
    var publicAddPeerSuccessEnabled: Bool
    var receivePrimitiveEnabled: Bool
    var syncStreamEnabled: Bool
    var persistentCurrentEnabled: Bool
    var syncKeyRotationEnabled: Bool

    static var current: SSHTransportGatePolicy {
        SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: GeneratedSSHTransportGates.peerHTTPRuntimeDisabled,
            configV2WriteEnabled: GeneratedSSHTransportGates.configV2WriteEnabled,
            remoteSecretWriteReleaseEnabled: GeneratedSSHTransportGates.remoteSecretWriteReleaseEnabled,
            publicAddPeerSuccessEnabled: GeneratedSSHTransportGates.publicAddPeerSuccessEnabled,
            receivePrimitiveEnabled: GeneratedSSHRuntimeGates.receivePrimitiveEnabled,
            syncStreamEnabled: GeneratedSSHRuntimeGates.syncStreamEnabled,
            persistentCurrentEnabled: GeneratedSSHRuntimeGates.persistentCurrentEnabled,
            syncKeyRotationEnabled: GeneratedSSHRuntimeGates.syncKeyRotationEnabled
        )
    }

    var addPeerProvisioningEnabled: Bool {
        peerHTTPRuntimeDisabled
            && configV2WriteEnabled
            && remoteSecretWriteReleaseEnabled
            && publicAddPeerSuccessEnabled
            && receivePrimitiveEnabled
            && syncStreamEnabled
            && persistentCurrentEnabled
            && syncKeyRotationEnabled
    }

    var privateDirectMeshProvisioningEnabled: Bool {
        peerHTTPRuntimeDisabled
            && configV2WriteEnabled
            && receivePrimitiveEnabled
            && syncStreamEnabled
            && persistentCurrentEnabled
    }

    var regularSSHUpdateEnabled: Bool { true }

    var peerHTTPVersionProbeEnabled: Bool { !peerHTTPRuntimeDisabled }
}
