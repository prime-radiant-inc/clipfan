import SwiftUI

/// Shared health presentation for a fleet Peer so the menubar fleet rows and the
/// Settings fleet cards stay visually consistent (same colors and wording).
extension Peer {
    /// Status dot / accent color: green synced, orange stale-or-failed, gray idle.
    var healthColor: Color {
        if last_push_ok { return .green }
        if let ts = last_push_ts, ts > Date.distantPast { return .orange }
        return .gray
    }

    /// One-word health summary matching healthColor.
    var healthWord: String {
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
