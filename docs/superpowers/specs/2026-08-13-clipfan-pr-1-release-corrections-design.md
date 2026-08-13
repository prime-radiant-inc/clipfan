# Clipfan PR #1 Release Corrections Design

## Goal

Make the existing cross-platform installer and release PR truthful and usable on
the paths it advertises: a clean macOS source build must assemble a runnable app,
and manual release workflow runs must use a valid version and changelog section.

## Scope

This work stays within the current PR's macOS packaging, release workflow, and
release-test scope. It does not implement the future Windows port, change the
daemon protocol, or redesign the SSH self-bootstrap behavior.

## Current failures

1. `apps/mac/build-app.sh` checks `.build/out/Products/Release/Sparkle.framework`,
   but SwiftPM emits the framework under the architecture-qualified release
   directory (and the `.build/release` convenience path). The script exits after
   the Swift build instead of assembling the app.
2. The workflow references `inputs.skip_signing` without declaring the input.
   On a manual run with Apple secrets, signing is not skipped by design, and the
   Sparkle appcast step derives its release-notes version directly from the branch
   name. That branch name has no matching changelog section, so the manual build
   fails before its build-only summary.

## Design

### 1. SwiftPM framework discovery

Keep the existing shell script and bundle layout, but replace the single guessed
Sparkle path with ordered candidates based on SwiftPM's actual output:

1. `.build/release/Sparkle.framework`
2. `.build/*/release/Sparkle.framework` (architecture-qualified output)

Select the first existing directory, fail with the candidates when none exists,
and continue copying the framework and KeyboardShortcuts resource bundle into
the same app locations already used by the script. Do not add a dependency on
XcodeGen or change the release workflow's Xcode build path.

### 2. Release workflow version and signing inputs

Declare a `workflow_dispatch` boolean input named `skip_signing`, defaulting to
`true`, so manual smoke tests do not unexpectedly consume Apple credentials.
Tag-triggered runs always retain the existing secret-driven signing gate.
The signing gate must require the Sparkle private Ed25519 key as well as the six
Apple credentials, because the same gate controls appcast generation.

Use one consistent shell version resolution in every workflow step that needs a
release version: strip the leading `v` from tag refs, and use `DAEMON_VERSION`
when the event is `workflow_dispatch`. Apply the same resolved version to
release notes and appcast generation. Manual runs remain build-only and do not
publish release assets.

### 3. Regression coverage and documentation

Add focused shell-level checks for the two corrected behaviors without asserting
large generated YAML or shell strings. The checks will exercise real filesystem
paths and the existing release-notes extractor. Keep existing payload, installer,
Go, and Swift tests unchanged except where a new test entry point is necessary.

Update release documentation to describe the optional manual signing input and
the separate tag/manual version behavior. The PR description's Windows follow-up
scope remains accurate and is not expanded here.

## Error handling

- A missing Sparkle framework remains a hard failure with an actionable path
  diagnostic.
- A missing manual-version source remains a hard failure rather than silently
  generating an incorrectly versioned appcast.
- Signing remains optional for tag releases when the Apple secrets are absent;
  signed artifacts and the appcast are only emitted when signing succeeds.

## Verification

The implementation must demonstrate:

- the new macOS bundle path test fails before the path fix and passes afterward;
- `bash apps/mac/build-app.sh` assembles `Clipfan.app` from a clean PR worktree;
- manual version resolution extracts the `DAEMON_VERSION` changelog section;
- all existing shell checks pass;
- `go test ./...` passes;
- `swift test` passes with all 297 tests;
- cross-target payload building and tarball packaging pass;
- the final branch is clean, committed, and pushed to the existing PR branch.
