import XCTest
@testable import Clipfan

final class LocalDaemonDiscoveryTests: XCTestCase {
    func testConfigDerivedLoopbackListenIsUsedForSignedEndpoints() throws {
        let plan = LocalDaemonDiscovery.plan(configData: data(#"{"listen":"127.0.0.1:49123","port":7853}"#))

        XCTAssertEqual(plan.signedEndpoints, [
            LocalDaemonEndpoint(url: try XCTUnwrap(URL(string: "http://127.0.0.1:49123")), port: 49123, purpose: .signed),
        ])
        XCTAssertEqual(plan.healthOnlyEndpoints.map(\.port), [49123, 7853])
    }

    func testConfigDerivedIPv6LoopbackListenIsPreservedForSignedEndpoints() throws {
        let plan = LocalDaemonDiscovery.plan(configData: data(#"{"listen":"[::1]:49123","port":7853}"#))

        XCTAssertEqual(plan.signedEndpoints, [
            LocalDaemonEndpoint(url: try XCTUnwrap(URL(string: "http://[::1]:49123")), port: 49123, purpose: .signed),
        ])
    }

    func testUnsafeListenDoesNotAuthorizeSignedEndpoints() {
        let wildcard = LocalDaemonDiscovery.plan(configData: data(#"{"listen":":49123","port":7853}"#))
        XCTAssertEqual(wildcard.signedEndpoints, [])
        XCTAssertEqual(wildcard.healthOnlyEndpoints.map(\.port), [49123, 7853])

        let publicHost = LocalDaemonDiscovery.plan(configData: data(#"{"listen":"0.0.0.0:49124","port":7853}"#))
        XCTAssertEqual(publicHost.signedEndpoints, [])
        XCTAssertEqual(publicHost.healthOnlyEndpoints.map(\.port), [49124, 7853])
    }

    func testSignedFallbackTo7853RequiresExplicitIdentityProof() {
        let config = data(#"{"listen":"localhost:49123","port":7853}"#)
        let unproved = LocalDaemonDiscovery.plan(configData: config)
        XCTAssertEqual(unproved.signedEndpoints.map(\.port), [49123])
        XCTAssertFalse(unproved.signedEndpoints.contains { $0.purpose == .signedCompatibility })

        let proof = LocalDaemonIdentityProof(
            configPath: "/Users/j/.config/clipfan/config.json",
            stateDirectory: "/Users/j/.local/state/clipfan",
            authVersion: clipfanRequestAuthVersion,
            hostID: "host-1"
        )
        let proved = LocalDaemonDiscovery.plan(configData: config, identityProof: proof)
        XCTAssertEqual(proved.signedEndpoints.map(\.port), [49123, 7853])
        XCTAssertEqual(proved.signedEndpoints.last?.purpose, .signedCompatibility)
    }

    func testHealthOnlyFallbackStaysSeparate() {
        let plan = LocalDaemonDiscovery.plan(configData: data(#"{"listen":"127.0.0.1:49123"}"#))

        XCTAssertEqual(plan.signedEndpoints.map(\.purpose), [.signed])
        XCTAssertEqual(plan.healthOnlyEndpoints, [
            LocalDaemonEndpoint(url: try XCTUnwrap(URL(string: "http://127.0.0.1:49123")), port: 49123, purpose: .healthOnly),
            LocalDaemonEndpoint(url: try XCTUnwrap(URL(string: "http://127.0.0.1:7853")), port: 7853, purpose: .healthOnly),
        ])
    }

    func testInvalidListenFallsBackToValidPortThenDefaultPort() {
        let portPlan = LocalDaemonDiscovery.plan(configData: data(#"{"listen":"bad","port":49125}"#))
        XCTAssertEqual(portPlan.healthOnlyEndpoints.map(\.port), [49125, 7853])
        XCTAssertEqual(portPlan.signedEndpoints, [])

        let defaultPlan = LocalDaemonDiscovery.plan(configData: data(#"{"listen":"bad","port":70000}"#))
        XCTAssertEqual(defaultPlan.healthOnlyEndpoints.map(\.port), [7853])
        XCTAssertEqual(defaultPlan.signedEndpoints, [])
    }

    private func data(_ json: String) -> Data {
        Data(json.utf8)
    }
}
