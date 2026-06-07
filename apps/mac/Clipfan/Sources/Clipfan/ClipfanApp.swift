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

        Window("About clipfan", id: "about") {
            AboutView()
                .environmentObject(daemon)
        }
        .windowResizability(.contentSize)
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
        ClipfanMenuBarIcon(isAnimatingCopy: isAnimatingCopy)
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
        withAnimation(.spring(response: 0.28, dampingFraction: 0.72)) {
            isAnimatingCopy = true
        }
        Task {
            try? await Task.sleep(nanoseconds: 520_000_000)
            await MainActor.run {
                guard generation == animationGeneration else { return }
                withAnimation(.easeOut(duration: 0.12)) {
                    isAnimatingCopy = false
                }
            }
        }
    }
}

/// Decides on launch whether the daemon is healthy, needs a kickstart, or the app
/// is being run for the first time and must install + onboard.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
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
            await RemoteUpdateOfferController.shared.maybeOffer()
        }
    }
}
