import Foundation

enum TailscaleClient {
    /// Shell out to `tailscale status --json` and return the live tailnet
    /// peers we could install clipfan on. Self is excluded.
    static func status() async throws -> [TailscalePeer] {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        proc.arguments = ["tailscale", "status", "--json"]
        let pipe = Pipe()
        proc.standardOutput = pipe
        try proc.run()
        proc.waitUntilExit()
        guard proc.terminationStatus == 0 else {
            throw NSError(domain: "tailscale", code: Int(proc.terminationStatus),
                          userInfo: [NSLocalizedDescriptionKey: "tailscale status failed (is tailscale installed and authed?)"])
        }
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let raw = try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]

        var peers: [TailscalePeer] = []
        if let peerDict = raw["Peer"] as? [String: [String: Any]] {
            for (_, info) in peerDict {
                guard let host = info["HostName"] as? String,
                      let online = info["Online"] as? Bool,
                      let os = info["OS"] as? String else { continue }
                let dnsName = (info["DNSName"] as? String) ?? ""
                let ips = (info["TailscaleIPs"] as? [String]) ?? []
                let user = (info["UserID"] as? Int).map { "uid-\($0)" } ?? ""
                peers.append(TailscalePeer(
                    hostName: host,
                    dnsName: dnsName,
                    ip: ips.first ?? "",
                    os: os,
                    online: online,
                    user: user
                ))
            }
        }
        return peers.sorted { a, b in
            if a.online != b.online { return a.online && !b.online }
            return a.hostName < b.hostName
        }
    }
}
