# clipfan Mesh Implementation Plan

> **Superseded where they conflict:** the code-verified mechanics live in
> [`../2026-06-07-mesh-machinery-notes.md`](../2026-06-07-mesh-machinery-notes.md);
> where this plan and the notes disagree, build to the notes.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. **This plan is SEQUENTIAL** — the foundations gate everything else; do not blind-fan-out.

**Goal:** Make any provisioning operation heal the whole fleet into a full mesh, drive it from a Go CLI command runnable on any host, and show the resulting mesh state in the macOS app.

**Architecture:** `clipfan mesh-heal` (Go CLI) reuses the per-pair provisioner (`sshprovision.DirectPairProvisioner.Provision`) and adds: decentralized roster discovery, per-host-resilient host-prep, per-edge change-detection (idempotency), host-key trust into the regular `known_hosts`, and a Go daemon-restart. Both discovery and change-detection are powered by a new `roster-read` subcommand a host runs on itself (reporting its own paths/platform/uid/gateway/peers), invoked over regular SSH. For mesh-state visibility, the **Go daemon** gathers a redacted `fleet-snapshot` from each peer over its existing pinned-sync-key SSH machinery and exposes an aggregated view on a local loopback endpoint the Mac app reads — so the Mac needs no SSH client of its own.

**Tech Stack:** Go (`internal/`, `cmd/clipfan`), SwiftUI/AppKit (`apps/mac/Clipfan`). Go tests: `go test ./...`. Swift: `cd apps/mac/Clipfan && swift test --filter <Name>`.

**Source spec:** `docs/superpowers/specs/2026-06-07-clipfan-mesh-onboarding-docs-design.md`.

**Verified existing APIs this plan builds on:**
- `sshprovision.DirectPairProvisioner.Provision(ctx, DirectPairProvisionInput{Local, Remote DirectPairProvisionHost, SharedKey string}) (…, error)` provisions one pair, unconditionally (always bumps config revision). `internal/sshprovision/pair_executor.go:75`. `DirectPairProvisionHost{Host DirectPairHost, AdminHost DirectPairHost, KnownHostsPath, SyncKeyPath string}`; `DirectPairHost{ID, SSHHost, SSHUser, SSHPort, InstallPath, GatewayPath, SSHServerMode}` — `normalizeDirectPairHost` (`pair_plan.go:165`) **requires a valid absolute `GatewayPath`** (else error); the existing CLI defaults `gateway=install` (`ssh_provision_direct.go:202-205`).
- `RegularSSHProvisionDriver{Runner, RegularKnownHostsPath, ConfirmedHostKeyLines map[string]string, ProvisionHosts, ConfigPathByHostID}`; `ConfirmHostKey` reads pre-scanned pins keyed by `host.ID`; other methods SSH to `adminHostFor(host)`; `WriteConfig` is a two-phase **direct file write**, no reload. `regular_ssh_driver.go`.
- **Command builders** (`internal/sshprovision/ssh_command.go`): regular-SSH ops are built by `regularSSHRemoteCommand` (line 367) with `StrictHostKeyChecking=yes -o UserKnownHostsFile=<RegularKnownHostsPath>` + `<installPath> <subcommand>`; e.g. `RegularSSHRunProbeCommand` (line 279). **There is no builder for an arbitrary `<install> roster-read` — add `RegularSSHRosterReadCommand` mirroring `RegularSSHRunProbeCommand`.** Pinned (sync-key) ops: `PinnedSSHProbeCommand`/`PinnedSSHSyncStreamCommand` (lines 101/105) → `pinnedSSHGatewayCommand`/`directSSHGatewayCommand` (109/142); the Tailscale-direct path hard-rejects any verb but probe/sync-stream — **add a `PinnedSSHFleetSnapshotCommand` and allow `fleet-snapshot` in `directSSHGatewayCommand`.**
- **Host-key trust:** regular-SSH uses `StrictHostKeyChecking=yes`; the Go side never writes the regular `known_hosts` (the scan only fills in-memory *sync* pins). The Swift `trustPrivateDirectMeshBootstrapHostKey` (`Installer.swift:691-770`) does `ssh-keyscan -T 5 -p <port> <host>` then upserts the regular `known_hosts` (with a conflict check). **Port this to Go; `mesh-heal` must trust each endpoint's host key before SSHing to it.** Gated by `--trust-keyscan`.
- `config.Load() (*Config, error)`, `config.Path() string`. **`config.ReadSSHPeer` is LOCAL-only + returns a redacted `map[string]any]` — do NOT use it for remote/typed reads.** `config.Config.SSH` is `*SSHConfig` (nil-guard). `config.SSHPeer{ID, SSHHost, SSHUser, SSHPort, InstallPath, GatewayPath, MigrationState, Proof SSHProof, Enabled, Accept, Connect, Persistent}` (`Enabled/Accept/Connect` are `omitempty`); `SSHProof{AcceptKeyID, ConnectKeyID, …}`; `MigrationStateSSHKeysReady == "ssh_keys_ready"`. A host has **no peer entry for itself** (so the local host is seeded from `config.Load()`, not via SSH). An `accept`-only peer may omit `ssh_host/user/port` (`validateSSHPeer` requires them only when `Connect` — `ssh.go:163`).
- Subcommand dispatch: add `case`s in `cmd/clipfan/main.go` (mirror `case "ssh-run-probe":`). `internal/cli` already imports `internal/daemon` (no cycle). `GET /v1/peers` is served to a signed loopback client (`transport/server.go:205`); `daemon.PeerState` carries `last_recv_ts`/`ssh_last_ack_ts`/`ssh_last_connect_ts`/`ssh_status`/`ssh_active`.
- Restart shell to port **verbatim** (it is platform-AGNOSTIC, one script): `Installer.swift:1268-1286` — `if command -v systemctl …; then systemctl --user daemon-reload; enable; restart clipfan.service && exit 0; fi`, then `if command -v launchctl …; then launchctl enable+bootstrap/load + kickstart gui/<uid>/com.primeradiant.clipfan && exit 0; fi`, then unconditional `nohup <installPath> daemon &`. The final `nohup` is the fallback for BOTH platforms (Linux without lingering, macOS without a reachable GUI domain).

**Dependency order (STRICT):** Phase 1 and Phase 2 are the foundations (parallel with each other). Phase 3 (D) needs Phase 1. Phase 4 (E) needs Phase 2. Phase 5 (F) needs Phase 3 and the quick-wins Workstream G.

---

## File Structure

- `internal/cli/roster_read.go` (+ test) — `RunRosterRead`: a host prints its OWN platform/uid/paths/gateway/peers as typed JSON (no secrets).
- `internal/sshprovision/ssh_command.go` (modify) — add `RegularSSHRosterReadCommand` + `PinnedSSHFleetSnapshotCommand`; allow `fleet-snapshot` in `directSSHGatewayCommand`.
- `internal/cli/mesh_heal_trust.go` (+ test) — Go host-key trust (keyscan → upsert regular known_hosts).
- `internal/cli/mesh_heal_roster.go` (+ test) — trust-then-`roster-read` over regular SSH + decentralized discovery.
- `internal/cli/mesh_heal_changedetect.go` (+ test) — `edgeIsHealthy`.
- `internal/cli/mesh_heal_hostprep.go` (+ test) — per-host resilient sync-pin host-prep.
- `internal/cli/mesh_heal_restart.go` (+ test) — Go daemon restart (ports the full agnostic shell).
- `internal/cli/mesh_heal.go` (+ test) — `RunMeshHeal` orchestration + JSON report.
- `internal/cli/fleet_snapshot.go` (+ test) — `fleet-snapshot` gateway handler (redacted).
- `internal/cli/ssh_gateway.go` (modify) — add the `fleet-snapshot` verb.
- `internal/daemon/fleet_aggregate.go` (+ test) — daemon gathers peers' `fleet-snapshot` over pinned SSH, aggregates, serves `GET /v1/fleet`.
- `cmd/clipfan/main.go` (modify) — register `roster-read`, `mesh-heal`.
- `apps/mac/Clipfan/Sources/Clipfan/MeshHealClient.swift` (+ test) — runs `mesh-heal`, decodes report.
- `apps/mac/Clipfan/Sources/Clipfan/Installer.swift`, `SettingsView.swift`/`StatusMenuView.swift` (modify) — invoke heal + "Repair mesh".
- `apps/mac/Clipfan/Sources/Clipfan/FleetMesh*.swift` (+ test) — read `GET /v1/fleet`, fleet-wide model + per-host edge rows.
- `apps/mac/Clipfan/Sources/Clipfan/WelcomeView.swift`/`WelcomeWindow.swift`/`Bootstrap.swift` (modify) — wizard.

---

## Phase 1 — Foundation 1: `clipfan mesh-heal` (Go)

> Build bottom-up. Each unit is testable with the fake `CommandRunner` from `internal/cli/ssh_provision_direct_test.go` (read it + `internal/sshprovision/*_test.go` first).

### Task 1.0: Read the reuse surface (no edits)
- [ ] Read in full: `internal/cli/ssh_provision_direct.go` (scan/parse/loop), `internal/sshprovision/{pair_executor,regular_ssh_driver,pair_plan,ssh_command}.go`, `internal/config/{ssh,config,ssh_peer_config}.go`, `internal/cli/ssh_provision_direct_test.go`, `apps/mac/Clipfan/Sources/Clipfan/Installer.swift:691-770` (trust) and `:1268-1286` (restart). Confirm field names/return types before writing.

### Task 1.1: `roster-read` self-report subcommand
**Why:** Peer entries carry a host's `ssh_host/port/user`+`install_path` (enough to *invoke* `clipfan` on it) but not its config/known_hosts/sync_key paths, gateway, platform, or uid; `config.ReadSSHPeer` is local-only + redacted. So the host self-reports.
**Files:** `internal/cli/roster_read.go`, `internal/cli/roster_read_test.go`.
- [ ] **Step 1 (failing test):** `buildRosterReadReport(cfg *config.Config, goos string, uid int, selfBinaryPath string) RosterReadReport` → `{origin, platform, uid, config_path, known_hosts_path, sync_key_path, install_path, gateway_path, peers:[{id, ssh_host, ssh_port, ssh_user, install_path, gateway_path, migration_state, enabled, accept, connect, accept_key_id, connect_key_id}]}`. Assert: JSON has peers+paths, **no** `shared_key`/private-key bytes; `gateway_path == install_path` (the convention); `enabled/accept/connect` carry their real boolean even when false (the report struct must NOT use `omitempty` on them — do not reuse `config.SSHPeer`'s tags).
- [ ] **Step 2:** `go test ./internal/cli/ -run RosterRead -v` → FAIL (`undefined`).
- [ ] **Step 3:** Implement `RosterReadReport` (new typed structs, no `omitempty` on the bools), `buildRosterReadReport` (copy allowlisted fields; paths from `config.Path()` + `cfg.SSH.{KnownHosts,SyncKey}`; `gateway_path = install_path = selfBinaryPath`), and `RunRosterRead(args, stdout, stderr)` doing `config.Load()`, `runtime.GOOS`, `os.Getuid()`, `os.Executable()`, JSON-encode. Nil-guard `cfg.SSH`.
- [ ] **Step 4:** test PASS; `go build ./...`. **Step 5:** commit (`feat: clipfan roster-read self-report`).

### Task 1.2: Per-edge change-detection
**Why:** The primitive rewrites config every run; check both ends' config state to skip already-correct edges (a probe is insufficient).
**Files:** `internal/cli/mesh_heal_changedetect.go` (+ test).
- [ ] **Step 1 (failing test):** `edgeIsHealthy(a, b edgePeerView)` with `edgePeerView{Found, Enabled, Accept, Connect bool; MigrationState, AcceptKeyID, ConnectKeyID string}`. Tests: both ready+enabled+accept+connect+`ssh_keys_ready`+non-empty key ids → healthy; one `Found:false` → unhealthy; one `ssh_material_staged` → unhealthy; ready but empty key id → unhealthy. (Note: this is a presence check, not a rotation check — stale-but-present key ids are out of scope per the spec's key-rotation risk.)
- [ ] **Step 2-3:** implement (presence + `MigrationStateSSHKeysReady` + non-empty `AcceptKeyID`/`ConnectKeyID` on both ends).
- [ ] **Step 4-5:** PASS; commit (`feat: mesh-heal change-detection`).

### Task 1.3: Host-key trust + `roster-read` invocation
**Why:** Regular SSH is `StrictHostKeyChecking=yes`; the Go side never trusts host keys into the regular `known_hosts`, and no builder runs `roster-read`. Both must be added or every SSH fails for un-pinned hosts.
**Files:** `internal/cli/mesh_heal_trust.go` (+ test); modify `internal/sshprovision/ssh_command.go`.
- [ ] **Step 1 (failing test):** `RegularSSHRosterReadCommand(spec RegularSSHRosterReadSpec) (SSHCommand, error)` produces the same `StrictHostKeyChecking=yes`/`UserKnownHostsFile` shape as `RegularSSHRunProbeCommand` with remote `<installPath> roster-read`. Separately, `trustHostKeyLines(existing []string, keyscanLines []string, host string, port int) ([]string, error)` upserts the keyscanned lines into a known_hosts line set, returning an error on a key *conflict* for an existing host (mirror the Swift `ssh_known_hosts_conflict` semantics).
- [ ] **Step 2-3:** implement the builder (mirror `RegularSSHRunProbeCommand`); implement `trustHostKeyLines` (pure, table-tested) and a `trustEndpoint(ctx, runner, endpoint, regularKnownHostsPath)` that runs `ssh-keyscan -T 5 -p <port> <host>` via the `CommandRunner` and writes the result through `trustHostKeyLines` to the regular known_hosts file (only when `--trust-keyscan`). Port the keyscan-target resolution from `Installer.swift`/`scanSSHProvisionDirectHostKeys`.
- [ ] **Step 4-5:** `go test ./internal/cli/ ./internal/sshprovision/ -run 'RosterReadCommand|TrustHostKey' -v` PASS; commit (`feat: regular-SSH roster-read builder + host-key trust`).

### Task 1.4: Roster discovery (trust-then-read, decentralized)
**Files:** `internal/cli/mesh_heal_roster.go` (+ test).
- [ ] **Step 1 (failing test):** with fakes for trust + read, `discoverRoster(seed []rosterEndpoint, trust trustFn, read rosterReader)` BFS-closes: for each unseen endpoint, `trust` then `read` (decode `RosterReadReport`), enqueue its `Connect`-able peers as endpoints (`{ID, SSHHost, SSHPort, SSHUser, InstallPath}`), **skip accept-only peers lacking an SSH locator**, and record trust/read failures in an `unreachable` slice rather than aborting. `rosterEndpoint` has everything needed to invoke `roster-read`.
- [ ] **Step 2-3:** implement BFS; production `read` = `RegularSSHRosterReadCommand` via the runner + JSON-decode; nil-guard; dedupe by host id.
- [ ] **Step 4-5:** PASS; commit (`feat: mesh-heal decentralized discovery`).

### Task 1.5: Resilient sync-pin host-prep
**Why:** `scanSSHProvisionDirectHostKeys` aborts on the first host and is the source of the *sync* pins (`ConfirmedHostKeyLines`) + the resolved-host/admin-host split the driver needs.
**Files:** `internal/cli/mesh_heal_hostprep.go` (+ test).
- [ ] **Step 1 (failing test):** `prepHosts(ctx, hosts, runner)` → `map[id]hostPrep` + `map[id]error`; one host's failure doesn't sink the others. `hostPrep` carries the confirmed sync host-key line, server mode, AND the resolved `Host.SSHHost/Port` separately from the original `AdminHost` endpoint.
- [ ] **Step 2-3:** factor the per-host body of `scanSSHProvisionDirectHostKeys` (keyscan-target resolution via `ssh -G`, keyscan, server-mode, admin/resolved split) into a per-host function in an error-capturing loop; leave the original untouched.
- [ ] **Step 4-5:** PASS; commit (`feat: mesh-heal resilient host-prep`).

### Task 1.6: Go daemon restart (full agnostic fallback)
**Files:** `internal/cli/mesh_heal_restart.go` (+ test).
- [ ] **Step 1 (failing test):** `restartShell(uid int, installPath string) string` returns the **single platform-agnostic** script ported verbatim from `Installer.swift:1268-1286`: try `systemctl --user … restart clipfan.service && exit 0`, then `launchctl … kickstart -k gui/<uid>/com.primeradiant.clipfan && exit 0`, then `nohup <installPath> daemon &`. Assert: unit is `clipfan.service`; the script ends with the unconditional `nohup` fallback (reachable on BOTH platforms); it does NOT branch on a `platform` argument.
- [ ] **Step 2-3:** implement `restartShell` (pure) + `restartDaemon(ctx, runner, adminHost, uid, installPath)` running `sh -c <script>` over regular SSH. `uid` comes from the host's `RosterReadReport`.
- [ ] **Step 4-5:** PASS; commit (`feat: mesh-heal Go daemon restart`).

### Task 1.7: The `mesh-heal` orchestration
**Files:** `internal/cli/mesh_heal.go` (+ test).
- [ ] **Step 1 (failing test):** fully faked, 3-host roster with one already-healthy edge and one unreachable host: `runMeshHeal(...)` **seeds the local host from `config.Load()`** (no SSH to self), trusts+discovers the rest, builds `DirectPairProvisionHost`s (paths + `GatewayPath` from each report; admin/resolved from prep), reads both ends' `edgePeerView` **from the cached discovery reports** (each host's report carries its view of all its edges, so A's report gives A→B and B's gives B→A — no re-SSH), skips the healthy edge (no `Provision`, neither host restarted), provisions the reachable unhealthy edge (per-pair error captured, continue), restarts only changed hosts via `restartDaemon` (uid from their reports), reports the unreachable host, returns no error on partial success. Assert report `{"healed","skipped","failed","restarted","unreachable"}`.
- [ ] **Step 2-3:** implement `runMeshHeal(args, stdout, stderr, opts)` (option-injection for fakes, mirroring `runSSHProvisionDirect`); flags `--regular-known-hosts`, `--trust-keyscan`, optional `--host`; default seed = local `config.Load()` peers + the local host itself. `RunMeshHeal(args, stdout, stderr) error` is the public entry.
- [ ] **Step 4-5:** PASS; `go build ./... && go vet ./...`; commit (`feat: clipfan mesh-heal orchestration`).

### Task 1.8: Register subcommands
- [ ] Add `case "roster-read":` → `cli.RunRosterRead(...)` and `case "mesh-heal":` → `cli.RunMeshHeal(...)` in `cmd/clipfan/main.go`. `go build ./... && go vet ./...`. Commit.

---

## Phase 2 — Foundation 2: `fleet-snapshot` + daemon aggregation (Go)

**Why:** The Mac must NOT need its own sync-key SSH client. So the Go daemon — which already runs pinned-sync-key SSH to peers — gathers each peer's redacted `fleet-snapshot` and serves an aggregated fleet view on loopback for the Mac to read (like it reads `/v1/peers`).

- [ ] **Task 2.1 — redacted snapshot payload (TDD):** `buildFleetSnapshot(cfg, localPeers) FleetSnapshot` → `{origin, version, peers:[{id, ssh_host, ssh_port, ssh_user, migration_state, ssh_status, ssh_active, last_recv_ts, ssh_last_ack_ts, ssh_last_connect_ts}]}`. Test: a config with `shared_key` set yields a marshalled snapshot with NO `shared_key`/proof/sync-key. Commit.
- [ ] **Task 2.2 — gateway verb + client builders (TDD):** Add `SSHGatewayFleetSnapshotCommand = "fleet-snapshot"`; a `case` in the gateway switch requiring `validateSSHGatewaySyncPeer`, fetching `/v1/peers` over loopback (add a signed GET to `transport.Client` mirroring `current`), writing `buildFleetSnapshot` JSON. Add `PinnedSSHFleetSnapshotCommand` (mirror `PinnedSSHProbeCommand`) and allow `fleet-snapshot` in `directSSHGatewayCommand`'s verb check. Test: unauthorized peer → rejected; happy path with a fake; the pinned builder + direct allowlist accept the verb. Commit.
- [ ] **Task 2.3 — daemon aggregation + local endpoint (TDD):** In `internal/daemon`, add a gatherer that, for each connected sync peer, runs `PinnedSSHFleetSnapshotCommand` via the daemon's existing process-starter, decodes it, and aggregates into a fleet view keyed by host; serve it at `GET /v1/fleet` (signed loopback, mirroring `peersHandler`). On-demand / throttled, not a tight poll. Test the aggregation (merge per-host views; mark a peer that didn't answer as unknown). Commit. Then `go build ./... && go test ./internal/cli/ ./internal/daemon/ -run 'FleetSnapshot|FleetAggregate' -v`.

---

## Phase 3 — Workstream D: Mac drivers (Phase 1)
**Files:** `MeshHealClient.swift` (new); `Installer.swift`, `SettingsView.swift`/`StatusMenuView.swift`.
- [ ] **3.1 (TDD):** `MeshHealReport: Codable` (`healed`/`skipped`/`failed`/`restarted`/`unreachable`); pure `decodeMeshHealReport(_:)` tested; `MeshHealClient.heal()` runs `clipfan mesh-heal …` via the existing `runCommand` pattern.
- [ ] **3.2:** In `Installer.provisionPrivateDirectMesh`, after binary install, replace the inline `ssh-provision-direct`+restart-all with one `MeshHealClient.heal()`; keep binary install; surface per-host failures. `swift build`.
- [ ] **3.3:** "Repair mesh" button (Settings → Fleet / menu) → `heal()`, show report. `swift build`.
- [ ] **3.4:** Update the Workstream-G success view to show heal status ("Added <host> · mesh healed (N edges)"). `swift build`. Commit each.

---

## Phase 4 — Workstream E: mesh-state visibility (Phase 2)
**Why:** The Mac reads the daemon's aggregated `GET /v1/fleet` (no SSH).
**Files:** `FleetMeshModel.swift` (+ test), `FleetMeshView.swift`; wire into `SettingsView`/`StatusMenuView`.
- [ ] **4.1 (TDD):** decode `/v1/fleet` into `[MeshHostRow]`; aggregate health = worst *observed* edge; unobserved edges render `.unknown` (not `.down`); header "M/N edges healthy" counts observed edges only; match the spec example (4 hosts → 6 undirected edges, 3 peers each).
- [ ] **4.2:** `DaemonClient` fetches `/v1/fleet` (signed, like `/v1/peers`) on Fleet-view open / refresh.
- [ ] **4.3:** `FleetMeshView` renders per-host rows + expandable edge detail; "unknown" styled distinctly. `swift build`. Commit each.

---

## Phase 5 — Workstream F: onboarding wizard (Phase 3 + quick-wins G)
**Files:** `WelcomeView.swift`, `WelcomeWindow.swift`, `Bootstrap.swift`; `BootstrapTests.swift`.
- [ ] **5.1 (TDD):** `OnboardingStep` (`welcome → localSetup → addHost → done`) + pure transitions (advance on `SetupState.success`; `addHost` skippable; `done` shows tips + fleet summary). Test mirroring existing `SetupState` tests.
- [ ] **5.2:** Rewrite `WelcomeView` to a stepped wizard (`●─○─○─○`); `addHost` reuses `AddPeerSheet` fields + `MeshHealClient.heal()`; re-runnable from the menu. `swift build`.
- [ ] **5.3:** `AppDelegate.firstRunInstall` opens the wizard; add a "Set up clipfan…" menu entry. `swift build`. Commit each.

---

## Self-Review (completed by plan author)

- **Spec coverage:** Foundation 1 → Phase 1; Foundation 2 → Phase 2; D/E/F → Phases 3/4/5. Round-2/3 + plan-review corrections all encoded.
- **/par round 2 (plan) fixes folded in:** (1) host-key trust into the regular known_hosts + a `RegularSSHRosterReadCommand` builder (Task 1.3), gated by `--trust-keyscan`, interleaved as trust-then-read in discovery (Task 1.4); (2) local/self host seeded from `config.Load()`, never via SSH-to-self (Task 1.7); (3) `GatewayPath` sourced from the `roster-read` report (`= install_path`, Task 1.1) and used in Task 1.7; (4) `fleet-snapshot` reachable without a Mac SSH client — the Go daemon gathers it over its existing pinned-SSH machinery (new `PinnedSSHFleetSnapshotCommand` + Tailscale-direct allowlist) and serves aggregated `/v1/fleet` locally (Task 2.3); the Mac just reads it (Phase 4); (5) the restart is the single platform-agnostic shell with the universal `nohup` fallback (Task 1.6). Minors: accept-only peers without an SSH locator are skipped in discovery (Task 1.4); the `omitempty`-false-drop trap is called out in Task 1.1; `edgeIsHealthy` is documented as presence-not-rotation (Task 1.2).
- **Type consistency:** `RosterReadReport`/`buildRosterReadReport`/`RunRosterRead`, `edgePeerView`/`edgeIsHealthy`, `RegularSSHRosterReadCommand`, `trustHostKeyLines`/`trustEndpoint`, `rosterEndpoint`/`discoverRoster`/`rosterReader`/`trustFn`, `prepHosts`/`hostPrep`, `restartShell`/`restartDaemon`, `runMeshHeal`/`RunMeshHeal`, `buildFleetSnapshot`/`FleetSnapshot`/`PinnedSSHFleetSnapshotCommand`, `MeshHealReport`/`decodeMeshHealReport`/`MeshHealClient`, `MeshHostRow`/`OnboardingStep` — consistent across tasks and tests.
