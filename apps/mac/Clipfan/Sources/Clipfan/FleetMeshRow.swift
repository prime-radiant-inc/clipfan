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
}

/// FleetRowMeshSection renders one host's mesh health inline in its fleet row: a
/// summary line (dot + healthy/total edges) that expands to the per-edge breakdown.
/// The whole-mesh Repair action lives at the fleet level, not per row.
struct FleetRowMeshSection: View {
    let host: MeshHostRow
    let expanded: Bool
    let toggle: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Button(action: toggle) {
                HStack(spacing: 6) {
                    Circle().fill(host.health.color).frame(width: 7, height: 7)
                    Text(meshRowSummary(host))
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                    if !host.reachable {
                        Text("· unreachable")
                            .font(.system(size: 10))
                            .foregroundStyle(.orange)
                    }
                    Spacer(minLength: 0)
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: 9))
                        .foregroundStyle(.tertiary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if expanded {
                ForEach(host.edges) { edge in
                    HStack(spacing: 6) {
                        Circle().fill(edge.health.color).frame(width: 6, height: 6)
                        Text(edge.other(than: host.id))
                            .font(.system(size: 11))
                        Spacer(minLength: 0)
                        Text(edge.detail)
                            .font(.system(size: 10))
                            .foregroundStyle(.secondary)
                    }
                    .padding(.leading, 13)
                }
            }
        }
    }
}
