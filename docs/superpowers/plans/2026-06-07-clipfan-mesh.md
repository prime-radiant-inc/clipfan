# clipfan Mesh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. **This plan is SEQUENTIAL** — the foundations gate everything else; do not blind-fan-out.

**Goal:** Make any provisioning operation heal the whole fleet into a full mesh, drive it from a Go CLI command runnable on any host, and show the resulting mesh state in the macOS app.

**Architecture:** A new Go CLI command `clipfan mesh-heal` reuses the existing per-pair provisioner (`sshprovision.DirectPairProvisioner.Provision`) but adds the three things the primitive lacks: decentralized roster discovery, per-host-resilient host-prep, and per-edge change-detection (so it's idempotent), plus a Go daemon-restart so applied config takes effect. Both discovery and change-detection are powered by **one new read primitive** — a `clipfan roster-read` subcommand that a host runs on itself (reporting its own paths, platform, uid, and peers) and that the orchestrator invokes over regular SSH. A separate read-only `fleet-snapshot` gateway verb gives the Mac a redacted roster + edge-health for the Fleet view. The Mac app (and a future Linux UI) become thin invokers of `mesh-heal`.

**Tech Stack:** Go (daemon/CLI under `internal/`, `cmd/clipfan`), SwiftUI/AppKit (`apps/mac/Clipfan`). Go tests: `go test ./...`. Swift tests: `cd apps/mac/Clipfan && swift test --filter <Name>`.

**Source spec:** `docs/superpowers/specs/2026-06-07-clipfan-mesh-onboarding-docs-design.md` (Foundations 1 & 2, Workstreams D, E, F).

**Verified existing APIs this plan builds on:**
- `sshprovision.DirectPairProvisioner.Provision(ctx, DirectPairProvisionInput{Local, Remote DirectPairProvisionHost, SharedKey string}) (DirectPairProvisionResult, error)` — provisions one pair, both directions, unconditionally (no skip, always bumps config revision). `internal/sshprovision/pair_executor.go:75`.
- `sshprovision.DirectPairProvisionHost{Host DirectPairHost, AdminHost DirectPairHost, KnownHostsPath, SyncKeyPath string}`; `DirectPairHost{ID, SSHHost, SSHUser, SSHPort, InstallPath, GatewayPath, SSHServerMode}` (read `internal/sshprovision/pair_plan.go` for the exact `DirectPairHost` fields before constructing it). `Provision` requires `KnownHostsPath`, `SyncKeyPath`, and a config path **per host**.
- `sshprovision.RegularSSHProvisionDriver{Runner, RegularKnownHostsPath, ConfirmedHostKeyLines map[string]string, ProvisionHosts map[string]DirectPairProvisionHost, ConfigPathByHostID map[string]string}` implements the driver; `ConfirmHostKey` reads pre-scanned pins keyed by `host.ID`; every other method SSHes to `adminHostFor(host)`; `WriteConfig` is a two-phase **direct file write** with no daemon reload. `internal/sshprovision/regular_ssh_driver.go`.
- The existing all-or-nothing orchestration to generalize from: `runSSHProvisionDirect` (`internal/cli/ssh_provision_direct.go:44`); its `scanSSHProvisionDirectHostKeys` (lines 83, 267-304) keyscans ALL hosts up front (resolving the keyscan target with `ssh -G`, rewriting `Host.SSHHost`/`Port` to the resolved value while keeping `AdminHost` = original endpoint, and detecting server mode) and aborts on any failure; its pair loop (97-109) aborts on first failure. `parseSSHProvisionDirectHost` requires `id,ssh,user,port,install,config,known_hosts,sync_key` host-spec fields.
- `config.Load() (*Config, error)`, `config.Path() string`. **`config.ReadSSHPeer(path, peerID)` is LOCAL-file only and returns `SSHPeerConfigReadResult{Peer map[string]any, …}` with secrets redacted — it is NOT a remote read and NOT a typed struct.** Do not use it for the over-SSH reads; this plan adds a typed `roster-read` primitive (Task 1.1) instead. `config.Config.SSH` is a pointer (`*SSHConfig`) — nil-guard it. `config.SSHPeer{ID, SSHHost, SSHUser, SSHPort, InstallPath, GatewayPath, MigrationState, Proof SSHProof, Enabled, Accept, Connect, Persistent}` with `Enabled/Accept/Connect` marked `omitempty`; `SSHProof{AcceptKeyID, ConnectKeyID, …}`; `config.MigrationStateSSHKeysReady == "ssh_keys_ready"`. `internal/config/ssh.go`, `config.go`, `ssh_peer_config.go`.
- Subcommand dispatch: add `case`s in `cmd/clipfan/main.go` (mirror `case "ssh-run-probe":` → `cli.RunSSHRunProbe(args[1:], stdout, stderr)`).
- The production remote-restart shell to **port in full** (not a single command): `apps/mac/Clipfan/Sources/Clipfan/Installer.swift:1268-1286` — it tries `systemctl --user daemon-reload + enable + restart clipfan.service`, then `launchctl enable + bootstrap/load + kickstart gui/<uid>/com.primeradiant.clipfan`, then `nohup <installPath> daemon` as a fallback. The `nohup` fallback is what covers a remote macOS where the GUI launchd domain isn't reachable over non-interactive SSH. The remote uid comes from `id -u` (gathered by the roster-read, Task 1.1).
- Gateway verbs live in `internal/cli/ssh_gateway.go`'s `command` switch (`runSSHGatewayWithHandlers`, line 71); peer auth is `validateSSHGatewaySyncPeer` (line 326). `GET /v1/peers` IS served to a signed loopback client (`internal/transport/server.go:205`, gated by the local-auth-version check) — `fleet-snapshot` can fetch it the way the gateway fetches `current`. `internal/cli` already imports `internal/daemon` (`internal/cli/fleet_reset.go`), so referencing `daemon.PeerState` is safe (no import cycle). The Swift `Peer`/`LocalDaemonSSHPeer` models already redact secrets (`Models.swift:485`).

**Dependency order (STRICT):** Phase 1 (mesh-heal) and Phase 2 (fleet-snapshot) are the foundations and have no Swift dependency — they can proceed in parallel with each other. Phase 3 (D) needs Phase 1. Phase 4 (E) needs Phase 2. Phase 5 (F) needs Phase 3 and reuses the add-peer success state from the quick-wins plan (Workstream G).

---

## File Structure

- `internal/cli/roster_read.go` (new) — `RunRosterRead` subcommand: a host prints its OWN platform, uid, paths, and peers as typed JSON (no secrets).
- `internal/cli/mesh_heal_roster.go` (new) — over-SSH invocation of `roster-read` + decentralized discovery (BFS to closure).
- `internal/cli/mesh_heal_changedetect.go` (new) — `edgeIsHealthy(...)` from both ends' roster peer views.
- `internal/cli/mesh_heal_hostprep.go` (new) — per-host resilient host-prep (factored from the scan, error-capturing).
- `internal/cli/mesh_heal_restart.go` (new) — Go daemon restart over regular SSH (ports the full Installer fallback shell).
- `internal/cli/mesh_heal.go` (new) — `RunMeshHeal` orchestration + JSON report.
- `internal/cli/mesh_heal_*_test.go`, `internal/cli/roster_read_test.go` (new) — tests (reuse the fake `CommandRunner` from `ssh_provision_direct_test.go`).
- `internal/cli/fleet_snapshot.go` (new) — the `fleet-snapshot` gateway handler (redacted roster + edge-health).
- `internal/cli/ssh_gateway.go` (modify) — add the `fleet-snapshot` verb to the switch.
- `cmd/clipfan/main.go` (modify) — register `roster-read` and `mesh-heal`.
- `apps/mac/Clipfan/Sources/Clipfan/MeshHealClient.swift` (new) — runs `mesh-heal`, decodes the report.
- `apps/mac/Clipfan/Sources/Clipfan/Installer.swift`, `SettingsView.swift`/`StatusMenuView.swift` (modify) — invoke heal + "Repair mesh".
- `apps/mac/Clipfan/Sources/Clipfan/FleetMesh*.swift` (new) — fleet-wide mesh model + per-host edge rows.
- `apps/mac/Clipfan/Sources/Clipfan/WelcomeView.swift`/`WelcomeWindow.swift`/`Bootstrap.swift` (modify) — the wizard.

---

## Phase 1 — Foundation 1: `clipfan mesh-heal` (Go)

> Build bottom-up. Each unit is independently testable with a fake `CommandRunner` (read `internal/cli/ssh_provision_direct_test.go` and `internal/sshprovision/*_test.go` first to reuse the fakes).

### Task 1.0: Read the reuse surface (no edits)

- [ ] **Step 1:** Read in full so you build against real signatures: `internal/cli/ssh_provision_direct.go` (esp. `scanSSHProvisionDirectHostKeys`, `parseSSHProvisionDirectHost`, `provisionHostsByID`); `internal/sshprovision/pair_executor.go`, `regular_ssh_driver.go`, `pair_plan.go` (for `DirectPairHost`); `internal/config/ssh.go` (`SSHPeer`/`SSHProof`), `config.go` (`Config`/`SSHConfig`); `internal/cli/ssh_provision_direct_test.go` (fake runner); `apps/mac/Clipfan/Sources/Clipfan/Installer.swift:1268-1286` (restart shell to port). Confirm field names/return types before writing.

### Task 1.1: `roster-read` primitive (the foundation for discovery + change-detection)

**Why:** Peer entries carry a host's `ssh_host/port/user` + `install_path` but NOT its `config`/`known_hosts`/`sync_key` paths, platform, or uid. So the orchestrator can *invoke* `clipfan` on a peer (it has the endpoint + install path) but must ask the host to report the rest. `config.ReadSSHPeer` is local-only and returns a redacted map, so this typed self-report replaces it.

**Files:** Create `internal/cli/roster_read.go`, `internal/cli/roster_read_test.go`.

- [ ] **Step 1: Write the failing test** — `buildRosterReadReport(cfg *config.Config, goos string, uid int, selfInstallPath string) RosterReadReport` returns `{origin, platform, uid, config_path, known_hosts_path, sync_key_path, install_path, peers:[{id, ssh_host, ssh_port, ssh_user, install_path, gateway_path, migration_state, enabled, accept, connect, accept_key_id, connect_key_id}]}`. Assert: the marshalled JSON contains the peers and paths but **no** `shared_key` / private-key material; `enabled/accept/connect` default to their real boolean (not dropped) when reading from a `Config` whose peer has them false.

- [ ] **Step 2: Run to verify it fails** (`undefined: buildRosterReadReport`). `go test ./internal/cli/ -run RosterRead -v`.

- [ ] **Step 3: Implement** `RosterReadReport` (typed structs), `buildRosterReadReport(...)` (copy allowlisted fields from `cfg.SSH.Peers`, paths from `cfg.SSH.{KnownHosts,SyncKey}` + `config.Path()`), and `RunRosterRead(args, stdout, stderr) error` that does `config.Load()`, reads `runtime.GOOS`, `os.Getuid()`, the running binary path, builds the report, and JSON-encodes it to stdout. No secrets in the output.

- [ ] **Step 4: Run to verify pass.** `go build ./...`.

- [ ] **Step 5: Commit** (`feat: clipfan roster-read self-report subcommand`).

### Task 1.2: Per-edge change-detection predicate

**Why:** The reused primitive rewrites config every run; to be idempotent and to scope restarts, decide per edge whether both ends are already correctly configured. A probe is insufficient (authorized_keys can exist while config peers are missing/staged) — check config state.

**Files:** Create `internal/cli/mesh_heal_changedetect.go`, `internal/cli/mesh_heal_changedetect_test.go`.

- [ ] **Step 1: Write the failing test** — `edgeIsHealthy(a, b edgePeerView)` where `edgePeerView{Found, Enabled, Accept, Connect bool; MigrationState, AcceptKeyID, ConnectKeyID string}` is populated from a `RosterReadReport` peer (Task 1.1). Tests: both ends ready+enabled+accept+connect+`ssh_keys_ready` with non-empty matching key ids → healthy; one end `Found:false` → unhealthy; one end `ssh_material_staged` → unhealthy; ready but empty `AcceptKeyID`/`ConnectKeyID` → unhealthy.

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** `edgeIsHealthy`:

```go
package cli

import "github.com/prime-radiant-inc/clipfan/internal/config"

type edgePeerView struct {
	Found          bool
	Enabled        bool
	Accept         bool
	Connect        bool
	MigrationState string
	AcceptKeyID    string
	ConnectKeyID   string
}

func edgeIsHealthy(a, b edgePeerView) bool {
	ready := func(v edgePeerView) bool {
		return v.Found && v.Enabled && v.Accept && v.Connect &&
			v.MigrationState == string(config.MigrationStateSSHKeysReady) &&
			v.AcceptKeyID != "" && v.ConnectKeyID != ""
	}
	return ready(a) && ready(b)
}
```

- [ ] **Step 4: Run to verify pass.**

- [ ] **Step 5: Commit** (`feat: mesh-heal per-edge change-detection`).

### Task 1.3: Roster discovery (decentralized, via `roster-read`)

**Why:** Build the full host set + each host's real paths/platform/uid by asking each reachable host to self-report.

**Files:** Create `internal/cli/mesh_heal_roster.go`, `internal/cli/mesh_heal_roster_test.go`.

- [ ] **Step 1: Write the failing test** — with a fake `rosterReader func(ctx, endpoint) (RosterReadReport, error)`, `discoverRoster(seed []rosterEndpoint, read rosterReader)` BFS-closes over peers: given A's report (peers B, C) and B's/C's reports, it returns all of {A,B,C} as `discoveredHost{report, endpoint}` and records an unreachable host (reader error) separately rather than aborting. `rosterEndpoint{ID, SSHHost, SSHPort, SSHUser, InstallPath}` (everything needed to *invoke* `roster-read`, all present in a peer entry).

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** `discoverRoster(...)`: BFS from `seed`; for each unseen endpoint call `read` (production impl: `ssh <user>@<host> -p <port> <installPath> roster-read` via the `CommandRunner`, JSON-decode); enqueue its peers as endpoints (id + ssh fields + install_path from each peer entry); record reader errors in an `unreachable` slice; nil-guard `cfg.SSH`. Return `([]discoveredHost, []unreachableHost)`.

- [ ] **Step 4: Run to verify pass.**

- [ ] **Step 5: Commit** (`feat: mesh-heal decentralized roster discovery`).

### Task 1.4: Resilient host-prep (per host, error-capturing)

**Why:** The existing `scanSSHProvisionDirectHostKeys` aborts on the first host and is the source of host-key pins + the resolved-host/admin-host split. Heal must prep each host independently.

**Files:** Create `internal/cli/mesh_heal_hostprep.go`, `internal/cli/mesh_heal_hostprep_test.go`.

- [ ] **Step 1: Write the failing test** — `prepHosts(ctx, hosts, runner)` returns `map[hostID]hostPrep` and `map[hostID]error`; a runner that errors for one host still preps the others. `hostPrep` MUST carry the confirmed host-key line, the server mode, AND the resolved `Host.SSHHost/Port` separately from the original `AdminHost` endpoint (the driver SSHes to `adminHostFor(host)` but keys host-pins by `host.ID`).

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** by factoring the per-host body of `scanSSHProvisionDirectHostKeys` (keyscan-target resolution via `ssh -G`, the keyscan, server-mode detection, admin-vs-resolved split) into a per-host function wrapped in an error-capturing loop. Leave `scanSSHProvisionDirectHostKeys` itself untouched (extract a shared helper if convenient).

- [ ] **Step 4: Run to verify pass.**

- [ ] **Step 5: Commit** (`feat: mesh-heal per-host resilient host-prep`).

### Task 1.5: Go daemon-restart (full fallback chain)

**Why:** Direct config writes don't hot-reload; a changed host needs a restart, there is no Go restart today, and a single `launchctl kickstart` fails on a non-bootstrapped agent / unreachable GUI domain.

**Files:** Create `internal/cli/mesh_heal_restart.go`, `internal/cli/mesh_heal_restart_test.go`.

- [ ] **Step 1: Write the failing test** — `restartShell(platform string, uid int, installPath string) string` returns a shell script: for `linux`, `systemctl --user daemon-reload; systemctl --user enable clipfan.service; systemctl --user restart clipfan.service`; for `darwin`, the launchctl enable+bootstrap/load+`kickstart -k gui/<uid>/com.primeradiant.clipfan` sequence **followed by** a `nohup <installPath> daemon` fallback. Assert the unit name is `clipfan.service` (not bare `clipfan`) and the macOS branch includes the `nohup` fallback. Port the exact sequence from `Installer.swift:1268-1286`.

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** `restartShell(...)` (pure) and `restartDaemon(ctx, runner, adminHost, platform, uid, installPath)` that runs `sh -c <restartShell>` over regular SSH against the admin host. `platform` and `uid` come from the host's `RosterReadReport` (Task 1.1).

- [ ] **Step 4: Run to verify pass.**

- [ ] **Step 5: Commit** (`feat: mesh-heal Go daemon restart with fallback chain`).

### Task 1.6: The `mesh-heal` orchestration

**Why:** Tie it together, resilient + idempotent.

**Files:** Create `internal/cli/mesh_heal.go`, `internal/cli/mesh_heal_test.go`.

- [ ] **Step 1: Write the failing test** — fully faked runner/reader/driver, 3-host roster where one edge is already healthy and one host unreachable: `runMeshHeal(...)` provisions the reachable unhealthy edge, SKIPS the healthy edge (no `Provision` call; neither of its hosts marked changed/restarted), reports the unreachable host, and returns NO error for partial success. Assert the JSON report `{"healed":[…],"skipped":[…],"failed":[…],"restarted":[…]}`.

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** `runMeshHeal(args, stdout, stderr, opts)` (mirror `runSSHProvisionDirect`'s option-injection for fakes): parse flags (`--regular-known-hosts`, `--trust-keyscan`, optional `--host` seeds); discover roster (1.3) — each `discoveredHost.report` carries platform/uid/paths; prep hosts (1.4); build `DirectPairProvisionHost`s from the reports (paths from the report, admin/resolved from prep); for each unordered pair: read both ends' edge views from the already-fetched reports (cache from discovery — do not re-SSH) → `edgeIsHealthy` → skip, else `DirectPairProvisioner.Provision` (capture per-pair error into the report, continue), recording both hosts as changed; restart each changed host via `restartDaemon` using its report's platform/uid/installPath; encode the report. Add `RunMeshHeal(args, stdout, stderr) error`.

- [ ] **Step 4: Run to verify pass.** Then `go build ./... && go vet ./...`.

- [ ] **Step 5: Commit** (`feat: clipfan mesh-heal orchestration`).

### Task 1.7: Register the subcommands

**Files:** Modify `cmd/clipfan/main.go`.

- [ ] **Step 1:** Add `case "roster-read":` → `cli.RunRosterRead(...)` and `case "mesh-heal":` → `cli.RunMeshHeal(...)`, mirroring the existing `case` blocks (each prints the error to stderr and returns 1, else 0).
- [ ] **Step 2:** `go build ./... && go vet ./...` (clean). Commit (`feat: register roster-read and mesh-heal subcommands`).

---

## Phase 2 — Foundation 2: `fleet-snapshot` gateway verb (Go)

**Why:** The Mac needs a peer's **redacted** roster + edge-health for the Fleet view. This is distinct from `roster-read` (Task 1.1): `roster-read` runs over the user's *regular* SSH and carries paths for provisioning; `fleet-snapshot` runs over the *sync-key gateway* (adjacent hosts only) and is redacted for display.

**Files:** Create `internal/cli/fleet_snapshot.go`, `internal/cli/fleet_snapshot_test.go`; modify `internal/cli/ssh_gateway.go`; add the command constant next to `SSHGatewayProbeCommand` (grep it for the file).

- [ ] **Task 2.1 — redacted snapshot payload (TDD):** `buildFleetSnapshot(cfg *config.Config, peers any) FleetSnapshot` returning `{origin, version, peers:[{id, ssh_host, ssh_port, ssh_user, migration_state, ssh_status, ssh_active, last_recv_ts, ssh_last_ack_ts, ssh_last_connect_ts}]}`. Test that a marshalled snapshot of a config with `shared_key` set contains NO `shared_key`, no `proof` key material, no sync-key paths. Implement by copying only the allowlist. Commit.

- [ ] **Task 2.2 — gateway verb (TDD):** Add `SSHGatewayFleetSnapshotCommand = "fleet-snapshot"`. In the `switch command`, add a `case` that requires `validateSSHGatewaySyncPeer(cfg, identity)`, fetches the local daemon's `/v1/peers` over loopback (add a signed GET to `transport.Client` mirroring `current` — confirm against `transport/client.go`), and writes `buildFleetSnapshot(...)` as JSON. Test the unauthorized-peer rejection and the happy path with a fake. Commit. Then `go build ./... && go test ./internal/cli/ -run FleetSnapshot -v`.

---

## Phase 3 — Workstream D: Mac drivers (replace Swift orchestration)

**Depends on Phase 1.**

**Files:** Create `apps/mac/Clipfan/Sources/Clipfan/MeshHealClient.swift`; modify `Installer.swift`, `SettingsView.swift`/`StatusMenuView.swift`.

- [ ] **Task 3.1 — MeshHealClient (TDD):** `MeshHealReport: Codable` matching the Go JSON (`healed`/`skipped`/`failed`/`restarted`); pure `decodeMeshHealReport(_ data: Data) -> MeshHealReport?` (test decoding a sample). `MeshHealClient.heal()` runs `clipfan mesh-heal …` via the existing `runCommand`/Process pattern and returns the report.
- [ ] **Task 3.2 — invoke from add-peer install:** In `Installer.provisionPrivateDirectMesh`, after the binary-install step, replace the inline `ssh-provision-direct` + restart-all logic with one `MeshHealClient.heal()` call. Keep binary install (heal does not install). Surface per-host failures. `swift build`.
- [ ] **Task 3.3 — Repair mesh action:** "Repair mesh" button (Settings → Fleet and/or menu) → `MeshHealClient.heal()`, shows the report summary. `swift build`.
- [ ] **Task 3.4 — augment add-peer success copy:** Now that add-peer heals, update the Workstream-G success view (quick-wins plan) to show heal status ("Added <host> · mesh healed (N edges)"). `swift build`. Commit each.

---

## Phase 4 — Workstream E: mesh-state visibility (macOS)

**Depends on Phase 2.**

**Files:** Create `apps/mac/Clipfan/Sources/Clipfan/FleetMeshModel.swift` (+ test) and `FleetMeshView.swift`; wire into `SettingsView`/`StatusMenuView`.

- [ ] **Task 4.1 — fleet-wide model (TDD):** Pure functions: from a set of per-host snapshots produce `[MeshHostRow]` where aggregate health = worst *observed* edge, counts correct, unobserved edges render `.unknown` (not `.down`); header "M/N edges healthy" counts observed edges only. Match the spec's corrected example (4 hosts → 6 undirected edges, 3 peers each).
- [ ] **Task 4.2 — snapshot gathering:** Client method that, for each gateway-adjacent host, runs `fleet-snapshot` over SSH and decodes it (reuse `MeshHealClient`'s Process pattern; redaction is server-side). On-demand / Fleet-view-open only.
- [ ] **Task 4.3 — render per-host rows + edge detail:** `FleetMeshView` renders Task 4.1 rows, expandable to edges, "unknown" styled distinctly. `swift build`. Commit each.

---

## Phase 5 — Workstream F: onboarding wizard

**Depends on Phase 3 and the quick-wins Workstream G.**

**Files:** Modify `WelcomeView.swift`, `WelcomeWindow.swift`, `Bootstrap.swift`; tests in `BootstrapTests.swift`.

- [ ] **Task 5.1 — step state machine (TDD):** `OnboardingStep` enum (`welcome → localSetup → addHost → done`) + pure transitions: advance from `localSetup` on `SetupState.success`; `addHost` skippable; `done` shows tips + fleet summary. Test transitions mirroring the existing `SetupState` tests.
- [ ] **Task 5.2 — wizard view:** Rewrite `WelcomeView` to render the current step with a `●─○─○─○` indicator; `addHost` reuses `AddPeerSheet`'s host fields + runs `MeshHealClient.heal()`; re-runnable from the menu. `swift build`.
- [ ] **Task 5.3 — wire menu re-entry + first-run:** `AppDelegate.firstRunInstall` opens the wizard; add a "Set up clipfan…" menu entry. `swift build`. Commit each.

---

## Self-Review (completed by plan author)

- **Spec coverage:** Foundation 1 → Phase 1 (Tasks 1.1–1.7); Foundation 2 → Phase 2; D → Phase 3; E → Phase 4; F → Phase 5. Round-2/3 corrections encoded: config-state-both-ends change-detection fed by typed `roster-read` (Tasks 1.1–1.2, not a probe and not the redacted `ReadSSHPeer` map); per-host resilient host-prep with the admin/resolved split (1.4); Go restart of only changed hosts with the full fallback chain + `clipfan.service` + uid (1.5); redacted `fleet-snapshot` (2.1); G not claiming "healed" until Phase 3 (3.4).
- **Adversarial-review fixes folded in:** the missing path source and the nonexistent over-SSH read are both resolved by the `roster-read` self-report primitive (Task 1.1) — a peer entry's `ssh_host/port/user`+`install_path` is enough to *invoke* it, and the host reports its own config/known_hosts/sync_key paths + platform + uid, so no platform-guessing and no chicken-and-egg. Restart now ports the full fallback shell with uid; the `*SSHConfig` pointer is nil-guarded; discovery caches reports so Task 1.6 doesn't re-SSH.
- **Placeholders:** Phase 1–2 Go tasks have concrete signatures, reused real APIs, and TDD tests. Phase 3–5 Swift tasks are interfaces + tests + file targets (their call sites depend on the Foundation APIs built first). Task 1.0 mandates reading the reuse surface before writing.
- **Type consistency:** `RosterReadReport`/`buildRosterReadReport`/`RunRosterRead`, `edgePeerView`/`edgeIsHealthy`, `rosterEndpoint`/`discoveredHost`/`discoverRoster`/`rosterReader`, `prepHosts`/`hostPrep`, `restartShell`/`restartDaemon`, `runMeshHeal`/`RunMeshHeal`, `buildFleetSnapshot`/`FleetSnapshot`, `MeshHealReport`/`decodeMeshHealReport`/`MeshHealClient`, `OnboardingStep` — consistent across each task and its test.
