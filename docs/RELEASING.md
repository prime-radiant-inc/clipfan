# Releasing clipfan

The menubar app is built, Developer ID-signed, notarized, stapled, and published
to GitHub Releases with a Sparkle auto-update appcast by
`.github/workflows/release.yml`, triggered on any `v*` tag.

## One-time setup: repository secrets

The workflow needs these secrets on `prime-radiant-inc/clipfan`
(Settings → Secrets and variables → Actions). They are the same credentials
used by `prime-radiant-inc/clearance` (same Apple Developer account and Sparkle
key), so the values can be copied from there.

All seven are configured on `prime-radiant-inc/clipfan` (verify with
`gh secret list`). They are the same credentials as `clearance` (same Apple
Developer account and Sparkle key).

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
1. cross-compiles the daemon and pasteboard helpers (`dist/build-all.sh`,
   daemon version stamped from `DAEMON_VERSION`) and verifies the full release
   payload set,
2. builds `Clipfan.app` via xcodegen + xcodebuild,
3. Developer ID-signs the app, the embedded Sparkle framework, and the bundled
   macOS daemon binaries (hardened runtime),
4. notarizes + staples,
5. publishes the `.zip`, `.dmg`, and `appcast.xml` to the GitHub Release, using
   the changelog section as release notes.

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
cd apps/mac/Clipfan && bash build-app.sh
```
