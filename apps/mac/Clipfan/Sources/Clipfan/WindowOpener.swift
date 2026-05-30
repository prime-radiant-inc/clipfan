import SwiftUI
import AppKit

/// Bridges SwiftUI's `openWindow` action to AppKit call sites. The clipboard
/// panel is a manually-hosted `NSPanel` with no scene environment, so it can't
/// read `@Environment(\.openWindow)` directly. The action is captured once at
/// launch from the always-present menubar label and reused from anywhere.
@MainActor
final class WindowOpener {
    static let shared = WindowOpener()
    private init() {}

    var openWindow: OpenWindowAction?

    func openSettings() {
        NSApp.activate(ignoringOtherApps: true)
        openWindow?(id: "settings")
    }
}
