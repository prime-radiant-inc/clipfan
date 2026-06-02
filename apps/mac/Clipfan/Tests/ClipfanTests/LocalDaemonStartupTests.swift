import XCTest
@testable import Clipfan

final class LocalDaemonStartupTests: XCTestCase {
    func testPreV2ConfigKeepsLegacyRawKeyCompatibility() {
        let missingVersion = LocalDaemonStartup.prepare(
            configData: data(#"{"shared_key":"secret","static_peers":[]}"#),
            clientSupportsHKDF: false
        )
        XCTAssertNil(missingVersion.diagnostic)
        XCTAssertTrue(missingVersion.permitsWholeConfigRawKeyWrites)
        XCTAssertFalse(missingVersion.requiresHKDFClient)

        let explicitV1 = LocalDaemonStartup.prepare(
            configData: data(#"{"config_version":1,"shared_key":"secret","static_peers":[]}"#),
            clientSupportsHKDF: false
        )
        XCTAssertNil(explicitV1.diagnostic)
        XCTAssertTrue(explicitV1.permitsWholeConfigRawKeyWrites)
        XCTAssertFalse(explicitV1.requiresHKDFClient)
    }

    func testConfigV2WithoutHKDFClientProducesStableDiagnosticAndDisablesRawWrites() {
        let preparation = LocalDaemonStartup.prepare(
            configData: data(#"{"config_version":2,"config_revision":7,"static_peers":[]}"#),
            clientSupportsHKDF: false
        )

        XCTAssertEqual(preparation.diagnostic, .configV2RequiresHKDFClient)
        XCTAssertEqual(preparation.diagnostic?.rawValue, "config_v2_requires_hkdf_client")
        XCTAssertFalse(preparation.permitsWholeConfigRawKeyWrites)
        XCTAssertTrue(preparation.requiresHKDFClient)
    }

    func testConfigV2WithHKDFClientStillDoesNotPermitWholeConfigRawKeyWrites() {
        let preparation = LocalDaemonStartup.prepare(
            configData: data(#"{"config_version":2,"config_revision":7,"static_peers":[]}"#),
            clientSupportsHKDF: true
        )

        XCTAssertNil(preparation.diagnostic)
        XCTAssertFalse(preparation.permitsWholeConfigRawKeyWrites)
        XCTAssertTrue(preparation.requiresHKDFClient)
    }

    private func data(_ json: String) -> Data {
        Data(json.utf8)
    }
}
