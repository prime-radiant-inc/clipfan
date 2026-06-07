# clipfan: self-healing mesh, onboarding wizard, and docs overhaul — design

**Date:** 2026-06-07
**Status:** Draft for review

## Summary

This work makes clipfan's fleet topology robust and legible, modernizes onboarding,
and brings the docs up to date. Concretely: any provisioning operation heals the
whole fleet into a full mesh; the macOS app shows the complete mesh state (every
host and the health of its edges), not just this Mac's own peers; first-run becomes
a multi-step wizard that sets up this Mac *and* walks the user through adding their
first host; the add-peer panel gains a real success state; the app gets an About
screen; the README is rewritten for users with development docs moved under `docs/`;
and two confirmed bugs are fixed — Mac tmux paste, and the dead outbound sync
indicator under SSH transport.

## Goals

- **Self-healing mesh:** every provisioning op (add host, wizard, explicit repair)
  converges the entire fleet to a full mesh — every host peers bidirectionally with
  every other host.
- **Full mesh visibility:** the macOS Fleet view shows all hosts and the health of
  their edges, using per-host rows with expandable edge detail.
- **Onboarding wizard:** a multi-step first-run flow (intro → set up this Mac → add
  your first host → done), re-runnable from the menu.
- **Add-peer success state:** an explicit "Done" (and "Add another") after a
  successful install, replacing the current silent auto-dismiss.
- **About screen:** a standard About window with app/daemon version and links.
- **User-focused README** with development content relocated under `docs/`, and **no
  prime-radiant ticket IDs** anywhere in `docs/`.
- **Bug fix — Mac tmux paste:** received clips reach tmux paste buffers on macOS
  peers, not just the OS clipboard.
- **Bug fix — outbound indicator:** the Fleet "↑" (last-sent) reflects SSH transport
  activity instead of reading "never".

## Non-goals

- Re-enabling a non-loopback HTTP listener. Daemon HTTP stays loopback-only; all
  remote reads/writes go over SSH. (Established by the hybrid-SSH transport design.)
- Auto-pruning unreachable hosts. Healing only **adds and re-verifies** edges;
  removing a host stays an explicit user action via the existing remove flow.
- Changing the clipboard sync protocol or the last-write-wins conflict policy.
- Backward-compatibility shims. (Per standing project rule, none are added without
  explicit approval; mixed-version fleets fail closed as today.)

## Current state (verified context)

- **Transport is SSH.** Each host runs a daemon (loopback HTTP on `127.0.0.1:7853`)
  plus, per peer, an outbound `ssh … sync-stream` and an inbound `clipfan
  ssh-gateway`. Discovery is `static`; the clipboard transport is SSH (Tailscale is
  only the underlay network for some hosts' SSH addresses).
- **The live fleet is a star, not a mesh.** `jesse-paradise-park`'s config lists one
  peer (`m4`); `m4`'s lists three (`flower-garden`, `magic-kingdom`,
  `jesse-paradise-park`). Cross-host delivery to non-adjacent peers works only
  because `m4` relays (`daemon.go` `onReceive` re-fanouts to non-origin peers). If
  `m4` is down, the others cannot sync.
- **Why it's a star:** the provisioning *engine* already builds a full mesh — `clipfan
  ssh-provision-direct` with N `--host` specs provisions all N(N-1)/2 pairs
  idempotently with revision-locked, two-phase config apply
  (`internal/cli/ssh_provision_direct.go`, `internal/sshprovision/pair_plan.go`,
  `config_applicator.go`; proven by `TestDirectMeshPlanBuildsThreeHostFullMesh`).
  But `AddPeerSheet` only ever passes `[this Mac] + [hosts added in this operation]`,
  so hosts added separately never become peers of each other.
- **Gateway verbs today:** `probe` and `sync-stream` only. There is no read-only path
  for the Mac to read a *remote* host's roster or peer-state; daemon HTTP is
  loopback-only.
- **Daemon launch / PATH:** on macOS the daemon's effective `PATH` is
  `/Users/jesse/.local/bin:/usr/bin:/bin:/usr/sbin:/sbin:/Users/jesse/.orbstack/bin`
  — it omits Homebrew's `/opt/homebrew/bin`, where `tmux` lives.

## Architecture: the `fleet-snapshot` gateway verb (shared foundation)

Both self-healing (roster discovery) and the mesh-state UI need the same missing
capability: the Mac must read a *remote* host's roster and edge-health. We add **one
new read-only SSH gateway command** that serves both.

- **Name:** `fleet-snapshot` (read-only; no state change).
- **Invocation & auth:** identical model to existing gateway verbs — the caller
  connects over SSH and runs `clipfan ssh-gateway --authorized-peer <localID>
  --authorized-key-id <id> --direct-command fleet-snapshot`. The gateway validates
  the peer via the existing `validateSSHGatewaySyncPeer` path, reads the local
  daemon's data over loopback, and writes a JSON document to stdout.
- **Returns** (single JSON document):
  - **Identity:** this host's `origin`, `version`, sync public key id, and SSH host
    key fingerprint.
  - **Roster:** the host's configured `ssh.peers[]` — for each: peer id, ssh
    host/port/user, platform if known, `migration_state`, and proof key ids. This is
    what roster discovery needs to learn endpoints and find missing edges.
  - **Edge health:** the host's runtime `Snapshot()` per peer — `ssh_status`,
    `ssh_active`, `last_recv_ts`, `ssh_last_ack_ts`, `ssh_last_connect_ts`,
    `ssh_last_error`. This is what the UI renders.
- **Bounds:** the response is size-bounded like other stream frames; it is read-only
  and never mutates config or transport state.

This verb is the dependency that workstreams **D** (discovery) and **E** (UI) build
on. It must land before they can complete.

## Workstream A — Documentation

**Problem.** The README leads with build-from-source (developer flow), buries daily
usage and troubleshooting, and carries a ticket id. Development docs and historical
specs live under `docs/` but leak ticket ids.

**Design.**

1. **Rewrite `README.md` user-first**, in this order: what clipfan is → how it works
   (brief) → install (binary/app first; build-from-source linked out) → getting
   started (set up this Mac, add a host) → daily use (copy/paste across hosts, tmux,
   clipboard history) → configuration → security summary → troubleshooting →
   pointers to `docs/`. Move the current build/release/CI material out.
2. **Reorganize `docs/`:** add `docs/development/` for build-from-source and
   developer setup (extracted from the README). Keep `ARCHITECTURE.md`, `ROADMAP.md`,
   `RELEASING.md`, and `ci/` where they are. Add `docs/TROUBLESHOOTING.md` covering
   the two most common failures (daemon not running; peers not syncing / mesh health).
3. **Scrub ticket ids in place** (Jesse's choice: keep `docs/superpowers/` as dev
   history, strip the ids). Known occurrences to remove: `README.md:10`,
   `docs/PLAN.md:8`, `docs/superpowers/plans/2026-05-28-clipboard-history.md:1938`,
   `docs/superpowers/specs/2026-05-28-clipboard-history-design.md:5`,
   `docs/superpowers/specs/2026-05-29-clip-id-recirculation-design.md` (lines 16, 76,
   130), `docs/superpowers/specs/2026-05-29-mac-app-ux-redesign-design.md:5`. Remove
   "Tracks/Ticket" headers; rewrite inline references to describe the behavior, not
   the ticket.

**Acceptance.**
- `grep -rnE 'PRI-[0-9]+|Tracks |Ticket:' README.md docs/` returns nothing.
- README's first install instruction is the user path (app/binary), not `go build`.
- A new contributor can find build-from-source under `docs/development/`.
- Spot-checked commands in the README match the current CLI surface.

## Workstream B — Bug fix: Mac tmux paste (PATH root cause)

**Root cause (confirmed by live reproduction).** On macOS the daemon is launched
with a `PATH` that omits Homebrew's bin dir, so `tmux.LoadBufferAll`'s
`exec.Command("tmux", …)` fails to find the binary. The OS-clipboard write still
succeeds (`pbpaste` works — no PATH needed), so received clips reach the Mac
clipboard but never the tmux paste buffer. Linux peers are unaffected (`tmux` is in
`/usr/bin`, which is on the daemon PATH). Reproduction: copying on Linux
`magic-kingdom` updated this Mac's `pbpaste` but not its tmux buffer; `tmux` was
absent from the daemon's PATH; a full-path `tmux load-buffer` on the same socket
worked.

**Design.** Resolve `tmux` robustly instead of trusting the inherited PATH, in
`internal/tmux/tmux.go`:

- Add a `tmuxBinary()` resolver: prefer `exec.LookPath("tmux")`, then fall back to a
  list of absolute candidates — `/opt/homebrew/bin/tmux`, `/usr/local/bin/tmux`,
  `/usr/bin/tmux`, `/bin/tmux` — returning the first that exists and is executable.
  `LoadBufferAll` uses the resolved path. Structure the resolver so the candidate
  list and the lookup are injectable, so tests can point candidates at a temp dir
  without depending on the host's real tmux install.
- **Defense in depth:** also have `dist/install.sh` bake a complete `PATH` into the
  launchd plist (include `/opt/homebrew/bin` and `/usr/local/bin`) so other
  PATH-dependent calls are covered. The resolver is the primary fix because it works
  regardless of how the daemon is launched (launchd vs. spawned by the app).

**Acceptance.**
- New unit test: the resolver finds `tmux` given a PATH that lacks it but with a
  known absolute candidate present (table-driven, using a temp dir as a candidate).
- Live verification on the real fleet: copy on one host → the tmux paste buffer
  updates on a **macOS** peer running tmux (not just `pbpaste`).
- No behavior change on Linux peers (regression check).

## Workstream C — Bug fix: outbound indicator under SSH ("↑ never")

**Root cause (confirmed).** The Fleet "↑" reads `last_push_ts`, set only by
`recordPush`, which is called only inside `fanout` — and `fanout` no-ops under SSH
transport (`peerHTTPDisabled`). SSH sends record `ssh_last_ack_ts` /
`ssh_last_connect_ts` in separate fields the indicator never reads. So under SSH the
outbound indicator is dead even though delivery works.

**Design.** Make the outbound indicator transport-aware:

- In `FleetRow.swift`, for SSH peers, source the "↑" from the SSH timestamps
  (`ssh_last_ack_ts`, falling back to `ssh_last_connect_ts`) rather than
  `last_push_ts`. Keep the HTTP path reading `last_push_ts`. Render the row's up/down
  block when *either* a transport-appropriate send time or a receive time exists
  (today it requires both, so SSH peers show neither arrow).
- No daemon change is required for correctness — the SSH ack/connect timestamps are
  already exposed in the peers payload — but verify they are populated end-to-end and
  surfaced through `Models.swift`/`DaemonClient`.

**Acceptance.**
- Swift test: given a peer with SSH ack/connect timestamps and no `last_push_ts`, the
  row model yields a non-"never" outbound time.
- Live: with SSH transport active, the Fleet "↑" advances after a copy.

## Workstream D — Self-healing mesh provisioning

**Problem.** Provisioning meshes only the hosts in one operation, producing a star.
We want any provisioning op to converge the whole fleet to a full mesh.

**Design.**

1. **Roster discovery (decentralized).** Starting from this Mac's configured peers,
   read each reachable host's roster via `fleet-snapshot` and take the transitive
   closure to obtain the full host set and each host's SSH endpoint and platform. In a
   connected fleet this reaches everyone (e.g. `m4`'s snapshot reveals
   `flower-garden` and `magic-kingdom`). No central authority; healing can run from
   any Mac.
2. **Heal = provision the full roster.** Build host specs (the existing
   `addPeerDirectMeshSpec` form) for **every** discovered host and invoke the existing
   `ssh-provision-direct` with all `--host` specs. The engine provisions every pair
   idempotently — adding missing edges and re-verifying existing ones (two-phase,
   revision-locked apply). Hosts the Mac is not an endpoint of are configured the same
   way the current add-peer flow reaches remote hosts.
3. **Add + re-verify only.** Unreachable hosts are retried/left in place; nothing is
   pruned automatically. Removal remains the explicit existing action.
4. **Triggers.** Run heal after: (a) the add-peer install, (b) the wizard's add-host
   step, and (c) an explicit **"Repair mesh"** action (added to Settings → Fleet
   and/or the menu). Heal is safe to re-run (idempotent), so it can also run on
   demand.

**Bootstrapping note.** Discovery uses `fleet-snapshot` over the sync-key gateway for
already-adjacent hosts; provisioning a *new* edge uses the same host-reach mechanism
the current add-peer flow uses (the implementer confirms the exact path in
`internal/sshprovision/regular_ssh_driver.go`). After a successful heal, this Mac is
adjacent to every host, so subsequent `fleet-snapshot` reads cover the whole fleet
directly.

**Acceptance.**
- Go test: given a 3+ host roster with a missing edge, the heal planner targets the
  missing pair and the idempotent apply leaves already-ready edges untouched (extends
  existing `pair_plan`/`config_applicator` tests).
- Live: after running heal on the real fleet, `jesse-paradise-park`'s config lists
  **all** other hosts as peers (not just `m4`), and a copy on `jesse-paradise-park`
  reaches `magic-kingdom` **without** depending on `m4` relaying (verify by checking
  direct edge health).

## Workstream E — Full mesh-state visibility (macOS)

**Problem.** The Fleet view shows only this Mac's peers. We want the whole mesh.

**Design.**

1. **Gather.** The Mac reads `fleet-snapshot` from every host in the discovered
   roster and assembles a fleet-wide model: the set of hosts, and for each ordered
   pair, the edge health as reported by the source host.
2. **Render — per-host rows with edge detail.** Each host is one row with an aggregate
   health dot (worst of its edges) and a short summary (e.g. "5 peers · live", or
   "3/4 edges · 1 down"). Expanding/hovering a row shows that host's edges with their
   individual health, e.g.:

   ```
   FLEET — 4 hosts · mesh 11/12 edges healthy
     ● jesse-paradise-park  (you)   running
     ● m4                    5 peers · live
     ● magic-kingdom         4 peers · live
     ⚠ flower-garden         3/4 edges · 1 down
          └ ● m4   ● magic-kingdom   ○ jesse-paradise-park
   ```

3. **Reuse** existing `FleetRow`/`HealthDot` idioms and the health mapping. Refresh on
   Fleet-view open and on an explicit refresh; this is read-only and bounded.

**Acceptance.**
- Swift tests for the fleet-wide model: aggregate health = worst edge; edge counts
  correct; a down edge surfaces on both endpoints' rows where data exists.
- Live: the Fleet view lists every host (not just `m4`) and flags a deliberately
  broken edge.

## Workstream F — Onboarding wizard

**Problem.** First-run only installs the local daemon; it never helps the user add a
host. (`WelcomeView` is a single screen with `SetupState` idle→installing→success/
failed.)

**Design.** Replace the single screen with a multi-step wizard in the existing
`WelcomeWindow` (the managed `NSWindow`; keep that pattern):

1. **Welcome** — what clipfan does, one Continue.
2. **Set up this Mac** — the current local install flow (reuse `BootstrapController`
   states), advancing on success.
3. **Add your first host** — collect SSH host/user (reuse `AddPeerSheet`'s host fields
   and provisioning), then run a **mesh heal** so the fleet is fully connected. Skippable
   ("I'll do this later").
4. **You're all set** — the existing tips (⇧⌘V, paste flow) plus a one-line fleet
   summary.

A step indicator (`●─○─○─○`) shows progress. The wizard is **re-runnable** from the
menu ("Set up clipfan…" / "Add a host…"). Model the step machine as pure, testable
state (mirroring how `SetupState` is unit-tested) so logic is covered without driving
SwiftUI.

**Acceptance.**
- Swift tests for the step state machine (advance on local-setup success; skip path;
  completion).
- Manual: a fresh run walks Welcome → local setup → add host (heals mesh) → done; the
  wizard reopens from the menu.

## Workstream G — Add-peer success state & dismiss

**Problem.** On success the sheet sets a progress string then silently
`Task.sleep`-then-`dismiss()`s after 1s, with no success UI and only "Cancel" + the
install button.

**Design.** Add an explicit success state to `AddPeerSheet`:

- Track `installSuccess`; on success show a success view (checkmark + "Added
  <host>"), and a brief note that the mesh was healed.
- Replace the silent auto-dismiss with explicit buttons: **Done** (dismiss) and **Add
  another** (reset the form to add another host). Remove the bare `Task.sleep`
  dismiss. (`UpdatePeerSheet` is the sibling pattern for success-then-dismiss.)

**Acceptance.**
- Swift test: after a successful install the model exposes the success state and the
  Done/Add-another affordances (no timed auto-dismiss).
- Manual: install → success screen → Done closes; Add another resets.

## Workstream H — About screen

**Problem.** There is no About screen.

**Design.**

- Add a `Window("about", id: "about")` scene in `ClipfanApp.swift` hosting a new
  `AboutView`, opened via the captured `openWindow` (the existing `WindowOpener`
  bridge), wired to an "About clipfan…" item in `StatusMenuView`.
- `AboutView` shows: app icon, name, app version
  (`CFBundleShortVersionString`), daemon version (`daemon.version`), a one-line
  description, and links (project/docs). Reuse the app's visual idiom; the standard
  AppKit about panel is the fallback if the custom window proves fiddly.

**Acceptance.**
- The menu opens an About window showing app and daemon versions.
- Version strings come from the real sources (no hard-coded literals).

## Testing strategy

- **Go:** unit tests for the tmux resolver (B) and the mesh heal planner (D, extending
  `pair_plan`/`config_applicator` tests); a test for `fleet-snapshot`'s output shape
  and read-only/auth behavior.
- **Swift:** tests for the SSH outbound-time mapping (C), the fleet-wide mesh model
  (E), the wizard step machine (F), and the add-peer success state (G).
- **Live fleet verification** on the real hosts (`jesse-paradise-park`, `m4`,
  `magic-kingdom`, `flower-garden`): tmux paste onto a Mac peer (B), outbound
  indicator advancing under SSH (C), post-heal direct edges (D), Fleet view showing
  all hosts and a broken edge (E). Test output must be clean; intentional error cases
  capture and assert their output.

## Dependencies & parallelization (for the implementation plan)

- **Independent, can start immediately and run in parallel:**
  - **A** (docs) — no code coupling.
  - **B** (tmux PATH fix) — isolated to `internal/tmux` + `dist/install.sh`.
  - **C** (outbound indicator) — isolated to `FleetRow.swift` (+ verify `Models`).
  - **G** (add-peer success state) — isolated to `AddPeerSheet.swift`.
  - **H** (About screen) — additive new view + scene + menu item.
- **Foundation:** the **`fleet-snapshot` gateway verb** (Architecture section). Blocks
  D and E.
- **Dependent:**
  - **D** (mesh heal) depends on `fleet-snapshot` (discovery).
  - **E** (mesh UI) depends on `fleet-snapshot` (gather) and shares model concepts with
    D.
  - **F** (wizard) depends on **D** (its add-host step triggers a heal) and reuses
    `AddPeerSheet` host fields from **G**.

A workable order: land the independents (A/B/C/G/H) in parallel; land `fleet-snapshot`;
then D and E in parallel; then F. The Swift UI changes (C/E/F/G/H) touch overlapping
files (`AddPeerSheet`, `FleetRow`, `StatusMenuView`, `ClipfanApp`), so they should be
sequenced or worktree-isolated to avoid clobbering.

## Risks & open questions

- **Regular-SSH reachability for healing.** Healing a new edge requires the host-reach
  mechanism the current add-peer flow uses. If a host is reachable for clipboard sync
  but not for that provisioning path, heal can't create its missing edges. The
  implementer verifies the exact reach path (`regular_ssh_driver.go`) and surfaces
  per-host failures in the UI rather than failing the whole heal.
- **Key rotation.** If a host's sync key rotates, previously-provisioned proofs on its
  peers go stale. Heal's re-verify should detect and re-stage, but this path needs an
  explicit test; out of scope to fully solve here beyond not regressing.
- **Partitioned discovery.** If the reachable set doesn't transitively cover the whole
  fleet, discovery sees only a subset. Acceptable: heal meshes what it can see and the
  UI shows what it can read; full coverage returns once partitions reconnect.
- **UI refresh cost.** Gathering `fleet-snapshot` from every host on each refresh is
  N round-trips; keep it on-demand/open-triggered and bounded, not a tight poll.
- **Wizard scope.** Keep the add-host step minimal (one host, then heal); resist
  growing it into a full fleet manager in first-run.
