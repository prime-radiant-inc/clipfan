import SwiftUI
import AppKit

/// Floating HUD panel that hosts the command panel. Becomes key without
/// activating the whole app, and closes when it loses key (click-away) or on Esc.
final class CommandPanelController: NSObject, NSWindowDelegate {
    static let shared = CommandPanelController()

    private var panel: NSPanel?

    private override init() { super.init() }

    var isVisible: Bool { panel?.isVisible ?? false }

    func toggle() { isVisible ? hide() : show() }

    func show() {
        if let panel {
            position(panel)
            panel.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            return
        }

        let view = CommandPanelView(
            daemon: .shared,
            onPaste: { [weak self] in self?.hide() },
            onDismiss: { [weak self] in self?.hide() }
        )
        let hosting = NSHostingView(rootView: view)

        let panel = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 660, height: 440),
            styleMask: [.titled, .fullSizeContentView, .nonactivatingPanel, .resizable],
            backing: .buffered,
            defer: true
        )
        panel.titleVisibility = .hidden
        panel.titlebarAppearsTransparent = true
        panel.isMovableByWindowBackground = true
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.isReleasedWhenClosed = false
        panel.standardWindowButton(.closeButton)?.isHidden = true
        panel.standardWindowButton(.miniaturizeButton)?.isHidden = true
        panel.standardWindowButton(.zoomButton)?.isHidden = true
        panel.contentView = hosting
        panel.delegate = self
        panel.backgroundColor = .clear

        // Rounded corners on the panel content.
        hosting.wantsLayer = true
        hosting.layer?.cornerRadius = 12
        hosting.layer?.masksToBounds = true

        self.panel = panel
        position(panel)
        panel.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func hide() {
        panel?.orderOut(nil)
    }

    private func position(_ panel: NSPanel) {
        guard let screen = NSScreen.main else { panel.center(); return }
        let f = panel.frame
        let visible = screen.visibleFrame
        let x = visible.midX - f.width / 2
        // Slightly above vertical center reads better, Spotlight-style.
        let y = visible.midY - f.height / 2 + visible.height * 0.08
        panel.setFrameOrigin(NSPoint(x: x, y: y))
    }

    // Close on click-away.
    func windowDidResignKey(_ notification: Notification) {
        hide()
    }
}
