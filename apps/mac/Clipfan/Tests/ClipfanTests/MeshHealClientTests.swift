import XCTest
@testable import Clipfan

final class MeshHealClientTests: XCTestCase {
    func testDecodeFullReport() throws {
        let json = """
        {"healed":["m4<->magic-kingdom"],"skipped":["m4<->flower-garden"],\
        "failed":[{"edge":"magic-kingdom<->flower-garden","reason":"provision_failed"}],\
        "restarted":["magic-kingdom"],\
        "unreachable":[{"id":"jesse-paradise-park","reason":"read: timeout"}]}
        """
        let report = try decodeMeshHealReport(Data(json.utf8))
        XCTAssertEqual(report.healed, ["m4<->magic-kingdom"])
        XCTAssertEqual(report.skipped, ["m4<->flower-garden"])
        XCTAssertEqual(report.failed, [MeshHealFailure(edge: "magic-kingdom<->flower-garden", reason: "provision_failed")])
        XCTAssertEqual(report.restarted, ["magic-kingdom"])
        XCTAssertEqual(report.unreachable, [MeshHealUnreachable(id: "jesse-paradise-park", reason: "read: timeout")])
    }

    func testDecodeToleratesNullAndMissingArrays() throws {
        let report = try decodeMeshHealReport(Data(#"{"healed":null,"failed":null}"#.utf8))
        XCTAssertEqual(report, MeshHealReport())
    }

    func testSummaryHealthy() {
        let report = MeshHealReport(healed: ["a<->b", "a<->c"], skipped: ["b<->c"])
        XCTAssertTrue(report.isFullyHealthy)
        XCTAssertEqual(report.healthyEdgeCount, 3)
        XCTAssertEqual(report.summary, "mesh healed (3 edges)")
    }

    func testSummaryWithFailures() {
        let report = MeshHealReport(healed: ["a<->b"],
                                    failed: [MeshHealFailure(edge: "b<->c", reason: "x")],
                                    unreachable: [MeshHealUnreachable(id: "d", reason: "y")])
        XCTAssertFalse(report.isFullyHealthy)
        XCTAssertEqual(report.summary, "mesh healed (1 edge) · 1 edge failed · 1 host unreachable")
    }

    func testHealBuildsTrustKeyscanArgsAndExpandsKnownHosts() async throws {
        var capturedExe = ""
        var capturedArgs: [String] = []
        let report = try await MeshHealClient.heal(
            regularKnownHosts: "~/.ssh/known_hosts",
            localProvisioningBinary: { "/usr/local/bin/clipfan" },
            runCommand: { exe, args in
                capturedExe = exe
                capturedArgs = args
                return #"{"healed":["a<->b"],"skipped":[],"failed":[],"restarted":["a"],"unreachable":[]}"#
            }
        )
        XCTAssertEqual(capturedExe, "/usr/local/bin/clipfan")
        XCTAssertEqual(capturedArgs.first, "mesh-heal")
        XCTAssertTrue(capturedArgs.contains("--trust-keyscan"))
        guard let flagIndex = capturedArgs.firstIndex(of: "--regular-known-hosts") else {
            return XCTFail("missing --regular-known-hosts")
        }
        let knownHosts = capturedArgs[flagIndex + 1]
        XCTAssertFalse(knownHosts.hasPrefix("~"), "known_hosts should be home-expanded: \(knownHosts)")
        XCTAssertTrue(knownHosts.hasSuffix("/.ssh/known_hosts"))
        XCTAssertEqual(report.healed, ["a<->b"])
        XCTAssertEqual(report.restarted, ["a"])
    }

    func testHealRejectsEmptyKnownHosts() async {
        do {
            _ = try await MeshHealClient.heal(regularKnownHosts: "   ",
                                              localProvisioningBinary: { "/x" },
                                              runCommand: { _, _ in "{}" })
            XCTFail("expected error for empty known_hosts")
        } catch {
            // expected
        }
    }
}
