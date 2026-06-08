import Foundation

/// OnboardingStep is the first-run wizard's position: a short welcome, the local
/// daemon setup (install.sh), an optional add-a-host step, and a done screen with
/// tips and a fleet summary. Transitions are pure so they can be tested without the
/// SwiftUI window.
enum OnboardingStep: Int, Equatable, CaseIterable {
    case welcome
    case localSetup
    case addHost
    case done

    /// The step a user-driven "Continue" / "Skip" advances to; nil when terminal.
    /// addHost is skippable — both skipping and finishing the add lead to done.
    var next: OnboardingStep? {
        switch self {
        case .welcome: return .localSetup
        case .localSetup: return .addHost
        case .addHost: return .done
        case .done: return nil
        }
    }

    /// Advance automatically when local setup reports success. Only localSetup reacts
    /// to the install state; every other step is user-driven, so it is returned
    /// unchanged.
    func advanced(on setupState: SetupState) -> OnboardingStep {
        if self == .localSetup, setupState == .success {
            return .addHost
        }
        return self
    }

    var isTerminal: Bool { self == .done }

    /// Zero-based position, used by the wizard's ●─○─○─○ progress indicator.
    var stepIndex: Int { rawValue }
}
