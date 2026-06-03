import XCTest
@testable import Clipfan

final class PeerUpdateLogTests: XCTestCase {
    func testAddPeerFailureForScopedConflictIsAttentionWithCopyableLog() {
        var log = AddPeerOperationLog(host: "flower-garden")
        log.record(.init(step: "Local", detail: "adding peer to local config"))
        let error = LocalDaemonSSHPeerConfigError.api(code: localDaemonConfigRevisionConflictCode, statusCode: 409)

        log.recordFailure(error)
        let failure = AddPeerOperationFailure(host: "flower-garden", error: error, log: log)

        XCTAssertEqual(failure.message, "flower-garden: Local peer config changed; retry.")
        XCTAssertTrue(failure.logText.contains("[Local] adding peer to local config"))
        XCTAssertTrue(failure.logText.contains("code=\(localDaemonConfigRevisionConflictCode) status=409"))
    }

    func testAddPeerFailureMessagesCoverScopedErrorBranches() {
        let cases: [(LocalDaemonSSHPeerConfigError, String)] = [
            (.api(code: localDaemonConfigRevisionConflictCode, statusCode: 409),
             "flower-garden: Local peer config changed; retry."),
            (.api(code: localDaemonSSHPeerMigrationStateChangeNotAllowedCode, statusCode: 409),
             "flower-garden: Local peer config changed; retry."),
            (.api(code: "missing_config_revision", statusCode: 409),
             "flower-garden: Local peer config is missing a revision; reload and retry."),
            (.api(code: "safe_mode_active", statusCode: 503),
             "flower-garden: Local daemon is in safe mode; repair the listener and retry."),
            (.api(code: "unknown_field", statusCode: 400),
             "flower-garden: Scoped peer config failed (unknown_field); retry."),
            (.missingHTTPResponse,
             "flower-garden: Local daemon did not return an HTTP response; retry."),
            (.missingResponseSignature,
             "flower-garden: Local daemon response was unsigned; retry."),
            (.badResponseSignature,
             "flower-garden: Local daemon response signature was invalid; retry.")
        ]

        for (error, message) in cases {
            var log = AddPeerOperationLog(host: "flower-garden")
            log.recordFailure(error)

            let failure = AddPeerOperationFailure(host: "flower-garden", error: error, log: log)

            XCTAssertEqual(failure.message, message)
        }
    }

    func testScopedOperationFailureLogTextCoversNonAPIErrorBranches() {
        let cases: [(LocalDaemonSSHPeerConfigError, String)] = [
            (.api(code: "safe_mode_active", statusCode: 503),
             "scoped peer config failed: code=safe_mode_active status=503"),
            (.missingHTTPResponse,
             "scoped peer config failed: missing_http_response"),
            (.missingResponseSignature,
             "scoped peer config failed: missing_response_signature"),
            (.badResponseSignature,
             "scoped peer config failed: bad_response_signature")
        ]

        for (error, expectedLogLine) in cases {
            var log = AddPeerOperationLog(host: "flower-garden")

            log.recordFailure(error)

            XCTAssertTrue(log.text.contains(expectedLogLine), "log \(log.text) should include \(expectedLogLine)")
        }
    }

    func testAddPeerInstallButtonTitleShowsRetryAfterFailure() {
        let error = LocalDaemonSSHPeerConfigError.api(code: localDaemonConfigRevisionConflictCode, statusCode: 409)
        var log = AddPeerOperationLog(host: "flower-garden")
        log.recordFailure(error)
        let failure = AddPeerOperationFailure(host: "flower-garden", error: error, log: log)

        XCTAssertEqual(addPeerInstallButtonTitle(installing: true, installCount: 1, failure: failure), "Installing…")
        XCTAssertEqual(addPeerInstallButtonTitle(installing: false, installCount: 1, failure: failure), "Retry")
        XCTAssertEqual(addPeerInstallButtonTitle(installing: false, installCount: 2, failure: nil), "Install on 2 hosts")
    }

    func testOperationLogsRedactSecretsAndSSHKeyPaths() {
        let error = InstallCommandFailure(
            executable: "/usr/bin/ssh",
            arguments: ["-i", "/Users/jesse/.ssh/id_ed25519", "jesse@flower-garden", "printf shared_key=supersecret token=abc123"],
            exitStatus: 1,
            stdout: #"{"shared_key":"supersecret","nonce":"abc123"}"#,
            stderr: """
            identity ~/.ssh/id_ed25519 hmac=abcdef
            -----BEGIN OPENSSH PRIVATE KEY-----
            private-material
            -----END OPENSSH PRIVATE KEY-----
            """
        )
        var log = AddPeerOperationLog(host: "flower-garden")

        log.recordFailure(error)

        XCTAssertTrue(log.text.contains("-i [redacted-path]"))
        XCTAssertFalse(log.text.contains("/Users/jesse/.ssh/id_ed25519"))
        XCTAssertFalse(log.text.contains("~/.ssh/id_ed25519"))
        XCTAssertFalse(log.text.contains("supersecret"))
        XCTAssertFalse(log.text.contains("abc123"))
        XCTAssertFalse(log.text.contains("abcdef"))
        XCTAssertFalse(log.text.contains("private-material"))
        XCTAssertTrue(log.text.contains("shared_key=[redacted]"))
        XCTAssertTrue(log.text.contains(#""nonce":"[redacted]""#))
        XCTAssertTrue(log.text.contains("[redacted-private-key]"))
    }

    func testAddPeerFailureMessageRedactsCommandFailureDetails() {
        let error = InstallCommandFailure(
            executable: "/usr/bin/ssh",
            arguments: ["-i", "/Users/jesse/.ssh/id_ed25519", "jesse@flower-garden"],
            exitStatus: 1,
            stdout: "",
            stderr: ""
        )
        var log = AddPeerOperationLog(host: "flower-garden")
        log.recordFailure(error)

        let failure = AddPeerOperationFailure(host: "flower-garden", error: error, log: log)

        XCTAssertTrue(failure.message.contains("-i [redacted-path]"))
        XCTAssertFalse(failure.message.contains("/Users/jesse/.ssh/id_ed25519"))
    }

    func testOperationLogRedactsTruncatedPrivateKeyBlock() {
        let redacted = PeerOperationLogRedactor.redact("""
        before
        -----BEGIN OPENSSH PRIVATE KEY-----
        private-material
        """)

        XCTAssertTrue(redacted.contains("before"))
        XCTAssertTrue(redacted.contains("[redacted-private-key]"))
        XCTAssertFalse(redacted.contains("private-material"))
    }

    func testOperationLogRedactionTruncatesLongText() {
        let redacted = PeerOperationLogRedactor.redact(String(repeating: "a", count: 12), maxCharacters: 5)

        XCTAssertEqual(redacted, """
        aaaaa
        [truncated 7 characters]
        """)
    }

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
