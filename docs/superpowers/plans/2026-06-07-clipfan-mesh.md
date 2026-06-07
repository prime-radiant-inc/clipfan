# clipfan Mesh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. **This plan is SEQUENTIAL** — the foundations gate everything else; do not blind-fan-out.

**Goal:** Make any provisioning operation heal the whole fleet into a full mesh, drive it from a Go CLI command runnable on any host, and show the resulting mesh state in the macOS app.

**Architecture:** A new Go CLI command `clipfan mesh-heal` reuses the existing per-pair provisioner (`sshprovision.DirectPairProvisioner.Provision`) but adds the three things the primitive lacks: decentralized roster discovery, per-host-resilient host-prep, and per-edge change-detection (so it's idempotent), plus a Go daemon-restart so applied config takes effect. A new read-only `fleet-snapshot` gateway verb lets the Mac read a peer's redacted roster + edge-health for the Fleet view. The Mac app (and a future Linux UI) become thin invokers of `mesh-heal`.

**Tech Stack:** Go (daemon/CLI under `internal/`, `cmd/clipfan`), SwiftUI/AppKit (`apps/mac/Clipfan`). Go tests: `go test ./...`. Swift tests: `cd apps/mac/Clipfan && swift test --filter <Name>`.

**Source spec:** `docs/superpowers/specs/2026-06-07-clipfan-mesh-onboarding-docs-design.md` (Foundations 1 & 2, Workstreams D, E, F).

**Verified existing APIs this plan builds on:**
- `sshprovision.DirectPairProvisioner.Provision(ctx, DirectPairProvisionInput{Local, Remote DirectPairProvisionHost, SharedKey string}) (DirectPairProvisionResult, error)` — provisions one pair, both directions, unconditionally (no skip, always bumps config revision). `internal/sshprovision/pair_executor.go:75`.
- `sshprovision.RegularSSHProvisionDriver{Runner, RegularKnownHostsPath, ConfirmedHostKeyLines map[string]string, ProvisionHosts map[string]DirectPairProvisionHost, ConfigPathByHostID map[string]string}` implements the driver; `ConfirmHostKey` reads pre-scanned pins; `WriteConfig` is a two-phase **direct file write** with no daemon reload. `internal/sshprovision/regular_ssh_driver.go`.
- The existing all-or-nothing orchestration to refactor from: `runSSHProvisionDirect` (`internal/cli/ssh_provision_direct.go:44`) — its `scanSSHProvisionDirectHostKeys` (lines 83, 267-304) keyscans ALL hosts up front and aborts on any failure; its pair loop (97-109) aborts on first failure.
- `config.Load() (*Config, error)`, `config.Path() string`, `config.ReadSSHPeer(path string, peerID string) (SSHPeerConfigReadResult, error)`. `config.SSHPeer{ID, SSHHost, SSHUser, SSHPort, InstallPath, GatewayPath, MigrationState, Proof SSHProof, Enabled, Accept, Connect, Persistent}`; `SSHProof{AcceptKeyID, ConnectKeyID, …}`; `config.MigrationStateSSHKeysReady == "ssh_keys_ready"`. `internal/config/ssh.go`, `internal/config/config.go`, `internal/config/ssh_peer_config.go`.
- Subcommand dispatch: add a `case "mesh-heal":` in `cmd/clipfan/main.go` (mirrors `case "ssh-run-probe":` → `cli.RunSSHRunProbe(args[1:], stdout, stderr)`).
- Restart commands (to port to Go): macOS `launchctl kickstart -k gui/<uid>/com.primeradiant.clipfan` (`DaemonClient.swift:95`); Linux `systemctl --user restart clipfan` (`Installer.swift:291` branch).
- Gateway verbs live in `internal/cli/ssh_gateway.go`'s `command` switch (`runSSHGatewayWithHandlers`, line 71); peer auth is `validateSSHGatewaySyncPeer` (line 326). The Swift `Peer`/`LocalDaemonSSHPeer` models already redact secrets via `redactingSecretLikeFields` (`Models.swift:485`).

**Dependency order (STRICT):** Phase 1 (mesh-heal) and Phase 2 (fleet-snapshot) are the foundations and have no Swift dependency — they can proceed in parallel with each other. Phase 3 (D) needs Phase 1. Phase 4 (E) needs Phase 2. Phase 5 (F) needs Phase 3 and reuses the add-peer success state from the quick-wins plan (Workstream G).

---

## File Structure

- `internal/cli/mesh_heal.go` (new) — `RunMeshHeal` entry + orchestration (discover roster → host-prep → per-edge change-detect → provision reachable pairs → restart changed hosts → JSON report).
- `internal/cli/mesh_heal_roster.go` (new) — roster discovery (read each reachable host's config over regular SSH; union; build host specs).
- `internal/cli/mesh_heal_changedetect.go` (new) — `edgeIsHealthy(...)` predicate from both ends' config peer state.
- `internal/cli/mesh_heal_restart.go` (new) — per-platform Go daemon restart over regular SSH.
- `internal/cli/mesh_heal_*_test.go` (new) — tests per unit (use the existing fake `CommandRunner` pattern from `ssh_provision_direct_test.go`).
- `internal/cli/fleet_snapshot.go` (new) — the `fleet-snapshot` gateway handler (redacted roster + edge-health).
- `internal/cli/ssh_gateway.go` (modify) — add `case sshprovision.SSHGatewayFleetSnapshotCommand:` to the verb switch + handler wiring.
- `cmd/clipfan/main.go` (modify) — register `mesh-heal`.
- `apps/mac/Clipfan/Sources/Clipfan/Installer.swift` (modify) — replace the Swift orchestration in `provisionPrivateDirectMesh` with a `clipfan mesh-heal` invocation.
- `apps/mac/Clipfan/Sources/Clipfan/MeshHealClient.swift` (new) — thin wrapper that runs `mesh-heal` and decodes its JSON report.
- `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift` / `StatusMenuView.swift` (modify) — a "Repair mesh" action.
- `apps/mac/Clipfan/Sources/Clipfan/FleetMesh*.swift` (new) — fleet-wide mesh model + per-host edge rows for the Fleet view.
- `apps/mac/Clipfan/Sources/Clipfan/WelcomeView.swift` / `WelcomeWindow.swift` / `Bootstrap.swift` (modify) — the multi-step wizard.

---

## Phase 1 — Foundation 1: `clipfan mesh-heal` (Go)

> The hard, dependency-heavy core. Build it bottom-up: change-detection predicate → roster discovery → restart → orchestration → subcommand. Each unit is independently testable with a fake `CommandRunner` (see `internal/cli/ssh_provision_direct_test.go` and `internal/sshprovision/*_test.go` for the established fakes — read those before starting to reuse the patterns).

### Task 1.0: Read the references first

- [ ] **Step 1: Read the reuse surface end-to-end** (no edits)

Read in full so you build against real signatures, not guesses:
- `internal/cli/ssh_provision_direct.go` (the orchestration you're generalizing, incl. `scanSSHProvisionDirectHostKeys`, `parseSSHProvisionDirectHost`, `provisionHostsByID`).
- `internal/sshprovision/pair_executor.go` and `regular_ssh_driver.go` (the provisioner + driver).
- `internal/sshprovision/ssh_run_probe.go` and `internal/cli/ssh_run_probe.go` (what a probe proves — connectivity + identity, NOT config).
- `internal/config/ssh_peer_config.go` `ReadSSHPeer` and `internal/config/ssh.go` `SSHPeer`/`SSHProof`.
- `internal/cli/ssh_provision_direct_test.go` (the fake `CommandRunner` + how hosts/keyscans are stubbed).

Confirm the exact field names/return types before writing code in the next tasks.

### Task 1.1: Per-edge change-detection predicate (idempotency)

**Why:** The reused primitive rewrites config every run; the only way to make heal idempotent and scope restarts is to check, before provisioning a pair, whether both ends are *already correctly configured*. A probe is insufficient (authorized_keys can exist while config peers are missing/staged).

**Files:** Create `internal/cli/mesh_heal_changedetect.go`, `internal/cli/mesh_heal_changedetect_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package cli

import "testing"

func TestEdgeIsHealthyRequiresBothEndsReadyWithMatchingProof(t *testing.T) {
	// a's view of peer b, and b's view of peer a, both ssh_keys_ready with
	// the expected accept/connect key ids, and enabled+accept+connect.
	a := edgePeerView{Found: true, Enabled: true, Accept: true, Connect: true,
		MigrationState: "ssh_keys_ready", AcceptKeyID: "kb", ConnectKeyID: "ka"}
	b := edgePeerView{Found: true, Enabled: true, Accept: true, Connect: true,
		MigrationState: "ssh_keys_ready", AcceptKeyID: "ka", ConnectKeyID: "kb"}
	if !edgeIsHealthy(a, b) {
		t.Fatal("expected healthy edge")
	}
}

func TestEdgeIsHealthyFalseWhenOneEndMissing(t *testing.T) {
	a := edgePeerView{Found: true, Enabled: true, Accept: true, Connect: true, MigrationState: "ssh_keys_ready"}
	b := edgePeerView{Found: false}
	if edgeIsHealthy(a, b) {
		t.Fatal("missing peer entry must be unhealthy")
	}
}

func TestEdgeIsHealthyFalseWhenStaged(t *testing.T) {
	a := edgePeerView{Found: true, Enabled: true, Accept: true, Connect: true, MigrationState: "ssh_material_staged"}
	b := edgePeerView{Found: true, Enabled: true, Accept: true, Connect: true, MigrationState: "ssh_keys_ready"}
	if edgeIsHealthy(a, b) {
		t.Fatal("staged migration state must be unhealthy")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cli/ -run TestEdgeIsHealthy -v`
Expected: FAIL — `undefined: edgePeerView` / `edgeIsHealthy`.

- [ ] **Step 3: Implement the predicate**

In `mesh_heal_changedetect.go`:

```go
package cli

import "github.com/prime-radiant-inc/clipfan/internal/config"

// edgePeerView is one host's config view of a specific peer.
type edgePeerView struct {
	Found          bool
	Enabled        bool
	Accept         bool
	Connect        bool
	MigrationState string
	AcceptKeyID    string
	ConnectKeyID   string
}

// edgeIsHealthy reports whether the A<->B edge is already fully provisioned on
// BOTH ends: each side has the other as an enabled accept+connect peer in
// ssh_keys_ready state. (Proof key-id cross-checks are added in step 5.)
func edgeIsHealthy(a, b edgePeerView) bool {
	ready := func(v edgePeerView) bool {
		return v.Found && v.Enabled && v.Accept && v.Connect &&
			v.MigrationState == string(config.MigrationStateSSHKeysReady)
	}
	return ready(a) && ready(b)
}
```

- [ ] **Step 4: Run to verify the first two tests pass**

Run: `go test ./internal/cli/ -run TestEdgeIsHealthy -v`
Expected: the missing-end and staged tests PASS; the matching-proof test passes too (proof not yet enforced).

- [ ] **Step 5: Enforce proof cross-check + commit**

Tighten `edgeIsHealthy` so A's stored `ConnectKeyID`/`AcceptKeyID` for B are non-empty and consistent with B's view (A connects with the key B accepts). Add a test `TestEdgeIsHealthyFalseWhenProofKeyMissing` (a `ready` edge but `AcceptKeyID`/`ConnectKeyID` empty → unhealthy). Run `go test ./internal/cli/ -run TestEdgeIsHealthy -v` → PASS.

```bash
git add internal/cli/mesh_heal_changedetect.go internal/cli/mesh_heal_changedetect_test.go
git commit -m "feat: mesh-heal per-edge change-detection predicate"
```

### Task 1.2: Roster discovery

**Why:** Heal needs the full host set + each host's real config paths/endpoints. Read each reachable host's config over regular SSH (the user's reach), union, and build provision host specs — no platform guessing (each host's config carries its own paths).

**Files:** Create `internal/cli/mesh_heal_roster.go`, `internal/cli/mesh_heal_roster_test.go`.

- [ ] **Step 1: Write the failing test** — given a fake reader returning host A's config (peers B, C with endpoints/paths) and B's/C's configs, `discoverRoster(seed, reader)` returns the union {A,B,C} with each host's ssh endpoint, install/config/known_hosts/sync_key paths, and platform-independent config path. Use a fake `rosterReader` func type so no real SSH runs.

- [ ] **Step 2: Run to verify it fails** (`undefined: discoverRoster`).

- [ ] **Step 3: Implement** `discoverRoster(seed []rosterHost, read rosterReader) ([]rosterHost, error)`:
  - `rosterReader` is `func(ctx, host rosterHost) (config.Config, error)` (reads that host's config.json over regular SSH — production impl runs `cat <configPath>` or a `clipfan` read over SSH against the admin host).
  - BFS from `seed`: read each host's `cfg.SSH.Peers`, add unseen peers (id + ssh host/port/user + install/config/known_hosts/sync_key from the peer entry and the reading host's `cfg.SSH`), continue until closure over reachable hosts.
  - Return the deduped host list. Hosts that can't be read are recorded as unreachable (returned in a separate slice or marked), not fatal.

- [ ] **Step 4: Run to verify pass.**

- [ ] **Step 5: Commit** (`feat: mesh-heal roster discovery`).

### Task 1.3: Resilient host-prep (per host, error-capturing)

**Why:** The existing `scanSSHProvisionDirectHostKeys` aborts on the first host's failure and is the source of host-key pins + server-mode + admin-host. Heal must prep each host independently, capturing failures so one unreachable host doesn't sink the run.

**Files:** Create `internal/cli/mesh_heal_hostprep.go`, `internal/cli/mesh_heal_hostprep_test.go`.

- [ ] **Step 1: Write the failing test** — `prepHosts(ctx, hosts, runner)` returns a `map[hostID]hostPrep` (with the confirmed host-key line + server-mode) for reachable hosts and a `map[hostID]error` for failures; a runner that errors for one host still returns prep for the others. Reuse the fake `CommandRunner` from `ssh_provision_direct_test.go`.

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** `prepHosts(...)` by factoring the per-host body of `scanSSHProvisionDirectHostKeys` (keyscan target resolution via `ssh -G`, the keyscan, server-mode detection) into a per-host function wrapped in a recover-and-record loop. Do NOT change `scanSSHProvisionDirectHostKeys` itself (leave `ssh-provision-direct` as-is); extract a shared helper both can call if convenient.

- [ ] **Step 4: Run to verify pass.**

- [ ] **Step 5: Commit** (`feat: mesh-heal per-host resilient host-prep`).

### Task 1.4: Go daemon-restart capability

**Why:** Direct config writes don't hot-reload; a changed host needs a restart, and there is no Go restart today.

**Files:** Create `internal/cli/mesh_heal_restart.go`, `internal/cli/mesh_heal_restart_test.go`.

- [ ] **Step 1: Write the failing test** — `restartCommand(platform, uid)` returns the right argv: macOS → `launchctl kickstart -k gui/<uid>/com.primeradiant.clipfan`; Linux → `systemctl --user restart clipfan`. Pure function, table-driven.

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** `restartCommand(...)` (pure) and `restartDaemon(ctx, runner, adminHost, platform)` that runs it over regular SSH against the admin host via the existing `CommandRunner`. Note (verified live): `systemctl --user` works over non-interactive SSH on this fleet because PAM provides `DBUS_SESSION_BUS_ADDRESS` and lingering is enabled; do not inject env.

- [ ] **Step 4: Run to verify pass.**

- [ ] **Step 5: Commit** (`feat: mesh-heal Go daemon restart`).

### Task 1.5: The `mesh-heal` orchestration

**Why:** Tie it together: discover roster → prep hosts (resilient) → for each pair, read both ends' config and `edgeIsHealthy` → skip healthy, else `provisioner.Provision` with per-pair error capture → restart only hosts whose config changed → emit a JSON report.

**Files:** Create `internal/cli/mesh_heal.go`, `internal/cli/mesh_heal_test.go`.

- [ ] **Step 1: Write the failing test** — with a fully-faked runner/driver and a 3-host roster where one edge is already healthy and one host is unreachable: `runMeshHeal(...)` provisions the reachable unhealthy edge, SKIPS the healthy edge (no `Provision` call, no restart for its hosts), reports the unreachable host, and does NOT return an error for partial success. Assert the JSON report shape (`{"healed":[…],"skipped":[…],"failed":[…]}`).

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** `runMeshHeal(args, stdout, stderr, opts)` (mirror `runSSHProvisionDirect`'s option-injection so tests pass fakes): parse flags (`--regular-known-hosts`, optional `--host` seeds, `--trust-keyscan`), discover roster (Task 1.2), prep hosts (Task 1.3), build `DirectPairProvisionHost`s, iterate unordered pairs: read both ends via `config.ReadSSHPeer`-over-SSH → `edgeIsHealthy` → skip or `DirectPairProvisioner.Provision` (capture per-pair error into the report, continue), track changed hostIDs, restart changed hosts (Task 1.4), encode the report JSON. Add `RunMeshHeal(args, stdout, stderr) error` as the public entry.

- [ ] **Step 4: Run to verify pass.** Then `go build ./...`.

- [ ] **Step 5: Commit** (`feat: clipfan mesh-heal orchestration`).

### Task 1.6: Register the subcommand

**Files:** Modify `cmd/clipfan/main.go`.

- [ ] **Step 1:** Add, alongside the other `case` blocks (after `case "ssh-apply-direct-config":`):

```go
		case "mesh-heal":
			if err := cli.RunMeshHeal(args[1:], stdout, stderr); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
```

- [ ] **Step 2:** Run `go build ./...` and `go vet ./...`. Expected: clean.
- [ ] **Step 3:** Commit (`feat: register mesh-heal subcommand`).

---

## Phase 2 — Foundation 2: `fleet-snapshot` gateway verb (Go)

**Why:** The Mac needs to read a peer's redacted roster + edge-health for the Fleet view. Add a read-only gateway verb, authed like the others, returning ONLY non-secret fields.

**Files:** Create `internal/cli/fleet_snapshot.go`, `internal/cli/fleet_snapshot_test.go`; modify `internal/cli/ssh_gateway.go`; add the command constant alongside `SSHGatewayProbeCommand`/`SSHGatewaySyncStreamCommand` in `internal/sshprovision/` (grep `SSHGatewayProbeCommand` for its definition file).

- [ ] **Task 2.1 — redacted snapshot payload (TDD):** Write `buildFleetSnapshot(cfg *config.Config, localPeers []daemon.PeerState) FleetSnapshot` returning `{origin, version, peers:[{id, ssh_host, ssh_port, ssh_user, migration_state, ssh_status, ssh_active, last_recv_ts, ssh_last_ack_ts, ssh_last_connect_ts}]}` — and assert it contains **no** `shared_key`, no `proof` secrets, no sync-key paths. Test first (assert a marshalled snapshot of a config that has `shared_key` set does not contain that key's bytes). Implement by copying only the allowlisted fields. Commit.

- [ ] **Task 2.2 — gateway verb wiring (TDD):** Add `SSHGatewayFleetSnapshotCommand = "fleet-snapshot"`. In `runSSHGatewayWithHandlers`'s `switch command`, add a `case` that (a) requires `validateSSHGatewaySyncPeer(cfg, identity)` to pass, (b) loads config + the local daemon's `/v1/peers` snapshot over loopback (reuse the gateway's existing loopback client path; if no signed `/v1/peers` client method exists yet, add a minimal signed GET mirroring how `current` is fetched — confirm against `transport/client.go`), (c) writes `buildFleetSnapshot(...)` as JSON to stdout. Test the rejection path (unauthorized peer → `ErrSSHGatewayCommandRejected`) and the happy path with a fake. Commit.

- [ ] **Task 2.3 — build + vet:** `go build ./... && go test ./internal/cli/ -run FleetSnapshot -v`. Commit if not already.

---

## Phase 3 — Workstream D: Mac drivers (replace Swift orchestration)

**Depends on Phase 1.** Replace the heavy Swift orchestration with a `mesh-heal` invocation; add a "Repair mesh" action.

**Files:** Create `apps/mac/Clipfan/Sources/Clipfan/MeshHealClient.swift`; modify `Installer.swift` (`provisionPrivateDirectMesh`), `SettingsView.swift`/`StatusMenuView.swift`.

- [ ] **Task 3.1 — MeshHealClient (TDD):** Add a `MeshHealReport: Codable` matching the Go JSON (`healed`/`skipped`/`failed`), and a pure decoder `decodeMeshHealReport(_ data: Data) -> MeshHealReport?`. Test decoding a sample report (healed 1 edge, 1 failed host). Implement `MeshHealClient.heal()` that runs `clipfan mesh-heal …` via the existing `runCommand`/Process pattern (mirror how `Installer` invokes `ssh-provision-direct` today) and returns the decoded report.

- [ ] **Task 3.2 — invoke mesh-heal from add-peer install:** In `Installer.provisionPrivateDirectMesh`, after the host install/bootstrap step, replace the inline `ssh-provision-direct` + restart-all logic with a single `MeshHealClient.heal()` call. Keep the binary-install step (heal does not install). Surface per-host failures from the report. `swift build`.

- [ ] **Task 3.3 — Repair mesh action:** Add a "Repair mesh" button (Settings → Fleet and/or the menu) that calls `MeshHealClient.heal()` and shows the report summary. Reuse `FleetRow`/health idioms. `swift build`.

- [ ] **Task 3.4 — augment add-peer success copy:** Now that add-peer actually heals, update the Workstream-G success view (from the quick-wins plan) to show heal status (e.g. "Added <host> · mesh healed (N edges)"). `swift build`. Commit each task.

---

## Phase 4 — Workstream E: mesh-state visibility (macOS)

**Depends on Phase 2.** Read `fleet-snapshot` from adjacent hosts, assemble a fleet-wide model, render per-host rows + edge detail with unobservable edges marked "unknown."

**Files:** Create `apps/mac/Clipfan/Sources/Clipfan/FleetMeshModel.swift` (+ test) and a `FleetMeshView.swift`; wire into `SettingsView`/`StatusMenuView`.

- [ ] **Task 4.1 — fleet-wide model (TDD):** Pure functions: given a set of per-host snapshots, produce `[MeshHostRow]` where each host's aggregate health = worst *observed* edge, edge counts are correct, and edges with no observation render `.unknown` (not `.down`). Test: a deliberately-down edge surfaces on both endpoints' rows where observed; an unobserved edge is `.unknown`; the header count ("M/N edges healthy") uses observed edges only. Match the spec's corrected example arithmetic (4 hosts → 6 undirected edges, 3 peers each).

- [ ] **Task 4.2 — snapshot gathering:** Add a client method that, for each gateway-adjacent host, runs `fleet-snapshot` over SSH and decodes it (reuse `MeshHealClient`'s Process pattern; redaction is enforced server-side). On-demand / Fleet-view-open only; bounded.

- [ ] **Task 4.3 — render per-host rows + edge detail:** `FleetMeshView` renders the rows from Task 4.1, expandable to edges, "unknown" styled distinctly. `swift build`. Commit each task.

---

## Phase 5 — Workstream F: onboarding wizard

**Depends on Phase 3** (its add-host step calls `mesh-heal`) **and the quick-wins Workstream G** (reuses `AddPeerSheet` host fields).

**Files:** Modify `WelcomeView.swift`, `WelcomeWindow.swift`, `Bootstrap.swift`; tests in `BootstrapTests.swift`.

- [ ] **Task 5.1 — step state machine (TDD):** Add an `OnboardingStep` enum (`welcome → localSetup → addHost → done`) and pure transition logic: advance from `localSetup` on `SetupState.success`; `addHost` is skippable; `done` shows tips + a fleet summary. Test the transitions (advance on success; skip path; completion) mirroring the existing `SetupState` tests in `BootstrapTests.swift`.

- [ ] **Task 5.2 — wizard view:** Rewrite `WelcomeView` to render the current step with a `●─○─○─○` indicator; step `addHost` reuses `AddPeerSheet`'s host fields + runs `MeshHealClient.heal()` (Phase 3); make the wizard re-runnable from the menu. `swift build`.

- [ ] **Task 5.3 — wire menu re-entry + first-run:** Ensure `AppDelegate.firstRunInstall` opens the wizard and the menu has a "Set up clipfan…" entry. `swift build`. Commit each task.

---

## Self-Review (completed by plan author)

- **Spec coverage:** Foundation 1 → Phase 1 (Tasks 1.1–1.6); Foundation 2 → Phase 2; Workstream D → Phase 3; E → Phase 4; F → Phase 5. The spec's three round-2/round-3 corrections are all encoded: per-edge change-detection that checks *config state on both ends* (Task 1.1, not a probe), per-host resilient host-prep (Task 1.3), Go restart of only changed hosts (Tasks 1.4–1.5), redaction in `fleet-snapshot` (Task 2.1), and G not claiming "mesh healed" until Phase 3 augments it (Task 3.4).
- **Placeholders:** the Go foundation tasks (Phase 1–2) include concrete signatures, real reused APIs, and TDD tests. Phases 3–5 (Swift) are specified as interfaces + tests + file targets rather than full line-level code, because their exact call sites depend on the Foundation APIs built in Phases 1–2 — the implementer wires them against the real signatures once those exist. Task 1.0 mandates reading the reuse surface first so nothing is guessed.
- **Type consistency:** `edgePeerView`/`edgeIsHealthy`, `rosterHost`/`discoverRoster`/`rosterReader`, `prepHosts`/`hostPrep`, `restartCommand`/`restartDaemon`, `runMeshHeal`/`RunMeshHeal`, `buildFleetSnapshot`/`FleetSnapshot`, `MeshHealReport`/`decodeMeshHealReport`/`MeshHealClient`, `OnboardingStep` — names used consistently across each task and its test.
