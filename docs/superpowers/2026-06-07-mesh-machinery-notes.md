# Mesh machinery notes (research-verified, for building Phase 1/2)

Ground-truth reference for implementing `docs/superpowers/plans/2026-06-07-clipfan-mesh.md`, from a deep read of the real code. These supersede any conflicting claim in the plan; build to these.

## 1. Self-addressing (`callback_host` / remote-observed) — PLAN WAS WRONG

A host has **no peer entry for itself**; `config.Load()` does NOT contain the host's own externally-reachable `ssh_host`. The plan's Task 1.7 "seed the local host from `config.Load()`" cannot supply the local endpoint's address. There is **no Go implementation** of this; it must be ported from Swift.

**Mechanism (port from `Installer.swift:1099-1126`, `:552-585`):** to learn the address peers use for the local host J, pick an *observer* peer P (P.id ≠ J), regular-SSH from J to P running this **verbatim zsh-safe** snippet, and take its single output line as J's `ssh_host`:
```
v=${SSH_CONNECTION:-$SSH_CLIENT}; v=${v%% *}; test -n "$v" || exit 44; printf '%s\n' "$v"
```
- Validate the result (port `validatePrivateDirectMeshSSHHost`, `Installer.swift:1170-1183`: reject whitespace/shell-metachars; allow raw IPv4 / valid IPv6).
- **Remote** hosts: their `ssh_host` is already in the local config peer list — no observation needed.
- **Local** host only: needs observation. Improve on Swift: try observers in turn (don't hard-fail on the first dead peer); prefer an observer reached over the same transport you want recorded (Tailscale → the `100.x` form, matching live `m4 = 100.114.54.38`).
- The Go `ssh-provision-direct` host-spec parser has NO `callback_host` field — the observed address must already be substituted into the `ssh=` field of the local host's `--host` spec (Swift does this at `Installer.swift:582-584`). Provenance is not persisted to config.

## 2. Two host-key trust stores + ONE keyscan — PLAN'S TRUST STEP WAS WRONG

Two known_hosts files: (a) the orchestrator's **regular** `~/.ssh/known_hosts` (so `StrictHostKeyChecking=yes` admin SSH works) and (b) each target's **sync** `~/.config/clipfan/ssh/known_hosts` (written remotely via `ssh-install-known-host`).

- **Regular trust MUST use `ssh-keygen -F <token> -f <known_hosts>`** (hashed-aware), NOT a `[]string` scan — a user's known_hosts routinely has `|1|…` HMAC-hashed entries a string scan can't match. Treat `ssh-keygen -F` **exit status 1 as "no match"** (the `CommandRunner` must surface exit codes; add a `RunAllowExit1`-style path). Port the conflict loop from `Installer.swift:741-771` (marker→conflict; same key→already-pinned; different key→conflict). Reuse `sshprovision.writeKnownHostsAtomic` (`known_hosts.go:551`). **Do NOT** reuse `verifyKnownHostPinData` for the regular store (it flags every hashed entry as a conflict, `known_hosts.go:205-233`).
- `~/.ssh/known_hosts` is often `0644`; do not reject on perms (the strict `UpsertKnownHostPin` read path rejects non-0600/nlink≠1 — don't use it for the regular store). Force `0600` on write (Swift `:782`). Taking the `.lock` around the regular write is fine (protects concurrent mesh-heal).
- **Unify the scans:** factor the per-host body of `scanSSHProvisionDirectHostKeys` (`ssh_provision_direct.go:267-304`) to **return the raw `ssh-keyscan` stdout** (+ resolved target + server mode). Feed that ONE output to BOTH: the regular-trust step (all key types, via `ssh-keygen -F` conflict check) and `selectSSHProvisionDirectHostKeyLine` (single best type → sync pin). This kills the double-scan/divergent-key risk. (Plan Task 1.5 already wants this factor — make it return the raw stdout.)
- `--trust-keyscan`: **one flag** gates both (same trust decision; you can't provision sync pins without first trusting the regular key to deliver the command). Mirror the existing hard-fail-when-unset.

## 3. Daemon fleet gather — PLAN'S "reuse process-starter" WAS WRONG

The daemon's `execSSHProcessStarter` is **stream-only** (pipes for the long-lived sync stream; no captured output). For the one-shot `fleet-snapshot` gather use `sshprovision.ExecCommandRunner` (captured, `MaxOutputBytes`-bounded). The daemon doesn't import it today but may (same module).

**Recommended (new `internal/daemon/fleet_aggregate.go`):** re-derive peers via `sshSyncPeersFromConfig(d.cfg)` (same package; note it filters on `Enabled && Connect && Persistent && ssh_keys_ready` — `Connect`, not `Accept`); per peer build `PinnedSSHFleetSnapshotCommand` from its fields (`AuthorizedPeer = localID`, `AuthorizedKeyID = p.connectKeyID`, `DirectGateway = p.directGateway`); run with `ExecCommandRunner{MaxOutputBytes: 256*1024}`; **strict-decode** by copying `regular_ssh_driver.runJSON` (reject truncation, reject non-empty stderr, reject trailing JSON). On any error → mark that host `unknown`.
- **Bounds:** per-peer `context.WithTimeout` (~10s); bounded concurrency (semaphore 4-8); reject on `StdoutTruncated`. On-demand with a short TTL cache (5-10s) behind a mutex — NOT a background poll.
- **Serve `GET /v1/fleet`** mirroring `peersHandler` (`daemon.go:440-453`) + `SetCurrentFunc`/`getCurrent` (`transport/server.go:154,583-593`, signed loopback via `readSignedLocal`/`writeSignedJSON`). Wire `d.sv.SetFleetFunc(d.fleetHandler)` next to `SetCurrentFunc` (`daemon.go:206`). Mac reads it like `/v1/peers` (Phase 4) — no Mac SSH client.
- **Riskiest:** the redacted `buildFleetSnapshot` payload must be an explicit allowlist struct (no `shared_key`/proof/sync-key); Task 2.1's test asserts a `shared_key`-set config marshals with none of those substrings. Don't rely on the diagnostics redaction layer.

## 4. Gateway `fleet-snapshot` verb — exact 7-edit checklist

1. Const `SSHGatewayFleetSnapshotCommand = "fleet-snapshot"` in `internal/sshprovision/authorized_keys.go:21-23`.
2. Handler field `FleetSnapshot func(SSHGatewayIdentity, io.Writer) error` in `SSHGatewayHandlers` (`internal/cli/ssh_gateway.go:34-37`).
3. Register in `defaultSSHGatewayHandlers` (`:87-102`) — optionally behind a new `releaseflags.SSHFleetSnapshotEnabled` gate (add to `internal/releaseflags/ssh_runtime_gates.go`; mirror `SSHSyncStreamEnabled`).
4. Switch `case sshprovision.SSHGatewayFleetSnapshotCommand:` in `runSSHGatewayWithHandlers` (`:71-84`), nil-check + call.
5. Auth: a `validateSSHGatewayFleetSnapshotPeer` modeled on `validateSSHGatewaySyncPeer` (`:326-346`) — default to the full predicate (`Enabled && Accept && Connect && Persistent && ssh_keys_ready` + `Proof.AcceptKeyID == identity.KeyID`); a read-only verb MAY relax to accept-side only, decide explicitly (security boundary).
6. `PinnedSSHFleetSnapshotCommand(spec) → pinnedSSHGatewayCommand(spec, SSHGatewayFleetSnapshotCommand)` in `internal/sshprovision/ssh_command.go:101-107`.
7. Add the verb to the `directSSHGatewayCommand` allowlist switch (`ssh_command.go:143-147`) — else Tailscale-direct (`DirectGateway=true`) calls fail to build.
No `cmd/clipfan/main.go` edit (it's a verb within `ssh-gateway`). Tests: mirror `ssh_gateway_test.go` (allow-injected-handler `:122-165`, reject-unknown `:386-431`, auth-reject `:358-384`, constant assertion `:461-485`).

## 5. Provisioning construction + test harness (for Tasks 1.5-1.7)

- `DirectPairProvisionHost{Host, AdminHost DirectPairHost, KnownHostsPath, SyncKeyPath}`. `Host` = **resolved runtime** endpoint (from `ssh -G` + keyscan); `AdminHost` = the **admin alias** (pre-resolution `ssh`/`port` from the spec); the per-host **config path is NOT a struct field** — it rides in `RegularSSHProvisionDriver.ConfigPathByHostID`. `Host.GatewayPath` defaults to `InstallPath`. `Host.SSHServerMode` from spec or keyscan-banner detection (`SSH-2.0-Tailscale`).
- If mesh-heal already holds resolved endpoints + confirmed host-key lines, it can **skip per-pair keyscan** and feed `ConfirmedHostKeyLines` + `ConfigPathByHostID` straight into a `RegularSSHProvisionDriver` + `DirectPairProvisioner`.
- **Test harness to reuse:** CLI seam = `sshProvisionDirectOptions{Runner, ConfigV2WriteGate, SharedKey}` (`ssh_provision_direct.go:34-38`); fake `CommandRunner` dispatches on `command.Args` (`ssh_provision_direct_test.go:691-817`); executor seam = `fakeDirectPairDriver` implementing the 6-method `DirectPairProvisionDriver` with an op log (`pair_executor_test.go:237-310`). `runMeshHeal` should take the same option-injection shape so its tests use these fakes.
