# Hybrid SSH Config v2 0b Implementation Plan

**Goal:** Implement Milestone 0b only: a minimal config v2 parser and revision-aware scoped writer, with all config v2 persistence guarded by generated `ConfigV2WriteEnabled`.

**Spec:** `docs/superpowers/specs/2026-06-01-hybrid-persistent-ssh-transport-design.md`

**Handoff:** `docs/superpowers/handoffs/2026-06-02-hybrid-ssh-next-slice.md`

---

## Brainstorming Scope Confirmation

Milestone 0b is plumbing, not public enablement. The slice should add a dormant, tested config v2 write primitive without changing the public release posture.

In scope:

- Parse `config_version:2` and expose revision state as `pre_v2`, `missing_revision`, or `versioned`.
- Preserve unknown top-level v2 fields and the future `ssh` object as raw JSON during scoped updates.
- Maintain `config_revision`: missing/pre-v2 first successful v2 write stores revision `1`; versioned writes increment exactly once.
- Reject stale revision expectations with `config_revision_conflict`.
- Keep local writes serialized by an adjacent lock and persisted through temp-file plus atomic rename semantics.
- Guard the public config v2 persistence entry point with generated `releaseflags.ConfigV2WriteEnabled`.
- Fail closed with `config_v2_writes_disabled` while the generated gate is false.
- Add tests proving existing app/daemon/updater-facing paths cannot persist `config_version:2` in this slice.

Success criteria:

- Existing v1 config files keep their current file shape unless an existing v1
  field is intentionally changed; v1 saves do not gain `config_version`,
  `config_revision`, or an empty future `ssh` object.
- With generated gates false, no production Go or Swift path can write a file
  that still contains `config_version:2`; attempts against v2 input fail with
  `config_v2_writes_disabled` and leave the on-disk bytes unchanged.
- With an enabled test gate, scoped v2 writes preserve stored unknown JSON values
  semantically. Formatting and field order are not part of the contract, but raw
  unknown values must decode to the same JSON values before and after the write.
- Every successful scoped v2 write increments `config_revision` exactly once.
- Stale or mismatched revision expectations return `config_revision_conflict`
  and do not write.
- Public release manifests and generated release-gate artifacts remain unchanged
  and false.

Out of scope:

- Do not flip committed public manifests.
- Do not enable any live app, daemon, installer, updater, or provisioning path to write `config_version:2`.
- Do not add remote `shared_key` writes or new Add Peer success UI.
- Do not remove peer HTTP runtime paths.
- Do not add environment-variable overrides for release gates.
- Do not implement listener cutover, safe mode, HKDF request auth, host-key pinning, SSH peer schema, or remote scoped config commands.

## Architecture

Add the v2 implementation to `internal/config` so existing config ownership and file-permission behavior stays centralized.

Generated gate ownership is already established by Milestone 0a:

- Go generated constant:
  `internal/releaseflags/ssh_transport_gates.go`
  exposes `releaseflags.ConfigV2WriteEnabled`.
- Swift generated constant:
  `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift`
  exposes `GeneratedSSHTransportGates.configV2WriteEnabled`.
- This slice consumes those existing generated constants. It must not edit
  `release/ssh-transport-gates.json`,
  `release/ssh-runtime-gates.json`, or generated gate output except if a
  reviewer explicitly finds those artifacts stale. The expected state is that
  all committed public gate values remain false.

Use two layers:

- A parser/envelope layer that reads typed fields already known to Clipfan and stores unknown raw fields, including future `ssh`, without parsing them.
- A scoped writer layer that rereads under `config.json.lock`, validates the caller's expected revision representation, applies a narrow mutation to typed fields, increments revision, and writes via temp file plus rename.

The exported production entry point must consume `releaseflags.ConfigV2WriteEnabled`. Package-internal tests may exercise the same writer with the gate enabled through an unexported helper, but no app/daemon/updater code path may bypass the generated gate.

Existing v1 writes remain valid for v1 configs. If an existing live path loads a v2 config and tries to save it while `ConfigV2WriteEnabled=false`, it must return `config_v2_writes_disabled` and leave the file unchanged. In particular, the daemon `/v1/config` max-history path and the macOS installer local static-peer update must not reserialize and persist a v2 document.

Revision contract:

- Missing `config_version` means `revision_state:"pre_v2"` and no external
  revision.
- `config_version:2` with missing or JSON `null` `config_revision` means
  `revision_state:"missing_revision"` and no external revision. This is accepted
  as migration input only; a successful scoped write must store
  `config_revision:1`.
- `config_version:2` with a JSON integer `config_revision >= 1` means
  `revision_state:"versioned"` and external revision `N`.
- `config_revision` values of `0`, negative numbers, non-integers, strings,
  booleans, arrays, or objects are invalid persisted revisions and fail parsing
  with `invalid_config_revision`.
- Positive revision integers larger than `uint64` fail parsing with
  `invalid_config_revision`.
- Unsupported non-null `config_version` values fail parsing with
  `unsupported_config_version`.
- Caller expectations are explicit pairs: `pre_v2` plus no revision,
  `missing_revision` plus no revision, or `versioned` plus exact revision `N`.
  `expected_config_revision:0`, nil revision for `versioned`, non-nil revision
  for non-versioned states, state mismatches, and stale numeric revisions all
  return `config_revision_conflict`.

Serialization contract:

- v1 save paths omit v2 metadata by default. If the existing `Config` struct gets
  load-time v2 detection fields, they must be pointers or otherwise omitted when
  nil/zero so normal v1 `Save` output stays compatible.
- Public `Save` rejects a loaded or constructed v2 config while
  `releaseflags.ConfigV2WriteEnabled` is false. It must not silently downgrade a
  v2 document to v1 by dropping `config_version`.
- The scoped v2 writer owns `config_version` and `config_revision`; callers only
  mutate allowed typed config fields.

Lock and path-safety contract:

- The v2 scoped writer requires the config directory to be an actual directory,
  not a symlink.
- The config file path must be a regular file when it exists. A symlinked config
  file is rejected for v2 scoped writes and left unchanged.
- Missing config files are not created by the scoped v2 writer in Milestone 0b;
  they return the underlying not-found error. First-run creation remains the
  existing v1 `Load`/`Save` path until the later listener/config cutover.
- The lock file is adjacent to the config file at `config.json.lock`, opened with
  owner-only permissions, and held exclusively while reading, validating,
  mutating, writing, and renaming.
- The writer rereads the current config after acquiring the lock; revision
  validation uses that locked reread, not any caller-cached document.
- Temporary files are created in the same directory with owner-only mode,
  exclusive create, and names matching exactly
  `.config-v2-<basename>.tmp.<pid>.<counter>`. Cleanup is limited to stale files
  in the same directory whose names start with `.config-v2-<basename>.tmp.`; no
  other config-directory files may be removed.
- The writer fsyncs the temp file where supported, renames it atomically over the
  config file, and fsyncs the parent directory where supported. If platform sync
  returns an unsupported-operation error, the writer continues only after the
  rename semantics are still atomic for the local filesystem.
- Rewritten config files are private mode `0600`; the directory remains `0700`.

Error propagation contract:

- The daemon `/v1/config` path must return a failed signed response containing
  `config_v2_writes_disabled` when the loaded config is v2 and the generated gate
  is false. It must not update in-memory state as if the write committed.
- `Installer.addPeerToLocalConfig` must throw a config I/O error containing
  `config_v2_writes_disabled` when the loaded config is v2 and the generated
  Swift gate is false. It must leave the file bytes unchanged.
- Disabled-gate errors are stable and diagnosable, but logs and API responses
  must not include raw config contents or `shared_key`.

Current config write-path inventory:

| Path | Current write behavior | 0b action |
|------|------------------------|-----------|
| `internal/config.Save` | Writes full local config used by first run and daemon settings | Guard loaded/constructed v2 with generated `releaseflags.ConfigV2WriteEnabled`; v1 behavior unchanged |
| `config.Load` first-run creation | Creates v1 config with `shared_key` and defaults | Leave v1; test it does not write v2 metadata |
| `internal/daemon.setMaxHistory` via signed `POST /v1/config` | Loads config, changes `max_history`, calls `config.Save` | Add daemon/API test that v2 input fails closed and does not report success |
| `apps/mac/Clipfan/Installer.addPeerToLocalConfig` | Reads JSON dictionary, mutates `static_peers`, atomically writes local config | Guard v2 input with generated Swift gate; test byte-for-byte unchanged failure |
| `apps/mac/Clipfan/Installer.install` remote staged config | Creates a new remote legacy v1 config during disabled public Add Peer flow | Do not change in 0b; Add Peer provisioning remains disabled by generated gates, and the staged JSON contains no `config_version:2` |
| `apps/mac/Clipfan/Installer.update` and `remoteUpdateCommand` | Installs binary payload and explicitly does not stage `config.json` | No config persistence path; existing Swift test already asserts update command excludes `config.json` |
| `dist/install.sh` | Installs staged files and documents config repair | No new v2 write in 0b; untouched unless tests reveal an existing issue |
| `internal/store` callers of `config.Load` | Read config for state/history limits | Read-only or indirect through `config.Save` inventory above |

If implementation finds another config persistence call site during the audit,
add it to this table and either guard it or document why it cannot write v2.

## File Structure

- Modify `internal/config/config.go`
  - Add `config_version` and `config_revision` fields to `Config` for load-time detection.
  - Make `Save` reject v2 persistence while the generated gate is false.
  - Reuse/improve atomic write helper for fsync and parent-directory sync where supported.
- Add `internal/config/v2.go`
  - Revision state and expectation types.
  - V2 parser/envelope with raw unknown-field preservation.
  - Gate-controlled scoped writer and unexported test helper.
  - Stable sentinel errors/codes: `config_v2_writes_disabled`, `config_revision_conflict`.
- Add `internal/config/v2_test.go`
  - Parser/revision-state tests.
  - Unknown field and future `ssh` preservation tests.
  - Revision increment and stale revision tests.
  - Lock/temp/rename/mode tests.
  - Disabled-gate tests.
- Modify `internal/daemon/config_test.go`
  - Prove daemon max-history update fails closed on existing v2 configs while the gate is false and leaves the file unchanged.
- Modify `apps/mac/Clipfan/Sources/Clipfan/Installer.swift`
  - Add generated-gate protection to local config mutation when the loaded config is v2.
- Modify `apps/mac/Clipfan/Tests/ClipfanTests/InstallerFlagTests.swift`
  - Prove `Installer.addPeerToLocalConfig` refuses to persist a v2 config while generated gates are false.

## TDD Tasks

- [ ] Review unit 1: add failing Go tests for parser and core writer behavior.
- [ ] Audit config persistence call sites with `rg` before implementation, update the inventory above if any additional writers exist, and decide a test or documented non-writer reason for each path.
- [ ] Add failing Go tests for parsing `config_version:2`, revision-state classification, invalid revisions, unsupported versions, and missing-revision external representation.
- [ ] Add failing Go tests proving v1 load/save output does not gain v2 metadata.
- [ ] Add failing Go tests for scoped writer success with an enabled test gate: revision `N` to `N+1`, pre-v2/missing revision to `1`, unknown top-level field preservation, and future `ssh` raw preservation.
- [ ] Add failing Go tests for stale revision rejection, invalid expectation pairs, and disabled generated-gate failure returning `config_v2_writes_disabled` with byte-for-byte no file change.
- [ ] Add failing Go tests for lock/temp/rename semantics: private mode retained, temp files from prior attempts cleaned on later success, symlinked config directories rejected, and symlinked config files rejected for v2 scoped writes.
- [ ] Implement parser and revision model in `internal/config`.
- [ ] Implement gated scoped writer with lock, locked reread, revision validation, raw-field merge, and atomic write.
- [ ] Review unit 2: add live-path guards using the new primitive.
- [ ] Add failing daemon test proving `/v1/config` max-history returns `config_v2_writes_disabled`, does not mutate in-memory state as committed, and cannot persist v2 while generated gates are false.
- [ ] Add failing Swift test proving `Installer.addPeerToLocalConfig` throws `config_v2_writes_disabled` and cannot persist v2 while `GeneratedSSHTransportGates.configV2WriteEnabled=false`.
- [ ] Guard existing Go `Save` and daemon path against v2 persistence while the generated gate is false.
- [ ] Guard Swift local config mutation against v2 persistence while the generated gate is false.
- [ ] Confirm public manifests and generated release-gate files remain unchanged and false.

## Verification

Required:

```bash
go test ./...
cd apps/mac/Clipfan && swift test
bash scripts/test-ssh-release-gates.sh
git diff --exit-code -- release/ssh-transport-gates.json release/ssh-runtime-gates.json internal/releaseflags/ssh_transport_gates.go internal/releaseflags/ssh_runtime_gates.go apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHRuntimeGates.swift
```

Run `bash dist/test-build-all-helper.sh` only if release payload, install/update, or dist artifacts are touched. This plan should not touch those artifacts.
