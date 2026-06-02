import XCTest
@testable import Clipfan

final class LocalDaemonRecoveryTests: XCTestCase {
    private let validSharedKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

    func testConfigV2OldClientWithSharedKeyUsesSignedListenerRepair() {
        let plan = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":2,"shared_key":"\#(validSharedKey)","static_peers":[]}"#),
            clientSupportsHKDF: false,
            capabilities: LocalDaemonRecoveryCapabilities(signedListenerRepairAvailable: true)
        )

        XCTAssertEqual(plan.diagnostic, .configV2RequiresHKDFClient)
        XCTAssertEqual(plan.diagnostic?.rawValue, "config_v2_requires_hkdf_client")
        XCTAssertFalse(plan.permitsWholeConfigRawKeyWrites)
        XCTAssertTrue(plan.requiresHKDFClient)
        XCTAssertEqual(plan.disposition, .recoverable)
        XCTAssertEqual(plan.recoveryPath, .signedListenerRepair)
    }

    func testConfigV2OldClientWithoutSharedKeyUsesOfflineListenerRepair() {
        let missingSharedKey = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":2,"listen":"0.0.0.0:9000","static_peers":[]}"#),
            clientSupportsHKDF: false,
            capabilities: LocalDaemonRecoveryCapabilities(
                signedListenerRepairAvailable: true,
                offlineListenerRepairAvailable: true
            )
        )

        XCTAssertEqual(missingSharedKey.diagnostic, .configV2RequiresHKDFClient)
        XCTAssertFalse(missingSharedKey.permitsWholeConfigRawKeyWrites)
        XCTAssertEqual(missingSharedKey.disposition, .recoverable)
        XCTAssertEqual(missingSharedKey.recoveryPath, .offlineListenerRepair)

        let invalidSharedKey = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":2,"listen":"0.0.0.0:9000","shared_key":"not-base64","static_peers":[]}"#),
            clientSupportsHKDF: false,
            capabilities: LocalDaemonRecoveryCapabilities(
                signedListenerRepairAvailable: true,
                offlineListenerRepairAvailable: true
            )
        )

        XCTAssertEqual(invalidSharedKey.diagnostic, .configV2RequiresHKDFClient)
        XCTAssertFalse(invalidSharedKey.permitsWholeConfigRawKeyWrites)
        XCTAssertEqual(invalidSharedKey.disposition, .recoverable)
        XCTAssertEqual(invalidSharedKey.recoveryPath, .offlineListenerRepair)
    }

    func testConfigV2InvalidSharedKeySafeListenerUsesConfirmedLocalFleetResetWhenAvailable() {
        let plan = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":2,"listen":"127.0.0.1:7853","shared_key":"not-base64","static_peers":["old"]}"#),
            clientSupportsHKDF: false,
            capabilities: LocalDaemonRecoveryCapabilities(
                offlineListenerRepairAvailable: true,
                localFleetResetAvailable: true
            )
        )

        XCTAssertEqual(plan.diagnostic, .configV2RequiresHKDFClient)
        XCTAssertFalse(plan.permitsWholeConfigRawKeyWrites)
        XCTAssertEqual(plan.disposition, .recoverable)
        XCTAssertEqual(plan.recoveryPath, .confirmedLocalFleetReset)
    }

    func testConfigV2UnbracketedIPv6ListenerUsesListenerRepairBeforeFleetReset() {
        let plan = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":2,"listen":"::1:7853","shared_key":"not-base64","static_peers":["old"]}"#),
            clientSupportsHKDF: false,
            capabilities: LocalDaemonRecoveryCapabilities(
                offlineListenerRepairAvailable: true,
                localFleetResetAvailable: true
            )
        )

        XCTAssertEqual(plan.diagnostic, .configV2RequiresHKDFClient)
        XCTAssertFalse(plan.permitsWholeConfigRawKeyWrites)
        XCTAssertEqual(plan.disposition, .recoverable)
        XCTAssertEqual(plan.recoveryPath, .offlineListenerRepair)
    }

    func testConfigV2MalformedLoopbackPortUsesListenerRepairBeforeFleetReset() {
        for listen in ["127.0.0.1:notaport", "127.0.0.1:99999", "[::1]:0"] {
            let plan = LocalDaemonRecovery.plan(
                configData: data(#"{"config_version":2,"listen":"\#(listen)","shared_key":"not-base64","static_peers":["old"]}"#),
                clientSupportsHKDF: false,
                capabilities: LocalDaemonRecoveryCapabilities(
                    offlineListenerRepairAvailable: true,
                    localFleetResetAvailable: true
                )
            )

            XCTAssertEqual(plan.diagnostic, .configV2RequiresHKDFClient)
            XCTAssertFalse(plan.permitsWholeConfigRawKeyWrites)
            XCTAssertEqual(plan.disposition, .recoverable)
            XCTAssertEqual(plan.recoveryPath, .offlineListenerRepair, "listen=\(listen)")
        }
    }

    func testPublicProfileBlocksConfirmedLocalFleetResetBeforeConfigV2Writes() throws {
        guard !GeneratedSSHTransportGates.configV2WriteEnabled else {
            throw XCTSkip("requires public generated ConfigV2WriteEnabled=false profile")
        }
        let plan = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":2,"listen":"127.0.0.1:7853","shared_key":"","static_peers":["old"]}"#),
            clientSupportsHKDF: false,
            capabilities: LocalDaemonRecoveryCapabilities(
                offlineListenerRepairAvailable: true,
                localFleetResetAvailable: GeneratedSSHTransportGates.configV2WriteEnabled
            )
        )

        XCTAssertEqual(plan.diagnostic, .configV2RequiresHKDFClient)
        XCTAssertFalse(plan.permitsWholeConfigRawKeyWrites)
        XCTAssertEqual(plan.disposition, .blocked)
        XCTAssertEqual(plan.recoveryPath, .none)
    }

    func testGeneratedConfigV2WriteGateEnablesConfirmedLocalFleetResetPlan() throws {
        guard GeneratedSSHTransportGates.configV2WriteEnabled else {
            throw XCTSkip("requires internal/test generated ConfigV2WriteEnabled=true profile")
        }
        XCTAssertTrue(GeneratedSSHTransportGates.peerHTTPRuntimeDisabled)
        XCTAssertFalse(GeneratedSSHTransportGates.remoteSecretWriteReleaseEnabled)
        XCTAssertFalse(GeneratedSSHTransportGates.publicAddPeerSuccessEnabled)

        let plan = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":2,"listen":"127.0.0.1:7853","shared_key":"","static_peers":["old"]}"#),
            clientSupportsHKDF: false,
            capabilities: LocalDaemonRecoveryCapabilities(
                offlineListenerRepairAvailable: true,
                localFleetResetAvailable: GeneratedSSHTransportGates.configV2WriteEnabled
            )
        )

        XCTAssertEqual(plan.diagnostic, .configV2RequiresHKDFClient)
        XCTAssertFalse(plan.permitsWholeConfigRawKeyWrites)
        XCTAssertEqual(plan.disposition, .recoverable)
        XCTAssertEqual(plan.recoveryPath, .confirmedLocalFleetReset)
    }

    func testConfigV2OldClientWithoutRepairPathIsBlocked() {
        let plan = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":2,"shared_key":"","static_peers":[]}"#),
            clientSupportsHKDF: false
        )

        XCTAssertEqual(plan.diagnostic, .configV2RequiresHKDFClient)
        XCTAssertFalse(plan.permitsWholeConfigRawKeyWrites)
        XCTAssertTrue(plan.requiresHKDFClient)
        XCTAssertEqual(plan.disposition, .blocked)
        XCTAssertEqual(plan.recoveryPath, .none)
    }

    func testFutureConfigVersionOldClientWithoutRepairPathIsBlocked() {
        let plan = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":3,"shared_key":"\#(validSharedKey)","static_peers":[]}"#),
            clientSupportsHKDF: false
        )

        XCTAssertEqual(plan.diagnostic, .configV2RequiresHKDFClient)
        XCTAssertFalse(plan.permitsWholeConfigRawKeyWrites)
        XCTAssertTrue(plan.requiresHKDFClient)
        XCTAssertEqual(plan.disposition, .blocked)
        XCTAssertEqual(plan.recoveryPath, .none)
    }

    func testPreV2RemainsCompatible() {
        let missingVersion = LocalDaemonRecovery.plan(
            configData: data(#"{"shared_key":"\#(validSharedKey)","static_peers":[]}"#),
            clientSupportsHKDF: false,
            capabilities: LocalDaemonRecoveryCapabilities(
                signedListenerRepairAvailable: true,
                offlineListenerRepairAvailable: true
            )
        )

        XCTAssertNil(missingVersion.diagnostic)
        XCTAssertTrue(missingVersion.permitsWholeConfigRawKeyWrites)
        XCTAssertFalse(missingVersion.requiresHKDFClient)
        XCTAssertEqual(missingVersion.disposition, .compatible)
        XCTAssertEqual(missingVersion.recoveryPath, .none)

        let explicitV1 = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":1,"shared_key":"\#(validSharedKey)","static_peers":[]}"#),
            clientSupportsHKDF: false
        )

        XCTAssertNil(explicitV1.diagnostic)
        XCTAssertTrue(explicitV1.permitsWholeConfigRawKeyWrites)
        XCTAssertFalse(explicitV1.requiresHKDFClient)
        XCTAssertEqual(explicitV1.disposition, .compatible)
        XCTAssertEqual(explicitV1.recoveryPath, .none)
    }

    private func data(_ json: String) -> Data {
        Data(json.utf8)
    }
}
