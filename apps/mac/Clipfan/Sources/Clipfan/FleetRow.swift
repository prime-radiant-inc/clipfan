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
private func health(for p: Peer, versionStatus: PeerVersionStatus?) -> FleetHealth {
    if p.isSSHTransport {
        return sshHealth(for: p)
    }
    switch versionStatus {
    case .current:
        return .healthy
    case .needsUpdate, .unknown:
        return .attention
    case nil:
        break
    }
    if p.last_push_ok { return .healthy }
    if let ts = p.last_push_ts, ts > Date.distantPast { return .attention }
    return .down
}

private func subtitle(for p: Peer, versionStatus: PeerVersionStatus?) -> String {
    if p.isSSHTransport {
        return sshSubtitle(for: p)
    }
    switch versionStatus {
    case .current:
        return "port \(p.port) · current"
    case .needsUpdate:
        return "port \(p.port) · update available"
    case .unknown:
        return "port \(p.port) · update recommended"
    case nil:
        return p.last_push_ok ? "port \(p.port) · synced" : "port \(p.port)"
    }
}

private func sshHealth(for p: Peer) -> FleetHealth {
    if p.ssh_active == true { return .healthy }
    let status = p.ssh_status?.lowercased()
    if status == "live" || status == "syncing" { return .healthy }
    if status == "attention" || status == "connecting" { return .attention }
    if let error = p.ssh_last_error, !error.isEmpty { return .attention }
    return .down
}

private func sshSubtitle(for p: Peer) -> String {
    switch p.ssh_status?.lowercased() {
    case "syncing":
        return "SSH · syncing"
    case "live":
        return "SSH · live"
    case "connecting":
        return "SSH · connecting"
    case "attention":
        return "SSH · attention"
    default:
        if p.ssh_active == true {
            return p.ssh_pending == true ? "SSH · syncing" : "SSH · live"
        }
        return "SSH · waiting"
    }
}

private func diagnostic(for p: Peer, versionStatus: PeerVersionStatus?) -> String? {
    if p.isSSHTransport {
        var parts = ["transport ssh", "status \(p.ssh_status ?? "waiting")"]
        if let host = p.ssh_host, !host.isEmpty {
            let port = p.ssh_port ?? 22
            if let user = p.ssh_user, !user.isEmpty {
                parts.append("endpoint \(user)@\(host):\(port)")
            } else {
                parts.append("endpoint \(host):\(port)")
            }
        }
        if let connected = p.ssh_last_connect_ts, connected > Date.distantPast {
            parts.append("connected \(peerTimeAgo(connected))")
        }
        if let ack = p.ssh_last_ack_ts, ack > Date.distantPast {
            parts.append("last ack \(peerTimeAgo(ack))")
        }
        if let recv = p.last_recv_ts, recv > Date.distantPast {
            parts.append("last receive \(peerTimeAgo(recv))")
        }
        if let error = p.ssh_last_error, !error.isEmpty {
            parts.append("last error \(error)")
        }
        return parts.joined(separator: "\n")
    }
    switch versionStatus {
    case .current(let version):
        return "peer HTTP current \(version)"
    case .needsUpdate(let version):
        return "peer HTTP update available from \(version)"
    case .unknown:
        return "peer HTTP version unknown"
    case nil:
        return nil
    }
}

/// One row in the unified fleet list — either the local host (isSelf) or a peer.
struct FleetRowModel: Identifiable {
    let id: String          // hostname
    let name: String
    let subtitle: String
    let health: FleetHealth
    let diagnostic: String?
    let isSelf: Bool
    let pushTS: Date?
    let recvTS: Date?
    let peer: Peer?         // nil for the self row
}

/// Build the ordered fleet rows: the local host first, then peers in order.
/// The self row carries no sync times (the local host never pushes to itself).
func fleetRows(origin: String,
               connected: Bool,
               peers: [Peer],
               safeMode: LocalDaemonSafeModeStatus? = nil,
               peerVersions: [String: PeerVersionStatus] = [:],
               policy: SSHTransportGatePolicy = .current) -> [FleetRowModel] {
    let safeModeActive = safeMode?.active == true
    let selfRow = FleetRowModel(
        id: origin,
        name: origin,
        subtitle: safeModeActive ? "this Mac · listener repair required" : (connected ? "this Mac · running" : "this Mac · daemon not running"),
        health: safeModeActive ? .attention : (connected ? .healthy : .down),
        diagnostic: nil,
        isSelf: true,
        pushTS: nil,
        recvTS: nil,
        peer: nil
    )
    if safeModeActive {
        return [selfRow]
    }
    let peerRows = peers.map { p in
        let versionStatus = policy.peerHTTPVersionProbeEnabled ? peerVersions[p.hostname] : nil
        return FleetRowModel(
            id: p.hostname,
            name: p.hostname,
            subtitle: subtitle(for: p, versionStatus: versionStatus),
            health: health(for: p, versionStatus: versionStatus),
            diagnostic: diagnostic(for: p, versionStatus: versionStatus),
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
                        .lineLimit(1)
                        .truncationMode(.middle)
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
                    .lineLimit(1)
            }
            .frame(minWidth: 0, maxWidth: .infinity, alignment: .leading)
            Spacer(minLength: 0)
            if let push = model.pushTS, let recv = model.recvTS {
                VStack(alignment: .trailing, spacing: 2) {
                    Text("↑ \(peerTimeAgo(push))")
                    Text("↓ \(peerTimeAgo(recv))")
                }
                .font(.system(size: 10))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: true, vertical: false)
            }
        }
        .help(model.diagnostic ?? model.subtitle)
    }
}
