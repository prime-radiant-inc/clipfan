import SwiftUI

/// App version + daemon version on one line; "—" when a source is unavailable.
func aboutVersionSummary(appVersion: String?, daemonVersion: String?) -> String {
    "App \(appVersion ?? "—") · Daemon \(daemonVersion ?? "—")"
}

struct AboutView: View {
    @EnvironmentObject private var daemon: DaemonClient

    private var appVersion: String? {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String
    }

    var body: some View {
        VStack(spacing: 14) {
            Image(systemName: "doc.on.clipboard.fill")
                .font(.system(size: 40, weight: .light))
                .foregroundStyle(.tint)
            Text("clipfan").font(.system(size: 20, weight: .semibold))
            Text("One clipboard across your Macs and Linux hosts.")
                .font(.callout).foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            Text(aboutVersionSummary(appVersion: appVersion, daemonVersion: daemon.version))
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(.secondary)
            Link("Documentation", destination: URL(string: "https://github.com/prime-radiant-inc/clipfan")!)
                .font(.callout)
        }
        .padding(28)
        .frame(width: 360, height: 260)
    }
}
