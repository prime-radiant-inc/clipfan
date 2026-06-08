import Foundation

/// MeshHealReport mirrors the JSON `clipfan mesh-heal` writes to stdout: per-edge
/// healing outcomes plus the hosts the run could not reach. Arrays decode tolerantly
/// (a null or omitted field becomes empty) so a partial report still parses.
struct MeshHealReport: Codable, Equatable {
    var healed: [String]
    var skipped: [String]
    var failed: [MeshHealFailure]
    var restarted: [String]
    var unreachable: [MeshHealUnreachable]

    init(healed: [String] = [],
         skipped: [String] = [],
         failed: [MeshHealFailure] = [],
         restarted: [String] = [],
         unreachable: [MeshHealUnreachable] = []) {
        self.healed = healed
        self.skipped = skipped
        self.failed = failed
        self.restarted = restarted
        self.unreachable = unreachable
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        healed = try container.decodeIfPresent([String].self, forKey: .healed) ?? []
        skipped = try container.decodeIfPresent([String].self, forKey: .skipped) ?? []
        failed = try container.decodeIfPresent([MeshHealFailure].self, forKey: .failed) ?? []
        restarted = try container.decodeIfPresent([String].self, forKey: .restarted) ?? []
        unreachable = try container.decodeIfPresent([MeshHealUnreachable].self, forKey: .unreachable) ?? []
    }
}

/// MeshHealFailure is one edge mesh-heal could not bring healthy.
struct MeshHealFailure: Codable, Equatable {
    var edge: String
    var reason: String
}

/// MeshHealUnreachable is one host mesh-heal could not contact during discovery.
struct MeshHealUnreachable: Codable, Equatable {
    var id: String
    var reason: String
}

extension MeshHealReport {
    /// True when every reachable edge ended healthy: nothing failed and no host was
    /// unreachable.
    var isFullyHealthy: Bool { failed.isEmpty && unreachable.isEmpty }

    /// Edges this run left healthy: those it provisioned plus those already-correct and
    /// skipped.
    var healthyEdgeCount: Int { healed.count + skipped.count }

    /// A short user-facing summary, e.g. "mesh healed (6 edges)" or
    /// "mesh healed (5 edges) · 1 edge failed · 1 host unreachable".
    var summary: String {
        var parts = ["mesh healed (\(healthyEdgeCount) \(pluralize("edge", healthyEdgeCount)))"]
        if !failed.isEmpty {
            parts.append("\(failed.count) \(pluralize("edge", failed.count)) failed")
        }
        if !unreachable.isEmpty {
            parts.append("\(unreachable.count) \(pluralize("host", unreachable.count)) unreachable")
        }
        return parts.joined(separator: " · ")
    }

    private func pluralize(_ noun: String, _ count: Int) -> String {
        count == 1 ? noun : noun + "s"
    }
}

/// decodeMeshHealReport parses the JSON `clipfan mesh-heal` writes to stdout.
func decodeMeshHealReport(_ data: Data) throws -> MeshHealReport {
    try JSONDecoder().decode(MeshHealReport.self, from: data)
}

/// MeshHealClient drives `clipfan mesh-heal`, which discovers the local fleet's roster
/// from config and brings every edge to a full mesh — provisioning a freshly-added
/// host's cross-edges and restarting the daemons it changed. It is the self-healing
/// follow-up to a pairwise provision, and the engine behind the "Repair mesh" action.
enum MeshHealClient {
    /// heal runs mesh-heal against the local fleet and returns its decoded report.
    /// trust-keyscan is always passed: mesh-heal hard-fails without it, mirroring
    /// ssh-provision-direct, because it must trust each endpoint's host key to reach it.
    static func heal(regularKnownHosts: String,
                     localProvisioningBinary: Installer.LocalProvisioningBinaryResolver = { try Installer.trustedLocalProvisioningBinaryPath() },
                     runCommand: Installer.CommandRunner = Installer.run) async throws -> MeshHealReport {
        let knownHosts = expandingHome(regularKnownHosts.trimmingCharacters(in: .whitespacesAndNewlines))
        guard !knownHosts.isEmpty else {
            throw InstallError.configIO("regular_known_hosts_required")
        }
        let binary = try localProvisioningBinary()
        let args = ["mesh-heal", "--trust-keyscan", "--regular-known-hosts", knownHosts]
        let output = try await runCommand(binary, args)
        guard let data = output.data(using: .utf8) else {
            throw InstallError.configIO("mesh_heal_output_not_utf8")
        }
        return try decodeMeshHealReport(data)
    }

    private static func expandingHome(_ path: String) -> String {
        if path == "~" { return NSHomeDirectory() }
        if path.hasPrefix("~/") { return NSHomeDirectory() + String(path.dropFirst(1)) }
        return path
    }
}
