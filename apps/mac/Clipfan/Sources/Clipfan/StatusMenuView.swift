import KeyboardShortcuts
import SwiftUI

/// The menubar popover (MenuBarExtra .window style) — a small custom panel so the
/// fleet can show colored health dots and bidirectional sync times, matching the
/// Settings → Fleet cards. The native .menu style can't render colored dots.
struct StatusMenuView: View {
    @EnvironmentObject var daemon: DaemonClient
    @Environment(\.openWindow) var openWindow
    @ObservedObject private var updater = Updater.shared

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(statusMenuCommandRows(toggleShortcutLabel: toggleShortcutLabel,
                                          appUpdateAvailable: updater.isUpdateAvailable)) { row in
                menuButton(row.title, shortcut: row.shortcut) {
                    perform(row.command)
                }
            }

            Divider()

            fleet
        }
        .padding(8)
        .frame(width: 360)
    }

    /// The current global toggle shortcut as a display string, or "" if unset.
    private var toggleShortcutLabel: String {
        KeyboardShortcuts.getShortcut(for: .toggleClipboard).map { "\($0)" } ?? ""
    }

    private func perform(_ command: StatusMenuCommand) {
        switch command {
        case .openClipboard:
            CommandPanelController.shared.show()
        case .installUpdate:
            Updater.shared.presentAvailableUpdate()
        case .settings:
            NSApp.activate(ignoringOtherApps: true)
            openWindow(id: "settings")
        case .quit:
            NSApp.terminate(nil)
        }
    }

    // MARK: fleet

    @ViewBuilder private var fleet: some View {
        Text("FLEET")
            .font(.system(size: 9, weight: .semibold))
            .foregroundStyle(.tertiary)
            .padding(.horizontal, 8)
            .padding(.top, 6).padding(.bottom, 2)
        ForEach(fleetRows(origin: daemon.origin,
                          connected: daemon.connected,
                          peers: daemon.peers)) { row in
            Button {
                NSApp.activate(ignoringOtherApps: true)
                openWindow(id: "settings")
            } label: {
                FleetRow(model: row)
                    .contentShape(Rectangle())
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
            }
            .buttonStyle(MenuRowButtonStyle())
            .focusable(false)
        }
    }

    // MARK: row helpers

    private func menuButton(_ title: String, shortcut: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Text(title).font(.system(size: 13))
                    .lineLimit(1)
                Spacer()
                Text(shortcut).font(.system(size: 11)).foregroundStyle(.tertiary)
            }
            .contentShape(Rectangle())
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
        }
        .buttonStyle(MenuRowButtonStyle())
        .focusable(false)
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
