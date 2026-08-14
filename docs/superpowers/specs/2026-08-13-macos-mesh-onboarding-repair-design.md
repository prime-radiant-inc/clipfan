# macOS Mesh Onboarding Repair Design

## Problem

The macOS onboarding installer can print `Loaded launchd job` after
`launchctl load` reports `Load failed: 5: Input/output error`. The app then
falls back to running the daemon as a child process, while the launchd job
remains absent or disabled. Later private-mesh Add Peer provisioning calls
`launchctl kickstart` directly, receives a non-zero status, and throws
`local_daemon_restart_failed` before the existing full-mesh heal runs.

The result is a durable pairwise config mutation without the intended
enrollment of the new Mac with the already-enrolled peers.

## Goals

- Make onboarding fail clearly when the macOS daemon service was not actually
  registered, instead of claiming success and leaving a child-only daemon.
- Repair an existing installed Mac's launchd service automatically when the app
  starts and the daemon is down.
- Make local daemon restart use the modern launchd `enable`/`bootstrap` path,
  with legacy `load` only as a fallback, before `kickstart`.
- Preserve the existing `mesh-heal` orchestration so adding a Mac enrolls all
  reachable peers after the explicit pair is provisioned.
- Keep the project repository free of Homebrew or machine-local artifacts.

## Non-goals

- Redesign the mesh protocol or peer configuration model.
- Change the existing pairwise provisioning security gates.
- Make unreachable peers appear successfully enrolled.
- Add a public Homebrew tap.

## Design

### Launchd installation

The Darwin branch of `dist/install.sh` will write the plist, enable the user
agent, bootstrap it into `gui/$user_uid`, fall back to `launchctl load` only if
bootstrap fails, and kickstart the service. It will verify the service with
`launchctl print` before reporting success. `--no-restart` will continue to
write the plist without enabling, loading, or verifying the job.

### App startup recovery

When the installed daemon is current but not answering, the app will rerun the
bundled installer in `upgradeExisting` mode. This repairs the launchd plist and
service registration before using the existing child-process fallback. A failed
repair remains visible through the existing onboarding failure state.

### Local restart

The app's local restart helper will use the same launchd sequence as the
installer: enable, bootstrap/load, then kickstart. This makes Add Peer robust
even when the prior onboarding attempt left a disabled or unloaded service.

### Mesh enrollment

No new mesh algorithm is needed. Once local restart succeeds, the existing
`provisionPrivateDirectMeshAndHeal` path proceeds from the explicit pair to
`mesh-heal`, which discovers the complete roster, provisions missing cross
edges, and restarts only changed peers. Existing per-host failure reporting
remains authoritative for unreachable peers.

## Verification

- Add a Darwin installer shell test with a fake `launchctl` that proves the
  bootstrap, fallback, kickstart, and verification sequence, plus the
  `--no-restart` gate.
- Add Swift coverage for the local launchd command sequence and startup
  recovery mode.
- Run the full Swift test suite, the Darwin installer test, shell syntax checks,
  and the existing Go test suite.
