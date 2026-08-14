# macOS Mesh Onboarding Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make macOS onboarding register the daemon service reliably so Add Peer can reach the existing full-mesh healing flow.

**Architecture:** Keep the existing pairwise provisioning and `mesh-heal` orchestration. Repair the macOS service boundary in the installer, app startup recovery, and local restart helper so launchd registration is real and repeatable.

**Tech Stack:** Bash installer and shell fixtures; Swift/AppKit startup and daemon client; XCTest; Go test suite for regression coverage.

## Global Constraints

- Use `launchctl bootstrap` for modern per-user service registration, with `launchctl load` only as fallback.
- `--no-restart` must not enable, load, kickstart, or verify the launchd job.
- Do not weaken SSH host-key, config-v2, or shared-key gates.
- Keep peer-heal failures visible as per-host failures; do not claim unreachable peers enrolled.
- Make the smallest changes consistent with existing installer and Swift test seams.

---

### Task 1: Lock down Darwin service registration with a failing shell test

**Files:**
- Create: `dist/test-macos-service-restart.sh`
- Modify: `dist/install.sh:120-145`

**Interfaces:**
- Consumes: the Darwin branch of `dist/install.sh` and a fake `launchctl` executable.
- Produces: a tested launchd registration sequence used by onboarding and remote repair.

- [ ] **Step 1: Write the failing test**

Create a Darwin fixture with fake `uname`, `launchctl`, and daemon payload. Make
the fake `launchctl` record calls, make `bootstrap` fail once so the test
covers the legacy fallback, and make `print` succeed only after `load`. Assert
that normal install records `enable`, `bootstrap`, `load`, `kickstart`, and
`print`, while `--no-restart` records none of those service mutations.

- [ ] **Step 2: Run the test to verify it fails**

Run: `bash dist/test-macos-service-restart.sh`

Expected: FAIL because the current installer calls only `load` and does not
verify the registered service.

- [ ] **Step 3: Implement the minimal installer change**

Replace the Darwin restart block with the following flow, preserving the
existing plist generation and `--no-restart` branch:

```bash
user_uid="$(id -u)"
service="gui/$user_uid/com.primeradiant.clipfan"
launchctl enable "$service" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$user_uid" "$plist" >/dev/null 2>&1 || \
    launchctl load "$plist" >/dev/null 2>&1 || true
launchctl kickstart -k "$service" >/dev/null 2>&1
launchctl print "$service" >/dev/null 2>&1
echo "Loaded launchd job: com.primeradiant.clipfan"
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `bash dist/test-macos-service-restart.sh`

Expected: `ALL PASS`.

- [ ] **Step 5: Commit**

```bash
git add dist/install.sh dist/test-macos-service-restart.sh
git commit -m "fix(mac): verify launchd service during onboarding"
```

### Task 2: Add failing Swift coverage for launchd restart and startup recovery

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/Bootstrap.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift`
- Modify: `apps/mac/Clipfan/Tests/ClipfanTests/BootstrapTests.swift`
- Create or modify: `apps/mac/Clipfan/Tests/ClipfanTests/DaemonClientTests.swift`

**Interfaces:**
- Consumes: `LaunchDecision.restartExisting` and the local `launchctl` restart path.
- Produces: `Bootstrap.recoveryMode(for:)` and a testable launchd argument builder.

- [ ] **Step 1: Write the failing tests**

Add a pure startup mapping assertion:

```swift
XCTAssertEqual(Bootstrap.recoveryMode(for: .restartExisting), .upgradeExisting)
```

Add a pure local-service command assertion that requires the generated command
sequence to include `enable`, `bootstrap`, `load`, and `kickstart` in that
order, using the current uid and plist path as inputs.

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `swift test --package-path apps/mac/Clipfan --filter 'BootstrapDecisionTests|DaemonClientTests'`

Expected: FAIL because the recovery mapping and command builder do not exist.

- [ ] **Step 3: Implement the minimal Swift changes**

Add `Bootstrap.recoveryMode(for:)` returning `.upgradeExisting` for
`.restartExisting` and nil for `.normal`; update `AppDelegate` to invoke
`BootstrapController.shared.install(mode: .upgradeExisting)` for that recovery
case and preserve the existing failure presentation.

Extract a pure `DaemonClient.launchdRestartArguments(uid:plistPath:)` helper,
then make `restartDaemon()` run `enable`, attempt `bootstrap`, use `load` as a
fallback, and finally `kickstart`. Return false only when the final kickstart
fails.

- [ ] **Step 4: Run the focused tests to verify they pass**

Run: `swift test --package-path apps/mac/Clipfan --filter 'BootstrapDecisionTests|DaemonClientTests'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift apps/mac/Clipfan/Sources/Clipfan/Bootstrap.swift apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift apps/mac/Clipfan/Tests/ClipfanTests/BootstrapTests.swift apps/mac/Clipfan/Tests/ClipfanTests/DaemonClientTests.swift
git commit -m "fix(mac): repair daemon service during app startup"
```

### Task 3: Run complete verification and hand off

**Files:**
- Verify: `dist/install.sh`, `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift`, `apps/mac/Clipfan/Sources/Clipfan/Bootstrap.swift`, `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift`

**Interfaces:**
- Consumes: the completed onboarding and local restart fixes.
- Produces: evidence that the existing full-mesh heal remains reachable after Add Peer provisioning.

- [ ] **Step 1: Run shell and syntax verification**

Run: `bash -n dist/install.sh dist/test-macos-service-restart.sh && shellcheck dist/install.sh dist/test-macos-service-restart.sh && bash dist/test-macos-service-restart.sh`

Expected: syntax and ShellCheck pass; test prints `ALL PASS`.

- [ ] **Step 2: Run the Swift suite**

Run: `swift test --package-path apps/mac/Clipfan`

Expected: all tests pass.

- [ ] **Step 3: Run the Go suite**

Run: `go test ./...`

Expected: all packages pass.

- [ ] **Step 4: Inspect the final diff**

Run: `git diff --check origin/main...HEAD && git status --short --branch`

Expected: no whitespace errors and a clean worktree.

- [ ] **Step 5: Commit any remaining documentation-only adjustment**

If verification requires no change, do not create an empty commit. Otherwise
commit only the specific verified adjustment with a detailed message.
