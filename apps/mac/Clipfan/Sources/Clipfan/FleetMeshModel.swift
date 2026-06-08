import Foundation

// MARK: - Wire types (decode of GET /v1/fleet)

/// FleetViewResponse decodes the daemon's aggregated mesh view. Each host carries its
/// own redacted snapshot when the daemon reached it, or a reachable:false marker.
struct FleetViewResponse: Codable {
    var origin: String
    var version: String?
    var hosts: [FleetViewHostResponse]
}

struct FleetViewHostResponse: Codable {
    var id: String
    var reachable: Bool
    var snapshot: FleetSnapshotResponse?
    var error: String?
}

struct FleetSnapshotResponse: Codable {
    var origin: String
    var version: String?
    var peers: [FleetSnapshotPeerResponse]
}

struct FleetSnapshotPeerResponse: Codable {
    var id: String
    var sshHost: String?
    var migrationState: String?
    var sshStatus: String?
    var sshActive: Bool?

    enum CodingKeys: String, CodingKey {
        case id
        case sshHost = "ssh_host"
        case migrationState = "migration_state"
        case sshStatus = "ssh_status"
        case sshActive = "ssh_active"
    }
}

// MARK: - Derived mesh model

/// MeshEdgeHealth is the state of one undirected edge as the fleet observed it.
/// `.unknown` means no endpoint reported the edge (e.g. both ends unreachable), and
/// is rendered distinctly rather than as "down" so an unobserved edge is never
/// mistaken for a broken one.
enum MeshEdgeHealth: String, Equatable {
    case healthy
    case degraded
    case unknown
}

/// MeshEdge is one undirected edge between two hosts, with `a` < `b` so its identity
/// is stable regardless of which end the data came from.
struct MeshEdge: Identifiable, Equatable {
    var a: String
    var b: String
    var health: MeshEdgeHealth
    var detail: String
    var id: String { "\(a)<->\(b)" }

    func connects(_ host: String) -> Bool { a == host || b == host }
    func other(than host: String) -> String { a == host ? b : a }
}

/// MeshHostRow is one host and the edges incident to it. `health` is the worst of its
/// observed incident edges, or `.unknown` when none were observed.
struct MeshHostRow: Identifiable, Equatable {
    var id: String
    var reachable: Bool
    var error: String?
    var edges: [MeshEdge]
    var health: MeshEdgeHealth
}

/// FleetMesh is the whole fleet as a graph: every host and every undirected edge,
/// plus the observed-edge tallies the header summarizes.
struct FleetMesh: Equatable {
    var origin: String
    var hosts: [MeshHostRow]
    var edges: [MeshEdge]
    var observedEdgeCount: Int
    var healthyEdgeCount: Int
    var headerSummary: String
}

private let meshReadyMigrationState = "ssh_keys_ready"

/// buildFleetMesh decodes a /v1/fleet payload and reconstructs the undirected edge
/// graph. Each host's snapshot lists its directed view of its peers; an edge is
/// healthy when every endpoint that reported it sees ssh_keys_ready, degraded when any
/// reporting endpoint sees something else, and unknown when no endpoint reported it.
func buildFleetMesh(from data: Data) throws -> FleetMesh {
    let response = try JSONDecoder().decode(FleetViewResponse.self, from: data)
    return buildFleetMesh(from: response)
}

func buildFleetMesh(from response: FleetViewResponse) -> FleetMesh {
    let hostIDs = response.hosts.map(\.id).sorted()
    var reachable: [String: Bool] = [:]
    var errors: [String: String] = [:]
    var views: [String: [String: FleetSnapshotPeerResponse]] = [:]
    for host in response.hosts {
        reachable[host.id] = host.reachable
        errors[host.id] = host.error
        var peerByID: [String: FleetSnapshotPeerResponse] = [:]
        for peer in host.snapshot?.peers ?? [] {
            peerByID[peer.id] = peer
        }
        views[host.id] = peerByID
    }

    var edges: [MeshEdge] = []
    for i in hostIDs.indices {
        for j in hostIDs.index(after: i)..<hostIDs.endIndex {
            edges.append(makeEdge(a: hostIDs[i],
                                  b: hostIDs[j],
                                  aViewB: views[hostIDs[i]]?[hostIDs[j]],
                                  bViewA: views[hostIDs[j]]?[hostIDs[i]]))
        }
    }

    let rows = hostIDs.map { id -> MeshHostRow in
        let incident = edges.filter { $0.connects(id) }
        let observed = incident.filter { $0.health != .unknown }
        return MeshHostRow(id: id,
                           reachable: reachable[id] ?? false,
                           error: errors[id],
                           edges: incident,
                           health: worstHealth(observed.map(\.health)) ?? .unknown)
    }

    let observedEdges = edges.filter { $0.health != .unknown }
    let healthyEdges = observedEdges.filter { $0.health == .healthy }
    return FleetMesh(origin: response.origin,
                     hosts: rows,
                     edges: edges,
                     observedEdgeCount: observedEdges.count,
                     healthyEdgeCount: healthyEdges.count,
                     headerSummary: "\(healthyEdges.count)/\(observedEdges.count) edges healthy")
}

private func makeEdge(a: String,
                      b: String,
                      aViewB: FleetSnapshotPeerResponse?,
                      bViewA: FleetSnapshotPeerResponse?) -> MeshEdge {
    let observations = [aViewB, bViewA].compactMap { $0 }
    if observations.isEmpty {
        return MeshEdge(a: a, b: b, health: .unknown, detail: "not observed")
    }
    let allReady = observations.allSatisfy { ($0.migrationState ?? "") == meshReadyMigrationState }
    let anyActive = observations.contains { $0.sshActive == true }
    let health: MeshEdgeHealth = allReady ? .healthy : .degraded
    let liveness = anyActive ? "live" : "idle"
    let detail: String
    switch health {
    case .healthy:
        detail = "provisioned · \(liveness)"
    case .degraded:
        detail = "incomplete — run Repair mesh"
    case .unknown:
        detail = "not observed"
    }
    return MeshEdge(a: a, b: b, health: health, detail: detail)
}

/// worstHealth returns the most-degraded health among observed edges, or nil when the
/// list is empty (no observed edges).
private func worstHealth(_ healths: [MeshEdgeHealth]) -> MeshEdgeHealth? {
    if healths.isEmpty { return nil }
    if healths.contains(.degraded) { return .degraded }
    if healths.contains(.healthy) { return .healthy }
    return .unknown
}

// MARK: - Per-row mesh lookup (Settings → Fleet rows)

/// The mesh row for a given host id, or nil when the fleet view is absent or doesn't
/// include the host. Lets each fleet row pull its own incident-edge health.
func meshHostRow(for hostID: String, in mesh: FleetMesh?) -> MeshHostRow? {
    mesh?.hosts.first { $0.id == hostID }
}

/// A compact per-host mesh summary for a fleet row: "mesh 3/3 edges",
/// "mesh 2/3 · 1 incomplete", "mesh 1/3 · 1 incomplete · 1 unobserved". The leading
/// fraction is healthy-over-total incident edges; suffixes call out incomplete
/// (degraded) and unobserved (unknown) edges so an unobserved edge never reads as broken.
func meshRowSummary(_ row: MeshHostRow) -> String {
    let total = row.edges.count
    let healthy = row.edges.filter { $0.health == .healthy }.count
    let degraded = row.edges.filter { $0.health == .degraded }.count
    let unobserved = row.edges.filter { $0.health == .unknown }.count
    var parts: [String] = []
    if degraded > 0 { parts.append("\(degraded) incomplete") }
    if unobserved > 0 { parts.append("\(unobserved) unobserved") }
    let base = "mesh \(healthy)/\(total)"
    return parts.isEmpty ? "\(base) edges" : "\(base) · " + parts.joined(separator: " · ")
}
