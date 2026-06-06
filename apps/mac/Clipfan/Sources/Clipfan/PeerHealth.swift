import SwiftUI

/// Shared health presentation for a fleet Peer so the menubar fleet rows and the
/// Settings fleet cards stay visually consistent (same colors and wording).
extension Peer {
    /// Status dot / accent color: green synced, orange stale-or-failed, gray idle.
    var healthColor: Color {
        if isSSHTransport {
            if ssh_active == true { return .green }
            let status = ssh_status?.lowercased()
            if status == "live" || status == "syncing" { return .green }
            if status == "attention" || status == "connecting" { return .orange }
            if let error = ssh_last_error, !error.isEmpty { return .orange }
            return .gray
        }
        if last_push_ok { return .green }
        if let ts = last_push_ts, ts > Date.distantPast { return .orange }
        return .gray
    }

    /// One-word health summary matching healthColor.
    var healthWord: String {
        if isSSHTransport {
            if ssh_active == true {
                return ssh_pending == true ? "syncing" : "live"
            }
            switch ssh_status?.lowercased() {
            case "syncing": return "syncing"
            case "live": return "live"
            case "connecting": return "connecting"
            case "attention": return "attention"
            default:
                if let error = ssh_last_error, !error.isEmpty {
                    return "attention"
                }
                return "waiting"
            }
        }
        if last_push_ok { return "healthy" }
        if let ts = last_push_ts, ts > Date.distantPast { return "offline" }
        return "idle"
    }
}

/// Relative "time ago" for a peer timestamp, or "never" for the unset sentinel.
func peerTimeAgo(_ date: Date?) -> String {
    guard let date, date > Date.distantPast else { return "never" }
    let f = RelativeDateTimeFormatter()
    f.unitsStyle = .short
    return f.localizedString(for: date, relativeTo: Date())
}
