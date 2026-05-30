import SwiftUI
import AppKit

/// Hosts the first-run Welcome window. A menubar (LSUIElement) app can't rely on
/// SwiftUI's openWindow at launch, so we manage a standard titled NSWindow
/// directly — mirroring CommandPanelController. Shown on first run and reopenable
/// from Settings for recovery.
@MainActor
final class WelcomeWindowController: NSObject, NSWindowDelegate {
    static let shared = WelcomeWindowController()

    private var window: NSWindow?

    private override init() { super.init() }

    /// Show the Welcome window and, when `startInstall` is set, kick off the
    /// first-run install. Bringing an LSUIElement app forward needs an explicit
    /// activate.
    func show(startInstall: Bool) {
        if startInstall {
            Task { await BootstrapController.shared.install() }
        }

        if let window {
            window.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            return
        }

        let view = WelcomeView(bootstrap: .shared) { [weak self] in self?.close() }
        let hosting = NSHostingView(rootView: view)

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 460, height: 360),
            styleMask: [.titled, .closable, .fullSizeContentView],
            backing: .buffered,
            defer: true
        )
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.isMovableByWindowBackground = true
        window.isReleasedWhenClosed = false
        window.standardWindowButton(.miniaturizeButton)?.isHidden = true
        window.standardWindowButton(.zoomButton)?.isHidden = true
        window.contentView = hosting
        window.delegate = self
        window.center()

        self.window = window
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func close() {
        window?.orderOut(nil)
    }
}
