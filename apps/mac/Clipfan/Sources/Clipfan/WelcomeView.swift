import SwiftUI
import AppKit

/// First-run setup wizard. Steps the user through a brief welcome, the local daemon
/// install (install.sh), an optional add-a-device step, and a done screen with tips
/// and a fleet summary. The step transitions live in OnboardingStep (pure, tested);
/// this view renders each step and reuses AddPeerSheet for the add-device flow.
struct WelcomeView: View {
    @ObservedObject var bootstrap: BootstrapController
    @ObservedObject private var daemon = DaemonClient.shared
    var onClose: () -> Void

    @State private var step: OnboardingStep = .welcome
    @State private var showAddPeer = false

    var body: some View {
        VStack(spacing: 20) {
            header
            stepIndicator
            content
        }
        .padding(32)
        .frame(width: 460)
        .onChange(of: bootstrap.state) { newState in
            step = step.advanced(on: newState)
        }
        .sheet(isPresented: $showAddPeer) { AddPeerSheet() }
    }

    private var header: some View {
        VStack(spacing: 10) {
            Image(systemName: "doc.on.clipboard.fill")
                .font(.system(size: 40, weight: .light))
                .foregroundStyle(.tint)
            Text("Welcome to clipfan")
                .font(.system(size: 22, weight: .semibold))
        }
    }

    /// ●─○─○─○ progress dots, one per OnboardingStep.
    private var stepIndicator: some View {
        HStack(spacing: 8) {
            ForEach(OnboardingStep.allCases, id: \.self) { s in
                Circle()
                    .fill(s.stepIndex <= step.stepIndex ? Color.accentColor : Color.secondary.opacity(0.25))
                    .frame(width: 7, height: 7)
            }
        }
    }

    @ViewBuilder private var content: some View {
        switch step {
        case .welcome:
            welcomeIntro
        case .localSetup:
            localSetup
        case .addHost:
            addHostStep
        case .done:
            doneStep
        }
    }

    // MARK: steps

    private var welcomeIntro: some View {
        VStack(spacing: 16) {
            Text("clipfan keeps your clipboard in sync across your Macs and Linux boxes — copy on one, paste on another.")
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
            Button("Get started") {
                step = Bootstrap.installedBinaryCurrent ? .addHost : .localSetup
            }
            .keyboardShortcut(.defaultAction)
            .controlSize(.large)
        }
    }

    @ViewBuilder private var localSetup: some View {
        switch bootstrap.state {
        case .idle, .installing:
            installing
        case .success:
            // advanced(on:) moves us to .addHost; render the installing view until it does.
            installing
        case let .failed(message, logPath):
            failed(message: message, logPath: logPath)
        }
    }

    private var addHostStep: some View {
        VStack(spacing: 16) {
            Image(systemName: "laptopcomputer.and.iphone")
                .font(.system(size: 28))
                .foregroundStyle(.tint)
            Text("Add another device").font(.system(size: 16, weight: .medium))
            Text("Connect another Mac or Linux box now, or add one later from Settings. Adding a device heals the whole mesh automatically.")
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
                .font(.system(size: 12))
            HStack(spacing: 12) {
                Button("Add a device…") { showAddPeer = true }
                Button("Continue") { step = .done }
                    .keyboardShortcut(.defaultAction)
            }
        }
    }

    private var doneStep: some View {
        VStack(spacing: 16) {
            Image(systemName: "checkmark.circle.fill")
                .font(.system(size: 28))
                .foregroundStyle(.green)
            Text("You're all set.").font(.system(size: 16, weight: .medium))
            VStack(alignment: .leading, spacing: 12) {
                tip("keyboard", "Press ⇧⌘V to open your clipboard history.")
                tip("doc.on.doc", "Pick an item — clipfan copies it, then you press ⌘V to paste.")
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Text(fleetSummary)
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            Button("Done", action: onClose)
                .keyboardShortcut(.defaultAction)
                .controlSize(.large)
        }
    }

    private var fleetSummary: String {
        let count = daemon.peers.count
        switch count {
        case 0: return "No other devices yet — add one anytime from Settings."
        case 1: return "1 device in your fleet."
        default: return "\(count) devices in your fleet."
        }
    }

    // MARK: install states

    private var installing: some View {
        VStack(spacing: 14) {
            Text("Setting up the background service that keeps your clipboard in sync.")
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text(currentStep).font(.system(size: 12)).foregroundStyle(.secondary)
            }
        }
    }

    private func failed(message: String, logPath: String) -> some View {
        VStack(spacing: 16) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 28))
                .foregroundStyle(.orange)
            Text("Setup didn't finish").font(.system(size: 16, weight: .medium))
            Text(message)
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
                .font(.system(size: 12))
            if let repair = LocalStorageRepair.prompt(message: message) {
                storageRepair(repair)
            }
            HStack(spacing: 12) {
                if FileManager.default.fileExists(atPath: logPath) {
                    Button("View log") {
                        NSWorkspace.shared.open(URL(fileURLWithPath: logPath))
                    }
                }
                Button("Retry") {
                    Task { await bootstrap.install() }
                }
                .keyboardShortcut(.defaultAction)
            }
        }
    }

    private func storageRepair(_ prompt: LocalStorageRepairPrompt) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(prompt.title)
                .font(.system(size: 13, weight: .semibold))
            Text(prompt.message)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
            Text("Code: \(prompt.code.rawValue)")
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(.secondary)
            ForEach(Array(prompt.roots.enumerated()), id: \.offset) { _, root in
                VStack(alignment: .leading, spacing: 3) {
                    Text("\(root.role): \(root.normalizedPath)")
                        .font(.system(size: 11, design: .monospaced))
                    if let storageClass = root.storageClass {
                        Text("Storage: \(storageClass)")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }
                    if let reason = root.reason {
                        Text("Reason: \(reason)")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    // MARK: helpers

    private func tip(_ symbol: String, _ text: String) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: symbol)
                .font(.system(size: 13))
                .foregroundStyle(.tint)
                .frame(width: 18)
            Text(text).font(.system(size: 13))
        }
    }

    private var currentStep: String {
        if case let .installing(progress) = bootstrap.state, let last = progress.last {
            return last
        }
        return "Preparing…"
    }
}
