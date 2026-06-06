# Changelog

## [0.3.23] - 2026-06-05

### Fixed

- Private SSH mesh setup now detects Tailscale SSH servers and probes them through a direct `ssh-gateway` command instead of relying on `authorized_keys` forced commands that Tailscale SSH bypasses.
- Persistent SSH sync now uses direct gateway startup for peers verified through Tailscale SSH while keeping the existing managed-key path for OpenSSH servers.

## [0.3.22] - 2026-06-05

### Fixed

- Add Peer private SSH mesh setup now updates the local installed helper from the bundled app payload before provisioning, but runs provisioning through the bundled helper so stale installed helpers are never trusted.
- Add Peer no longer asks users to choose Linux or macOS for manual hosts; setup infers remote platform paths from `uname` while preserving custom paths.

## [0.3.21] - 2026-06-05

### Fixed

- Private SSH mesh provisioning now installs managed Clipfan sync keys before user key entries so stale unrestricted authorized_keys lines cannot bypass the forced ssh-gateway command.

## [0.3.20] - 2026-06-05

### Fixed

- Private SSH mesh provisioning now prefers ED25519 host keys from keyscan output instead of accepting an RSA key just because it appears first.
- Runtime known_hosts updates now allow multiple key types for the same exact host while still rejecting same-key-type, wildcard, hashed, or marked conflicts.
- Add Peer now asks for SSH keyscan trust at the moment of provisioning instead of requiring a persistent checkbox before the button can be used.

## [0.3.19] - 2026-06-05

### Fixed

- Private SSH mesh probes now use only the generated sync key and never fall back to normal SSH identities when validating a peer.
- Add Peer now replaces stale unrestricted authorized_keys entries for the same Clipfan sync key so older installs cannot bypass the managed forced-command gateway.

## [0.3.18] - 2026-06-05

### Fixed

- Add Peer now detects this Mac's SSH callback address from the remote host's observed SSH connection, avoiding local hostname guesses during direct mesh setup.
- Direct mesh setup now handles raw IPv6 callback hosts in SSH keyscan and SCP upload paths.

## [0.3.17] - 2026-06-05

### Fixed

- Add Peer now derives this Mac's SSH callback host automatically from macOS LocalHostName and no longer requires entering the local Mac name during setup.
- Advanced local SSH settings keep optional user, port, and host override controls without blocking the automatic path.

## [0.3.16] - 2026-06-05

### Fixed

- Add Peer now prefers `.local` SSH names for selected macOS tailnet hosts when MagicDNS is unavailable, avoiding mesh configs that use a Tailscale IP another peer cannot reach.
- Private SSH mesh probes now fail quickly with an explicit timeout when a peer cannot open the pinned SSH connection.

## [0.3.15] - 2026-06-05

### Added

- Fleet settings can now remove a host from the local fleet config, with a signed daemon API and `clipfan remove-host` CLI path.

### Fixed

- Remove Host clears both legacy `static_peers` rows and SSH peer config entries, including stale hosts left from earlier setup attempts.
- Remove Host now treats daemon restart/refresh problems as post-remove warnings instead of reporting that the config removal failed.

## [0.3.14] - 2026-06-05

### Fixed

- Add Peer now discovers the local Mac's Tailscale identity from the macOS Tailscale app binary when GUI PATH does not include `tailscale`.
- Private SSH mesh setup no longer falls back to short local hostnames such as `m4` for the local mesh endpoint, avoiding failed keyscans against names that remote peers cannot resolve.

## [0.3.13] - 2026-06-05

### Fixed

- Private direct SSH mesh bootstrap now honors user SSH config and default identities for regular admin auth while keeping pinned runtime SSH targets resolved and managed.
- Bootstrap host-key discovery now accepts OpenSSH's default `HostKeyAlias none` output without failing before keyscan.

## [0.3.12] - 2026-06-05

### Changed

- Add Peer now provisions one private SSH mesh peer at a time, with single-selection tailnet picking and simpler private mesh copy.

### Fixed

- Private direct SSH mesh setup now seeds regular SSH `known_hosts` before strict bootstrap probes, including local-host pins needed by direct provisioning.
- Host key seeding now uses OpenSSH lookup behavior for existing pins, detects conflicts without silently rotating keys, and supports non-ED25519 SSH host keys.

## [0.3.11] - 2026-06-04

### Fixed

- Direct SSH mesh provisioning now migrates existing static configs into revisioned config v2, persists missing host IDs, and clears stale legacy peer rows when switching to SSH transport.

## [0.3.10] - 2026-06-04

### Added

- Private direct SSH mesh provisioning for live three-host testing with macOS and Linux hosts.
- Add Peer now exposes remote host rows for direct SSH mesh setup while keeping public Add Peer release gates closed.
- The app can bootstrap remote daemon payloads, tmux setup, shared sync key material, and direct SSH mesh config without the old peer HTTP deploy path.
- Config v2 parsing, revision validation, gated scoped config updates, and safe-mode recovery plumbing for the SSH transport rollout.

### Changed

- Peer HTTP fanout and version checks are skipped when the generated SSH runtime gates disable peer HTTP.
- Remote update and install paths now use stricter SSH host-key handling and safer staged payload installation.

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
