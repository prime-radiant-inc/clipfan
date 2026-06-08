import SwiftUI

extension MeshEdgeHealth {
    /// Status color matching the fleet rows: green provisioned, orange incomplete,
    /// gray unobserved. Unknown is deliberately gray (not red) so an edge we simply
    /// could not observe never reads as broken.
    var color: Color {
        switch self {
        case .healthy: return .green
        case .degraded: return .orange
        case .unknown: return .gray
        }
    }

    var word: String {
        switch self {
        case .healthy: return "healthy"
        case .degraded: return "incomplete"
        case .unknown: return "unknown"
        }
    }
}

/// FleetMeshView shows the whole mesh's edge health, gathered by the daemon from each
/// peer (GET /v1/fleet), and offers a one-click Repair that re-runs mesh-heal. It is a
/// read path otherwise: the Mac never SSHes the fleet itself.
struct FleetMeshView: View {
    @EnvironmentObject var daemon: DaemonClient
    @State private var repairing = false
    @State private var repairResult: String?
    @State private var expandedHost: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            header
            if let mesh = daemon.fleetMesh {
                Text(mesh.headerSummary)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                ForEach(mesh.hosts) { host in
                    hostRow(host)
                }
            } else {
                Text("Mesh health loads once the daemon can reach your peers.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }
            repairControls
        }
        .padding(12)
        .background(Color.secondary.opacity(0.06))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.secondary.opacity(0.12)))
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .task { await daemon.refreshFleet() }
    }

    private var header: some View {
        HStack {
            Text("Mesh health").font(.subheadline.weight(.semibold))
            Spacer()
            if daemon.fleetMeshLoading {
                ProgressView().controlSize(.small)
            }
            Button("Refresh") {
                Task { await daemon.refreshFleet() }
            }
            .buttonStyle(.borderless)
            .font(.system(size: 11))
        }
    }

    private func hostRow(_ host: MeshHostRow) -> some View {
        let isExpanded = expandedHost == host.id
        return VStack(alignment: .leading, spacing: 4) {
            Button {
                expandedHost = isExpanded ? nil : host.id
            } label: {
                HStack(spacing: 8) {
                    Circle().fill(host.health.color).frame(width: 8, height: 8)
                    Text(host.id).font(.system(size: 12, weight: .medium))
                    if !host.reachable {
                        Text("unreachable").font(.system(size: 10)).foregroundStyle(.orange)
                    }
                    Spacer()
                    Text("\(host.edges.filter { $0.health != .unknown }.count)/\(host.edges.count) edges")
                        .font(.system(size: 10)).foregroundStyle(.secondary)
                    Image(systemName: isExpanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: 9)).foregroundStyle(.secondary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if isExpanded {
                ForEach(host.edges) { edge in
                    HStack(spacing: 6) {
                        Circle().fill(edge.health.color).frame(width: 6, height: 6)
                        Text(edge.other(than: host.id))
                            .font(.system(size: 11))
                        Spacer()
                        Text(edge.detail)
                            .font(.system(size: 10))
                            .foregroundStyle(.secondary)
                    }
                    .padding(.leading, 16)
                }
            }
        }
    }

    private var repairControls: some View {
        HStack {
            Button {
                repairMesh()
            } label: {
                Label(repairing ? "Repairing…" : "Repair mesh",
                      systemImage: "wrench.and.screwdriver")
            }
            .disabled(repairing)
            if let repairResult {
                Text(repairResult)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
    }

    private func repairMesh() {
        repairing = true
        repairResult = nil
        Task {
            defer { repairing = false }
            do {
                let report = try await MeshHealClient.heal(regularKnownHosts: "~/.ssh/known_hosts")
                repairResult = report.summary
                await daemon.refreshFleet()
            } catch {
                repairResult = "Repair failed: \(error.localizedDescription)"
            }
        }
    }
}
