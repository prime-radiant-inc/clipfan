import SwiftUI
import AppKit

/// First-run setup window. Reflects BootstrapController state: a brief welcome
/// while install.sh runs, a success screen that teaches the two things a new user
/// needs (the hotkey and how paste works), or a failure with Retry + View log.
struct WelcomeView: View {
    @ObservedObject var bootstrap: BootstrapController
    var onClose: () -> Void

    var body: some View {
        VStack(spacing: 20) {
            header
            content
        }
        .padding(32)
        .frame(width: 460)
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

    @ViewBuilder private var content: some View {
        switch bootstrap.state {
        case .idle, .installing:
            installing
        case .success:
            success
        case let .failed(message, logPath):
            failed(message: message, logPath: logPath)
        }
    }

    // MARK: states

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

    private var success: some View {
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
            Button("Done", action: onClose)
                .keyboardShortcut(.defaultAction)
                .controlSize(.large)
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
