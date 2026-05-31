# GitHub auto-build, sign, notarize + Sparkle — design

**Date:** 2026-05-30
**Scope:** Group C. A tagged push (`v*`) builds, Developer ID-signs, notarizes,
and publishes the clipfan menubar app with Sparkle auto-update, mirroring
`prime-radiant-inc/clearance`'s release workflow.

## Decisions (settled in brainstorming)

- The menubar app is built with **xcodegen + xcodebuild** (not bare SwiftPM), so
  Sparkle's framework + XPC services embed and sign the standard way Xcode does.
- CI publishes a **signed, notarized, stapled `.dmg` + `.zip`** only (no `.pkg`),
  so only the **Developer ID Application** cert is needed.
- Sparkle reuses the developer's existing EdDSA key
  (`SUPublicEDKey = ZY/ZPlRrnPohsWVic4GcjZ8tJg8qScm9MRHj3EWO4mg=`); the private
  key is added to the clipfan repo as `SPARKLE_PRIVATE_ED_KEY`.
- The appcast is hosted on GitHub Releases
  (`releases/latest/download/appcast.xml`), same as clearance.

## Part 1 — App build via xcodegen

New `apps/mac/Clipfan/project.yml` (xcodegen) describing one app target:

- Name `Clipfan`, bundle id `com.primeradiant.clipfan`, macOS 13 deployment.
- Sources: `Sources/Clipfan`.
- Info.plist: the existing `Info.plist`, extended (see Part 2).
- SwiftPM package dependencies: `KeyboardShortcuts` (existing) and
  `Sparkle` (new, 2.x). xcodegen reads them from `Package.swift` /
  declares them in `packages:`.
- `MARKETING_VERSION` / `CURRENT_PROJECT_VERSION` build settings, overridable by
  CI (`xcodebuild MARKETING_VERSION=… CURRENT_PROJECT_VERSION=…`).
- Hardened runtime enabled (`ENABLE_HARDENED_RUNTIME = YES`); no App Sandbox (the
  app shells out to `install.sh`, `launchctl`, and spawns the daemon).
- The **dist payload** (daemon binaries, helpers, `install.sh`, unit files) is
  embedded as `Contents/Resources/dist` via a folder reference to `../../../dist`,
  reproducing what `build-app.sh` copies today. CI runs `dist/build-all.sh`
  before `xcodebuild` so the payload exists.

`build-app.sh` stays for local SwiftPM dev builds; xcodegen is the release path.
`Package.swift` keeps the Sparkle dependency too so `swift build` still compiles.

The bundle id, `LSUIElement`, and the Resources/dist layout must match what
`install.sh` and the daemon-spawn code already expect (no behavior change).

## Part 2 — App-side Sparkle integration

- `Package.swift` + `project.yml`: add `Sparkle` (https://github.com/sparkle-project/Sparkle, from 2.6.0).
- `Info.plist` gains:
  - `SUFeedURL` = `https://github.com/prime-radiant-inc/clipfan/releases/latest/download/appcast.xml`
  - `SUPublicEDKey` = `ZY/ZPlRrnPohsWVic4GcjZ8tJg8qScm9MRHj3EWO4mg=`
  - `SUEnableAutomaticChecks` = `YES`
- New `apps/mac/Clipfan/Sources/Clipfan/Updater.swift`: a thin wrapper owning an
  `SPUStandardUpdaterController` (started automatically), exposing
  `checkForUpdates()`.
- `ClipfanApp` holds the updater for the app's lifetime.
- `StatusMenuView` gains a "Check for Updates…" row (in the actions group, above
  the fleet) that calls `updater.checkForUpdates()`. The row is disabled while an
  update check can't run (Sparkle's `canCheckForUpdates`).

Since the menubar app has no main menu, the in-app "Check for Updates…" item is
the manual entry point; automatic background checks run via Sparkle's scheduler.

## Part 3 — CI workflow

New `.github/workflows/release.yml`, triggered on `push` tags `v*`. Adapted from
clearance:

1. **Keychain setup** — import the Developer ID Application `.p12` from
   `DEVELOPER_ID_APPLICATION_CERT_BASE64` / `…_PASSWORD` into a temp keychain
   (drop the Installer cert; we don't build a `.pkg`).
2. **Install tools** — `brew install xcodegen`.
3. **Build daemon payload** — `bash dist/build-all.sh` (stamps the version via
   the Group A ldflags), so `Resources/dist` is populated.
4. **Generate + build app** — `xcodegen generate` then `xcodebuild -scheme Clipfan
   -configuration Release MARKETING_VERSION=$VERSION CURRENT_PROJECT_VERSION=$RUN_NUMBER
   CODE_SIGNING_ALLOWED=NO clean build`; locate `Clipfan.app`; assert its
   `CFBundleShortVersionString` equals the tag.
5. **Codesign** — deep-sign inside-out with hardened runtime + timestamp:
   Sparkle framework/XPC under `Contents/Frameworks`, the app executable under
   `Contents/MacOS`, **and the macOS daemon Mach-O binaries** in
   `Resources/dist` (`clipfan-darwin-*`, `clipfan-pasteboard-helper-darwin-*`) by
   name — the Linux binaries are not Mach-O and are left unsigned (notarization
   ignores them). Then sign the outer `.app` and `codesign --verify --deep --strict`.
6. **Notarize + staple** — `ditto` zip → `notarytool submit --wait` →
   `stapler staple` the `.app`.
7. **Package** — stapled `.zip` (for the appcast) + `.dmg` (`hdiutil`).
8. **Appcast** — clone Sparkle, build `generate_appcast`, run it over a dir
   containing the stapled `.zip` with `--ed-key-file` (from
   `SPARKLE_PRIVATE_ED_KEY`) and `--download-url-prefix` pointing at the tag's
   release assets; emit `appcast.xml`.
9. **Release** — `gh release create/upload` the `.zip`, `.dmg`, and `appcast.xml`.
10. **Cleanup** — delete the temp keychain (`if: always()`).

## Secrets (clipfan repo)

Reused from clearance's Apple account; added to `prime-radiant-inc/clipfan`:

- `DEVELOPER_ID_APPLICATION_CERT_BASE64`, `DEVELOPER_ID_APPLICATION_CERT_PASSWORD`,
  `DEVELOPER_ID_APPLICATION_SIGNING_IDENTITY`
- `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_SPECIFIC_PASSWORD`
- `SPARKLE_PRIVATE_ED_KEY` (exported from the keychain, set via `gh secret set`)

(No Installer cert, no `SPARKLE_PUBLIC_ED_KEY` secret — the public key lives in
Info.plist.)

## Testing / verification

- **Local (no secrets):** `xcodegen generate` + `xcodebuild … CODE_SIGNING_ALLOWED=NO
  clean build` produces `Clipfan.app` with Sparkle embedded and "Check for
  Updates…" present; `swift test` still green; `swift build` still compiles.
- **End-to-end (needs secrets + a tag):** push `v0.3.0`; the workflow signs,
  notarizes, and publishes; a prior installed build sees the update. Done together.

## Risks

- Embedding Sparkle into an xcodegen target and signing the bundled daemon
  binaries are the fragile parts; both are validated by a real signed CI run,
  which requires the secrets. The local unsigned build de-risks the structure.
- Hardened runtime must not block the app spawning the daemon / running
  `install.sh` (it doesn't — process spawning is permitted; the binaries are
  signed).

## Files touched

- New `apps/mac/Clipfan/project.yml` (xcodegen).
- `apps/mac/Clipfan/Info.plist` — Sparkle keys + version build-setting refs.
- `apps/mac/Clipfan/Package.swift` — add Sparkle.
- New `apps/mac/Clipfan/Sources/Clipfan/Updater.swift`.
- `apps/mac/Clipfan/Sources/Clipfan/ClipfanApp.swift` — own the updater.
- `apps/mac/Clipfan/Sources/Clipfan/StatusMenuView.swift` — "Check for Updates…".
- New `.github/workflows/release.yml`.
