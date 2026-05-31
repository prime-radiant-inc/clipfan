# Releasing clipfan

The menubar app is built, Developer ID-signed, notarized, stapled, and published
to GitHub Releases with a Sparkle auto-update appcast by
`.github/workflows/release.yml`, triggered on any `v*` tag.

## One-time setup: repository secrets

The workflow needs these secrets on `prime-radiant-inc/clipfan`
(Settings → Secrets and variables → Actions). They are the same credentials
used by `prime-radiant-inc/clearance` (same Apple Developer account and Sparkle
key), so the values can be copied from there.

| Secret | What it is |
|--------|-----------|
| `DEVELOPER_ID_APPLICATION_CERT_BASE64` | base64 of the Developer ID Application `.p12` |
| `DEVELOPER_ID_APPLICATION_CERT_PASSWORD` | password for that `.p12` |
| `DEVELOPER_ID_APPLICATION_SIGNING_IDENTITY` | e.g. `Developer ID Application: Your Name (TEAMID)` |
| `APPLE_ID` | Apple ID email used for notarization |
| `APPLE_APP_SPECIFIC_PASSWORD` | app-specific password for that Apple ID |
| `APPLE_TEAM_ID` | 10-char Apple Team ID |
| `SPARKLE_PRIVATE_ED_KEY` | the Sparkle EdDSA private key (matches the `SUPublicEDKey` in `Info.plist`) |

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

```sh
git tag v0.3.0
git push origin v0.3.0
```

The workflow then:
1. cross-compiles the daemon (`dist/build-all.sh`, version stamped from the tag),
2. builds `Clipfan.app` via xcodegen + xcodebuild,
3. Developer ID-signs the app, the embedded Sparkle framework, and the bundled
   macOS daemon binaries (hardened runtime),
4. notarizes + staples,
5. publishes the `.zip`, `.dmg`, and `appcast.xml` to the GitHub Release.

Installed copies pick up the update from
`https://github.com/prime-radiant-inc/clipfan/releases/latest/download/appcast.xml`.

## Local build (no signing)

```sh
cd apps/mac/Clipfan
xcodegen generate
xcodebuild -scheme Clipfan -configuration Release CODE_SIGNING_ALLOWED=NO clean build
```

or the SwiftPM dev path that still works for day-to-day:

```sh
bash dist/build-all.sh
cd apps/mac/Clipfan && bash build-app.sh
```
