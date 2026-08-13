# Changelog

## [1.0.10] - 2026-08-13

### Added

- Added cross-platform macOS and Linux release tarballs for amd64 and arm64, plus a universal installer.

### Fixed

- Source-built macOS apps now embed Sparkle and KeyboardShortcuts resources, and new macOS installs bootstrap self-SSH for mesh provisioning.
- Release builds now resolve versions consistently and allow unsigned manual and fork builds while requiring all Apple and Sparkle credentials for signed artifacts.

## [1.0.9] - 2026-06-18

### Fixed

- Fixed Clipfan idle CPU regressions by removing the menu app's hidden background daemon polling and backing off the daemon clipboard poll after repeated idle reads.
- Prevented stale tmux servers that still have old global buffer hooks from recirculating daemon writebacks through `clipfan copy`.
- Daemon upgrade detection now compares the bundled helper bytes with the installed helper instead of trusting matching version strings, so stale same-version payloads are replaced.

## [1.0.8] - 2026-06-14

### Fixed

- App-launched macOS daemons now get the local and Homebrew tool paths they need to detect screenshot and Preview image copies when the menu app starts the daemon itself.

## [1.0.7] - 2026-06-11

### Fixed

- Large clips (up to the 64 MiB stream payload limit) now sync: the daemon's `/v1/current` apply endpoint accepts bodies up to the stream frame limit instead of silently truncating at 1 MiB and failing signature verification with a 401.
- A clip a peer cannot apply no longer kills the sync stream or poisons the reconnect loop: the gateway answers with a `rejected` ack and keeps serving, and the sending daemon drops the rejected clip from its pending queue instead of resending it on every reconnect.
- The gateway now tolerates transient local-daemon poll failures (up to ~10 s) instead of tearing down the stream on the first error.
- Clips larger than the stream payload limit are skipped at publish time with a logged warning instead of wedging the per-peer send queue.
- The daemon now logs the stderr of its ssh sync processes, so remote gateway errors (previously discarded) are visible in the daemon log.

## [1.0.6] - 2026-06-10

### Fixed

- Fixed a copy-triggered menu bar lockup by replacing the status item `TimelineView` animation with a bounded sequence of pre-rendered frames.

## [1.0.5] - 2026-06-10

### Added

- The menu bar now shows **Install Update…** when Sparkle finds a new app release, and startup probes recommend available app updates through Sparkle's standard installer.

### Fixed

- Fixed the menu bar copy animation so new cards slide into the stack in the same staged fan motion as the prototype.

## [1.0.4] - 2026-06-10

### Fixed

- Restored subtle menu bar card faces so dark mode and the copy animation read as a stacked hand without regressing to a solid square.

## [1.0.3] - 2026-06-10

### Fixed

- Fixed the menu bar icon rendering as a black square by switching the compact status glyph to outline template artwork with enough negative space.

## [1.0.2] - 2026-06-10

### Added

- Animated the menu bar icon so new local clipboard writes fan a card into the stack.

### Changed

- Removed row icons from the menu bar dropdown.
- Open Clipfan windows now participate in the macOS app switcher.
- Aligned the app icon, About screen icon, and menu bar card stack artwork.

### Fixed

- Removed the global tmux buffer hooks from the installed tmux integration so daemon writebacks, unrelated tmux sockets, and stale paste buffers cannot re-submit as fresh local copies.
- Prevented mesh-heal from scanning virtual/private bridge address piles by filtering Docker, bridge, VM, and tunnel interfaces before roster-read advertises LAN candidates.
- Mesh-heal now fails closed when a host reports an oversized LAN candidate list, keeping the primary address instead of keyscanning hundreds of self-reported candidates.

## [1.0.1] - 2026-06-10

### Changed

- Tagged but not published; replaced by v1.0.2 before release after the tmux buffer hook issue was found.

## [1.0.0] - 2026-06-09

### Removed

- Removed the disabled peer HTTP sync runtime, including the `/v1/clip` receive route, peer HTTP push client, daemon fanout path, peer HTTP release gates, and Swift peer-version probing/update-offer UI.

### Changed

- `clipfan copy` and the SSH gateway now apply clipboard updates to the local daemon through the signed loopback `/v1/current` endpoint.
- Fleet sync is SSH-stream-only, and config v2 writes are now treated as baseline release behavior instead of a generated transport gate.
- Current docs and release-gate scripts now describe the SSH sync architecture rather than the removed peer HTTP implementation.

## [0.4.3] - 2026-06-07

### Fixed

- mesh-heal now heals a peer that shares only a LAN with the rest of the fleet (a peer on a different tailnet): when a host's Tailscale address is unreachable from a peer it must mesh with, mesh-heal falls back to a LAN address that peer can reach, verified by the host's SSH key (so a shared docker/bridge address can't be mistaken for the peer). Resolves the cross-tailnet known limitation noted in 0.4.0.

## [0.4.2] - 2026-06-07

### Changed

- About is now a pane in Settings instead of a separate window; the menubar "About clipfan…" opens it there.
- Mesh health appears inline on each fleet row, with an expandable per-edge breakdown, instead of a separate card; "Repair mesh" is now a fleet-level button next to Refresh.
- Removed the redundant "Set up clipfan…" menubar entry; re-running setup lives on the Diagnostics pane.

## [0.4.1] - 2026-06-07

### Security

- The local API client no longer follows HTTP redirects, so a request's nonce-bound signed headers can never be forwarded to a redirect target.
- The SSH gateway's `fleet-snapshot` and `sync-stream` verbs no longer return the config file path to the calling peer when local config is missing — the error is now opaque.

## [0.4.0] - 2026-06-07

### Added

- Self-healing mesh provisioning: adding a host now heals the whole fleet into a full mesh — it provisions the new host's edges to every existing peer (not just the pair you added) and restarts only the daemons it changed. Exposed as `clipfan mesh-heal` and run automatically from Add Peer, with a one-click "Repair mesh" action.
- Mesh-state visibility in the macOS app: the daemon aggregates a redacted per-host fleet view (`GET /v1/fleet`, gathered over each peer's pinned SSH) and the Fleet settings tab shows per-host edge health, with unobserved edges distinguished from down ones.
- First-run onboarding wizard: welcome → local setup → add a device → done, reusable from the menubar ("Set up clipfan…").
- About screen.
- `clipfan roster-read` self-report and a `fleet-snapshot` SSH gateway verb (redacted; no secrets leave the host).

### Changed

- Rewrote the README to be user-focused and moved developer documentation under `docs/`.
- Add Peer now dismisses automatically after a successful install and reports the mesh-heal summary.

### Fixed

- Fixed Mac→host paste.

### Known limitations

- A peer on a different tailnet that shares only a LAN with the others can't be reached by its Tailscale address; mesh-heal surfaces the unreachable edges but does not yet fall back to a LAN address for cross-tailnet peers.

## [0.3.29] - 2026-06-06

### Changed

- Fleet rows now show SSH transport state and endpoint diagnostics instead of legacy network-sync port details for private SSH mesh hosts.
- Add Peer copy now better matches the automatic macOS/Linux detection and callback-address flow.

### Fixed

- SSH peer health now reflects persistent SSH runtime activity instead of stale legacy push status.
- Stale persistent SSH sessions can no longer overwrite current peer health after reconnects, including stale connect, pending, receive, ack, and error paths.

## [0.3.28] - 2026-06-06

### Fixed

- Linux peers now load received clips into standard tmux sockets whose default permissions are group-writable inside the private user socket directory, restoring tmux paste-buffer sync for headless hosts.

## [0.3.27] - 2026-06-05

### Fixed

- The macOS release now bundles a `v0.3.27` daemon payload so installed daemons are actually upgraded to the fleet-status and SSH config refresh fixes from `0.3.26`.

## [0.3.26] - 2026-06-05

### Fixed

- Fleet status now shows ready SSH peers immediately after Add Peer provisioning, even before clipboard traffic has been exchanged.
- SSH peer config mutations now refresh the daemon's in-memory config, discovery snapshot, and persistent SSH sessions without requiring a daemon restart.
- Removed or disabled hosts no longer linger in the fleet UI because of stale push/receive activity.

## [0.3.25] - 2026-06-05

### Fixed

- Add Peer now parses the remote-observed callback host correctly when the remote login shell is `zsh`, avoiding `invalid_remote_observed_callback_host` during private SSH mesh setup.

## [0.3.24] - 2026-06-05

### Fixed

- Private SSH mesh provisioning can now be safely retried for peers that are already staged or ready, refreshing config without trying to reset their migration state.
- Direct config apply now rejects unsupported existing SSH peer states before mutating peer fields, avoiding partial config updates on failed retries.

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
- The app can bootstrap remote daemon payloads, tmux setup, shared sync key material, and direct SSH mesh config without the old HTTP deploy path.
- Config v2 parsing, revision validation, gated scoped config updates, and safe-mode recovery plumbing for the SSH transport rollout.

### Changed

- Legacy HTTP sync and version checks stay out of the SSH transport runtime while direct SSH mesh work continues.
- Remote update and install paths now use stricter SSH host-key handling and safer staged payload installation.

## [0.3.9] - 2026-06-01

### Fixed

- Peer update verification now probes signed remote daemon versions without macOS App Transport Security blocking the request.
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
