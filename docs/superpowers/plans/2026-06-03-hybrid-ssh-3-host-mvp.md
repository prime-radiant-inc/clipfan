# Hybrid SSH 3-Host MVP Plan

## Purpose

Replace the original comprehensive hybrid SSH rollout path with a smaller plan
that gets Clipfan to a live, usable, three-host SSH sync tool first.

The long design remains useful as a reference for edge cases and future
hardening. This plan is the execution path for the MVP.

## Product Target

The MVP is a direct SSH mesh across at least three hosts, including Linux:

- A fresh macOS controller plus at least two daemon peers.
- At least one peer is Linux.
- A copy on any host converges to the other two hosts.
- Runtime sync uses OpenSSH with Clipfan-managed command-locked keys.
- Regular user SSH remains the install, update, and provisioning authority.
- The Clipfan daemon never requires an off-host HTTP listener in the supported
  SSH configuration.

The topology is a direct mesh, not a relay network. For three hosts, there are
three unordered host pairs. Each pair has exactly one persistent SSH stream,
selected by deterministic host ID ordering. The stream is bidirectional, so both
hosts can originate clipboard changes over the same SSH channel.

## Non-Goals For MVP

Do not implement these before the direct three-host mesh works:

- On-demand SSH fallback.
- Relay or multihop sync.
- Automated sync-key rotation.
- Fleet `shared_key` rotation.
- Reciprocal outbound provisioning as a separate UI concept.
- Transport-current persistence.
- Rich remediation/tombstone history.
- Broad cleanup workflows beyond removing managed material that Clipfan created.
- Public-green Add Peer success before a command-locked SSH stream actually
  exchanges latest clipboard state.

Failure behavior is intentionally simple for MVP: if a direct stream is down,
that peer pair is down and the UI reports the reason.

## Current Foundation To Keep

Keep the existing branch foundations:

- Generated release gates that fail closed.
- Config v2 parsing, revision expectations, unknown-field preservation, and
  gated scoped writes.
- Current public manifests remain false until the explicit cutover milestone.
- Regular user SSH install/update remains separate from Clipfan sync keys.
- Peer HTTP version checks remain skipped when the generated runtime gate says
  peer HTTP is disabled.
- Local recovery/reset work may exist, but it must not grow into a blocker for
  the MVP unless it protects the no-public-HTTP cutover.

## MVP Architecture

### Host Identity

Each host has:

- Stable Clipfan host ID.
- Config v2 with `config_revision`.
- Fleet `shared_key`.
- Local sync keypair at the Clipfan-managed sync-key path.
- Local command gateway binary path.

The sync key identifies the host for OpenSSH authentication. The `shared_key`
remains the application credential for hello/auth and encrypted clipboard
payloads.

### Pair Model

For every unordered host pair:

- One host is `connect:true`.
- The other host is `accept:true`.
- The connect side owns the persistent SSH process.
- The accept side has a managed `authorized_keys` line for the connect side's
  sync public key.
- The connect side has a known_hosts entry pinned to the accept side's exact
  SSH host and port.
- The stream is bidirectional once established.

Pair direction is deterministic. Use host ID lexical ordering unless there is a
strong reason to prefer an existing local host as the connector during dogfood.

### Provisioning Model

The macOS app is the controller for MVP provisioning. It may require regular SSH
access from the Mac to every host in the fleet.

Adding host C to existing hosts A and B creates pair plans for A-C and B-C:

- Install or update Clipfan on C over regular SSH.
- Ensure C has config v2, host ID, fleet `shared_key`, and a local sync key.
- For each existing host, write the required known_hosts, managed
  authorized_keys line, and peer config on the two hosts involved in that pair.
- Verify the command-locked gateway version for each pair.
- Start or restart affected daemons.

This is centralized provisioning, not reciprocal setup. It is enough for a
three-host mesh and keeps the UI concept simple.

## Milestone 1: Scope Freeze And Release Gates

Goal: Stop the old plan from driving implementation.

Acceptance:

- This MVP plan is the active execution plan.
- Public release gates remain false.
- No new fallback, relay, rotation, or cleanup milestones are started.
- Existing tests still pass.

Verification:

```bash
go test ./...
cd apps/mac/Clipfan && swift test
bash scripts/test-ssh-release-gates.sh
```

## Milestone 2: Direct Pair Provisioning Primitive

Goal: Provision one command-locked direct pair over regular SSH.

Implement:

- Host-key confirmation and exact host/port known_hosts pinning.
- Local sync key creation/loading on both hosts.
- Managed `authorized_keys` forced-command line installation on the accept side.
- Minimal forced-command probe that proves the line runs Clipfan and not a
  shell.
- Scoped config writes for the two peer records involved in the pair.
- Redacted operation log visible in the Mac app.

Acceptance:

- From the Mac app, provision one pair between a macOS host and a Linux host.
- The accept side cannot get a shell through the Clipfan-managed key.
- Known_hosts mismatch fails closed before writing peer-ready state.
- Re-running provisioning is idempotent for the managed line and known_hosts
  entry.
- Remove/re-add is the supported repair path.

Do not implement:

- Rotation.
- On-demand receive.
- Relay.
- Cleanup tombstones.

## Milestone 3: Command-Locked Version And Shared-Key Verification

Goal: Prove the provisioned direct pair has the correct application credential.

Implement:

- Command-locked `version` gateway path.
- Hello/auth using the fleet `shared_key`.
- Remote config read/write over regular SSH for the final `shared_key` when the
  remote host is newly enrolled.
- Promotion only after command-locked version succeeds.

Acceptance:

- A newly installed Linux peer can receive the fleet `shared_key` through the
  controlled provisioning path.
- Wrong or missing `shared_key` fails at command-locked hello/auth.
- The UI reports `paired`, `auth failed`, `host key mismatch`, or `command line
  not installed` with copyable logs.

Do not implement:

- Automated `shared_key` rotation.
- Post-secret tombstones beyond a redacted failure log.

## Milestone 4: Persistent Bidirectional Sync Stream

Goal: Move latest clipboard state across one provisioned direct pair.

Implement:

- `sync-stream` forced command.
- Signed/encrypted hello.
- Latest-state frame.
- Ack/error/ping/pong frames.
- Bidirectional use of one SSH stream.
- Reconnect with bounded backoff.
- Existing clip echo, seen-set, concealed-clip, timestamp, and current-state
  rules reused from the HTTP path.

Acceptance:

- Copy on macOS appears on Linux.
- Copy on Linux appears on macOS.
- Concealed clips remain local-only.
- Restarting one daemon reconnects without re-provisioning.
- If the stream is down, the UI reports disconnected and does not pretend sync
  succeeded.

Do not implement:

- On-demand send when the stream is down.
- Relay through a third host.

## Milestone 5: Three-Host Full Mesh

Goal: Make the product useful for the real MVP: three hosts, including Linux.

Implement:

- Add Host operation that provisions direct pairs between the new host and all
  existing hosts.
- Deterministic one-stream-per-pair ownership.
- Daemon session manager that maintains all `connect:true,persistent:true`
  streams.
- Fanout of each new local latest-state update to all currently writable direct
  streams.
- Inbound latest-state apply and re-fanout only where the existing dedupe model
  says it is a real new latest state.

Acceptance:

- Fresh Mac + Linux + Linux fleet can be created from the Mac app.
- Copy on host A reaches B and C.
- Copy on host B reaches A and C.
- Copy on host C reaches A and B.
- Bringing down one direct pair degrades only that pair; remaining direct streams
  keep working.
- No daemon binds an off-host HTTP listener in the supported SSH config.

This is the first milestone that can be called the live dogfood MVP.

## Milestone 6: Linux Runtime Acceptance

Goal: Make Linux a first-class runtime peer, not just a build artifact.

Implement or verify:

- Linux install/update via `dist/install.sh` and systemd user service.
- Linux clipboard backend selection: Wayland, X11/xclip, and headless.
- tmux copy path still works with the Clipfan shim.
- Linux daemon can originate a text clipboard update.
- Linux daemon can receive and apply a text clipboard update.
- Image/path behavior remains no worse than the existing Linux behavior.

Acceptance:

- At least one graphical Linux peer passes real clipboard smoke.
- At least one headless or tmux-oriented Linux peer passes text/tmux smoke.
- The three-host MVP acceptance test includes Linux-originated copies.

## Milestone 7: Public Release Hardening

Goal: Add only the hardening required before people other than Jesse install it.

Mandatory:

- Public no-peer-HTTP runtime inventory: supported SSH config does not use public
  daemon HTTP for peer sync or peer version readiness.
- Host-key confirmation UX is understandable and hard to bypass accidentally.
- Managed `authorized_keys` line is clearly marked and removable.
- Remove Peer deletes local config records and best-effort removes only managed
  remote material that Clipfan created.
- Update path preserves existing sync keys, known_hosts, managed lines, and peer
  config.
- Failed provisioning is retryable and does not leave a peer marked green.
- Full release verification includes a real or fixture-backed three-host direct
  mesh acceptance test.

Still deferred after public MVP:

- Sync-key rotation.
- On-demand fallback.
- Relay.
- Advanced cleanup history.
- Reciprocal topology setup.
- Fleet credential rotation.

## Release Gate Cutovers

Use gates to express real readiness, not aspiration:

- `ConfigV2WriteEnabled`: only true when config v2 writes are the supported
  public config path and backup/recovery behavior for the listener cutover is
  acceptable.
- `PeerHTTPRuntimeDisabled`: true with the same local listener cutover that makes
  off-host HTTP unsupported.
- `RemoteSecretWriteReleaseEnabled`: true only after provisioning can write and
  verify the remote `shared_key`.
- `ssh_public_add_peer_success_enabled`: true only after the three-host direct
  mesh acceptance test passes.

Do not add environment-variable overrides for these generated gates.

## Verification Matrix

Required for every substantial implementation slice:

```bash
go test ./...
cd apps/mac/Clipfan && swift test
bash scripts/test-ssh-release-gates.sh
```

Also run:

```bash
bash dist/test-build-all-helper.sh
```

when release payload, install/update, Linux service, or dist artifacts change.

MVP acceptance requires a separate end-to-end smoke:

```text
Host A: macOS controller
Host B: Linux peer
Host C: Linux or macOS peer

1. Fresh install/update all three hosts.
2. Provision full direct mesh from the Mac app.
3. Confirm every pair has one command-locked persistent stream.
4. Copy text on A; B and C converge.
5. Copy text on B; A and C converge.
6. Copy text on C; A and B converge.
7. Stop one stream; remaining direct streams keep working.
8. Confirm no daemon is bound to an off-host HTTP listener.
```

## Operating Principle

Prefer a small system whose failures are visible over a large system whose
fallbacks hide broken provisioning. The MVP should make direct SSH sync reliable
for three hosts before adding resilience features.
