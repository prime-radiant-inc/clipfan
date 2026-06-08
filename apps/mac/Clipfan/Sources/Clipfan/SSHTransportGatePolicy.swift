struct SSHTransportGatePolicy {
    var remoteSecretWriteReleaseEnabled: Bool
    var publicAddPeerSuccessEnabled: Bool
    var receivePrimitiveEnabled: Bool
    var syncStreamEnabled: Bool
    var persistentCurrentEnabled: Bool
    var syncKeyRotationEnabled: Bool

    static var current: SSHTransportGatePolicy {
        SSHTransportGatePolicy(
            remoteSecretWriteReleaseEnabled: GeneratedSSHTransportGates.remoteSecretWriteReleaseEnabled,
            publicAddPeerSuccessEnabled: GeneratedSSHTransportGates.publicAddPeerSuccessEnabled,
            receivePrimitiveEnabled: GeneratedSSHRuntimeGates.receivePrimitiveEnabled,
            syncStreamEnabled: GeneratedSSHRuntimeGates.syncStreamEnabled,
            persistentCurrentEnabled: GeneratedSSHRuntimeGates.persistentCurrentEnabled,
            syncKeyRotationEnabled: GeneratedSSHRuntimeGates.syncKeyRotationEnabled
        )
    }

    var addPeerProvisioningEnabled: Bool {
        remoteSecretWriteReleaseEnabled
            && publicAddPeerSuccessEnabled
            && receivePrimitiveEnabled
            && syncStreamEnabled
            && persistentCurrentEnabled
            && syncKeyRotationEnabled
    }

    var privateDirectMeshProvisioningEnabled: Bool {
        receivePrimitiveEnabled
            && syncStreamEnabled
            && persistentCurrentEnabled
    }

    var regularSSHUpdateEnabled: Bool { true }
}
