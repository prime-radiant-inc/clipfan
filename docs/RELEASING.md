# Releasing clipfan

The release workflow is triggered on any `v*` tag and publishes the daemon
payloads, universal installer, and Mac app zip. When all signing credentials are
present it also Developer ID-signs, notarizes, staples, and publishes the DMG and
Sparkle auto-update appcast. A `workflow_dispatch` run is build-only and skips
signing by default.

## One-time setup: repository secrets

Signed releases need these seven secrets on `prime-radiant-inc/clipfan`
(Settings → Secrets and variables → Actions). Unsigned tag releases can still
publish without them, which lets forks build the daemon and unsigned Mac app.
The upstream repository has all seven configured (verify with `gh secret list`);
they are the same credentials used by `prime-radiant-inc/clearance` (same Apple
Developer account and Sparkle key), so values can be copied from there when
rotating.

| Secret | What it is |
|--------|-----------|
| `DEVELOPER_ID_APPLICATION_SIGNING_IDENTITY` | `Developer ID Application: Jesse Vincent (87WJ58S66M)` |
| `APPLE_TEAM_ID` | `87WJ58S66M` |
| `DEVELOPER_ID_APPLICATION_CERT_BASE64` | base64 of the Developer ID Application `.p12` (single-identity) |
| `DEVELOPER_ID_APPLICATION_CERT_PASSWORD` | password for that `.p12` |
| `APPLE_ID` | Apple ID email used for notarization |
| `APPLE_APP_SPECIFIC_PASSWORD` | app-specific password for that Apple ID |
| `SPARKLE_PRIVATE_ED_KEY` | the Sparkle EdDSA private key (matches `SUPublicEDKey` in `Info.plist`) |

To rotate a secret later: `gh secret set NAME --repo prime-radiant-inc/clipfan`.

No Installer cert and no `SPARKLE_PUBLIC_ED_KEY` secret are needed — clipfan ships
a `.dmg`/`.zip` (not a `.pkg`) and the Sparkle public key lives in `Info.plist`.

### Setting them with `gh`

```sh
gh secret set DEVELOPER_ID_APPLICATION_CERT_BASE64    --repo prime-radiant-inc/clipfan < cert.p12.base64
gh secret set DEVELOPER_ID_APPLICATION_CERT_PASSWORD  --repo prime-radiant-inc/clipfan
gh secret set DEVELOPER_ID_APPLICATION_SIGNING_IDENTITY --repo prime-radiant-inc/clipfan
gh secret set APPLE_ID                                --repo prime-radiant-inc/clipfan
gh secret set APPLE_APP_SPECIFIC_PASSWORD             --repo prime-radiant-inc/clipfan
gh secret set APPLE_TEAM_ID                           --repo prime-radiant-inc/clipfan
# Sparkle private key — export it from the login keychain (approve the keychain
# prompt), then pipe it in:
/path/to/Sparkle/bin/generate_keys -x sparkle-private-key
gh secret set SPARKLE_PRIVATE_ED_KEY                  --repo prime-radiant-inc/clipfan < sparkle-private-key
rm sparkle-private-key
```

The Sparkle public key already embedded in `apps/mac/Clipfan/Info.plist`
(`SUPublicEDKey = ZY/ZPlRrnPohsWVic4GcjZ8tJg8qScm9MRHj3EWO4mg=`) is the developer's
existing key shared with clearance — the matching private key is what goes in
`SPARKLE_PRIVATE_ED_KEY`.

## Cutting a release

The tag drives the version (`vX.Y.Z` → `CFBundleShortVersionString X.Y.Z`); the
build number is the workflow run number.

The daemon version is separate. `dist/build-all.sh` stamps daemon binaries from
`DAEMON_VERSION`, so UI-only app releases do not force peer daemon updates. Bump
`DAEMON_VERSION` only when the daemon payload, protocol, installer behavior, or
peer-compatibility contract changes.

Add a matching `## [X.Y.Z] - YYYY-MM-DD` section to `CHANGELOG.md` before
tagging. The release workflow extracts that section, embeds it into the Sparkle
appcast, and uses it as the GitHub Release notes.

```sh
git tag v0.3.0
git push origin v0.3.0
```

The workflow then:
1. verifies the SSH release gates (`scripts/test-ssh-release-gates.sh`),
2. cross-compiles the daemon and pasteboard helpers (`dist/build-all.sh`,
   daemon version stamped from `DAEMON_VERSION`) and verifies the full release
   payload set,
3. builds `Clipfan.app` via xcodegen + xcodebuild,
4. when all seven signing secrets are available, Developer ID-signs the app,
   embedded Sparkle framework, and bundled macOS daemon binaries (hardened
   runtime), then notarizes and staples,
5. publishes the daemon tarballs, universal installer, and app zip; signed runs
   additionally publish the `.dmg` and `appcast.xml`, using the changelog section
   as release notes.

## Manual build-only verification

Run the workflow manually to build the full unsigned payload without creating a
GitHub Release. The version used for the app and release notes comes from
`DAEMON_VERSION` rather than the branch name:

```sh
gh workflow run release.yml --repo prime-radiant-inc/clipfan -f skip_signing=true
```

Set `skip_signing` to `false` only when explicitly testing the signed path. A
manual run still does not publish release assets; use a `v*` tag for an actual
release.

Installed copies pick up the update from
`https://github.com/prime-radiant-inc/clipfan/releases/latest/download/appcast.xml`.

## Security verification before tagging

Run the release payload and test checks before creating a version tag:

```sh
bash dist/build-all.sh
test -x dist/clipfan-pasteboard-helper-darwin-amd64
test -x dist/clipfan-pasteboard-helper-darwin-arm64
TMPDIR=/tmp go test ./...
(cd apps/mac/Clipfan && swift test)
bash dist/test-build-all-helper.sh
bash scripts/test-release-version.sh
bash scripts/test-release-workflow.sh
bash apps/mac/test-build-app.sh
```

The helper executable checks are release-critical: the daemon depends on the
Darwin pasteboard helper to detect concealed or transient pasteboard items and
to write image pasteboard payloads correctly.

## Local build (no signing)

```sh
cd apps/mac/Clipfan
xcodegen generate
xcodebuild -scheme Clipfan -configuration Release CODE_SIGNING_ALLOWED=NO clean build
```

or the SwiftPM dev path that still works for day-to-day:

```sh
bash dist/build-all.sh
test -x dist/clipfan-pasteboard-helper-darwin-amd64
test -x dist/clipfan-pasteboard-helper-darwin-arm64
bash apps/mac/test-build-app.sh
```

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-10 · commit `5ed989c` · verified against code (7 claims deferred to review)._
