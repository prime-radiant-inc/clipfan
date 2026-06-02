# Hybrid SSH Transport Handoff

This note is the short handoff for a developer starting the next slice after the
hybrid SSH release-gate foundation.

## Current Repository State

- Handoff branch: `codex/clipfan-hybrid-ssh-handoff`
- Base branch: local `main`
- Local `main` includes the hybrid SSH design, gate-foundation plan, and
  implementation through commit `4ebeb1e` (`feat: wire Swift SSH transport gates`).
- Local `main` was ahead of `origin/main` when this handoff branch was created;
  the implementation had not been pushed.
- The only known unrelated dirty file after the merge is `.gitignore`, which adds
  a local roborev ignore entry.

## In-Tree Source Documents

- Full design:
  `docs/superpowers/specs/2026-06-01-hybrid-persistent-ssh-transport-design.md`
- Completed implementation plan for the first slice:
  `docs/superpowers/plans/2026-06-01-hybrid-ssh-transport-gate-foundation.md`

The full design is intentionally comprehensive. Use this handoff as the entry
point, then open the full design for exact acceptance criteria.

## Completed Slice

Milestones `0a1-0a5` are implemented:

- strict release gate manifests:
  - `release/ssh-transport-gates.json`
  - `release/ssh-runtime-gates.json`
- strict Go validation in `internal/releaseflags`
- generated Go and Swift gate constants
- release-gate regeneration script and release workflow check
- Swift `SSHTransportGatePolicy`
- Swift consumer wiring that:
  - disables public Add Peer provisioning while public gates are false
  - keeps regular user SSH update available
  - skips/clears peer HTTP version checks when peer HTTP runtime is disabled
  - avoids treating disabled peer HTTP version state as healthy fleet sync

## Verification Already Run

These passed on local `main` after the fast-forward merge:

```bash
go test ./...
cd apps/mac/Clipfan && swift test
bash scripts/test-ssh-release-gates.sh
bash dist/test-build-all-helper.sh
```

## Next Slice

The next implementation slice should be Milestone `0b`: config v2 revision parser
and gated scoped-writer plumbing.

Required behavior from the design:

- parse `config_version:2`
- preserve unknown v2 fields on scoped updates
- maintain and validate config revision
- reject stale revisions
- keep atomic lock/rename semantics
- guard the config v2 persistence entry point with generated
  `ConfigV2WriteEnabled`
- while `ConfigV2WriteEnabled=false`, fail closed with
  `config_v2_writes_disabled`
- do not let any app, daemon, or updater path actually persist
  `config_version:2` in this slice
- public/release builds must not write `config_version:2` until the later
  `17d3a` local listener/config cutover

## Suggested Next Workflow

1. Create a new branch from local `main`.
2. Write a new plan under `docs/superpowers/plans/` for Milestone `0b`.
3. Get design/plan review before implementation.
4. Implement with TDD:
   - start with config v2 parser/revision tests
   - add gated scoped-writer tests proving disabled writes fail closed
   - add preservation tests for unknown fields
   - add stale revision and atomic write tests
5. Run at least:

```bash
go test ./...
cd apps/mac/Clipfan && swift test
bash scripts/test-ssh-release-gates.sh
```

Run `bash dist/test-build-all-helper.sh` if the slice touches release payload,
install/update, or generated distribution artifacts.

## Release-Gate Rules To Preserve

- Do not flip committed public manifest values in Milestone `0b`.
- Do not add an environment override for generated release gates.
- Do not write remote `shared_key`.
- Do not add public-green Add Peer UI.
- Do not remove public peer HTTP runtime paths in this slice; those are owned by
  later no-peer-HTTP and replacement milestones.
- Keep regular user SSH install/update separate from command-locked sync keys.
