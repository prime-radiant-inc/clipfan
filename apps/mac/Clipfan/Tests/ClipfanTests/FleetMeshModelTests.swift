import XCTest
@testable import Clipfan

final class FleetMeshModelTests: XCTestCase {
    /// Builds a /v1/fleet payload for a set of hosts. `links[host]` lists the peers
    /// that host reports, each with a migration state, so a test can express partial
    /// or asymmetric observations. A host absent from `reachable` is rendered as not
    /// reachable with no snapshot.
    private func fleetJSON(origin: String,
                           hosts: [String],
                           reachable: Set<String>,
                           links: [String: [(String, String)]]) -> Data {
        var hostObjects: [[String: Any]] = []
        for host in hosts {
            if reachable.contains(host) {
                let peers = (links[host] ?? []).map { peer, state -> [String: Any] in
                    ["id": peer, "migration_state": state, "ssh_active": state == "ssh_keys_ready"]
                }
                hostObjects.append([
                    "id": host,
                    "reachable": true,
                    "snapshot": ["origin": host, "version": "test", "peers": peers]
                ])
            } else {
                hostObjects.append(["id": host, "reachable": false, "error": "read: timeout"])
            }
        }
        let payload: [String: Any] = ["origin": origin, "version": "test", "hosts": hostObjects]
        return try! JSONSerialization.data(withJSONObject: payload)
    }

    private func fullMeshLinks(_ hosts: [String], state: String = "ssh_keys_ready") -> [String: [(String, String)]] {
        var links: [String: [(String, String)]] = [:]
        for host in hosts {
            links[host] = hosts.filter { $0 != host }.map { ($0, state) }
        }
        return links
    }

    func testFourHostFullMeshHasSixHealthyEdges() throws {
        let hosts = ["alpha", "beta", "gamma", "delta"]
        let data = fleetJSON(origin: "alpha", hosts: hosts, reachable: Set(hosts), links: fullMeshLinks(hosts))
        let mesh = try buildFleetMesh(from: data)

        XCTAssertEqual(mesh.edges.count, 6, "4 hosts form 6 undirected edges")
        XCTAssertEqual(mesh.observedEdgeCount, 6)
        XCTAssertEqual(mesh.healthyEdgeCount, 6)
        XCTAssertEqual(mesh.headerSummary, "6/6 edges healthy")
        XCTAssertTrue(mesh.edges.allSatisfy { $0.health == .healthy })
        XCTAssertEqual(mesh.hosts.count, 4)
        // each host sits on 3 edges
        XCTAssertTrue(mesh.hosts.allSatisfy { $0.edges.count == 3 && $0.health == .healthy })
    }

    func testEdgeIsCanonicalAndDeduplicated() throws {
        let hosts = ["beta", "alpha"]
        let data = fleetJSON(origin: "alpha", hosts: hosts, reachable: Set(hosts), links: fullMeshLinks(hosts))
        let mesh = try buildFleetMesh(from: data)
        XCTAssertEqual(mesh.edges.count, 1)
        // endpoints sorted so the edge id is stable regardless of host order
        XCTAssertEqual(mesh.edges[0].a, "alpha")
        XCTAssertEqual(mesh.edges[0].b, "beta")
        XCTAssertEqual(mesh.edges[0].id, "alpha<->beta")
    }

    func testHalfBuiltEdgeIsDegradedAndWorsensHostHealth() throws {
        let hosts = ["alpha", "beta", "gamma"]
        var links = fullMeshLinks(hosts)
        // alpha sees beta as still staging (half-built edge); beta sees alpha ready
        links["alpha"] = [("beta", "ssh_material_staged"), ("gamma", "ssh_keys_ready")]
        let data = fleetJSON(origin: "alpha", hosts: hosts, reachable: Set(hosts), links: links)
        let mesh = try buildFleetMesh(from: data)

        let alphaBeta = try XCTUnwrap(mesh.edges.first { $0.id == "alpha<->beta" })
        XCTAssertEqual(alphaBeta.health, .degraded, "a not-ready view makes the edge degraded")
        XCTAssertEqual(mesh.observedEdgeCount, 3)
        XCTAssertEqual(mesh.healthyEdgeCount, 2)
        XCTAssertEqual(mesh.headerSummary, "2/3 edges healthy")
        // alpha and beta sit on the degraded edge → degraded; gamma stays healthy
        XCTAssertEqual(mesh.hosts.first { $0.id == "alpha" }?.health, .degraded)
        XCTAssertEqual(mesh.hosts.first { $0.id == "beta" }?.health, .degraded)
        XCTAssertEqual(mesh.hosts.first { $0.id == "gamma" }?.health, .healthy)
    }

    func testUnobservedEdgeIsUnknownAndExcludedFromHeader() throws {
        let hosts = ["alpha", "beta", "delta"]
        // alpha<->beta observed & healthy; delta is unreachable AND no one lists it,
        // so its two edges are unobserved.
        let links: [String: [(String, String)]] = [
            "alpha": [("beta", "ssh_keys_ready")],
            "beta": [("alpha", "ssh_keys_ready")]
        ]
        let data = fleetJSON(origin: "alpha", hosts: hosts, reachable: ["alpha", "beta"], links: links)
        let mesh = try buildFleetMesh(from: data)

        XCTAssertEqual(mesh.edges.count, 3)
        XCTAssertEqual(mesh.observedEdgeCount, 1)
        XCTAssertEqual(mesh.healthyEdgeCount, 1)
        XCTAssertEqual(mesh.headerSummary, "1/1 edges healthy")
        let deltaEdges = mesh.edges.filter { $0.a == "delta" || $0.b == "delta" }
        XCTAssertEqual(deltaEdges.count, 2)
        XCTAssertTrue(deltaEdges.allSatisfy { $0.health == .unknown })
        // delta is unreachable with no observed edges → unknown, and flagged unreachable
        let delta = try XCTUnwrap(mesh.hosts.first { $0.id == "delta" })
        XCTAssertEqual(delta.health, .unknown)
        XCTAssertFalse(delta.reachable)
    }

    func testEdgeObservedFromOneEndOnlyIsStillHealthy() throws {
        // delta unreachable, but alpha and beta both still report delta as ready, so
        // delta's edges are observed (from the reachable side) and healthy.
        let hosts = ["alpha", "beta", "delta"]
        let links: [String: [(String, String)]] = [
            "alpha": [("beta", "ssh_keys_ready"), ("delta", "ssh_keys_ready")],
            "beta": [("alpha", "ssh_keys_ready"), ("delta", "ssh_keys_ready")]
        ]
        let data = fleetJSON(origin: "alpha", hosts: hosts, reachable: ["alpha", "beta"], links: links)
        let mesh = try buildFleetMesh(from: data)
        XCTAssertEqual(mesh.observedEdgeCount, 3)
        XCTAssertEqual(mesh.headerSummary, "3/3 edges healthy")
    }

    // MARK: - Per-row mesh summary (Settings → Fleet rows)

    private func hostRow(id: String, edges: [MeshEdgeHealth]) -> MeshHostRow {
        let meshEdges = edges.enumerated().map { idx, health in
            MeshEdge(a: id, b: "peer-\(idx)", health: health, detail: "")
        }
        return MeshHostRow(id: id, reachable: true, error: nil, edges: meshEdges, health: .healthy)
    }

    func testMeshRowSummaryAllHealthy() {
        let row = hostRow(id: "alpha", edges: [.healthy, .healthy, .healthy])
        XCTAssertEqual(meshRowSummary(row), "mesh 3/3 edges")
    }

    func testMeshRowSummaryFlagsIncompleteEdges() {
        let row = hostRow(id: "alpha", edges: [.healthy, .healthy, .degraded])
        XCTAssertEqual(meshRowSummary(row), "mesh 2/3 · 1 incomplete")
    }

    func testMeshRowSummaryFlagsUnobservedEdges() {
        let row = hostRow(id: "alpha", edges: [.healthy, .healthy, .unknown])
        XCTAssertEqual(meshRowSummary(row), "mesh 2/3 · 1 unobserved")
    }

    func testMeshRowSummaryFlagsIncompleteAndUnobserved() {
        let row = hostRow(id: "alpha", edges: [.healthy, .degraded, .unknown])
        XCTAssertEqual(meshRowSummary(row), "mesh 1/3 · 1 incomplete · 1 unobserved")
    }

    func testMeshHostRowLookup() {
        let mesh = FleetMesh(origin: "alpha",
                             hosts: [hostRow(id: "alpha", edges: [.healthy]),
                                     hostRow(id: "beta", edges: [.degraded])],
                             edges: [],
                             observedEdgeCount: 0,
                             healthyEdgeCount: 0,
                             headerSummary: "")
        XCTAssertEqual(meshHostRow(for: "beta", in: mesh)?.id, "beta")
        XCTAssertNil(meshHostRow(for: "zeta", in: mesh))
        XCTAssertNil(meshHostRow(for: "beta", in: nil))
    }
}
