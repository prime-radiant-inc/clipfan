import XCTest
@testable import Clipfan

final class OnboardingStepTests: XCTestCase {
    func testNextProgressesThroughTheWizard() {
        XCTAssertEqual(OnboardingStep.welcome.next, .localSetup)
        XCTAssertEqual(OnboardingStep.localSetup.next, .addHost)
        XCTAssertEqual(OnboardingStep.addHost.next, .done)
        XCTAssertNil(OnboardingStep.done.next, "done is terminal")
    }

    func testLocalSetupAdvancesOnInstallSuccess() {
        XCTAssertEqual(OnboardingStep.localSetup.advanced(on: .success), .addHost)
    }

    func testLocalSetupStaysWhileInstallInProgressOrFailed() {
        XCTAssertEqual(OnboardingStep.localSetup.advanced(on: .idle), .localSetup)
        XCTAssertEqual(OnboardingStep.localSetup.advanced(on: .installing(progress: ["staging"])), .localSetup)
        XCTAssertEqual(OnboardingStep.localSetup.advanced(on: .failed(message: "boom", logPath: "/tmp/log")), .localSetup)
    }

    func testOtherStepsIgnoreInstallSuccess() {
        XCTAssertEqual(OnboardingStep.welcome.advanced(on: .success), .welcome)
        XCTAssertEqual(OnboardingStep.addHost.advanced(on: .success), .addHost)
        XCTAssertEqual(OnboardingStep.done.advanced(on: .success), .done)
    }

    func testAddHostIsSkippable() {
        // Skipping and completing both lead to done.
        XCTAssertEqual(OnboardingStep.addHost.next, .done)
    }

    func testTerminalAndOrdering() {
        XCTAssertTrue(OnboardingStep.done.isTerminal)
        XCTAssertFalse(OnboardingStep.welcome.isTerminal)
        XCTAssertEqual(OnboardingStep.allCases, [.welcome, .localSetup, .addHost, .done])
        XCTAssertEqual(OnboardingStep.welcome.stepIndex, 0)
        XCTAssertEqual(OnboardingStep.done.stepIndex, 3)
    }
}
