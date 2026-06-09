import AppKit
import SwiftUI

let aboutGitHubURL = URL(string: "https://github.com/prime-radiant-inc/clipfan")!
let aboutAppIconResourceName = "AppIcon"

/// App version + daemon version on one line; "—" when a source is unavailable.
func aboutVersionSummary(appVersion: String?, daemonVersion: String?) -> String {
    "App \(appVersion ?? "—") · Daemon \(daemonVersion ?? "—")"
}

func aboutAppIconImage(bundle: Bundle = .main) -> NSImage {
    if let url = bundle.url(forResource: aboutAppIconResourceName, withExtension: "icns"),
       let image = NSImage(contentsOf: url) {
        return image
    }
    if let image = NSImage(named: NSImage.Name(aboutAppIconResourceName)) {
        return image
    }
    return NSApp.applicationIconImage
}

struct AboutView: View {
    @EnvironmentObject private var daemon: DaemonClient

    private var appVersion: String? {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String
    }

    var body: some View {
        VStack(spacing: 14) {
            Image(nsImage: aboutAppIconImage())
                .resizable()
                .frame(width: 64, height: 64)
            Text("clipfan").font(.system(size: 20, weight: .semibold))
            Text("One clipboard across your Macs and Linux hosts.")
                .font(.callout).foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            Text(aboutVersionSummary(appVersion: appVersion, daemonVersion: daemon.version))
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(.secondary)
            Link("GitHub", destination: aboutGitHubURL)
                .font(.callout)
        }
        .padding(28)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
