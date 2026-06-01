# Changelog

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

