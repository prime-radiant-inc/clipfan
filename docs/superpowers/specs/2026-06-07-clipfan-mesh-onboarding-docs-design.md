# clipfan: self-healing mesh, onboarding wizard, and docs overhaul — design

**Date:** 2026-06-07
**Status:** Draft for review (revised after adversarial review)

## Summary

This work makes clipfan's fleet topology robust and legible, modernizes onboarding,
and brings the docs up to date. Concretely: a new CLI command heals the fleet into a
full mesh from any host; the macOS app shows the mesh state it can observe (every
host and the health of its edges); first-run becomes a multi-step wizard that sets up
this Mac *and* walks the user through adding their first host; the add-peer panel
gains a real success state; the app gets an About screen; the README is rewritten for
users with development docs moved under `docs/`; and two confirmed bugs are fixed —
Mac tmux paste, and the dead outbound sync indicator under SSH transport.

## Goals

- **Self-healing mesh, in the CLI.** A `clipfan mesh-heal` command provisions the
  complete mesh among the fleet roster, idempotently and resiliently, runnable on
  **any** host (macOS or Linux) using the invoking user's SSH reach. Provisioning
  orchestration lives in the Go CLI, not in the Mac app, so a future Linux desktop UI
  can drive the same command.
- **Mesh visibility.** The macOS Fleet view shows all hosts and the health of their
  edges (per-host rows with expandable edge detail), marking edges it cannot observe
  as unknown rather than pretending.
- **Onboarding wizard:** a multi-step first-run flow (intro → set up this Mac → add
  your first host → done), re-runnable from the menu.
- **Add-peer success state:** an explicit "Done" (and "Add another") after a
  successful install, replacing the current silent auto-dismiss.
- **About screen:** a standard About window with app/daemon version and links.
- **User-focused README** with development content relocated under `docs/`, and **no
  prime-radiant ticket IDs** anywhere in `docs/`.
- **Bug fix — Mac tmux paste:** received clips reach tmux paste buffers on macOS
  peers, not just the OS clipboard.
- **Bug fix — outbound indicator:** the Fleet "↑" reflects SSH transport activity
  instead of reading "never".

## Non-goals

- Re-enabling a non-loopback HTTP listener. Daemon HTTP stays loopback-only; all
  remote reads/writes go over SSH.
- Auto-pruning unreachable hosts. Healing only adds and re-verifies edges; removing a
  host stays an explicit user action via the existing remove flow.
- Self-installing brand-new hosts. Getting the clipfan *binary* onto a new host stays
  a push from the UI that adds it (the Mac app ships the cross-arch binaries today);
  `mesh-heal` only links hosts that already have clipfan installed. Packaging the
  installer for a Linux UI is future work.
- Changing the clipboard sync protocol or the last-write-wins conflict policy.
- Backward-compatibility shims (none without explicit approval; mixed-version fleets
  fail closed as today).

## Current state (verified context)

- **Transport is SSH.** Each host runs a daemon (loopback HTTP on `127.0.0.1:7853`)
  plus, per peer, an outbound `ssh … sync-stream` and an inbound `clipfan
  ssh-gateway`. Discovery is `static`; the clipboard transport is SSH (Tailscale is
  only the underlay network for some hosts' SSH addresses).
- **The live fleet is a star, not a mesh.** `jesse-paradise-park`'s config lists one
  peer (`m4`); `m4`'s lists three (`flower-garden`, `magic-kingdom`,
  `jesse-paradise-park`). Cross-host delivery to non-adjacent peers works only because
  `m4` relays: on receive, the daemon re-publishes over SSH via `publishSSH`
  (`daemon.go:766`; the HTTP `fanout` path no-ops under SSH). If `m4` is down, the
  others cannot sync.
- **The pair-provisioning engine already builds a full mesh.** `clipfan
  ssh-provision-direct` with N `--host` specs loops every pair `(i, j)` and provisions
  it idempotently with revision-locked, two-phase config apply
  (`internal/cli/ssh_provision_direct.go:97-109`; `internal/sshprovision/pair_plan.go`;
  `config_applicator.go`). The end-to-end proof is
  `TestRunSSHProvisionDirectBuildsThreeHostMesh`. (Note: `BuildDirectMeshPlan` /
  `TestDirectMeshPlanBuildsThreeHostFullMesh` exercise an *unused* plan-builder — the
  CLI uses its own inline loop, so that test is not the operative proof.)
- **But provisioning orchestration is Mac-only and all-or-nothing.** The roster
  building, platform probing, binary install, the `ssh-provision-direct` invocation,
  and the daemon restarts all live in Swift `Installer.swift`
  (`provisionPrivateDirectMesh`), which only ever passes `[this Mac] + [hosts added
  now]` — hence the star. Two limits matter for healing: `ssh-provision-direct` aborts
  the whole run on the first unreachable host (`ssh_provision_direct.go:83-86`,
  `99-106`); and the Swift flow re-installs every non-local host and restarts **every**
  daemon on each run (`Installer.swift:599-642`).
- **Per-host keys; reach is over the user's regular SSH.** Each host has its own
  ed25519 sync key; meshing two hosts requires configuring `authorized_keys`,
  `known_hosts`, and config peer entries on **both** sides. Provisioning reaches and
  configures each host over the invoking user's regular SSH (with `ssh-keyscan` host
  key trust), not the clipfan sync key. The orchestrator must be able to resolve and
  SSH to each host; stored peer endpoints are peer-relative (`.local` mDNS, bare
  hostnames) and not guaranteed reachable from an arbitrary orchestrator.
- **Gateway verbs today:** `probe` and `sync-stream` only. The gateway can reach the
  local daemon over loopback to push received clips (`PushAsToRecipient`) and to read
  `current`, but `transport.Client` has **no** method to read `/v1/peers`, and there
  is no read-only path for a remote host to read another host's roster or edge-health.
- **Daemon launch / PATH:** on macOS the daemon's effective `PATH` omits Homebrew's
  `/opt/homebrew/bin`, where `tmux` lives (the running daemon is app-spawned, not
  launched from the plist that `install.sh` writes — so the plist's PATH is moot for
  the running process).

## Architecture

Two foundations underpin the mesh work.

### Foundation 1 — `clipfan mesh-heal` (Go CLI, runnable on any host)

A new CLI command that converges the fleet to a complete mesh. Design properties:

- **Lives in Go**, in the `clipfan` binary, so it runs on macOS and Linux and is
  driven identically by the Mac app today and a Linux desktop UI later. The
  orchestration that is currently in Swift `Installer.provisionPrivateDirectMesh`
  (roster building, platform probe, `ssh-provision-direct` invocation, restarts) moves
  here; the Swift side becomes a thin invoker.
- **Runs as the invoking user**, using the user's regular SSH reach (the same
  mechanism the current add-peer flow uses to reach hosts). It is *not* the background
  daemon autonomously SSHing — it's a user-invoked command (from the UI or a shell),
  so it has the user's credentials/agent.
- **Roster discovery (decentralized, in Go).** Build the roster as the union of this
  host's configured peers plus the rosters of reachable hosts, read over the user's
  SSH reach (read each host's `ssh.peers[]` and the host's own config paths directly,
  so no platform guessing is needed). Platform, where required for a freshly probed
  host, comes from a `uname` probe over SSH (as the installer already does).
- **Provisions the complete mesh among the roster**, reusing the existing
  pair-provisioning primitive — but **per-pair and resilient**: iterate pairs, and a
  pair that fails (unreachable host, unresolved address) is recorded and skipped
  rather than aborting the run. The command returns a per-host / per-edge status
  report. Idempotent: already-`ready` edges are re-verified, not rebuilt, and hosts
  whose binary is already current are not re-installed; only changed hosts' daemons
  restart.
- **Completeness:** running `mesh-heal` on a host provisions every pair that host can
  reach (configuring both ends of each). A single host with reach to all others
  completes the whole mesh; where reach is partial, another host completes the
  remainder when it runs `mesh-heal`. "Every host can provision its links to the
  complete mesh" is satisfied because the command exists on, and runs from, every
  host.

The exact subcommand surface (a new `mesh-heal` vs. extending `ssh-provision-direct`,
flag shapes, report JSON) is for the implementation plan; the implementer confirms the
existing reach mechanism in `internal/sshprovision/regular_ssh_driver.go` rather than
assuming it.

### Foundation 2 — read-only mesh-state surface

The UI needs each host's view of its edges. Two read paths, both read-only:

- **Local:** this host's own edge-health is the daemon's existing `Snapshot()` via the
  signed loopback `GET /v1/peers`.
- **Remote (adjacent hosts):** a new read-only gateway verb, `fleet-snapshot`, invoked
  over an existing sync-key edge (validated by the existing
  `validateSSHGatewaySyncPeer` path). It returns the host's identity, its roster from
  `config.Load()`, and its runtime edge-health. **Plumbing note:** the gateway process
  cannot read `/v1/peers` today (`transport.Client` exposes only push/`current`), so
  serving runtime edge-health requires new code — either a signed loopback `/v1/peers`
  client method or a small new daemon endpoint the gateway can call. The roster half
  needs no daemon round-trip (`config.Load()` suffices).

A host can only `fleet-snapshot` peers it already shares a sync edge with. Pre-heal
(star) that is just the adjacent host(s); post-heal (full mesh) it is the whole fleet.
The UI therefore renders the edges it can observe and marks the rest **unknown** —
honest, and complete once the mesh is healed.

## Workstream A — Documentation

**Problem.** The README leads with build-from-source, buries daily usage and
troubleshooting, and carries a ticket id; `docs/` leaks ticket ids.

**Design.**

1. **Rewrite `README.md` user-first:** what clipfan is → how it works (brief) →
   install (app/binary first; build-from-source linked out) → getting started (set up
   this Mac, add a host) → daily use (copy/paste across hosts, tmux, history) →
   configuration → security summary → troubleshooting → pointers to `docs/`.
2. **Reorganize `docs/`:** add `docs/development/` for build-from-source and developer
   setup (extracted from the README); keep `ARCHITECTURE.md`, `ROADMAP.md`,
   `RELEASING.md`, `ci/` as-is; add `docs/TROUBLESHOOTING.md` (daemon not running;
   peers not syncing / mesh health).
3. **Scrub ticket references in place** across `docs/` and `README.md`: remove all
   `PRI-NNNN` ids and the `**Tracks:**` / `**Ticket:**` headers, and rewrite inline
   process references (e.g. "Update Linear PRI-…", "move the ticket to …") to describe
   behavior, not tickets. Known id occurrences: `README.md:10`, `docs/PLAN.md:8`,
   `docs/superpowers/plans/2026-05-28-clipboard-history.md` (lines 1938, ~1940),
   `docs/superpowers/specs/2026-05-28-clipboard-history-design.md:5`,
   `docs/superpowers/specs/2026-05-29-clip-id-recirculation-design.md` (16, 76, 130),
   `docs/superpowers/specs/2026-05-29-mac-app-ux-redesign-design.md:5`.

**Acceptance.**
- This grep finds nothing (note the exclusion — this design doc and any future spec
  legitimately quote the patterns):
  `grep -rnE 'PRI-[0-9]+|\*\*(Tracks|Ticket):\*\*|Update Linear|move the ticket' README.md docs/ --exclude='2026-06-07-clipfan-mesh-onboarding-docs-design.md'`
- README's first install instruction is the user path (app/binary), not `go build`.
- Build-from-source is discoverable under `docs/development/`.
- Spot-checked README commands match the current CLI surface.

## Workstream B — Bug fix: Mac tmux paste (PATH root cause)

**Root cause (confirmed by live reproduction).** On macOS the daemon runs with a PATH
that omits Homebrew's bin dir, so `tmux.LoadBufferAll`'s `exec.Command("tmux", …)`
fails to find the binary; the OS-clipboard write still succeeds (`pbpaste` works — no
PATH needed), so received clips reach the clipboard but not the tmux buffer. Linux
peers are unaffected (`tmux` is in `/usr/bin`). Reproduction: copying on Linux
`magic-kingdom` updated this Mac's `pbpaste` but not its tmux buffer; `tmux` was absent
from the daemon's PATH; a full-path `tmux load-buffer` on the same socket worked.

**Design.** In `internal/tmux/tmux.go`, resolve `tmux` robustly: a `tmuxBinary()`
resolver that prefers `exec.LookPath("tmux")` then falls back to absolute candidates
(`/opt/homebrew/bin/tmux`, `/usr/local/bin/tmux`, `/usr/bin/tmux`, `/bin/tmux`),
returning the first that exists and is executable; `LoadBufferAll` uses the resolved
path. Structure the resolver so candidates and the lookup are injectable for testing.
(`install.sh` already bakes a login-shell PATH into the plist — but the running daemon
is app-spawned and ignores it, so the resolver, which is launch-independent, is the
fix; no `install.sh` change is required.)

**Acceptance.**
- Unit test: the resolver finds `tmux` given a PATH lacking it but with a temp-dir
  candidate present.
- Live: copy on one host → the tmux paste buffer updates on a macOS peer running tmux
  (not just `pbpaste`); no change on Linux peers.

## Workstream C — Bug fix: outbound indicator under SSH ("↑ never")

**Root cause (confirmed).** The Fleet "↑" reads `last_push_ts`, set only by
`recordPush`, which is called only inside `fanout` — and `fanout` no-ops under SSH
(`peerHTTPDisabled`, `daemon.go:773`/`142`). The row also requires *both* a push and a
recv timestamp to render the up/down block (`FleetRow.swift:204`), so SSH peers show
neither arrow.

**Design.** Make the indicator transport-aware in `FleetRow.swift`: for SSH peers
source the "↑" from **`ssh_last_ack_ts`** (an ack means a clip was sent and accepted) —
do **not** fall back to `ssh_last_connect_ts`, which is set on connect even with zero
sends and would show a send time when nothing was sent. Render the up/down block when a
transport-appropriate send time *or* a recv time exists. Verify the SSH timestamps
surface through `Models.swift`/`DaemonClient` (they are already in the peers payload).

**Acceptance.**
- Swift test: a peer with `ssh_last_ack_ts` and no `last_push_ts` yields a non-"never"
  outbound time; a peer with only `ssh_last_connect_ts` (connected, never sent) does
  not show a spurious send time.
- Live: with SSH transport, the Fleet "↑" advances after a copy.

## Workstream D — Self-healing mesh (the `mesh-heal` command + drivers)

**Problem.** Provisioning is Mac-only, star-producing, and all-or-nothing.

**Design.** Build Foundation 1 (`clipfan mesh-heal`) and drive it:

1. **Roster discovery in Go** (decentralized, over the user's SSH reach): union of
   local config peers and reachable hosts' rosters; read each host's real config paths
   directly so no platform derivation is needed; `uname`-probe platform only when
   provisioning a host fresh.
2. **`mesh-heal` provisions the complete mesh** among the roster via the existing
   pair primitive, **per-pair and resilient** — failures are recorded per host/edge and
   skipped, not fatal. Idempotent: re-verify ready edges, skip re-install of
   already-current hosts, restart only changed hosts' daemons.
3. **Reach is a real constraint.** A pair can only be provisioned by a runner that can
   resolve and SSH to both hosts. The command reports unreachable/unresolved pairs;
   the UI surfaces them so the user can fix an address or run heal from a host with the
   needed reach. (Stored endpoints may be `.local`/peer-relative.)
4. **Drivers.** The Mac app (and later the Linux UI) invoke `mesh-heal` after an
   add-host and from an explicit **"Repair mesh"** action; the heavy Swift
   orchestration is removed in favor of the CLI command. Initial binary install of a
   brand-new host stays the UI's responsibility (non-goal boundary).

**Acceptance.**
- Go test: given a roster with a missing edge and one unreachable host, `mesh-heal`
  provisions the reachable missing edge, leaves ready edges untouched, and reports the
  unreachable host without aborting (extends `pair_plan`/`config_applicator` tests;
  uses the fake command runner already used by `ssh_provision_direct` tests).
- Live: after running heal on the real fleet, `jesse-paradise-park`'s config lists all
  other hosts as peers (not just `m4`), and a copy on `jesse-paradise-park` reaches
  `magic-kingdom` over a **direct** edge (verify by edge health, not via `m4`).

## Workstream E — Mesh-state visibility (macOS)

**Problem.** The Fleet view shows only this Mac's peers.

**Design.** Using Foundation 2: read this host's local `Snapshot()` and
`fleet-snapshot` from each adjacent host; assemble a fleet-wide model of hosts and
per-edge health from the observations available. Render **per-host rows with edge
detail** (reusing `FleetRow`/`HealthDot`): each host gets an aggregate dot (worst
observed edge) and a summary; expanding shows its edges, with unobservable edges marked
**unknown** rather than healthy/down. Example:

```
FLEET — 4 hosts · mesh 11/12 edges healthy
  ● jesse-paradise-park  (you)   running
  ● m4                    5 peers · live
  ● magic-kingdom         4 peers · live
  ⚠ flower-garden         3/4 edges · 1 down
       └ ● m4   ● magic-kingdom   ○ jesse-paradise-park
```

Refresh on Fleet-view open and on explicit refresh; reads are bounded and read-only.
Pre-heal the view is necessarily partial (only adjacent hosts are observable); post-heal
it covers the whole fleet.

**Acceptance.**
- Swift tests: aggregate = worst *observed* edge; unobservable edges render "unknown,"
  not "down"; counts correct.
- Live: the Fleet view lists every host (not just `m4`) and flags a deliberately broken
  edge once it is observable.

## Workstream F — Onboarding wizard

**Problem.** First-run only installs the local daemon (`WelcomeView` is one screen with
`SetupState` idle→installing→success/failed); it never helps add a host.

**Design.** Replace the single screen with a multi-step wizard in the existing managed
`WelcomeWindow`:

1. **Welcome** — what clipfan does.
2. **Set up this Mac** — the current local install (reuse `BootstrapController`).
3. **Add your first host** — collect SSH host/user (reuse `AddPeerSheet`'s host fields
   and provisioning), then run **`mesh-heal`** so the fleet is fully connected.
   Skippable.
4. **You're all set** — existing tips (⇧⌘V, paste flow) + a one-line fleet summary.

A step indicator (`●─○─○─○`) shows progress; the wizard is re-runnable from the menu.
Model the step machine as pure, testable state (as `SetupState` is tested today).

**Acceptance.**
- Swift tests for the step machine (advance on local-setup success; skip path;
  completion).
- Manual: a fresh run walks Welcome → local setup → add host (heals mesh) → done; the
  wizard reopens from the menu.

## Workstream G — Add-peer success state & dismiss

**Problem.** On success the sheet silently `Task.sleep`-then-`dismiss()`s after 1s, with
only "Cancel" + the install button — no success UI.

**Design.** Add an explicit success state to `AddPeerSheet`: track `installSuccess`;
show a success view ("Added <host>", with a note that the mesh was healed); replace the
silent auto-dismiss with **Done** (dismiss) and **Add another** (reset the form).
(`UpdatePeerSheet` is the sibling success-then-dismiss pattern.)

**Acceptance.**
- Swift test: after a successful install the model exposes the success state and the
  Done/Add-another affordances (no timed auto-dismiss).
- Manual: install → success screen → Done closes; Add another resets.

## Workstream H — About screen

**Problem.** There is no About screen.

**Design.** Add a `Window("about", id: "about")` scene in `ClipfanApp.swift` hosting a
new `AboutView`, opened via the captured `openWindow` (`WindowOpener` bridge) and wired
to an "About clipfan…" item in `StatusMenuView`. `AboutView` shows app icon, name, app
version (`CFBundleShortVersionString`), daemon version (`daemon.version`), a one-line
description, and links. The standard AppKit about panel is the fallback if the custom
window proves fiddly.

**Acceptance.**
- The menu opens an About window showing app and daemon versions (from real sources, no
  hard-coded literals).

## Testing strategy

- **Go:** unit tests for the tmux resolver (B); `mesh-heal` roster discovery, per-pair
  resilience, and idempotent skip (D), using the existing fake command runner;
  `fleet-snapshot` output shape, read-only behavior, and auth (Foundation 2).
- **Swift:** SSH outbound-time mapping (C); the fleet-wide mesh model incl. "unknown"
  edges (E); the wizard step machine (F); the add-peer success state (G).
- **Live fleet verification** on the real hosts (`jesse-paradise-park`, `m4`,
  `magic-kingdom`, `flower-garden`): tmux paste onto a Mac peer (B); outbound indicator
  advancing under SSH (C); post-heal direct edges and a partial-heal report with one
  host down (D); Fleet view showing all hosts and a broken edge (E). Test output must be
  clean; intentional error cases capture and assert their output.

## Dependencies & parallelization (for the implementation plan)

- **Independent, parallel-ready now:** **A** (docs), **B** (tmux PATH fix), **C**
  (outbound indicator), **G** (add-peer success), **H** (About).
- **Foundation 1 — `mesh-heal` CLI (Go):** blocks **D**'s drivers and the wizard's
  add-host step (**F**).
- **Foundation 2 — read-only mesh-state surface (`fleet-snapshot` + plumbing):** blocks
  **E**.
- **Dependent:** **D** (drivers/UI) on Foundation 1; **E** on Foundation 2; **F** on
  **D** (its add-host step calls `mesh-heal`) and reuses `AddPeerSheet` fields from
  **G**.
- The Swift UI changes (C/E/F/G/H) touch overlapping files (`AddPeerSheet`,
  `FleetRow`, `StatusMenuView`, `ClipfanApp`); sequence or worktree-isolate the Swift
  work so parallel agents don't clobber each other.

Suggested order: land A/B/C/G/H in parallel; build the two Go foundations
(`mesh-heal`, `fleet-snapshot`); then D and E in parallel; then F.

## Risks & open questions

- **Reach prerequisite.** `mesh-heal` can only provision pairs whose hosts the runner
  can resolve and SSH to. Peer-relative addresses (`.local`, bare hostnames) may not
  resolve from every runner. Mitigation: per-host failure reporting, address
  confirmation in the UI, and the ability to run heal from a host with the needed reach.
- **Roster propagation.** Decentralized discovery sees only the transitively reachable
  set; a new host is fully meshed once each host has discovered it and run heal (or one
  well-connected host heals all reachable pairs). Acceptable; full coverage returns as
  partitions reconnect.
- **Moving orchestration to Go is real work.** The Swift `provisionPrivateDirectMesh`
  logic (probe/stage/install/provision/restart) must be reimplemented in Go with the
  resilient, idempotent semantics above; the Swift side shrinks to an invoker. The
  implementer must preserve the existing config-preserving install behavior (a heal
  must not clobber a host's `config.json`).
- **Key rotation.** A rotated sync key staleens peers' proofs; heal's re-verify should
  detect and re-stage, but this path needs an explicit test; not otherwise solved here.
- **UI refresh cost.** Gathering `fleet-snapshot` from every adjacent host is N
  round-trips; keep it on-demand/open-triggered and bounded, not a tight poll.
