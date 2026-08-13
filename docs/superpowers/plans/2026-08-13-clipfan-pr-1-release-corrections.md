# Clipfan PR #1 Release Corrections Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make PR #1's macOS source build and release workflow work on both tag pushes and manual build-only runs, with regression coverage and accurate release documentation.

**Architecture:** Keep the existing shell-based packaging flow. Add one small executable version resolver used by each workflow step that needs a release version, and make the SwiftPM app script discover the framework from the output layouts SwiftPM actually produces. Use end-to-end shell tests for the app bundle and version resolver instead of asserting rendered YAML or generated command strings.

**Tech Stack:** Bash, GitHub Actions YAML, SwiftPM, Xcode/macOS tooling, Go test, Swift test.

## Global Constraints

- Do not implement the future Windows port or change the daemon protocol.
- Preserve the existing app bundle layout: Sparkle under `Contents/Frameworks` and the KeyboardShortcuts resource bundle under `Contents/Resources`.
- Manual workflow runs remain build-only and must not publish GitHub Releases.
- Tag releases may publish unsigned artifacts when the complete signing credential set is absent.
- A fully signed release requires all six Apple credentials plus `SPARKLE_PRIVATE_ED_KEY`.
- Use existing shell conventions and keep the production changes minimal.

---

### Task 1: Add tested release-version resolution

**Files:**
- Create: `scripts/release-version.sh`
- Create: `scripts/test-release-version.sh`
- Modify: `.github/workflows/release.yml:112-130,329-335`

**Interfaces:**
- `scripts/release-version.sh` reads `GITHUB_EVENT_NAME`, `GITHUB_REF_NAME`, and `DAEMON_VERSION` from its environment/current working directory and prints a version without a leading `v`.
- For a `push` event with a `v*` ref, it returns the tag version.
- For `workflow_dispatch`, it returns `DAEMON_VERSION` without a leading `v`.
- It exits non-zero with a diagnostic if no non-empty version is available.

- [ ] **Step 1: Write the failing resolver test**

Create a temporary fixture with `DAEMON_VERSION=v1.0.9`, invoke the resolver script in a subprocess for both event types, and assert the expected output. Also assert that an empty `DAEMON_VERSION` fails. The test must call the real script by path and must not inspect workflow text.

- [ ] **Step 2: Run the resolver test to verify it fails**

Run:

```sh
bash scripts/test-release-version.sh
```

Expected: failure because `scripts/release-version.sh` does not yet exist.

- [ ] **Step 3: Implement the minimal resolver**

Implement `scripts/release-version.sh` with `set -euo pipefail`. Use the tag ref only for `push` events whose ref starts with `v`; otherwise read and trim `DAEMON_VERSION`, remove one leading `v`, reject an empty result, and print the version.

- [ ] **Step 4: Run the focused resolver test**

Run:

```sh
bash scripts/test-release-version.sh
```

Expected: all resolver cases pass.

- [ ] **Step 5: Wire the resolver into the workflow**

Replace the duplicated version/fallback snippets in release-notes extraction, app build, app packaging, and Sparkle appcast generation with:

```sh
VERSION="$(bash scripts/release-version.sh)"
```

- [ ] **Step 6: Commit the resolver change**

```sh
git add scripts/release-version.sh scripts/test-release-version.sh .github/workflows/release.yml
git commit -m "fix(release): resolve tag and manual build versions consistently"
```

### Task 2: Make manual signing behavior explicit and complete

**Files:**
- Modify: `.github/workflows/release.yml:15-20,55-78`
- Create: `scripts/test-release-workflow.sh`

**Interfaces:**
- `workflow_dispatch.inputs.skip_signing` is a boolean, optional, and defaults to `true`.
- Manual runs with the default input set `steps.gate.outputs.sign=false` and skip keychain, signing, notarization, and appcast steps.
- Tag runs retain secret-driven signing behavior.
- The signing gate requires `DEVELOPER_ID_APPLICATION_CERT_BASE64`, `DEVELOPER_ID_APPLICATION_CERT_PASSWORD`, `DEVELOPER_ID_APPLICATION_SIGNING_IDENTITY`, `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, `APPLE_TEAM_ID`, and `SPARKLE_PRIVATE_ED_KEY` before setting `sign=true`.

- [ ] **Step 1: Add the workflow input and gate assertion test**

Create `scripts/test-release-workflow.sh` with `set -euo pipefail`. Have it load the workflow through Ruby's YAML parser, read the `on` key through either `workflow["on"]` or `workflow[true]` for Psych compatibility, and assert that `workflow_dispatch.inputs.skip_signing.type == "boolean"` and `default == true`. Also assert that the workflow text contains `SPARKLE_PRIVATE_ED_KEY` in the signing-gate environment and condition. Keep assertions structural; do not match the entire rendered workflow.

- [ ] **Step 2: Run the focused workflow test to verify it fails**

Run:

```sh
bash scripts/test-release-workflow.sh
```

Expected: failure because the workflow has no declared `skip_signing` input.

- [ ] **Step 3: Implement the explicit gate**

Declare the input and simplify the expression to use the boolean input on manual runs. Add `SPARKLE_PRIVATE_ED_KEY` to the gate environment and completeness check so the appcast cannot be attempted without its signing key.

- [ ] **Step 4: Run the focused workflow test**

Run:

```sh
bash scripts/test-release-workflow.sh
```

Expected: the structural input and secret-gate checks pass.

- [ ] **Step 5: Commit the workflow gate change**

```sh
git add .github/workflows/release.yml scripts/test-release-workflow.sh
git commit -m "fix(release): make manual signing and Sparkle credentials explicit"
```

### Task 3: Fix and regression-test the macOS source bundle

**Files:**
- Modify: `apps/mac/build-app.sh:39-60`
- Create: `apps/mac/test-build-app.sh`

**Interfaces:**
- The build script selects `.build/release/Sparkle.framework` when present, otherwise the first architecture-qualified `.build/*/release/Sparkle.framework` directory.
- The script exits non-zero with both searched locations when no framework is available.
- The assembled app contains `Contents/Frameworks/Sparkle.framework` and `Contents/Resources/KeyboardShortcuts_KeyboardShortcuts.bundle`.

- [ ] **Step 1: Run the existing failing integration test**

From a clean PR worktree with SwiftPM dependencies available, run:

```sh
bash apps/mac/build-app.sh
```

Expected on the unmodified PR: SwiftPM completes, then the script fails at the incorrect `.build/out/Products/Release/Sparkle.framework` path.

- [ ] **Step 2: Add the focused bundle regression test**

Create `apps/mac/test-build-app.sh` that runs `apps/mac/build-app.sh`, asserts the app bundle exists, asserts the Sparkle framework and KeyboardShortcuts bundle exist at their runtime locations, and exits non-zero on any missing artifact. Use `set -euo pipefail` and absolute paths derived from the test script location.

- [ ] **Step 3: Run the new test to verify it fails**

Run:

```sh
bash apps/mac/test-build-app.sh
```

Expected: failure at the existing Sparkle path check.

- [ ] **Step 4: Implement framework discovery**

Add a small local function in `apps/mac/build-app.sh` that checks the unqualified release path first and then the architecture-qualified release paths. Capture its output into `sparkle_src`, emit an actionable error if it returns no path, and leave the existing copy, resource cleanup, rpath, and codesign behavior unchanged.

- [ ] **Step 5: Run the new bundle test**

Run:

```sh
bash apps/mac/test-build-app.sh
```

Expected: the app assembles and both embedded runtime resources are present.

- [ ] **Step 6: Commit the app bundle change**

```sh
git add apps/mac/build-app.sh apps/mac/test-build-app.sh
git commit -m "fix(mac): discover SwiftPM Sparkle framework output"
```

### Task 4: Update release documentation

**Files:**
- Modify: `docs/RELEASING.md:7-13,51-80,102-117`

**Interfaces:**
- Documentation states that signing secrets are required only for signed/notarized artifacts and the Sparkle appcast.
- Documentation states that manual workflow runs are build-only, default to skipping signing, and resolve their displayed app version from `DAEMON_VERSION`.
- Local source-build instructions point to the now-working `apps/mac/build-app.sh` path.

- [ ] **Step 1: Update the release guide**

Edit only the release behavior and verification sections. Preserve the existing daemon-version separation and tag-release instructions.

- [ ] **Step 2: Run documentation and shell syntax checks**

Run:

```sh
bash -n scripts/release-version.sh scripts/test-release-version.sh scripts/test-release-workflow.sh apps/mac/build-app.sh apps/mac/test-build-app.sh
ruby -e 'require "yaml"; YAML.load_file(".github/workflows/release.yml")'
git diff --check
```

- [ ] **Step 3: Commit the documentation change**

```sh
git add docs/RELEASING.md
git commit -m "docs(release): document manual build and signing modes"
```

### Task 5: Full verification and PR handoff

**Files:**
- No planned source changes; inspect the complete branch diff.

- [ ] **Step 1: Run all focused shell checks**

```sh
bash scripts/test-release-version.sh
bash scripts/test-release-workflow.sh
bash apps/mac/test-build-app.sh
bash dist/test-bootstrap-self-ssh.sh
bash dist/test-linux-service-restart.sh
bash dist/test-nonexec-stage-install.sh
bash dist/test-tmux-gating.sh
bash scripts/test-extract-release-notes.sh
bash scripts/test-ssh-release-gates.sh
```

- [ ] **Step 2: Run the Go and Swift suites**

```sh
go test ./...
(cd apps/mac/Clipfan && swift test)
```

Expected: Go exits 0 and Swift reports 297 tests with 0 failures.

- [ ] **Step 3: Build and package all release payloads**

```sh
bash dist/build-all.sh
CLIPFAN_VERIFY_ONLY=1 bash dist/build-all.sh
bash dist/make-release-tarballs.sh /tmp/clipfan-pr-1-release-artifacts
for archive in /tmp/clipfan-pr-1-release-artifacts/*.tar.gz; do tar -tzf "$archive" >/dev/null; done
```

- [ ] **Step 4: Review the complete diff and branch state**

```sh
git diff --check origin/pr-1...HEAD
git diff --stat origin/pr-1...HEAD
git status --short --branch
```

- [ ] **Step 5: Push the verified branch to the existing PR**

```sh
git push origin HEAD:feat/cross-platform-install
```

- [ ] **Step 6: Re-read the remote PR metadata and report the exact commits, tests, and any environment-limited checks**
