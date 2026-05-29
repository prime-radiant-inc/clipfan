import SwiftUI

/// The menubar popover (MenuBarExtra .window style) — a small custom panel so the
/// fleet can show colored health dots and bidirectional sync times, matching the
/// Settings → Fleet cards. The native .menu style can't render colored dots.
struct StatusMenuView: View {
    @EnvironmentObject var daemon: DaemonClient
    @Environment(\.openWindow) var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()

            menuButton("Open Clipboard", systemImage: "doc.on.clipboard", shortcut: "⇧⌘V") {
                CommandPanelController.shared.show()
            }
            menuButton("Settings…", systemImage: "gearshape", shortcut: "⌘,") {
                NSApp.activate(ignoringOtherApps: true)
                openWindow(id: "settings")
            }
            menuButton("Quit", systemImage: "power", shortcut: "⌘Q") {
                NSApp.terminate(nil)
            }

            Divider()

            fleet
        }
        .padding(8)
        .frame(width: 280)
    }

    // MARK: header

    private var header: some View {
        HStack(spacing: 8) {
            Image(systemName: "doc.on.clipboard")
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 1) {
                Text("clipfan").font(.system(size: 13, weight: .semibold))
                Text(daemon.connected ? "this Mac · \(daemon.origin)" : "daemon not running")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
    }

    // MARK: fleet

    @ViewBuilder private var fleet: some View {
        if daemon.peers.isEmpty {
            Text("No peers yet — add one in Settings")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .padding(.horizontal, 8)
                .padding(.vertical, 6)
        } else {
            Text("FLEET")
                .font(.system(size: 9, weight: .semibold))
                .foregroundStyle(.tertiary)
                .padding(.horizontal, 8)
                .padding(.top, 6).padding(.bottom, 2)
            ForEach(daemon.peers) { peer in
                Button {
                    NSApp.activate(ignoringOtherApps: true)
                    openWindow(id: "settings")
                } label: {
                    HStack(alignment: .top, spacing: 8) {
                        Circle().fill(peer.healthColor).frame(width: 8, height: 8)
                            .padding(.top, 4)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(peer.hostname).font(.system(size: 12))
                            Text("↑ \(peerTimeAgo(peer.last_push_ts))   ↓ \(peerTimeAgo(peer.last_recv_ts))")
                                .font(.system(size: 10))
                                .foregroundStyle(.secondary)
                        }
                        Spacer(minLength: 0)
                    }
                    .contentShape(Rectangle())
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                }
                .buttonStyle(MenuRowButtonStyle())
            }
        }
    }

    // MARK: row helpers

    private func menuButton(_ title: String, systemImage: String, shortcut: String,
                            action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Image(systemName: systemImage)
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
                    .frame(width: 16)
                Text(title).font(.system(size: 13))
                Spacer()
                Text(shortcut).font(.system(size: 11)).foregroundStyle(.tertiary)
            }
            .contentShape(Rectangle())
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
        }
        .buttonStyle(MenuRowButtonStyle())
    }
}

/// A menu-row button: transparent at rest, accent-highlighted on hover, so the
/// custom popover feels like a native menu.
private struct MenuRowButtonStyle: ButtonStyle {
    @State private var hovering = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(hovering ? Color.white : Color.primary)
            .background(
                RoundedRectangle(cornerRadius: 6)
                    .fill(hovering ? Color.accentColor : Color.clear)
            )
            .contentShape(Rectangle())
            .onHover { hovering = $0 }
    }
}
