# macOS OpenSSH Fixture Readiness

This document defines the CI readiness bar for the real macOS OpenSSH fixture
used by hybrid SSH milestones. It must be satisfied before any public
OpenSSH-dependent milestone treats the fixture as release-blocking.

## Owner

The runner owner is the repository release maintainer. The owner is responsible
for runner availability, credential hygiene, user cleanup, and deciding when the
fixture-available gate may be marked green.

## Runner Selection

Use the GitHub-hosted `macos-26` runner when it can provide a real OpenSSH
server fixture with:

- `sshd` available and controllable by the job.
- Per-run, non-admin fixture accounts.
- Writable ephemeral home, config, key, and known_hosts directories.
- No dependency on persistent host secrets.

If hosted `macos-26` cannot satisfy those conditions, use the fallback
self-hosted runner named `clipfan-macos-ssh-fixture`. The self-hosted runner must
be dedicated to this fixture class, owned by the repository release maintainer,
and isolated from developer laptops and unrelated CI workloads.

## Fixture Users

Each run must use dedicated non-admin fixture users. They must be either:

- Created uniquely for that run and deleted during cleanup.
- Reused only after a cleanup check proves their home, SSH state, processes,
  authorized_keys, known_hosts, and temp directories contain no prior run data.

The fixture user must not have admin privileges, passwordless sudo, persistent
SSH agent material, shell profile dependencies, or access to the maintainer's
normal account data.

## Secret Handling

The fixture must never store the fleet `shared_key` or private sync keys outside
ephemeral test directories created for the current run. In particular, tests may
not write those secrets to:

- The runner user's normal home directory.
- Persistent keychains.
- Global or user OpenSSH config.
- Shared temp directories not namespaced to the run.
- Cached artifacts, logs, workflow summaries, or uploaded test output.

All fixture secrets must be generated per run or injected only into per-run
ephemeral paths, and cleanup must remove those paths before the gate reports
success.

## Cleanup and Isolation Gate

The fixture-available gate is green only when all checks below pass:

- The runner selected is either eligible hosted `macos-26` or the self-hosted
  runner label is exactly `clipfan-macos-ssh-fixture`.
- Fixture accounts are non-admin and scoped to the run, or a pre-run isolation
  check proves reusable accounts are clean.
- No prior fixture `sshd`, gateway, daemon, agent, or test process remains for
  the fixture users.
- Per-run home, state, config, key, known_hosts, authorized_keys, log, and temp
  directories are absent before setup or created empty with expected ownership
  and modes.
- Managed `.ssh` and `authorized_keys` paths are owned by the fixture user and
  are not group- or world-writable.
- The user's normal `~/.ssh/config`, normal `known_hosts`, login shell profile,
  and keychain are not read or mutated by fixture setup.
- Post-run cleanup deletes fixture users created for the run, removes all
  per-run directories, kills remaining fixture-owned processes, and verifies no
  `shared_key` or private sync key bytes remain in fixture paths.
- Logs and artifacts are redacted and contain no private keys, fleet
  `shared_key`, HMAC material, nonces paired with signatures, raw clipboard
  payloads, or unredacted command argv containing secret paths.

Any failed cleanup or isolation check makes the fixture unavailable for blocking
purposes.

## Sentinel and Blocking Behavior

Before Milestone 17d3b, and specifically for the Milestone 17d3a local
listener/config cutover, this fixture may run as a non-blocking sentinel. A
sentinel failure must be visible in CI output, but it must not block 17d3a
packaging or the local hardening gate by itself.

OpenSSH-dependent milestones and 17d3b blocking modes fail closed. If the
fixture is missing, ineligible, dirty, unable to create isolated users, or unable
to prove cleanup, the gate must report `macos_ssh_fixture_unavailable` and must
not fall back to fake SSH, peer HTTP, developer SSH config, or persistent
credentials.

Once a milestone depends on real macOS OpenSSH behavior,
`macos_ssh_fixture_unavailable` is a release-blocking failure until the
repository release maintainer restores a green fixture-available gate.
