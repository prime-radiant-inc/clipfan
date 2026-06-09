import SwiftUI
import AppKit
import KeyboardShortcuts

@main
struct ClipfanApp: App {
    @StateObject private var daemon = DaemonClient.shared
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    init() {
        DaemonClient.shared.start()
        _ = Updater.shared   // start Sparkle's background update checks
        KeyboardShortcuts.onKeyDown(for: .toggleClipboard) {
            CommandPanelController.shared.toggle()
        }
    }

    var body: some Scene {
        MenuBarExtra {
            StatusMenuView()
                .environmentObject(daemon)
        } label: {
            MenuBarLabel()
                .environmentObject(daemon)
        }
        .menuBarExtraStyle(.window)

        Window("clipfan", id: "settings") {
            SettingsView()
                .environmentObject(daemon)
                .frame(minWidth: 720, minHeight: 480)
        }
        .windowResizability(.contentMinSize)
    }
}

/// The always-present menubar icon. Rendered at launch, so it's the reliable
/// place to capture the scene's `openWindow` action for AppKit call sites.
private struct MenuBarLabel: View {
    @Environment(\.openWindow) private var openWindow
    @EnvironmentObject private var daemon: DaemonClient
    @State private var tracker = MenuBarCopyAnimationTracker()
    @State private var isAnimatingCopy = false
    @State private var animationGeneration = 0

    private var latestHistoryID: String? {
        daemon.history.first?.id
    }

    var body: some View {
        ClipfanMenuBarIcon(isAnimatingCopy: isAnimatingCopy, animationGeneration: animationGeneration)
            .task { WindowOpener.shared.openWindow = openWindow }
            .onAppear {
                if daemon.historyLoaded {
                    tracker.seedInitialHistory(latestHistoryID)
                }
            }
            .onChange(of: daemon.historyLoaded) { loaded in
                guard loaded else { return }
                tracker.seedInitialHistory(latestHistoryID)
            }
            .onChange(of: latestHistoryID) { historyID in
                guard daemon.historyLoaded,
                      tracker.shouldAnimate(latestHistoryID: historyID) else { return }
                triggerCopyAnimation()
            }
    }

    private func triggerCopyAnimation() {
        animationGeneration += 1
        let generation = animationGeneration
        isAnimatingCopy = true
        Task {
            try? await Task.sleep(nanoseconds: MenuBarFanInsertTiming.quickMenuBar.dismissDelayNanoseconds)
            await MainActor.run {
                guard generation == animationGeneration else { return }
                isAnimatingCopy = false
            }
        }
    }
}

/// Decides on launch whether the daemon is healthy, needs a kickstart, or the app
/// is being run for the first time and must install + onboard.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        ApplicationActivationController.shared.start()
        Task { @MainActor in
            await DaemonClient.shared.refresh()
            let binaryInstalled = Bootstrap.binaryInstalled
            let installedBinaryCurrent = Bootstrap.installedBinaryCurrent
            let configV2Present = Bootstrap.configV2Present()
            let supportsConfigV2 = Bootstrap.needsConfigV2CapabilityProbe(
                binaryInstalled: binaryInstalled,
                installedBinaryCurrent: installedBinaryCurrent,
                configV2Present: configV2Present
            ) ? Bootstrap.installedBinarySupportsConfigV2() : true

            switch LaunchDecision.decide(binaryInstalled: binaryInstalled,
                                         daemonHealthy: DaemonClient.shared.connected,
                                         installedBinaryCurrent: installedBinaryCurrent,
                                         configV2Present: configV2Present,
                                         installedBinarySupportsConfigV2: supportsConfigV2) {
            case .normal:
                break
            case .upgradeExisting:
                await BootstrapController.shared.install(mode: .upgradeExisting)
                if case .failed = BootstrapController.shared.state {
                    WelcomeWindowController.shared.show(startInstall: false)
                }
            case .restartExisting:
                await DaemonClient.shared.ensureDaemonRunning()
                await DaemonClient.shared.refresh()
                if !DaemonClient.shared.connected,
                   await BootstrapController.shared.presentStorageRepairIfAvailable() {
                    WelcomeWindowController.shared.show(startInstall: false)
                }
            case .firstRunInstall:
                WelcomeWindowController.shared.show(startInstall: true)
            case .blockedDowngrade:
                BootstrapController.shared.state = .failed(
                    message: "config_v2_downgrade_blocked: install the current Clipfan build before restarting the daemon.",
                    logPath: Bootstrap.installLog.path
                )
                WelcomeWindowController.shared.show(startInstall: false)
            }
            await DaemonClient.shared.refresh()
        }
    }
}

@MainActor
final class ApplicationActivationController {
    static let shared = ApplicationActivationController()
    static let commandPanelWindowIdentifier = NSUserInterfaceItemIdentifier("clipfan.command-panel")

    private var observers: [NSObjectProtocol] = []

    private init() {}

    static func activationPolicy(hasVisibleClipfanWindow: Bool) -> NSApplication.ActivationPolicy {
        hasVisibleClipfanWindow ? .regular : .accessory
    }

    func start() {
        guard observers.isEmpty else { return }

        [
            NSWindow.didBecomeKeyNotification,
            NSWindow.didResignKeyNotification,
            NSWindow.didBecomeMainNotification,
            NSWindow.didResignMainNotification,
            NSWindow.didMiniaturizeNotification,
            NSWindow.didDeminiaturizeNotification,
            NSWindow.willCloseNotification,
        ].forEach { name in
            observers.append(NotificationCenter.default.addObserver(
                forName: name,
                object: nil,
                queue: .main
            ) { [weak self] _ in
                Task { @MainActor [weak self] in
                    self?.update()
                }
            })
        }

        update()
    }

    func update() {
        NSApp.setActivationPolicy(Self.activationPolicy(
            hasVisibleClipfanWindow: Self.hasVisibleClipfanWindow(in: NSApp.windows)
        ))
    }

    static func hasVisibleClipfanWindow(in windows: [NSWindow]) -> Bool {
        windows.contains(where: isVisibleClipfanWindow)
    }

    static func isVisibleClipfanWindow(visible: Bool,
                                       miniaturized: Bool,
                                       panel: Bool,
                                       identifier: NSUserInterfaceItemIdentifier?) -> Bool {
        guard visible, !miniaturized else { return false }
        return !panel || identifier == commandPanelWindowIdentifier
    }

    private static func isVisibleClipfanWindow(_ window: NSWindow) -> Bool {
        isVisibleClipfanWindow(
            visible: window.isVisible,
            miniaturized: window.isMiniaturized,
            panel: window is NSPanel,
            identifier: window.identifier
        )
    }
}
