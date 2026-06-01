import SwiftUI
import AppKit

/// Floating HUD panel that hosts the command panel. Becomes key without
/// activating the whole app, and closes when it loses key (click-away) or on Esc.
@MainActor
final class CommandPanelController: NSObject, NSWindowDelegate {
    static let shared = CommandPanelController()

    private var panel: NSPanel?

    /// Guards click-away dismissal. Showing the panel and calling NSApp.activate
    /// can transiently bounce key-window status, firing windowDidResignKey before
    /// the panel is even on screen — which would make it flash and vanish. We only
    /// honor resignKey after the run loop settles following a show.
    private var dismissOnResignKey = false

    private override init() { super.init() }

    var isVisible: Bool { panel?.isVisible ?? false }

    func toggle() { isVisible ? hide() : show() }

    func show() {
        if let panel {
            present(panel)
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
            styleMask: [.titled, .closable, .nonactivatingPanel, .resizable],
            backing: .buffered,
            defer: true
        )
        panel.title = "Clipboard"
        panel.titleVisibility = .visible
        panel.titlebarAppearsTransparent = false
        panel.isMovableByWindowBackground = true
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.isReleasedWhenClosed = false
        // Close hides the panel (handled in windowShouldClose); minimize and zoom
        // are meaningless on a transient HUD, so present-but-disabled.
        panel.standardWindowButton(.miniaturizeButton)?.isEnabled = false
        panel.standardWindowButton(.zoomButton)?.isEnabled = false
        panel.contentView = hosting
        panel.delegate = self
        // The titled window supplies its own frame and corner rounding; the
        // content fills it edge to edge (no inset rounded card).

        self.panel = panel
        present(panel)
    }

    /// Order the panel front and arm click-away dismissal one run-loop tick later,
    /// once the activation-time key-window churn has settled.
    private func present(_ panel: NSPanel) {
        dismissOnResignKey = false
        position(panel)
        panel.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        DispatchQueue.main.async { [weak self] in
            self?.dismissOnResignKey = true
        }
    }

    func hide() {
        dismissOnResignKey = false
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

    // Close on click-away, but only once a show has fully settled.
    func windowDidResignKey(_ notification: Notification) {
        guard dismissOnResignKey else { return }
        hide()
    }

    // The close button hides the panel (like Esc / click-away) rather than
    // destroying it, so re-summoning is instant.
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        hide()
        return false
    }
}
