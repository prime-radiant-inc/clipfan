# Changelog

## [0.3.9] - 2026-06-01

### Fixed

- Peer update verification now probes signed remote daemon versions without macOS App Transport Security blocking cleartext peer HTTP.
- The release workflow now uses Node 24-based GitHub Actions for checkout and Go setup.

## [0.3.8] - 2026-06-01

### Changed

- App release versions and daemon update versions are now decoupled, so UI-only Mac app updates do not force peer daemon updates.

### Fixed

- The menubar icon now uses template artwork so it remains visible on dark menu bars.

## [0.3.7] - 2026-06-01

### Fixed

- Remote Linux updates now install correctly from staging directories that do not allow execution, such as `/tmp` mounted with `noexec`.
- Peer update verification now waits for the peer daemon to report the version just installed over SSH, so fleet rows turn green after the updated daemon is actually serving.
- The peer update sheet stays open with copyable verification details when install succeeds but daemon verification is still pending.

## [0.3.6] - 2026-06-01

### Changed

- The menubar item now uses a monochrome card-stack icon based on the app icon.
- New clipboard history entries animate an extra card sliding onto the icon stack after initial history load.

## [0.3.5] - 2026-06-01

### Fixed

- Peer update failures now leave a visible, selectable log with a Copy Log button.
- Fleet rows turn green after a peer update only after the signed daemon version probe verifies the peer is running the current version.
- The menubar fleet list has more room, stable timestamp columns, host-name truncation, and no default focus ring on the first command.

## [0.3.4] - 2026-06-01

### Added

- Signed `/v1/version` endpoint so authorized peers can report their installed clipfan version without exposing local-only state.
- The Mac app probes peer versions and offers to open Fleet settings when remote installs need an update.
- Sparkle appcast entries now embed release notes extracted from this changelog.

### Changed

- Fleet settings marks peers whose version is older or cannot be verified, while keeping the existing manual SSH update flow.

## [0.3.3] - 2026-06-01

### Added

- Existing peers can be updated from the Fleet settings screen over SSH.
- The `clipfan version` CLI reports the stamped daemon version after install.

### Fixed

- Linux updates restart the user service after replacing the daemon binary.

## [0.3.2] - 2026-06-01

### Fixed

- Release-build app updates refresh the installed local daemon binary after Sparkle replaces the app.

## [0.3.1] - 2026-05-31

### Fixed

- Hardened local endpoint access and remote install staging after the security scan follow-up.
