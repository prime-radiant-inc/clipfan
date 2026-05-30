import SwiftUI

/// Health of a fleet member, mapped to a dot color. Colors mirror PeerHealth:
/// green synced, orange stale-or-failed, gray idle/down.
enum FleetHealth {
    case healthy, attention, down

    var color: Color {
        switch self {
        case .healthy:   return .green
        case .attention: return .orange
        case .down:      return .gray
        }
    }
}

/// Map a peer's push state to FleetHealth, mirroring Peer.healthColor exactly
/// (avoids fragile SwiftUI Color equality checks).
private func health(for p: Peer) -> FleetHealth {
    if p.last_push_ok { return .healthy }
    if let ts = p.last_push_ts, ts > Date.distantPast { return .attention }
    return .down
}

/// One row in the unified fleet list — either the local host (isSelf) or a peer.
struct FleetRowModel: Identifiable {
    let id: String          // hostname
    let name: String
    let subtitle: String
    let health: FleetHealth
    let isSelf: Bool
    let pushTS: Date?
    let recvTS: Date?
    let peer: Peer?         // nil for the self row
}

/// Build the ordered fleet rows: the local host first, then peers in order.
/// The self row carries no sync times (the local host never pushes to itself).
func fleetRows(origin: String, connected: Bool, peers: [Peer]) -> [FleetRowModel] {
    let selfRow = FleetRowModel(
        id: origin,
        name: origin,
        subtitle: connected ? "this Mac · running" : "this Mac · daemon not running",
        health: connected ? .healthy : .down,
        isSelf: true,
        pushTS: nil,
        recvTS: nil,
        peer: nil
    )
    let peerRows = peers.map { p in
        FleetRowModel(
            id: p.hostname,
            name: p.hostname,
            subtitle: p.last_push_ok ? "port \(p.port) · synced" : "port \(p.port)",
            health: health(for: p),
            isSelf: false,
            pushTS: p.last_push_ts,
            recvTS: p.last_recv_ts,
            peer: p
        )
    }
    return [selfRow] + peerRows
}

/// A small colored health dot.
struct HealthDot: View {
    let health: FleetHealth
    var size: CGFloat = 9
    var body: some View {
        Circle().fill(health.color).frame(width: size, height: size)
    }
}

/// One fleet row rendered identically in the dropdown and Settings → Fleet.
struct FleetRow: View {
    let model: FleetRowModel

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            HealthDot(health: model.health)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(model.name).font(.system(size: 13, weight: .semibold))
                    if model.isSelf {
                        Text("you")
                            .font(.system(size: 9.5))
                            .padding(.horizontal, 6).padding(.vertical, 1)
                            .background(Color.accentColor.opacity(0.18))
                            .clipShape(Capsule())
                            .foregroundStyle(Color.accentColor)
                    }
                }
                Text(model.subtitle).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
            if let push = model.pushTS, let recv = model.recvTS {
                Text("↑ \(peerTimeAgo(push))   ↓ \(peerTimeAgo(recv))")
                    .font(.system(size: 10)).foregroundStyle(.secondary)
            }
        }
    }
}
