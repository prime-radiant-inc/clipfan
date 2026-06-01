import XCTest
@testable import Clipfan

final class PeerUpdateLogTests: XCTestCase {
    func testProgressDetailsAreKeptForCopying() {
        var log = PeerUpdateLog(host: "flower-garden")

        log.record(.init(step: "Probe", detail: "running uname on jesse@flower-garden"))
        log.record(.init(step: "Install", detail: "updating clipfan on jesse@flower-garden"))

        XCTAssertEqual(log.text, """
        [flower-garden] starting peer update
        [Probe] running uname on jesse@flower-garden
        [Install] updating clipfan on jesse@flower-garden
        """)
    }

    func testCommandFailureIncludesStdoutAndStderrInCopyableText() {
        var log = PeerUpdateLog(host: "flower-garden")
        let error = InstallCommandFailure(
            executable: "/usr/bin/ssh",
            arguments: ["jesse@flower-garden", "bash install.sh --no-tmux"],
            exitStatus: 1,
            stdout: "stdout line",
            stderr: "stderr line"
        )

        log.recordFailure(error)

        XCTAssertTrue(log.text.contains("failed with exit 1"))
        XCTAssertTrue(log.text.contains("stdout:\nstdout line"))
        XCTAssertTrue(log.text.contains("stderr:\nstderr line"))
    }
}

