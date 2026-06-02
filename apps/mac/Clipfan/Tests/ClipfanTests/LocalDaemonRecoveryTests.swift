import XCTest
@testable import Clipfan

final class LocalDaemonRecoveryTests: XCTestCase {
    func testConfigV2OldClientWithSharedKeyUsesSignedListenerRepair() {
        let plan = LocalDaemonRecovery.plan(
            configData: data(#"{"config_version":2,"shared_key":"secret","static_peers":[]}"#),
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
            configData: data(#"{"config_version":2,"static_peers":[]}"#),
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
            configData: data(#"{"config_version":2,"shared_key":"","static_peers":[]}"#),
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
            configData: data(#"{"config_version":3,"shared_key":"secret","static_peers":[]}"#),
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
            configData: data(#"{"shared_key":"secret","static_peers":[]}"#),
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
            configData: data(#"{"config_version":1,"shared_key":"secret","static_peers":[]}"#),
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
