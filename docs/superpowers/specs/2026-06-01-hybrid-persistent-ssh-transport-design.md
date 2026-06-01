# Hybrid Persistent SSH Transport Design

## Purpose

clipfan should not require a daemon HTTP listener reachable from the public
internet. The fleet transport will move to OpenSSH with command-locked keys for
runtime sync and runtime-readiness version checks, while install, update, and
provisioning continue to use normal user SSH credentials.

The result is a multi-master clipboard overlay: any connected host can originate
a sync-eligible visible clip, any active SSH channel can carry clips in both
directions, and connected components converge on the latest visible clipboard
state within the limits of available SSH connectivity. Concealed clips are
local-only and intentionally excluded from fleet convergence.

## Current System

Today every daemon runs an HTTP server. Local app and CLI calls use
`127.0.0.1:7853`, but peer sync and version checks use `http://<peer>:7853`.
The default config is `listen: ":7853"`, and remote install writes that value
into the peer config. Requests and responses are signed with `shared_key`, and
clipboard payloads are encrypted inside the HTTP body, but the daemon port still
has to be network-reachable for peer sync.

The Mac app already uses regular SSH/SCP for install and update. That path
probes `uname`, uploads the release payload, runs `install.sh`, and reads the
installed daemon version.

## Goals

- No clipfan daemon HTTP endpoint is reachable off-host in the supported
  configuration.
- Runtime sync and runtime-readiness version checks use OpenSSH with
  command-locked, passwordless clipfan keys.
- Install and update use regular user SSH credentials, not command-locked
  clipfan sync keys.
- Provisioning, install/update, legacy static-peer update, and pre-provisioning
  binary version checks may use regular SSH after host-key confirmation because
  those operations require normal user authority and are not runtime sync.
- The daemon owns fleet sync. The Mac app configures, monitors, installs, and
  updates; it is not the sync engine.
- Sync is multi-master. Any host with an active SSH connection to a peer can
  send and receive the latest sync-eligible visible clip over that connection.
- Persistent SSH streams are the normal path. On-demand SSH sends are the
  fallback when no writable persistent stream exists and the peer has a complete
  outbound on-demand path. "Not configured" means persistent streaming is not
  enabled or not currently writable for that peer; it does not make accept-only
  peers or peers with incomplete outbound SSH material eligible for fallback.
- The protocol converges on latest state only. Clipboard history remains
  single-host UI behavior and is not distributed.
- Existing clip identity, recipient binding, timestamp bounds, seen-set dedup,
  current-clip echo suppression, and concealed-item handling remain the
  correctness model.
- Failure details are visible and copyable from the Mac app.
- Host-global sync-key rotation for command-locked clipfan keys is part of the
  initial public SSH sync enablement. It is required so a host can replace a
  compromised or stale sync key without rotating the fleet `shared_key`.

## Non-Goals

- No distributed clipboard history.
- No shell access through clipfan-managed keys.
- No public TLS reverse proxy for the daemon.
- No compatibility mode that keeps peer HTTP sync enabled by default.
- No root daemon or privileged helper.
- No Windows peer support in this transport release. The design assumes
  Unix-like peers with OpenSSH, `authorized_keys`, POSIX file modes, and user
  home directories.
- No automated `shared_key` rotation workflow in this release. Host-global
  sync-key rotation is in scope; fleet credential rotation is not.
- No reciprocal outbound provisioning UI or reverse-dial setup in this release.
  A configured persistent SSH stream is bidirectional once established; adding a
  separate remote-to-local outbound path is a future topology feature.
- No attempt to protect clipfan state from the same Unix user, root, or a host
  with the fleet `shared_key`.

## Security Model

### Listener Boundary

The daemon's HTTP server becomes loopback-only:

```json
{
  "listen": "127.0.0.1:7853"
}
```

The listener-boundary behavior in this section is active in internal/test
profiles before Milestone 17d3a and in public/release builds only when the 17d3a
local listener/config cutover enables it. In those profiles, `install.sh` and
first-run config generation write loopback listen addresses. Startup migration
treats the exact legacy listen values `":7853"`, `"0.0.0.0:7853"`, and
`"[::]:7853"` as generated defaults and normalizes them to
`"127.0.0.1:7853"` before binding. There is no attempt to distinguish a
hand-written `":7853"` from the generated default; those exact values are the
legacy-default compatibility rule. Any other non-loopback listen uses safe mode.
Wildcard listens on non-default ports, such as `":9000"`, `"0.0.0.0:9000"`,
and `"[::]:9000"`, are explicit unsafe listeners, not generated defaults.
Public/release builds before Milestone 17d3a keep the legacy generated
listener/config defaults and do not persist generated-listen migration. That is
the intentional compatibility window before public peer HTTP is disabled.

This release supports IPv4 loopback binding only. The persisted local bind is
`127.0.0.1:<port>`, not `[::1]:<port>`, even when the legacy input was
`[::]:<port>`. IPv6 loopback support can be added later as an explicit dual-bind
feature with separate tests; it is not part of this transport cutover.

Daemon-start listener migration write behavior is normative:

| Loaded config | Runtime bind | Startup persistence | Revision behavior |
|---------------|--------------|---------------------|-------------------|
| public/release before 17d3a first-run or installer-generated config | legacy generated listen such as `":7853"` | no SSH-transport listener migration write | legacy peer HTTP behavior remains until public cutover; no config v2 write |
| internal/test loopback-enabled or public 17d3a+ first-run or installer-generated SSH config | persisted `127.0.0.1:<port>` | no migration write | starts at the normal v2 initial revision |
| pre-v2 or missing-revision config with accepted non-canonical loopback listen | `127.0.0.1:<port>` in memory before bind | no startup write; app migration later performs the first v2 write | first successful v2 write stores revision 1 |
| config v2 with valid revision and accepted non-canonical loopback listen | `127.0.0.1:<port>` before bind | if `ConfigV2WriteEnabled` is true, owner/mode checks pass, and public-cutover backup requirements below are satisfied, startup atomically persists only listener migration fields | increments revision exactly once; if the gate is false, no write occurs |
| pre-v2 or missing-revision config with an exact generated wildcard listen | `127.0.0.1:<port>` in memory before bind | no startup write; app migration later performs the first v2 write | first successful v2 write stores revision 1 |
| config v2 with valid revision and an exact generated wildcard listen | `127.0.0.1:<port>` before bind | if `ConfigV2WriteEnabled` is true, owner/mode checks pass, and public-cutover backup requirements below are satisfied, startup atomically persists only listener migration fields | increments revision exactly once; if the gate is false, no write occurs |
| any explicit non-loopback listen that is not an exact generated default | safe-mode loopback repair surface only | no automatic rewrite | no revision change until signed or offline listener repair succeeds |

Any daemon-start persistence of listener migration in a public/release profile is
a public cutover config write. Before the daemon may atomically rename the
migrated config, it must either observe the app/updater-created 17d3a cutover
backup/remediation record for the current config revision or create that
timestamped backup and durable redacted remediation-history record itself under
the user-owned state directory. Backup failure means no config write occurs; the
daemon may continue with the in-memory loopback bind when the loaded config is
otherwise safe, but status reports `cutover_backup_required` until the backup
preflight succeeds. Internal/test profiles may exercise startup persistence
without public release packaging, but public 17d3a CI must prove the backup
record exists before the first persisted listener/config cutover write.

Existing loopback-but-not-canonical listener values are accepted and normalized
to `127.0.0.1:<port>` before bind; persistence follows the migration tables.
That includes `localhost:<port>`, any IPv4 `127/8` address such as
`127.0.0.2:<port>`, and `[::1]:<port>`.
The daemon may load an old spelling, but any daemon/app write that includes the
listener must write the canonical `127.0.0.1:<port>` spelling. It never writes a
loopback host spelling that the Mac app would not probe directly.

An explicit custom non-loopback listen is not silently rewritten and is never
bound by a supported SSH-transport daemon. Instead the daemon starts in
`public_listen_requires_confirmation` safe mode. Safe mode derives its bind
address from the configured port: `0.0.0.0:9000`, `[::]:9000`, and
`203.0.113.10:9000` all become `127.0.0.1:9000`. A missing port uses the
config's effective `port` value.
Port parsing order is: parse `listen` host/port when valid, otherwise use
`port` when it is in 1-65535, otherwise use 7853 and report
`invalid_listen_port` in safe-mode status. The status payload includes
`configured_listen`, `effective_repair_listen`, `parse_error`, and
`peer_sync_started:false`.

- it binds the derived loopback address for local app/CLI repair traffic,
- it does not start the SSH session manager or legacy peer sync,
- it reports the unsafe configured listen in peer/status APIs,
- the app must rewrite the persisted config to a loopback listen and restart
  the daemon before peer sync can start.

If the safe-mode loopback bind fails, daemon startup fails closed. Peer HTTP
sync is removed from the supported transport path rather than hidden behind a
silent fallback.

Safe mode allows only local loopback health/status/config-repair APIs needed by
the Mac app and CLI to display and fix the unsafe listener. It does not start
outbound persistent SSH sessions, does not run on-demand sends, and does not
allow command-locked gateway `version`, `receive`, or `sync-stream`
reservations. A forced-command gateway invoked while local daemon status is
`public_listen_requires_confirmation` returns
`{"type":"error","code":"public_listen_requires_confirmation"}` before hello.
Safe mode is evaluated before migration-state exceptions: the
`shared_key_written_unverified` version-only exception and normal
`ssh_keys_ready` version probes never run while safe mode is active.
Regular SSH install/update/repair operations initiated by the app may still run
because they do not depend on the local daemon accepting peer sync.

Safe-mode endpoint policy:

| Endpoint class | Safe-mode behavior |
|----------------|--------------------|
| `GET /v1/health` | allowed, unauthenticated `ok` only |
| local signed version/status endpoint | allowed only for local daemon/app compatibility fields and safe-mode status; no peer version probes |
| signed listener repair read/update | allowed for `listen`, `port`, `previous_listen`, `configured_listen`, `effective_repair_listen`, `parse_error`, `safe_mode`, and `config_revision` only |
| peers/status and SSH logs | allowed read-only through `GET /v1/status`, compatibility `GET /v1/peers`, and `GET /v1/ssh/logs` so the app can show repair state and copy prior failures |
| clipboard copy/paste/history/current APIs | rejected with `public_listen_requires_confirmation` |
| gateway session reservation, current watch, receive, sync-stream | rejected with `public_listen_requires_confirmation` |
| peer update/install over regular SSH | app-side operation allowed; not a daemon endpoint |

Safe-mode exact local endpoints:

- `GET /v1/health`: unauthenticated loopback health only. Response is the
  existing minimal `ok` payload and does not include peer or config details.
- `GET /v1/version`: signed loopback request. Response fields are limited to
  local binary/daemon versions, local daemon protocol version, `config_version`,
  `safe_mode`, and `config_revision`; it never probes peer versions.
- `GET /v1/status`: signed loopback request. In `safe_mode_v1`, response fields
  are limited to local daemon status, `hostname`, `configured_listen`,
  `effective_repair_listen`, `parse_error`, `safe_mode`,
  `safe_mode_schema:"safe_mode_v1"`, `peer_sync_started:false`,
  `config_revision`, legacy/static peer suggestions derived without parsing
  `ssh.peers[]`, and copyable log IDs. It never starts SSH sessions or public
  HTTP probes.
- `GET /v1/peers`: signed loopback compatibility alias for existing app code.
  In safe mode it returns the peer/status slice from `GET /v1/status` plus
  `origin`, `version`, `safe_mode`, and `config_revision`. It never starts peer
  version probes, SSH sessions, public HTTP probes, or current/watch work.
- `GET /v1/ssh/logs?peer=<peer-id>&limit=<bytes>&cursor=<opaque>`: signed
  loopback request. `cursor` is optional and returned only by a prior response.
  `limit` is clamped to the documented log export limit.
  Response fields are only `peer_id`, `safe_mode`, `entries`, `truncated`, and
  `next_cursor` when older entries remain. Entries contain redacted `ts`,
  `source`, `durable`, `log_id`, `phase`, `code`, and `message` fields only.
  `source` is one of `runtime_ring`, `provisioning`, `listener_repair`,
  `remediation`, `ssh_material_cleanup`, or `post_secret_tombstone`. The endpoint
  reads both daemon-owned bounded in-memory runtime rings and daemon-owned
  durable remediation/provisioning records; safe-mode signed repair exposes the
  same endpoint, while `safe_mode_health_only` exposes no log endpoint. It never
  returns raw argv, home-directory paths, private key paths, `shared_key`, HMACs,
  nonces, encrypted bodies, frame payloads, clipboard content, or clip IDs unless
  an explicit developer-log mode separately allows clip IDs outside safe mode.
  Cursors are opaque, peer-scoped, source-scoped, and never encode secrets or raw
  log text. Runtime-ring cursors may expire when the bounded ring overwrites
  their page or the daemon restarts; durable cursors remain valid until the
  bounded durable record store compacts the referenced page, at which point the
  response returns `cursor_expired` plus the newest available page.
  In `safe_mode_v1`, this endpoint is global/listener-only: `peer` must be
  omitted or `peer=local`, sources are limited to `listener_repair` and
  listener/config `remediation`, and requests for a real SSH peer return
  `ssh_peer_logs_unavailable_before_schema` with no entries. Peer-scoped runtime
  rings, provisioning records, SSH-material cleanup records, and post-secret
  tombstones require `safe_mode_ssh_status_v2` after Milestone 3 defines the
  SSH peer schema and log scoping.
- `GET /v1/config/listener`: signed loopback request. Response fields are only
  `listen`, `port`, `previous_listen`, `configured_listen`,
  `effective_repair_listen`, `parse_error`, `safe_mode`, and
  `config_version`, `config_revision`, and `revision_state`. It never returns
  `shared_key`, peer records, SSH paths, known_hosts paths, private key paths,
  or authorized_keys paths.
- `PATCH /v1/config/listener`: signed loopback request with
  `expected_config_revision`, `expected_revision_state`, `listen`, `port`, and
  optional `previous_listen`; all peer, secret, SSH key, and known_hosts fields
  are rejected in safe mode.

All signed safe-mode endpoints require a valid config v2 local request key
derived from `shared_key`. If `shared_key` is missing or invalid, the daemon
must not invent an unauthenticated repair API; the supported repair path is
explicit offline listener repair by the app or CLI.
For pre-v2 configs with an explicit unsafe listener and a valid existing
`shared_key`, the new app/daemon use the same HKDF-derived
`clipfan-v1/request-hmac` signing before persisting `config_version:2`. Raw-key
signatures from old app/CLI builds remain unsupported once the new daemon is
handling safe-mode repair.

Safe-mode schema evolution is explicit. Milestone 1b2 implements only
`safe_mode_v1`, which must not parse or expose `ssh.peers[]`, persisted SSH
migration states, or SSH runtime health. Milestone 3 may extend safe-mode status
to `safe_mode_ssh_status_v2` after SSH peer schema validation exists. Later
runtime milestones may add their own health fields only after defining the
corresponding daemon state and app rendering contract. Implementations must not
ship undocumented safe-mode fields just because later milestones reference them.
Before `safe_mode_ssh_status_v2`, callers that ask for peer-scoped SSH logs get
the stable `ssh_peer_logs_unavailable_before_schema` response rather than an
empty peer log that could be mistaken for successful log lookup.

Listener repair revision outcomes are normative:

| Loaded config shape | Repair status response | Required PATCH expectation | Successful repair write |
|---------------------|------------------------|----------------------------|-------------------------|
| pre-v2 config with no `config_version` | `config_version:null`, `config_revision:null`, `revision_state:"pre_v2"` | `expected_config_revision:null`, `expected_revision_state:"pre_v2"` | writes `config_version:2`, `config_revision:1`, canonical loopback listener fields, and no SSH peer/secret fields |
| config v2 missing `config_revision` | `config_version:2`, `config_revision:null`, `revision_state:"missing_revision"` | `expected_config_revision:null`, `expected_revision_state:"missing_revision"` | writes `config_version:2`, `config_revision:1`, canonical loopback listener fields, and no SSH peer/secret fields |
| config v2 with valid revision `N` | `config_version:2`, `config_revision:N`, `revision_state:"versioned"` | `expected_config_revision:N`, `expected_revision_state:"versioned"` | writes only listener repair fields and increments to `N+1` |

Signed and offline listener repair both take the config lock, re-read the file,
revalidate owner/mode, reclassify the listener, and compare the expectation
against the re-read revision state before writing. If a pre-v2 or
missing-revision config gains a valid revision before repair, the null
expectation fails with `config_revision_conflict` and reports the current
revision state. A null expectation never authorizes rewriting a versioned config.
Signed local requests carry the explicit header
`X-Clipfan-Auth-Version: clipfan-v1/request-hmac`. Signed requests also carry
the existing `X-Clipfan-Ts`, `X-Clipfan-Nonce`, and `X-Clipfan-Sig` headers.
Requests with JSON bodies set `Content-Type: application/json`. Signed
responses carry `X-Clipfan-Auth-Version: clipfan-v1/request-hmac` and
`X-Clipfan-Response-Sig`. The canonical signing input is the same for pre-v2 and
v2 configs once handled by the new daemon; `config_version` is not part of
auth-mode negotiation. The v1 request canonical string is:

```text
<METHOD>
<REQUEST_URI>
<TIMESTAMP>
<NONCE>
auth_version=clipfan-v1/request-hmac
<BODY_BYTES>
```

The `<REQUEST_URI>` value is a canonical request target, not an arbitrary raw
URL string:

- It contains only the path and optional query string, never scheme, host,
  fragment, or whitespace.
- The path starts with `/`. Route literals are lowercase ASCII as documented by
  the endpoint, and dynamic path segments are percent-encoded with RFC 3986
  unreserved characters left unescaped, spaces encoded as `%20`, and uppercase
  hex. A slash, dot segment, empty segment, control character, or decoded `..`
  inside a dynamic segment is rejected before handler dispatch.
- Query parameters are endpoint-declared. Unknown parameters and duplicate keys
  are rejected with `bad_request`. Present parameters are serialized in
  lexicographic key order as `key=value`, using the same percent-encoding rules
  as path segments. Empty values are allowed only where an endpoint declares
  them; absent optional parameters are omitted. The `?` separator is omitted
  when there are no query parameters.
- The actual HTTP request target bytes must equal the canonical URI. If a client
  sends a different-but-equivalent spelling, such as reordered query keys, `+`
  for space, lowercase percent hex, an unescaped reserved character, or an
  unnecessary trailing `?`, the server returns `non_canonical_uri` after local
  auth parsing and before executing the handler.
- Go and Swift clients use the same generated canonical-URI helper. Fixtures
  cover duplicate query keys, query ordering, escaping, path-token rejection, and
  the exact signed bytes for endpoints such as
  `/v1/ssh/logs?limit=50&peer=fsck`.

Response signing uses the same auth version line in the response canonical
string:

```text
response
<REQUEST_NONCE>
auth_version=clipfan-v1/request-hmac
<BODY_BYTES>
```

New clients read the local config, derive the HKDF request key from a valid
existing `shared_key`, send the header, and try only auth version
`clipfan-v1/request-hmac`. Old raw-key clients omit or send the wrong auth
version and receive `auth_version_mismatch` before signature verification when
the header is absent/wrong, or `bad_signature` when the header is right but the
signature was computed with the wrong key or canonical form.

Safe-mode auth matrix:

This matrix applies only after config owner/mode checks and the storage-locality
preflight allow the daemon to bind. After Milestone 17d3a,
`unsupported_runtime_storage` or `storage_check_inconclusive` has higher
precedence than every safe-mode row below: the daemon binds no socket, including
health-only loopback, and recovery uses the app/CLI offline preflight path.

| Mode | Config/auth state | Daemon bind | Available daemon APIs | Repair path |
|------|-------------------|-------------|-----------------------|-------------|
| `safe_mode_signed_repair` | valid `shared_key`, unsafe listener, pre-v2 or v2 config | derived loopback | unauthenticated `GET /v1/health`, signed `GET /v1/version`, signed `GET /v1/status`, signed `GET/PATCH /v1/config/listener`, read-only signed logs/status using HKDF request signing | signed listener repair |
| `safe_mode_health_only` | parseable user-owned config with missing or invalid `shared_key` and unsafe listener | derived loopback if minimal parse succeeds | unauthenticated `GET /v1/health` only | stop user service, wait for daemon-lock release, then explicit offline listener repair by current app/CLI after owner/mode validation and config lock |
| startup failure | unreadable, unparseable, wrong-owner, or wrong-mode config file | no trusted daemon repair API | none beyond process startup error | manual recovery only; app/CLI must not mutate the untrusted file |

`safe_mode_health_only` is exact behavior, not a degraded signed-repair mode. It
starts only the minimal loopback health responder when the config owner/mode and
listener are minimally trustworthy but `shared_key` cannot authenticate local
requests. All signed endpoints, gateway reservations, SSH session starts,
current/watch APIs, peer version probes, clipboard APIs, and config mutation APIs
are unavailable until the current app/CLI performs offline repair or confirmed
local fleet reset under the config lock.
If a health-only daemon is running, offline repair must not edit the config while
that daemon owns the daemon lock. The app first stops the user service, waits for
the daemon lock to release, then takes the adjacent config lock and performs the
offline listener-only repair. If the service cannot be stopped or the daemon lock
does not release before timeout, the app reports `daemon_lock_timeout` with
copyable lock metadata and performs no mutation. If the daemon was launched
manually and no service stop is available, the UI instructs the user to stop that
process and retry; it still does not mutate while the daemon lock is held.

Config validation has two tiers:

- Minimal load/repair validation parses JSON, validates owner/mode, reads
  listener fields, `config_version`, `config_revision`, and enough status
  metadata to bind loopback repair APIs. It must allow safe mode to start even
  when `transport:"ssh"` peers are missing sync key files, known_hosts, proof,
  or a valid `shared_key`. Missing or invalid `shared_key` selects
  `safe_mode_health_only`; it never exposes signed repair/status/log endpoints.
- Strict SSH runtime validation runs only after the listener is safe. It checks
  `shared_key`, directional proof shape, and peer material for all SSH runtime
  actions. It additionally checks local sync key paths and known_hosts for
  outbound `connect:true` sessions, key publication, key rotation, and
  provisioning promotion that uses this host's public key. Accept-only inbound
  gateway reservations require local peer config plus local authorized_keys
  proof; they do not require the local sync private key.

When safe-mode repair, invalid `shared_key`, missing sync key, and config
revision conflicts occur together, precedence is deterministic:

1. Config parse, owner, and mode failures block startup and require manual
   recovery because neither daemon nor app can trust the file enough to mutate it
   automatically.
2. Signed safe-mode listener repair is available after minimal load only when
   `shared_key` is valid enough to authenticate local config v2 requests. When
   `shared_key` is missing or invalid, the daemon exposes only minimal
   unauthenticated loopback health and the app/CLI uses explicit offline
   listener repair only after stopping the user service when needed, proving the
   daemon lock is no longer held, and validating that the config is parseable,
   user-owned, privately moded, and locked.
   Missing sync key material with a valid `shared_key` is reported as a
   strict-runtime warning; outbound SSH sync, key publication, key rotation, and
   future remote-to-local outbound setup remain stopped, while accept-only inbound
   reservations follow the normal local authorized_keys proof rule.
3. Any signed listener repair write with a stale `expected_config_revision`
   fails with `config_revision_conflict` and does not repair the listener.
4. Offline repair can edit only listener fields and `previous_listen`, and only
   after the daemon lock is clear; it never generates sync keys, rewrites
   `shared_key`, edits peer records, or changes known_hosts.

Only one clipfan daemon may own a config/state directory. Startup takes an
exclusive daemon lock before binding loopback. If the lock is held or the
derived loopback port is already occupied by a process that does not answer a
valid signed clipfan status/version ownership check, startup fails closed with
`daemon_already_running` or `daemon_port_conflict`. The daemon never falls back
to a public bind to avoid a port conflict. The app may offer a signed config
repair to choose a different loopback port when the daemon is running in
safe-mode repair. If the daemon failed before binding and no loopback API is
available, or if the app has stopped a health-only daemon and observed daemon-lock
release, the app offers explicit offline config repair: it reads the user-owned
config file directly, takes the same adjacent config lock, verifies owner/mode
and config revision when present, writes only `listen`/`port` repair fields with
atomic rename, preserves private mode, and then starts the daemon.
Offline repair never edits peer records, `shared_key`, SSH keys, known_hosts, or
authorized_keys.
Unauthenticated `GET /v1/health` proves only local daemon liveness. It is never
ownership or config-identity proof; those require signed `/v1/status` or signed
`/v1/version` for the same config/state directory.
When the conflict is another local clipfan daemon owned by the same user, the UI
offers to focus/use the running daemon or stop it. When the conflict is another
process or another Unix user on the same host, the UI offers an offline repair
to choose a different loopback port and restart; it does not attempt to kill the
other process.

Daemon lock contract:

- Lock path is `<state_dir>/daemon.lock`, where `<state_dir>` is the resolved
  clipfan state directory for the target Unix user. The state directory is mode
  `0700`; the lock file is mode `0600` and owned by the same Unix user as the
  config and daemon.
- The daemon opens the lock file and takes a non-blocking exclusive advisory
  lock with the platform flock API on supported Unix platforms before binding
  any socket, opening transport-current state for mutation, starting clipboard
  polling, or starting SSH session management. Windows is not supported by this
  transport release.
- After acquiring the lock, the daemon truncates and writes redacted diagnostic
  JSON containing `pid`, `started_at`, `config_path`, `state_dir`, `listen`,
  `daemon_version`, and `hostname`. This metadata is diagnostic only; the kernel
  advisory lock is authoritative.
- A stale lock file is not an error when no process holds the advisory lock. The
  next daemon acquisition rewrites it. A stale-looking PID while the advisory
  lock is still held remains `daemon_already_running`; the daemon and app do not
  kill that process automatically.
- If the lock is held, the app first tries the signed loopback status/version
  path for the configured effective listener. If that succeeds and identifies a
  same-user clipfan daemon for the same config/state directory, the app uses or
  restarts that daemon through the normal service path. If status cannot prove
  ownership, the app shows a copyable lock diagnostic and offers only explicit
  service stop/retry or offline repair after the user stops the daemon.
- Offline config repair takes the adjacent config lock, not the daemon lock. It
  must refuse to edit while the daemon lock is held unless the user has stopped
  the daemon or the signed safe-mode listener repair API is unavailable and the
  app has proven the advisory daemon lock is no longer held. This keeps config
  repair from racing a running daemon.
- Restart flows stop the user service, wait for daemon-lock release, then start
  the service again. If the lock is not released before timeout, the app reports
  `daemon_lock_timeout` with lock metadata and does not start a second daemon.

Listen migration decisions:

| Input listen | Port field | Effective runtime bind | Persisted by actor/gate | Peer sync | UI state |
|--------------|------------|------------------------|-------------------------|-----------|----------|
| `""` | missing or invalid | `127.0.0.1:7853` | first-run/app v2 write stores `127.0.0.1:7853` | normal if peers provisioned | normal |
| `""` | valid non-default | `127.0.0.1:<port>` | first-run/app v2 write stores `127.0.0.1:<port>` | normal if peers provisioned | normal |
| `"127.0.0.1:<port>"` | any | `127.0.0.1:<port>` | unchanged unless already being written by app/config API | normal if peers provisioned | normal |
| `"localhost:<port>"` | any | `127.0.0.1:<port>` | pre-v2/missing revision does not write on startup; v2/gated startup or app migration writes per the normative table | normal if peers provisioned | normalized |
| `"127.x.y.z:<port>"` | any | `127.0.0.1:<port>` | pre-v2/missing revision does not write on startup; v2/gated startup or app migration writes per the normative table | normal if peers provisioned | normalized |
| `"[::1]:<port>"` | any | `127.0.0.1:<port>` | pre-v2/missing revision does not write on startup; v2/gated startup or app migration writes per the normative table | normal if peers provisioned | normalized |
| `":7853"` | any | `127.0.0.1:7853` | pre-v2/missing revision does not write on startup; v2/gated startup or app migration writes per the normative table | unprovisioned peers disabled | migrated |
| `"0.0.0.0:7853"` | any | `127.0.0.1:7853` | pre-v2/missing revision does not write on startup; v2/gated startup or app migration writes per the normative table | unprovisioned peers disabled | migrated |
| `"[::]:7853"` | any | `127.0.0.1:7853` | pre-v2/missing revision does not write on startup; v2/gated startup or app migration writes per the normative table | unprovisioned peers disabled | migrated |
| `":<non-default-port>"` | any | `127.0.0.1:<non-default-port>` | unchanged until user confirms signed/offline listener repair | stopped | `public_listen_requires_confirmation` |
| `"0.0.0.0:<non-default-port>"` | any | `127.0.0.1:<non-default-port>` | unchanged until user confirms signed/offline listener repair | stopped | `public_listen_requires_confirmation` |
| `"[::]:<non-default-port>"` | any | `127.0.0.1:<non-default-port>` | unchanged until user confirms signed/offline listener repair | stopped | `public_listen_requires_confirmation` |
| malformed listen without parseable port | valid port | `127.0.0.1:<port>` | unchanged until user confirms signed/offline listener repair | stopped | `public_listen_requires_confirmation` with `parse_error` |
| malformed listen without parseable port | missing or invalid | `127.0.0.1:7853` | unchanged until user confirms signed/offline listener repair | stopped | `public_listen_requires_confirmation` with `parse_error` |
| other non-loopback host/port | any | `127.0.0.1:<configured port>` | unchanged until user confirms signed/offline listener repair | stopped | `public_listen_requires_confirmation` |

Loopback HTTP remains the local API for the menubar app, CLI, and
`ssh-gateway` helper process. Local endpoints keep signed request/response
authentication and loopback-only checks.

Normal-mode signed peer-config endpoints:

- All endpoints in this section require loopback, `X-Clipfan-Auth-Version:
  clipfan-v1/request-hmac`, normal signed request/response auth, and config v2.
  All writes require `expected_config_revision`; stale writes return
  `config_revision_conflict` with the current revision and do not write.
  Unknown submitted wrapper fields or peer-object fields return `unknown_field`.
  Stored unknown config v2 fields are preserved. Peer writes are scoped merges,
  not whole-record replacements: each endpoint owns only the accepted fields it
  documents and must preserve stored unknown peer fields, proof subobjects,
  non-secret service metadata, update metadata, connection health, last-failure
  fields, and log references unless that endpoint explicitly owns them.
- `GET /v1/config/ssh/peers/<peer_id>` returns one redacted peer record and the
  current `config_revision`. It may return `id`, `enabled`, `accept`, `connect`,
  `persistent`, `on_demand`, `ssh_host`, `ssh_user`, `ssh_port`, `install_path`,
  `gateway_path`, `migration_state`, `proof`, and non-secret service metadata.
  It never returns `shared_key`, sync private key material, HMACs, encrypted
  clipboard bodies, or raw signed frames.
- `PUT /v1/config/ssh/peers/<peer_id>` upserts the non-secret identity,
  topology, and setup-state fields for one peer. For an existing peer, omitted
  accepted fields retain their stored values unless that field is explicitly
  nullable and the request sends `null`; the request does not replace the whole
  stored peer object. Creating a peer requires the endpoint's required fields to
  be present and valid before any write occurs. Request body:

```json
{
  "expected_config_revision": 12,
  "peer": {
    "id": "fsck",
    "enabled": true,
    "accept": false,
    "connect": true,
    "persistent": true,
    "on_demand": true,
    "ssh_host": "fsck.com",
    "ssh_user": "jesse",
    "ssh_port": 22,
    "install_path": "/home/jesse/.local/bin/clipfan",
    "gateway_path": "/home/jesse/.local/bin/clipfan",
    "migration_state": "loopback_unprovisioned"
  }
}
```

  This endpoint can create peers only as `loopback_unprovisioned`. Existing
  peer updates may omit `migration_state` or repeat the peer's current persisted
  state, but the upsert endpoint cannot change migration state and cannot write
  `ssh_material_staged`, `provision_failed`,
  `shared_key_written_unverified`, or `ssh_keys_ready`. Any staged, failure, or
  promotion state, including the first `ssh_material_staged` or
  `provision_failed` write, must use the transition endpoint below so proof,
  reason, log ID, and audit semantics are identical for every state change. A
  pre-secret Add/Repair Peer failure becomes durable `provision_failed` only
  after a persisted peer record exists. Earlier failures use the operation-local
  failure log matrix in the provisioning section and do not create a peer record
  just to store failure state.
- `PATCH /v1/config/ssh/peers/<peer_id>/proof` updates directional proof without
  changing migration state. Request body:

```json
{
  "expected_config_revision": 13,
  "accept_proof": {
    "key_id": "a4a4a4a4a4a4a4a4",
    "gateway_path": "/Users/jesse/.local/bin/clipfan",
    "verified_at": "2026-06-01T12:34:56Z",
    "verified_by": "local_file"
  },
  "connect_proof": {
    "key_id": "b5b5b5b5b5b5b5b5",
    "gateway_path": "/home/jesse/.local/bin/clipfan",
    "verified_at": "2026-06-01T12:35:10Z",
    "verified_by": "regular_ssh"
  }
}
```

  Either proof object may be omitted. The daemon validates proof shape and stores
  only the matching `proof.accept_*` or `proof.connect_*` fields.
- `POST /v1/config/ssh/peers/<peer_id>/transition` changes migration state after
  the app has performed the corresponding regular-SSH or command-locked proof.
  Request body:

```json
{
  "expected_config_revision": 14,
  "from_state": "shared_key_written_unverified",
  "to_state": "ssh_keys_ready",
  "reason": "gateway_version_verified",
  "log_id": "peer-log-1780257600"
}
```

  Legal transitions for this endpoint are the persisted-state transitions in the
  normative table below. Runtime health changes, UI colors, and transient
  migration suggestions are not legal `to_state` values. Peer removal is handled
  only by `DELETE /v1/config/ssh/peers/<peer_id>`, and disabling is handled only
  by `POST /v1/config/ssh/peers/<peer_id>/disable`.
  Remote pre-secret staging before the remote daemon has auth material does not
  call this signed loopback endpoint; `clipfan provision transition`
  `mode:"pre_secret_offline"` enforces the same transition table under the remote
  config lock and is limited to `loopback_unprovisioned` to
  `ssh_material_staged`.

  | from_state | to_state | Required fields/proof | Side effects |
  |------------|----------|-----------------------|--------------|
  | `loopback_unprovisioned` | `ssh_material_staged` | `reason`, `log_id`; non-secret peer record has host ID, locator, known-host pin when `connect:true`, and staged proof shape for any enabled direction already written | increments config revision; does not start sync |
  | `loopback_unprovisioned` or `ssh_material_staged` | `provision_failed` | stable `reason`, `log_id`, failed phase code, and `remote_secret_absence_proof` as defined below | records pre-secret failure state; does not delete peer or start sync |
  | `provision_failed` | `loopback_unprovisioned` or `ssh_material_staged` | `reason:"retry_progress"`, `log_id`, and the same proof/material requirements as the target state | resumes from explicit retry phase; increments config revision once |
  | `ssh_material_staged` | `shared_key_written_unverified` | `reason:"remote_shared_key_written"` or `reason:"secret_write_outcome_unknown"`, redacted remote secret-write `log_id`, valid local fleet credential, and either post-write shared-key validation result or proof that a shared-key-capable remote command was spawned without verifiable absence | enables command-locked `version` only; `receive` and `sync-stream` stay blocked |
  | `shared_key_written_unverified` | `ssh_keys_ready` | `reason:"gateway_version_verified"`, `log_id`, and current proof for every enabled direction at promotion time | permits runtime gates to start SSH sync on next scheduler pass |
  | `shared_key_written_unverified` or `ssh_keys_ready` | `ssh_material_staged` | `reason:"remote_shared_key_cleanup_verified"`, `log_id`, and regular-SSH cleanup proof that the remote peer config no longer contains this fleet `shared_key` for the local host | stops runtime sync before the write response; records verified remediation and keeps only non-secret staged material |
  | `ssh_keys_ready` | `loopback_unprovisioned` | stable identity-reset/removal-prep `reason`, `log_id`, and proof that transport-current/order barriers tied to the old peer identity were cleared | stops runtime sync and requires full reprovisioning |

  Every transition requires `expected_config_revision`. Stale revisions fail
  with `config_revision_conflict` and the current revision. Unknown `from_state`,
  `to_state`, or `reason` values are rejected. A successful transition appends a
  redacted audit log and increments the config revision exactly once. A remote
  fleet `shared_key` write can never be represented as `provision_failed` or
  `ssh_material_staged` unless the same transition carries verified cleanup
  proof; unverified cleanup leaves the peer in `shared_key_written_unverified`
  or removes it only through the explicit delete/remediation flow with an audit
  entry preserving that the remote may have received the fleet credential.

  `remote_secret_absence_proof` is a structured object, not a free-form phase
  label. It includes `failed_phase`, `secret_write_command_spawned`,
  `absence_verified_by`, `verified_at`, `remote_config_revision` when known, and
  a redacted `log_id`. When `secret_write_command_spawned:false`, `failed_phase`
  must be one of the strictly pre-secret phases: host-key confirmation,
  upload/install, identity probe, daemon stop, required sync-key provision,
  known-hosts provision, non-secret config write, managed authorized_keys write,
  pre-secret forced-command probe, local peer create, local proof patch, or
  staged transition. Optional remote accept-only sync-key creation failure is a
  notice, not a valid `remote_secret_absence_proof.failed_phase`. When any
  command capable of writing a config containing
  `shared_key` has been spawned, app-side phase labels no longer prove absence.
  The app may still transition to `provision_failed` only after a regular-SSH
  provision read takes the remote config lock and proves the fleet `shared_key`
  is absent from the remote peer config. If that locked read cannot complete,
  the peer must transition to `shared_key_written_unverified` with
  `reason:"secret_write_outcome_unknown"` and visible remediation; it must not
  be represented as `provision_failed` or `ssh_material_staged`.
- `POST /v1/config/ssh/peers/<peer_id>/disable` sets `enabled:false` with
  `expected_config_revision` and a stable reason code.
  `DELETE /v1/config/ssh/peers/<peer_id>` removes the peer record with
  `expected_config_revision`, stable reason code, and `log_id` after user
  confirmation. Both actions stop local outbound sessions and reject inbound
  reservations for that peer before the write response is returned.
- Disable retains local managed authorized_keys lines and dedicated known_hosts
  pins so repair/re-enable can use the existing material. Disabled peer config
  still gates reservations closed, and the UI shows retained key material status
  instead of implying remote cleanup occurred.
- Delete behavior is state-specific. For `loopback_unprovisioned`, delete
  removes the peer row and appends a redacted audit entry because no remote fleet
  `shared_key` or SSH material was written for that peer. For
  `ssh_material_staged` and `provision_failed`, delete may remove the peer row
  only after it persists a durable `ssh_material_cleanup` remediation record for
  any local or remote SSH material that may have been written. That record is not
  a post-secret tombstone and must not imply that the remote fleet `shared_key`
  leaked. It records only redacted cleanup locators: `peer_id`, previous
  migration state, removal time, reason, `log_id`, direct SSH target tuple,
  gateway/install path, local/remote host IDs, key IDs, dedicated known-hosts
  reference or fingerprint, managed authorized_keys marker IDs, last
  provisioning phase, which cleanup proofs are already verified, and remaining
  user actions. It must not retain `shared_key`, private key material, HMACs,
  encrypted frame bodies, clipboard content, full SSH argv, or unredacted home
  paths. If the failed pre-secret phases prove no managed SSH material was ever
  written, the durable record may say `cleanup_required:false`; otherwise it
  survives restart until cleanup is verified or explicitly dismissed.
  For `shared_key_written_unverified` and `ssh_keys_ready`, delete must first
  persist a durable post-secret peer-remediation tombstone in the same config
  transaction that removes the peer row. The tombstone records `peer_id`,
  previous migration state, removal time, reason, `log_id`, whether remote
  fleet-secret cleanup was verified, whether remote managed-key cleanup was
  verified, the same redacted SSH-material cleanup locators, and the remaining
  user actions: retry cleanup over regular SSH, rotate fleet `shared_key`, or
  dismiss only after explicit acknowledgement. SSH-material cleanup records and
  post-secret tombstones are returned by signed status/log APIs and survive
  app/daemon restart until cleanup is verified or the user explicitly dismisses
  the warning. A dismissed record remains in redacted audit history but is no
  longer rendered as an active warning.
- Delete rejects reservations before returning, then attempts best-effort cleanup
  of local managed authorized_keys accept lines for that peer under the local
  authorized_keys lock. Cleanup failure persists a redacted
  `stale_local_authorized_key_line` remediation event and exposes a "Clean up
  local key" retry action. Cleanup of this host's outbound connect line on the
  remote peer requires regular SSH; if regular SSH is unavailable, the peer stays
  removed locally and a stale remote line warning remains in the durable
  tombstone/remediation list until a user-authorized cleanup succeeds.

The Mac app must use these scoped endpoints for local peer changes whenever the
daemon is available. It must not fall back to whole-config writes for peer
record, proof, promotion, demotion, or removal. Offline listener repair remains
limited to listener fields. Confirmed local fleet reset and confirmed
corrupt-transport-state reset are separate recovery flows with their own
milestones and must not be implemented as whole-config peer repair.

Recovery ordering for unsafe listeners and invalid credentials is fixed:

| Local condition | First offered recovery | Fleet reset availability |
|-----------------|------------------------|--------------------------|
| unsafe listener, valid `shared_key`, daemon safe-mode API reachable | signed listener repair, then daemon restart/status check | hidden until listener repair succeeds |
| unsafe listener, missing or invalid `shared_key`, daemon unavailable, or signed repair unavailable | offline listener repair under the config lock | hidden until listener repair succeeds |
| safe listener, invalid or lost `shared_key`, config lock available | confirmed local fleet reset | internal/test only until 17d3a; public only when `ConfigV2WriteEnabled=true`, then after destructive confirmation |
| safe listener, daemon unavailable, `shared_key` valid | restart/diagnose daemon before destructive recovery | available only as an explicit advanced recovery |
| any local config/state lock held by a running daemon or another repair | no file mutation; show stop/retry guidance | unavailable until the lock is clear |

The app must not offer confirmed local fleet reset as the first action for an
unsafe listener. Listener repair is the only recovery that may edit an unsafe
listener config before reset eligibility is evaluated.
Before the 17d3a public cutover, confirmed local fleet reset may be implemented
and tested only in internal/test builds. Public builds with
`ConfigV2WriteEnabled=false` must not expose a reset action that writes config
v2; they show upgrade/current-build recovery guidance instead. The public reset
action becomes available in the same 17d3a release profile that enables public
config v2 writes.

All local clients discover the daemon through a shared loopback discovery helper:
the Mac app, CLI, and forced-command gateway must not each invent their own
daemon URL rules. The helper reads the user-owned config when available, derives
the port with the same rule as daemon safe mode, and never connects to a
persisted non-loopback address. Port derivation parses the `listen` port when
valid, otherwise uses `port` when it is in 1-65535, otherwise uses 7853. The
helper probes `127.0.0.1:<derived-port>` first, then `127.0.0.1:7853` only as a
final compatibility probe when the derived port is not 7853. A compatibility
probe is accepted for signed app/CLI/gateway use only after signed
`/v1/status` or `/v1/version` proves the same config path, state directory,
auth version, and host ID; unauthenticated health proves only liveness and is
usable only for health-only safe-mode UI.
In safe mode, the app and CLI must be able to read the unsafe listen status over
loopback when signed repair is available and show a user action labeled "Move
daemon listener to loopback". Only that explicit action writes config. The write
changes `listen` to the derived loopback listen, records the previous value in a
diagnostic `previous_listen` field, restarts the daemon, and verifies that peer
sync remains stopped until the restart uses the confirmed loopback config. When
`safe_mode_health_only` exposes only unauthenticated health, app/CLI recovery
uses the health-only stop/wait/offline-repair path described above rather than a
signed status or mutation endpoint.

### SSH Key Boundary

clipfan-managed keys are only for sync and version. They are not used for
install or update.

Each host has a clipfan sync identity:

```text
~/.config/clipfan/ssh/sync_ed25519
~/.config/clipfan/ssh/sync_ed25519.pub
~/.config/clipfan/ssh/known_hosts
```

Private key, config, state, and known_hosts files are mode `0600`; directories
are mode `0700`. The sync public key contains no secret. clipfan may write the
public key file as `0600` for uniform ownership checks or `0644` when created by
OpenSSH tooling; security checks must not depend on the public key being secret.

A local sync key pair is required before this host can start outbound
`connect:true` sessions, publish this host's public key to a peer, or enable
future remote-to-local outbound setup. Accept-only peers do not need outbound
locator fields, known-host records, or the local sync private key for inbound
gateway reservations after `accept:true` proof exists, because inbound
authentication uses the remote peer's key in this host's managed authorized_keys
line. Strict
SSH runtime validation rejects missing local sync key paths for outbound
`connect:true` actions and key-rotation/provisioning actions that use this
host's public key. A missing local sync key on this host is orange
`missing_sync_key` and blocks outbound sessions plus this host's sync-key
rotation until regenerated. A remote accept-only peer whose own local sync key
could not be created during one-way Add Peer uses the distinct warning
`remote_accept_only_missing_sync_key`; it does not block current inbound gateway
reservations, this host's outbound persistent stream to that peer, public Add
Peer green/success after a qualifying latest-state exchange, or this host's
host-global sync-key rotation. It blocks only future reciprocal outbound setup
from that remote host and rotation of that remote host's own sync key until
regular-SSH repair creates the missing key. Minimal safe-mode load still allows
listener repair.

Adding a peer with regular user SSH credentials installs the local host's sync
public key into the peer user's `authorized_keys` as a forced command. clipfan
always emits the portable explicit option set instead of relying on OpenSSH
`restrict` support:

```text
no-agent-forwarding,no-X11-forwarding,no-port-forwarding,no-pty,no-user-rc,command="/home/jesse/.local/bin/clipfan ssh-gateway --authorized-peer m4 --authorized-key-id 66402c9468c58941" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f clipfan-sync:m4:66402c9468c58941
```

The forced command supplies trusted key-line metadata:
`--authorized-peer <peer-id>` and `--authorized-key-id <key-id>`. The peer
identity used for gateway authorization comes only from `--authorized-peer`; the
key ID records which managed line OpenSSH selected. The requested command is
untrusted and is used only to select an allowed verb through
`SSH_ORIGINAL_COMMAND`.

The installed clipfan path is discovered through a machine-readable installer
contract, not by parsing human installer output. The future installer flag
`install.sh --json-result` writes exactly one JSON object to stdout with
`install_path`, `config_path`, `state_dir`, `uid`, `effective_user`, and
`version`; human logs stay on stderr. The app validates the returned absolute
`install_path`, then runs `<install_path> version --json` over regular SSH and
requires the version response to report the same installed path/account metadata
before storing it as `install_path` or `gateway_path`. Legacy human text such as
`Installing ... -> ...` is not a protocol. After 17d3b, for already-provisioned
peers only, the app stores that path per peer and rewrites managed
authorized_keys lines when the path changes. Public update/check before 17d3b
remains `regular_ssh_install_update_mutation` only: it may report a stale gateway
path, but it must not mutate managed authorized_keys, sync keys, dedicated
known_hosts, peer config, migration state, or fleet secrets. The examples above
show the default user install path; hosts using `DEST` store the validated
resolved path instead.

### Application-Layer Auth

SSH authenticates the transport, but `shared_key` remains the fleet
application credential. Every persistent stream starts with a signed hello
challenge before either side sends clipboard state.

The raw `shared_key` is not used directly for every primitive. The daemon
derives domain-separated keys with HKDF-SHA256:

- `clipfan-v1/request-hmac` for local request signatures in config v2,
- `clipfan-v1/ssh-hello-hmac` for SSH hello/version/watch HMACs,
- `clipfan-v1/body-aead` for AES-GCM clipboard body encryption.

HKDF is exactly RFC 5869 HKDF-SHA256:

- IKM is the raw bytes obtained by standard base64-decoding `shared_key`.
- Salt is the ASCII byte string `clipfan-v1/hkdf-salt`.
- Info is the ASCII label shown above.
- Output length is 32 bytes for all three labels.
- The 32-byte `clipfan-v1/body-aead` output is an AES-256-GCM key.

Envelope crypto has an explicit transport boundary. SSH `state` frames and new
config-v2 current/receive paths use the HKDF-derived `clipfan-v1/body-aead`
key. Legacy peer HTTP `/v1/clip` bodies keep the existing
`SHA256(raw shared_key)` AES-GCM body key until peer HTTP runtime is disabled or
that endpoint is made loopback/test-only. Receivers select the body key from the
authenticated endpoint/protocol version; they must not try both keys or add a
silent dual-decode compatibility path.

Fixed derivation vector:

```text
shared_key base64: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
shared_key bytes: 3031323334353637383961626364656630313233343536373839616263646566
salt: 636c697066616e2d76312f686b64662d73616c74
clipfan-v1/request-hmac: 1c23ce9e76df9696c06b04fa7f16dabd200d87043e31f592a91344899161a132
clipfan-v1/ssh-hello-hmac: 0404698459498d07ba8d3f333d6d549b1553a83204a29b6e0e98d79882eb2537
clipfan-v1/body-aead: 462bb7403a9e46da77af365a4a4eb2e342cdd9ba5755d21586fd4ee1334c0300
```

`shared_key` validation is strict in config v2 and state-specific:

- A remote config missing `shared_key` is allowed only for regular-SSH
  pre-secret staging states that cannot run command-locked gateway purposes:
  unconfigured identity probe, non-secret config skeleton, and
  `ssh_material_staged`.
- Missing local fleet credential or missing `shared_key` in the scoped final
  config write payload blocks command-locked version, receive, sync-stream, the
  final secret write, promotion to `shared_key_written_unverified`, and promotion
  to `ssh_keys_ready` with `missing_shared_key`. The remote staged config is
  expected to lack `shared_key` until that scoped final write succeeds; the
  promotion validator checks the target post-write config, not the pre-write
  staged file.
- Before provisioning writes any remote config, the app takes the remote config
  lock through regular SSH and classifies the remote `shared_key` as `missing`,
  `same_fleet_key`, `mismatched_valid`, `invalid_malformed`, or
  `unreadable_or_locked`. `missing` and `same_fleet_key` may continue. A
  `same_fleet_key` legacy HTTP peer is recorded as `legacy_shared_key_present`;
  the staged skeleton deliberately removes the key only after the old daemon is
  stopped and downgrade restart is blocked. `mismatched_valid`,
  `invalid_malformed`, and `unreadable_or_locked` fail before any remote config,
  key, authorized_keys, service, or local peer mutation, and the UI offers only
  regular-SSH reset/cleanup or cancel.
- Non-standard base64, decoded length other than exactly 32 bytes, or malformed
  legacy values block command-locked peer sync, final secret write/promotion,
  and signed local APIs with `invalid_shared_key`. Regular-SSH cleanup and
  offline listener repair remain available through their explicitly allowed
  paths.
- First-run setup generates a fresh 32-byte random key and stores it as standard
  base64.
- Migration from a pre-v2 config validates the existing key before any remote
  config or `shared_key` write. If invalid, the app shows only the confirmed
  local fleet-reset flow or cancel. It does not silently coerce, pad, truncate,
  reuse malformed key material, or expose an unauthenticated local reset API.
- Command-locked version verification must fail with `bad_auth` when peer
  `shared_key` values differ; provisioning surfaces that as
  `shared_key_mismatch`.

Confirmed local fleet reset is a separate offline recovery flow, not listener
repair and not a signed loopback API. It is available only when the current OS
user can read a parseable config that is owned by that user, is not
group/world-writable, and can be locked with the adjacent config lock. The app
and CLI require an explicit destructive confirmation before running it. The
reset capability is phased with the primitives it cleans up. Before the public
17d3a config-v2 cutover, minimal reset writes a timestamped backup, generates a
new 32-byte fleet `shared_key`, preserves or derives the local host identity,
removes legacy `static_peers`, persists loopback-only config v2 single-host state
with a fresh revision, and refuses to run when SSH material or transport-current
state is already present. After the corresponding primitives exist, Milestone
11e owns the post-SSH-material reset extension: it regenerates or clears local
sync key material, clears or tombstones `ssh.peers[]`, proof, migration state,
remediation records, dedicated known_hosts entries, only clipfan-managed
authorized_keys lines belonging to the old local host identity,
transport-current state, and pending transport state encrypted under the old key.
Reset never contacts remote hosts and never edits unmarked authorized_keys lines.
Other hosts that still hold the old `shared_key` remain outside the new fleet
until re-enrolled with regular SSH credentials or reset separately.

If the config is unparseable, wrong-owner, unsafe-mode, or locked by another
process, local fleet reset is unavailable and recovery is manual. No daemon
endpoint is allowed to generate or replace `shared_key` when the existing
`shared_key` cannot authenticate signed local requests.

Sync keys alone are not enough to read clipboard payloads or participate in the
fleet. A leaked sync private key can open the forced command, but without
`shared_key` it cannot complete the application handshake or decrypt clip
payloads.

Install/update authority remains separate. A user who can authenticate with
regular SSH can install or update code as that Unix user. The command-locked
sync key cannot run install/update.

SSH transport v1 relies on the SSH channel for post-hello frame integrity. A
compromised SSH server, compromised forced-command gateway process, same-user
process, root process, or host that already has the fleet `shared_key` is inside
the trusted-host boundary. The protocol does not try to protect a host from its
own SSH server or its own clipfan gateway after forced-command execution starts.

On multi-user Unix hosts, clipfan's supported boundary is the target Unix user.
Config, state, sync keys, known_hosts, and authorized_keys entries belong to the
same `ssh_user` that runs the daemon. Other unprivileged users should not be
able to read those files when permissions are correct. The same Unix user, root,
or a user with access to the fleet `shared_key` remains outside the protection
boundary. Installing to one account while the daemon/config/authorized_keys live
under another account is unsupported and must fail provisioning rather than
trying to bridge accounts with sudo or shared files.

## Configuration

The local daemon config grows an SSH transport section:

```json
{
  "config_version": 2,
  "listen": "127.0.0.1:7853",
  "previous_listen": "",
  "shared_key": "base64-32-bytes-shared-across-fleet",
  "hostname": "m4",
  "transport": "ssh",
  "ssh": {
    "sync_key": "~/.config/clipfan/ssh/sync_ed25519",
    "known_hosts": "~/.config/clipfan/ssh/known_hosts",
    "max_sessions": 16,
    "max_sessions_per_peer": 2,
    "log_limit_bytes": 65536,
    "peers": [
      {
        "id": "fsck",
        "ssh_host": "fsck.com",
        "ssh_user": "jesse",
        "ssh_port": 22,
        "install_path": "/home/jesse/.local/bin/clipfan",
        "gateway_path": "/home/jesse/.local/bin/clipfan",
        "enabled": true,
        "accept": false,
        "connect": true,
        "persistent": true,
        "on_demand": true,
        "migration_state": "ssh_keys_ready",
        "proof": {
          "connect_key_id": "a4a4a4a4a4a4a4a4",
          "connect_verified_at": "2026-06-01T12:34:56Z",
          "connect_verified_by": "regular_ssh"
        }
      }
    ]
  },
  "max_history": 200
}
```

`id` is the clipfan peer identity used for recipient binding and UI status.
`enabled: false` disables all sync for the peer without deleting its
configuration. `accept: true` means the daemon may accept inbound gateway
sessions from that peer ID when the peer is also enabled and provisioned.
`connect: true` means this host should initiate outbound sessions to the peer.
For `connect: true` peers, `ssh_host`, `ssh_user`, and `ssh_port` are required
and describe how this daemon initiates SSH. For `connect: false` accept-only
peers, those outbound locator fields are optional and ignored; the UI renders
them as accept-only peers, labels them "ready to receive inbound sync; no local
fallback possible", and does not offer reconnect/update-through-sync actions.
`persistent` and `on_demand` are stored as `false` or omitted for `connect:false`
accept-only peers in this release. This release does not provide UI or
provisioning steps to flip an accept-only peer into reciprocal outbound
`connect:true`; doing that requires a later explicit reciprocal setup flow with
its own local SSH endpoint, remote known-host proof, managed authorized_keys
proof, and review slice. On-demand delivery is an outbound action and is
eligible only when `connect:true` plus all outbound material is valid:
`ssh_host`, `ssh_user`, `ssh_port`, sync private key, key-ID/proof, and pinned
known-host entry. Accept-only peers do not need local known-host records unless
outbound `connect` is later enabled by a future release. Any established SSH stream is
bidirectional, so only one side needs outbound reachability for both hosts to
exchange latest state while the stream is alive.

`install_path` is the path returned by regular-SSH install/update. `gateway_path`
is the path embedded in managed authorized_keys forced commands. They are
usually the same. Both are absolute paths owned by the target Unix user. They
must contain only `[A-Za-z0-9._/@+-]`; paths with spaces, quotes, shell
metacharacters, or control characters are unsupported in this release. Character
allowlisting is not sufficient by itself: after POSIX lexical cleaning, the
cleaned path must equal the original string, must remain absolute, and must not
contain any `..` path component. The app verifies the final file is a regular
file owned by the target Unix user and not group/world writable before storing or
using it. If an install/update returns a non-canonical path, a relative path, a
path containing `..`, a symlink final component, a wrong-owner file, or an
unsafe mode, provisioning fails with `invalid_install_path` or
`invalid_gateway_path` before any managed authorized_keys or `shared_key` write.
When `install_path` changes after update or `DEST` changes, the app rewrites only
managed authorized_keys lines whose `gateway_path` is stale, and only when the
peer is already provisioned and the 17d3b transport gates allow managed-line
mutation. Before 17d3b, public update/check may surface the stale path but must
not rewrite managed authorized_keys.

`migration_state` is persisted in the daemon config next to the peer record.
The daemon reads it during config load and reservation checks. The app updates
it only through signed loopback config APIs or by writing config during remote
install before the daemon starts. Daemon runtime health such as current stream
state is kept in daemon memory and exposed in status snapshots; it does not
replace the persisted migration state. Config writes use atomic rename and
preserve private file permissions.

Local config writes are serialized with an advisory lock file adjacent to the
config file, for example `config.json.lock`, opened with owner-only permissions.
Writers read the current file under the lock, verify the config revision, apply
only the intended structured change, write a temporary file in the same
directory with mode `0600`, fsync the temp file where supported, atomically
rename it into place, and fsync the parent directory where supported. If the
revision changed before the lock was acquired, the writer returns
`config_revision_conflict` and does not write. A crash may leave the old
complete config or the new complete config; temporary files are ignored and
cleaned on the next successful write.

Config, state, SSH key, and known_hosts storage are supported only on local
POSIX filesystems with reliable advisory locking and atomic rename semantics.
NFS, SMB, cloud-synced folders, network homes, and shared home directories are
unsupported for clipfan runtime storage in this release. The daemon and app do
not attempt cross-host coordination for two machines sharing the same home
directory; such setups must be repaired by moving clipfan config/state to local
per-host storage before SSH transport is enabled.

Before enabling each storage-backed SSH feature, the app and daemon run a
runtime storage locality check on every clipfan config/state/key root that
feature will use. Config v2 writes and public 17d3a listener cutover require the
existing config and daemon state roots to pass. Sync-key generation, dedicated
known_hosts writes, and transport-current persistence each add their own root
checks in the milestone that introduces that storage primitive; earlier
milestones must not invent future paths just to satisfy locality checks. Known
unsupported filesystem types or known cloud-sync roots fail closed with
`unsupported_runtime_storage`; there is no user override in this release. When
the daemon can safely expose signed status, the status payload includes the
failing root, normalized path, detected storage class when known, and
`ssh_transport_enabled:false`, but never includes private key paths beyond the
documented redacted path labels. If the platform cannot identify the filesystem
type, the check records
`storage_check_inconclusive` and keeps SSH transport plus listener/config public
cutover disabled.

After Milestone 17d3a, unsupported or inconclusive storage must never leave a
configured public listener active and must not bind even health-only loopback
endpoints, because the daemon lock cannot be trusted on that storage. The daemon
fails before binding any socket and before opening transport state. Storage
diagnostics come from the app/CLI offline preflight path: the app/CLI reads local
config metadata after owner/mode checks that are safe to perform, reports failing
root, normalized path, detected storage class when known,
`ssh_transport_enabled:false`, and applies the same path redaction rules as the
signed status payload. Roots classified as local must still pass the normal
owner/mode/lock/atomic-rename smoke test before runtime starts. The app repair
action for `unsupported_runtime_storage` or `storage_check_inconclusive` is to
move clipfan config/state to local per-host storage and restart; it must not
attempt to coordinate locks across the shared storage.

The 17d3a updater/service abort path is part of this guarantee. Before a public
17d3a local or remote update reports success, the app/updater runs the offline
storage locality preflight for the target's current config and daemon state roots
and reads the configured listener after safe owner/mode checks. If storage is
unsupported or inconclusive and the existing config may bind off-host, the
updater stops and disables the user clipfan service through the existing
launchd/systemd/user-service mechanism before installing or starting the new
daemon, then reports `unsupported_runtime_storage` or
`storage_check_inconclusive` with offline repair instructions. It does not write
config v2, does not start the new daemon, and does not claim the update or
17d3a cutover succeeded. If the service cannot be stopped or disabled, the
updater fails with `public_listener_service_still_active`, shows copyable
service/log diagnostics, and must not report the host as secured or green.
Regular SSH remote update follows the same rule on the remote host: a failed
storage preflight may leave the binary payload untouched or pre-secret uploaded,
but it must not restart a public-listening daemon or mark the peer updated.

Concrete detection rules:

- macOS checks each root and its containing volume with `statfs` and volume
  locality metadata. Reject filesystem names including `nfs`, `smbfs`, `afpfs`,
  `webdav`, `fusefs`, `sshfs`, and any volume reported as not local. Reject
  known cloud-sync roots under `~/Library/Mobile Documents`,
  `~/Library/CloudStorage`, Dropbox, Google Drive, and OneDrive directories.
- Linux checks the matching mount entry from `/proc/self/mountinfo` and the
  filesystem magic/name from `statfs`. Reject `nfs`, `nfs4`, `cifs`, `smb3`,
  `9p`, `fuse.sshfs`, `fuse.rclone`, and other FUSE/network-backed types that
  are not explicitly classified local by tests. Reject known Dropbox, Google
  Drive, OneDrive, Syncthing, and rclone mount roots.
- Missing platform detection APIs, unreadable mount tables, or roots whose
  mount cannot be classified return `storage_check_inconclusive`, which fails
  closed for SSH transport and listener/config public cutover.

Config v2 stores `config_revision` as a monotonically increasing unsigned
integer once the field exists. A missing revision during one-way migration has a
virtual internal revision of 0 only for computing the first successful write;
external signed read/status/repair APIs represent it as `config_revision:null`
with an explicit `revision_state` and do not accept
`expected_config_revision:0` for that missing-revision state. The first
successful v2 write stores revision 1. Every successful signed or offline
repair write increments it exactly once after validating the caller's expected
revision representation. Signed config read/status APIs return an integer only
for versioned configs; stale writes fail with `config_revision_conflict` and
include the current external revision representation in the error response when
the caller is authenticated.

Milestone 0 config v2 preservation uses a typed-known-fields plus raw-JSON
passthrough representation. The parser stores unknown top-level fields and the
future `ssh` object as `json.RawMessage` until the full SSH schema lands.
Scoped updates mutate only owned typed fields, update `config_revision`, and
re-emit unknown raw fields unchanged at the value level. They must not
round-trip through a typed zero-value SSH struct or drop nested future fields.

`proof` records the app's last successful provisioning proof for each enabled
direction. For `accept:true`, `accept_key_id` is the key ID of the remote
peer's public sync key installed in this host's managed authorized_keys line,
and `accept_verified_by` is `local_file` when the app verified the local file or
`regular_ssh` when it verified the target host over regular SSH. For
`connect:true`, `connect_key_id` is this host's sync key ID as verified in the
remote host's authorized_keys by a regular-SSH provision command. The daemon may
validate local `accept` proof by inspecting local managed authorized_keys at
startup or during repair checks. The daemon cannot silently refresh remote
`connect` proof without regular SSH credentials, so proof older than the latest
regular-SSH check is reported as `remote_authorized_key_unknown` until the app
runs a new check. Missing or malformed persisted `connect` proof is a local
config/material problem and blocks outbound attempts. A peer cannot be promoted
to `ssh_keys_ready` unless every
enabled direction has current proof for the key ID and gateway path that will be
used at promotion time. After promotion, `ssh_keys_ready` is persisted as the
last successful provisioning state; runtime local-material checks decide whether
actions are allowed now.

Sync-key identity metadata is stored next to the private key as
`<sync_key>.clipfan.json`. The file is owned by the clipfan Unix user, is not
group/world writable, and is written atomically with the private/public key pair.
The metadata schema is:

```json
{
  "schema": "clipfan-sync-key-v1",
  "host_id": "m4",
  "key_id": "a4a4a4a4a4a4a4a4",
  "public_key_sha256": "SHA256:...",
  "public_key": "ssh-ed25519 AAAA...",
  "created_at": "2026-06-01T12:34:56Z"
}
```

The OpenSSH public-key comment is not identity metadata and must not be trusted
for host binding. An existing sync key is reusable only when the private key,
public key, and metadata sidecar all exist, have acceptable owner/mode, the
metadata `host_id` equals the expected host ID, and the metadata key ID and
public-key digest match the actual public key. Missing or mismatched metadata is
`sync_key_identity_mismatch` and does not overwrite an existing private key.
Repair or key rotation must be explicit. This metadata is a local consistency
contract enforced by owner/mode checks and the regular-SSH provisioning session;
it is not a cryptographic defense against the same Unix user or root.

Directional proof is always evaluated from the host that loads that peer
record:

- In host A's local record for peer B, `accept:true` means A's managed
  authorized_keys contains B's sync public key and A may accept inbound
  command-locked SSH from B.
- In host A's local record for peer B, `connect:true` means B's managed
  authorized_keys contains A's sync public key and A may initiate
  command-locked SSH to B.
- Each daemon validates only its own local record. A proof stored in B's config
  never satisfies A's local `accept` proof, and A's daemon cannot silently prove
  outbound `connect` drift on B without an explicit regular-SSH repair/check.

Default one-way-outbound proof example, where local host `m4` initiates SSH to
remote host `fsck` and reciprocal outbound is disabled:

| Phase | Local `m4` record for `fsck` | Remote `fsck` record for `m4` |
|-------|------------------------------|-------------------------------|
| after non-secret peer creation | `accept:false`, `connect:true`, `migration_state:"loopback_unprovisioned"`, no proof yet | `accept:true`, `connect:false`, `migration_state:"loopback_unprovisioned"`, no proof yet |
| after authorized_keys proof patch and staged transition | `accept:false`, `connect:true`, `migration_state:"ssh_material_staged"`, `proof.connect_key_id=<m4-sync-key-id>`, `proof.connect_verified_by:"regular_ssh"` | `accept:true`, `connect:false`, `migration_state:"ssh_material_staged"`, `proof.accept_key_id=<m4-sync-key-id>`, `proof.accept_verified_by:"regular_ssh"` |
| after remote shared_key write | local record promoted to `shared_key_written_unverified`; connect proof retained | remote record promoted to `shared_key_written_unverified`; accept proof retained |
| after command-locked version verification and promotion | local record `ssh_keys_ready`; `accept:false`; `connect:true`; `proof.connect_key_id=<m4-sync-key-id>` | remote record `ssh_keys_ready`; `accept:true`; `connect:false`; `proof.accept_key_id=<m4-sync-key-id>` |

Reciprocal outbound setup is deferred. The public release does not install
`fsck`'s sync public key in `m4`'s authorized_keys, does not collect the local
SSH endpoint needed for `fsck -> m4` dialing, does not write local
`accept:true`, and does not write remote `connect:true`. Without a future
reviewed reciprocal setup flow, the default local record remains `accept:false`
and the remote record remains `connect:false`.

Reciprocal persistent streams are arbitrated so the default
`max_sessions_per_peer: 2` still reserves one slot for short-lived `version` or
on-demand work. When both peers have `connect:true` and `persistent:true`, both
may attempt outbound connection after reconnect/backoff, but after hello each
side keeps at most one established long-lived stream for that peer pair. If two
persistent streams race, both sides retain the stream whose initiator host ID is
lexicographically lower and close the other with `duplicate_stream_replaced`.
If only the higher-ID initiator can connect, that single stream is retained
because no duplicate exists. A retained stream is bidirectional, so reciprocal
peers do not need two long-lived streams for correctness.

Managed paths are expanded with `~` against the target Unix user's home
directory, cleaned, and converted to absolute paths before use. When clipfan
creates or writes managed config, key, state, or known_hosts files, every parent
inside the managed clipfan directory must be a real directory, not a symlink;
private files are opened without following a final symlink where the platform
supports it. Path traversal outside the managed clipfan directory is rejected
for generated key/state paths. User-supplied installed binary paths are allowed
only when discovered through regular-SSH install/update and stored as absolute
paths for forced-command rendering.

`config_version: 2` identifies the SSH transport schema. Migration from missing
or older versions is one-way after the app confirms or auto-applies the
loopback migration. Downgrade to a pre-SSH daemon is unsupported. The release
does not rely on old-daemon parsing behavior. The installer/updater must install
the new binary and stop any old user service before any process writes
`config_version: 2`. After v2 migration, any attempt to launch or reinstall a
pre-SSH daemon is blocked by the app/updater with manual recovery instructions.
The old-version matrix is still tested, but only to prove the block works; it is
not a prerequisite for choosing whether to block.
Signed config update APIs include a config revision. The daemon rejects stale
app writes with `config_revision_conflict`; the app must reload, merge its
intended peer change, and retry rather than overwriting daemon-updated migration
state.

If config v2 has been written, pre-SSH daemons are blocked, and the new daemon
cannot start, recovery is offline and explicit: the app or CLI validates the
config owner/mode, takes the config lock, writes a timestamped backup next to
the config, offers only loopback `listen`, `port`, and `previous_listen`
repair, and then retries the new daemon. It does not edit peer records, secrets,
SSH keys, known_hosts, authorized_keys, downgrade the config, or restart an old
binary automatically.

Within config version 2, peer field names used by provisioning are stable for
this release. A retry after the app has updated must understand every persisted
v2 migration state listed here, including `ssh_material_staged` and
`shared_key_written_unverified`. The app may add optional fields only when the
daemon preserves them on read/modify/write or when the field is explicitly
owned by the app and rewritten from current probes. Removing or renaming a
provisioning field requires a future `config_version` migration; it must not be
hidden inside a retry path.

`previous_listen` is an optional diagnostic field written only by the app's
safe-mode repair action. It records the pre-repair non-loopback `listen` value
for troubleshooting. It is app-owned diagnostic metadata, and daemon
read/modify/write config paths must preserve it unless the user explicitly
clears repair history. The daemon never binds or syncs from `previous_listen`,
and older configs without the field remain valid.

Existing `static_peers` are not automatically persisted as `ssh.peers` by the
daemon. The daemon exposes them as transient legacy peer suggestions in status
and logs so the app can show "SSH setup required". The app writes a real
`ssh.peers[]` record only when the user starts Add/Repair Peer and the
provisioning flow reaches the scoped config write phase. Tailscale discovery can
still help populate an Add Peer picker, but it is no longer the transport.

Legacy peer update/version actions in the 17d3a cutover are user-prompted
regular-SSH operations, not background sync probes. For a transient
`source:"static_peers"` suggestion, the app may offer "Check version over SSH"
and "Update over SSH" actions. Those actions default `ssh_host` from the legacy
hostname, default `ssh_port` to 22, require an explicit `ssh_user`, accept the
same optional identity-file field as normal install/update, and require TOFU or
host-key mismatch confirmation before any mutating SSH/SCP command. Because no
persisted `ssh.peers[]` record exists yet, the operation stores host-key pins in
an operation-scoped mode-0600 temporary known_hosts file and reuses that file for
all SSH/SCP commands in the same action. It does not write dedicated known_hosts,
sync keys, authorized_keys, or peer config unless the user chooses the separate
Add/Repair Peer flow.

Public release packages that expose any regular-SSH install, update, cleanup,
or version-check action must include the operation-scoped or persisted host-key
enforcement boundary from Milestones 4e3b, 4e3c, and 9d1a. If that boundary and
its host-key tests are not in the same release candidate, those public actions
must be disabled or hidden; preserving an existing ad hoc SSH/SCP mutation path
is not an allowed compatibility exception.

After a legacy-peer update, the app verifies only the installed binary with
regular SSH `clipfan version --json` at the resolved installed path. It does not
attempt command-locked runtime readiness and does not mark the peer green; the
row remains a transient orange "SSH setup required" suggestion until the user
provisions SSH sync. There is no automatic background version check for legacy
suggestions after public HTTP removal.

Transport selection is explicit:

- `transport: "ssh"` starts only the SSH session manager for peer sync. It does
  not start discovery-based off-host HTTP fanout.
- Missing `transport` in an old config is treated as `legacy_http` for
  migration display, but release builds for the SSH transport do not silently
  continue public HTTP peer sync.
- Old configs have a dedicated migration load path: config load succeeds,
  generated wildcard listens are rewritten or explicit public listens enter
  safe mode, old `static_peers` are exposed as transient disabled SSH peer
  suggestions with `display_state: "legacy_http"` and `source:"static_peers"`,
  and the daemon rejects only attempts to run off-host HTTP. This load path does
  not create persisted `ssh.peers[]` records and does not increment config
  revision before the app can show repair actions.
- `legacy_http` is a transient UI/migration display classification, not a
  persisted `ssh.peers[].migration_state` and not a runtime transport in the SSH
  release. Only pre-SSH release binaries, tests, or explicitly internal
  development builds may execute legacy peer HTTP fanout.
- `static_peers` is read only for migration UI and for one-time provisioning
  suggestions. It is not used for runtime fanout after `transport: "ssh"`.
- Config load rejects duplicate `ssh.peers[].id` values, peers whose ID equals
  the local `hostname`, invalid ports, and post-migration configs that
  explicitly request off-host HTTP runtime behavior. Strict SSH runtime
  validation, not minimal load, rejects missing sync key paths when SSH transport
  is enabled.
- Per-peer migration state is persisted in daemon config only after the app has
  written a real `ssh.peers[]` record. Until then, UI/status labels for old
  `static_peers` are derived transient suggestions and must include their
  `source:"static_peers"` so code cannot confuse them with enforceable SSH peer
  config.

## Identity Invariants

- `hostname` is the local clipfan host ID. It is stable until the user renames
  the host in clipfan config.
- Peer IDs and local `hostname` values must be 1-63 characters of
  `[A-Za-z0-9._-]`, must not start with `-`, and are compared case-sensitively.
  Other values are rejected before writing config, authorized_keys comments, or
  HMAC inputs.
- Signed string fields use printable ASCII without control characters.
  Protocol challenge nonces, including request, response, hello, and version
  nonces, are 16 random bytes encoded as 32 lower-case hex characters. Clipboard
  body AEAD nonces are separate envelope fields: exactly 12 random bytes encoded
  with standard base64, yielding 16 base64 characters including any padding.
  AEAD nonces must be unique for the selected body key; senders generate a fresh
  random nonce for every sealed clipboard envelope and never reuse a nonce when
  resealing. `daemon_version` is 1-64 characters of
  `[A-Za-z0-9._+-]`. Protocol error codes are 1-64 characters of `[a-z0-9_]+`;
  human messages are never part of HMAC input. Protocol error `message` values
  are untrusted remote text: receivers cap them to 256 bytes after UTF-8
  validation, replace control characters with escaped notation, and drive UI
  state only from stable `code` values.
- `ssh.peers[].id` values are unique within a config file. Config load rejects
  duplicates.
- Peer IDs, not DNS names, are the security identity used by recipient binding,
  hello verification, status coalescing, and authorized key comments.
- `ssh_host` is only the network locator passed to OpenSSH. It is allowed to
  change without changing the peer ID.
- Among enabled peers with `connect:true` and complete outbound locator fields,
  the tuple `ssh_user`, canonical `ssh_host`, and `ssh_port` must be unique.
  Adding a second enabled outbound peer with a different peer ID but the same
  target tuple is rejected as `duplicate_ssh_target` and the app offers repair
  of the existing peer record instead. `connect:false` accept-only peers do not
  participate in duplicate target checks because their outbound locator fields
  are optional and ignored. Disabled/removed historical records may keep the
  tuple for audit/log display but cannot reserve outbound sessions.
- `ssh_user` must be 1-64 characters of `[A-Za-z0-9._-]`, must not start with
  `-`, and must not contain `@`, `/`, `:`, whitespace, quotes, shell
  metacharacters, or control characters.
- `ssh_host` must be one of: a DNS/MagicDNS name with labels containing
  `[A-Za-z0-9-]` plus dots, an IPv4 address, or an IPv6 address stored without
  brackets. It must be 1-253 characters, must not start with `-`, and must not
  contain userinfo, port suffixes, slashes, whitespace, quotes, shell
  metacharacters, or control characters. IDNs must be entered in punycode
  A-label form for this release.
- `ssh_host` is canonicalized before storage, duplicate-target comparison,
  host-key record lookup, and `ssh-keyscan`/known_hosts tuple construction. DNS
  and MagicDNS names are stored as lower-case ASCII after stripping exactly one
  trailing dot; empty labels, doubled dots, leading dots, and non-ASCII U-labels
  are rejected. Punycode IDN A-labels are lower-cased. IPv4 literals are parsed
  and stored as canonical dotted decimal with no octal/hex forms or leading-zero
  ambiguity. IPv6 literals are parsed and stored in RFC 5952 lower-case
  compressed form without brackets. `ssh_user` remains case-sensitive and is not
  normalized. UI labels may preserve a user-entered display name in a separate
  non-authoritative field, but `ssh_host` is the canonical stored locator.
- `ssh_port` must be an integer in 1-65535. The app stores the port separately
  and always passes it to OpenSSH with `-p <port>`.
- OpenSSH destinations are rendered only as argv elements. For regular SSH and
  runtime SSH, the destination argv element is exactly `<ssh_user>@<ssh_host>`.
  IPv6 hosts are not bracketed in the destination element because the port is
  supplied with `-p`; known_hosts entries use OpenSSH's `[host]:port` form for
  non-default ports.
- `scp` upload destinations are rendered separately because remote targets use
  colon syntax. For DNS and IPv4 hosts, the remote target argv is
  `<ssh_user>@<ssh_host>:<absolute-remote-path>`. For IPv6 hosts, the remote
  target argv is `<ssh_user>@[<ssh_host>]:<absolute-remote-path>`. The port is
  still passed with `-P <ssh_port>` for `scp`, never embedded in the target
  string. Remote upload paths must be absolute, must pass the same
  `[A-Za-z0-9._/@+-]+` validation as provision paths, and are passed as one argv
  element rather than shell-interpolated.
- In every hello, `host_id` is the sender of that hello and `peer_id` is the
  intended receiver.
- The receiver of a hello always checks `hello.peer_id == <local hostname>`.
- On an inbound forced-command gateway, the gateway also checks
  `hello.host_id == --authorized-peer`.
- The initiator checks the gateway reply has `host_id == <configured peer ID>`
  and `peer_id == <local hostname>`.
- A peer that proves `shared_key` but presents an unexpected `host_id` is
  rejected with `identity_mismatch`.
- An old config without `hostname` must be migrated before creating sync keys,
  managed authorized_keys lines, proof records, transport-current state, or any
  command-locked gateway identity. The app/daemon derives the host ID once using
  the same first-run hostname logic, validates it with the peer-ID rules above,
  persists it under the config lock, and then uses only the persisted value for
  sync-key metadata, `clipfan version --json`, hello identity, gateway
  reservation checks, proof records, and duplicate-host-ID repair. For existing
  config v2 files this write increments `config_revision`; for pre-v2 configs it
  writes only the legacy `hostname` field and does not change `listen`,
  `transport`, or `config_version`.
- Renaming a host is an explicit identity reset, not an automatic silent rename.
  If no peer is beyond `loopback_unprovisioned` and no sync key metadata or
  transport-current state exists, the app may rewrite `hostname` under the config
  lock. Otherwise the rename flow must stop sync, clear transport-current and
  ordering barrier state tied to the old origin host ID, generate new sync-key
  metadata for the new host ID, remove or mark stale managed authorized_keys and
  proof records that reference the old ID, and require regular-SSH
  reprovisioning before any peer can return to `ssh_keys_ready`.

## Commands

The main binary keeps normal user commands:

```text
clipfan daemon
clipfan copy
clipfan paste
clipfan version
```

It supports direct test-mode gateway invocations for local tests only:

```text
clipfan ssh-gateway --authorized-peer <peer-id> --authorized-key-id <key-id> version
clipfan ssh-gateway --authorized-peer <peer-id> --authorized-key-id <key-id> sync-stream
clipfan ssh-gateway --authorized-peer <peer-id> --authorized-key-id <key-id> receive
clipfan ssh-gateway --authorized-peer <peer-id> --authorized-key-id <key-id> probe-authorized-key
```

Production SSH never embeds `version`, `sync-stream`, `receive`, or
`probe-authorized-key` in the forced-command argv. Production uses the
forced-command contract below: fixed authorized_keys command argv plus the
requested verb in `SSH_ORIGINAL_COMMAND`.

Regular SSH provisioning uses `clipfan version --json` before final daemon
config exists. That command must not require `shared_key` or a running daemon.
It returns:

```json
{
  "version": "v0.4.0",
  "host_id": "fsck",
  "uid": 501,
  "effective_user": "jesse",
  "home_dir": "/home/jesse",
  "config_path": "/home/jesse/.config/clipfan/config.json",
  "state_dir": "/home/jesse/.local/state/clipfan",
  "install_path": "/home/jesse/.local/bin/clipfan",
  "ssh_protocols": [1],
  "configured": false
}
```

`host_id` comes from existing clipfan config when present, otherwise from the
same hostname derivation first-run config would use. A derived host ID in a
`configured:false` response is a candidate identity only; provisioning must
persist that host ID in the remote config before generating sync keys,
authorized_keys markers, proof records, or final shared-key config. A config
file that exists but lacks `hostname` is treated as `configured:false` for SSH
provisioning until the host-ID migration write succeeds. If the command cannot
determine `host_id`, effective user, or install path, Add Peer stops before
writing `shared_key`.

The gateway has two invocation modes:

- Direct test mode accepts a trusted argv verb and is used only by local unit or
  integration tests that execute `clipfan ssh-gateway ... version` directly.
- Forced-command mode is the only production SSH mode. It ignores any argv verb,
  takes the trusted peer identity from `--authorized-peer`, records the selected
  managed key from `--authorized-key-id`, and derives the requested verb only
  from `SSH_ORIGINAL_COMMAND`.

Over SSH, the forced command is always:

```text
/home/jesse/.local/bin/clipfan ssh-gateway --authorized-peer m4 --authorized-key-id 66402c9468c58941
```

The runtime SSH client requested command is one of these exact
`SSH_ORIGINAL_COMMAND` strings:

```text
version
sync-stream
receive
```

The pre-secret authorized-key probe uses one additional exact
`SSH_ORIGINAL_COMMAND` string:

```text
probe-authorized-key
```

The gateway reads `SSH_ORIGINAL_COMMAND`, accepts only the four exact requested
commands above, and maps them to the final verb. It rejects extra arguments,
shell metacharacters, empty commands, `scp`, `sftp`, `install`, `update`,
interactive TTYs, and any command whose trusted peer identity or key ID appears
only in `SSH_ORIGINAL_COMMAND`.

`probe-authorized-key` is not a command-locked runtime gateway purpose. It exists
only to prove that the just-written managed authorized_keys line is effective
before the fleet `shared_key` is written. It must not contact the local daemon,
read clipfan config, require `shared_key`, emit hello/version/state/ack frames,
read or write clipboard data, or perform install/update/config/service
mutation.

The app runs the pre-secret probe with the same sync private key, pinned
known_hosts file, and exact target user/host/port tuple that runtime SSH will
use:

```text
ssh
  -i ~/.config/clipfan/ssh/sync_ed25519
  -o BatchMode=yes
  -o IdentitiesOnly=yes
  -o ControlMaster=no
  -F /dev/null
  -o StrictHostKeyChecking=yes
  -o UserKnownHostsFile=~/.config/clipfan/ssh/known_hosts
  -o GlobalKnownHostsFile=/dev/null
  -o UpdateHostKeys=no
  -o ConnectTimeout=5
  -p <ssh_port>
  <ssh_user>@<ssh_host>
  probe-authorized-key
```

On success, `probe-authorized-key` writes exactly one newline-terminated JSON
object to stdout and exits 0:

```json
{"type":"clipfan-authorized-key-probe-v1","ok":true,"authorized_peer":"m4","authorized_key_id":"66402c9468c58941","gateway_path":"/home/jesse/.local/bin/clipfan","protocols":[1]}
```

Success stderr is empty. The app accepts the probe only when `authorized_peer`,
`authorized_key_id`, `gateway_path`, target user/host/port, and protocol support
match the managed line it just installed. On failure, the command exits non-zero,
writes no stdout JSON, writes only a stable one-line diagnostic code to stderr,
and never leaks config, `shared_key`, HMACs, nonces, clipboard bodies, or signed
frames. Unsupported command strings fail with `unsupported_gateway_command`; TTY
allocation fails with `tty_not_allowed`; missing/invalid forced-command metadata
fails with `invalid_forced_command`.

`version` uses the one-shot gateway handshake:

1. Caller writes `hello` with `purpose: "version"` to stdin.
2. Gateway verifies the hello.
3. Gateway writes its own hello to stdout.
4. Caller verifies the gateway hello.
5. Gateway writes signed version JSON to stdout and exits.

For runtime verbs, the gateway must not emit its hello, signed version JSON,
current state, ack, or any other signed/authenticated material until it has
verified the initiator hello for the requested purpose.

The version response is:

```json
{"type":"version","host_id":"fsck","daemon_version":"v0.4.0","protocols":[1],"sig":"hex-hmac"}
```

In JSON frames, `protocols` is an integer array. In canonical signing input,
that same list is encoded as a sorted, unique, comma-separated decimal list with
no spaces. The version signature is lower-case hex HMAC-SHA256 over these exact
UTF-8 bytes using the `clipfan-v1/ssh-hello-hmac` key:

```text
clipfan-ssh-version-v1
purpose=version
request_nonce=<initiator hello nonce>
response_nonce=<gateway hello nonce>
host_id=<gateway host_id>
daemon_version=<gateway daemon_version>
protocols=<gateway protocols>

```

Command-locked version checks require both the sync key and `shared_key`. Before
sync key/shared_key provisioning is complete, install/update code uses regular
SSH credentials and `clipfan version --json` instead.

If the gateway and caller have no common protocol version, it returns:

```json
{"type":"error","code":"unsupported_protocol","message":"peer requires clipfan daemon update"}
```

`receive` performs the on-demand hello/state/ack sequence defined below. It
never accepts clipboard state before a valid hello.

`sync-stream` is full-duplex newline-delimited JSON over stdin/stdout. It
bridges the SSH channel to the local daemon through loopback-only daemon APIs.

## Current Transport State

SSH sync needs a daemon-owned transport-current record that is distinct from
the existing OS clipboard echo guard and from `store.State`.

The daemon stores one current transport envelope in its state directory:

```json
{
  "current": {
    "id": "9f3a1c7b8e2d4a06b15c0f9e7d2a4b13",
    "origin": "magic-kingdom",
    "ts": "2026-06-01T12:34:56.789Z",
    "kind": "text",
    "body": "BASE64_CIPHERTEXT",
    "nonce": "BASE64_12_BYTE_AES_GCM_NONCE",
    "concealed": false,
    "local_image_path": ""
  },
  "current_null_reason": "",
  "ordering_barrier": {
    "id": "9f3a1c7b8e2d4a06b15c0f9e7d2a4b13",
    "origin": "magic-kingdom",
    "ts": "2026-06-01T12:34:56.789Z"
  }
}
```

`current` may be `null`. `current_null_reason` is persisted with `current` and
`ordering_barrier` so restart cannot turn a concealed or user-cleared no-op
marker into startup no-current readiness. When `current` is non-null,
`current_null_reason` must be the empty string. When `current` is null,
`current_null_reason` must be `no_visible_current`, `concealed_clear`, or
`user_cleared_current`. `no_visible_current` is valid only when
`ordering_barrier` is empty. `ordering_barrier` is persisted independently of
`current` and records the greatest ordering key the daemon has accepted or
created locally, including concealed local clips that are not sent.
`local_image_path` is persisted-local-only metadata for clipboard writeback and
history. It is never included in SSH `state` frames, canonical watch state JSON,
HMAC input, peer logs, or UI sync details.

Transport-current persistence is correctness state, not a cache. The primary
file is `<state_dir>/transport-current.json`; the last known-good backup is
`<state_dir>/transport-current.prev.json`; the lock is
`<state_dir>/transport-current.lock`. The state directory must be mode `0700`;
both JSON files and lock files are regular files, target-user-owned, mode
`0600`, and never symlink final components. Every write takes the transport
state lock, validates the existing primary when present, writes a temporary
backup from the existing primary when valid, fsyncs and renames it to
`transport-current.prev.json`, writes the new combined `current`,
`current_null_reason`, and `ordering_barrier` JSON to a temporary file, fsyncs
it, renames it to
`transport-current.json`, and fsyncs the state directory. The daemon persists
the ordering barrier, current, and null reason in the same JSON document so a
visible current or no-op null marker cannot be published without the barrier
that orders it.

On startup, the daemon first loads and validates `transport-current.json`. If it
is invalid, it loads `transport-current.prev.json`, records
`transport_current_recovered_from_backup`, and continues from that last
known-good barrier/current/null-reason. If both files are absent, first-run and
post-reset startup use `current:null`, `current_null_reason:"no_visible_current"`,
and empty barrier. During legacy-field recovery, a missing `current_null_reason`
with `current:null` and empty barrier becomes `no_visible_current`; a missing
`current_null_reason` with `current:null` and non-empty barrier becomes
`user_cleared_current` and records `transport_current_null_reason_recovered` so
the daemon preserves the barrier and does not report false green readiness. A
missing `current_null_reason` with non-null `current` becomes the empty string.
Unknown null reasons or `no_visible_current` with a non-empty barrier make that
transport-current file invalid. If either file exists but neither primary nor
backup validates, the daemon records `transport_state_corrupt`, does not start
SSH sync, and local current/watch/gateway state endpoints return
`transport_state_corrupt` until the user runs a confirmed
`reset_corrupt_transport_state_clear_barrier` or local fleet-reset recovery. It
must not silently start with an empty ordering barrier after a corrupt existing
transport state file.

`recipient` is not persisted. The daemon fills `recipient` when a caller asks
for current state for a specific next hop.

In the SSH/current transport envelope, `body` is AES-GCM ciphertext using the
`clipfan-v1/body-aead` key derived from the fleet `shared_key` and a random
nonce, with no additional authenticated data. Legacy peer HTTP envelope bodies
use the existing `SHA256(raw shared_key)` body key only while that transport
remains in its pre-cutover compatibility window; SSH receivers never fall back
to the legacy body key.
For `kind:"text"`, the decrypted body is the UTF-8 text bytes already used by
the existing HTTP envelope. For `kind:"image"`, the decrypted body is the image
payload bytes, currently PNG bytes as produced by the existing clipboard/image
path. Receivers never use a sender's local image path to reconstruct an image:
after decrypting an image envelope, `ApplyEnvelope` writes the bytes to the
receiver's own image store, records the receiver-local image path in local state
and history, and writes the local OS clipboard through the existing image
writeback path. A text envelope whose decrypted body is a clipfan image-store
path for the receiver returns `ignored_echo` and is not written or relayed,
because it is an image representation echo that would clobber the real image.
An image envelope that decrypts but cannot be saved, exceeds the payload limit,
or cannot be written to the local clipboard returns `rejected` with a stable
reason code.
`recipient` is not authenticated inside the ciphertext. In legacy HTTP, the
signed request authenticates the whole JSON envelope in transit; in SSH v1, the
hello-verified SSH channel authenticates the frame in transit. Rewriting only
the outer `recipient` field per next hop is therefore compatible with the
existing envelope format and preserves recipient binding because the receiver
checks the outer recipient before applying the clip. If a future protocol binds
recipient as AEAD associated data, senders must reseal per next hop.

Local copies and restores update this record after minting a clip ID and
sealing the body. Received peer envelopes update this record only when the
daemon applies them as the local current clip. Concealed local clips are never
stored as transport-current state. When a local concealed clip is observed, the
daemon mints a clip ID, persists it as `ordering_barrier`, clears any previous
`current` record, persists `current_null_reason:"concealed_clear"`, and emits a
null current event with
`null_reason:"concealed_clear"` to active streams.
That null event is a no-op for peers, but it prevents reconnects from resending
an older visible clip as if it were still local latest. Inbound concealed
envelopes never replace transport-current state.

Transport-current retention is intentional and user-visible. It stores only one
encrypted latest visible envelope for reconnect convergence; it is separate from
local clipboard history. Clearing local history does not clear transport-current
unless the user chooses a clear-current action that says sync current will also
be cleared. The normal user operation is
`clear_visible_current_preserve_barrier`: it clears `current`, preserves the
ordering barrier, persists `current_null_reason:"user_cleared_current"`, emits
null current events to active streams, and prevents later reconnect from
resending the cleared visible clip. User-initiated clear-current events use
`null_reason:"user_cleared_current"`. It must not share handlers, confirmation
copy, or tests with
`reset_corrupt_transport_state_clear_barrier`, which clears both current and the
ordering barrier only after primary and backup transport state are corrupt or as
part of the later full fleet-reset extension. Disabling or removing one peer
stops sends to that peer immediately but does not erase the global
transport-current record; if the peer is re-enabled later, the current latest
visible state may be sent unless the user cleared sync current. The peer-disable
UI must say this. Fleet reset, loss of `shared_key`, or re-enrollment with a new
fleet credential deletes transport-current encrypted under the old key; old
in-flight envelopes are discarded.

On startup, a daemon loads `current`, `current_null_reason`, and
`ordering_barrier` through the transport-current recovery policy. It can send
`current` to peers before any new local clipboard change occurs, and it uses
`ordering_barrier` to reject stale visible state even when `current` is null
because the latest local clip was concealed or user-cleared.

Migration from pre-SSH state does not backfill transport-current from
`store.State`, `current.txt`, or image-store paths. Those files do not contain a
complete encrypted envelope, recipient/origin metadata, or an ordering barrier.
After migration, the first newly observed local visible clipboard item is minted
as a fresh transport envelope and becomes current; the first received peer state
can also populate transport-current if it wins ordering. Existing local history
and image files remain local history only.

The persisted ordering key is:

```text
(timestamp, origin host ID, clip ID)
```

The same key is used for local apply decisions, peer relay, current persistence,
and tests. `store.State` remains the local shim state for xclip/wl-paste and
does not contain enough data to drive SSH sync by itself.

Local clipboard changes, history restores, and inbound `ApplyEnvelope` calls are
serialized through one daemon ordering lock before comparing or updating the
ordering barrier. The daemon persists the new barrier/current state before
emitting watch events or relay work for that state.

## Local Daemon Bridge APIs

The daemon keeps loopback HTTP as the local control API and adds local-only
signed endpoints for the gateway process. The in-process session manager may
call the same daemon methods directly, but the HTTP bridge remains the contract
for forced-command gateway processes.

| Method & path | Auth | Purpose |
|---------------|------|---------|
| `GET /v1/current?recipient=<peer-id>` | signed request, signed response, loopback only | Return the current latest payload for that recipient, `{"sender":"<local>","clip":...}`, or `{"sender":"<local>","clip":null,"null_reason":"<persisted-null-reason>"}` when `current` is null. The null reason comes from transport-current recovery and is `no_visible_current`, `concealed_clear`, or `user_cleared_current`. This is not an SSH `state` frame and has no `seq`. |
| `GET /v1/current/watch?recipient=<peer-id>&include_snapshot=true` | signed request, per-event HMAC, loopback only | Atomically register a watch and stream an initial current-payload snapshot followed by changes for one recipient as newline-delimited events. Events carry local `watch_seq` for per-watch ordering and signing. They are not SSH `state` frames and have no SSH `state.seq`. |
| `POST /v1/current/receive` | signed request/response, loopback only | Submit one SSH `state` frame through the daemon apply path and return a structured receive result. |
| `POST /v1/ssh/sessions` | signed request/response, loopback only | Reserve an inbound gateway slot for `peer_id` and `purpose`; returns a lease token or `too_many_sessions`. |
| `PATCH /v1/ssh/sessions/<token>` | signed request/response, loopback only | Renew an active gateway session lease; returns the renewed expiration or a stable stale-token error. |
| `DELETE /v1/ssh/sessions/<token>` | signed request/response, loopback only | Release a gateway session reservation. |

Reservation request body:

```json
{"peer_id":"fsck","purpose":"sync-stream","gateway_pid":12345}
```

Reservation success response:

```json
{"token":"...","expires_at":"2026-06-01T12:00:30Z","renew_after_seconds":10}
```

Reservation tokens are opaque CSPRNG values with at least 256 bits of entropy,
encoded as unpadded base64url or hex. They do not encode `peer_id`, purpose,
PID, timestamps, or secrets. The daemon compares them in constant time where
practical, never writes raw tokens to default logs/status, and redacts them as
`reservation_token:<redacted>` in diagnostic exports.

Renewal request body repeats `peer_id`, `purpose`, and `gateway_pid` so a stale
token cannot be renewed for a different gateway context. Renewal success returns
the same response shape with the same token and a later `expires_at`. Unknown
tokens return `reservation_not_found`; expired tokens return
`reservation_expired` and do not recreate capacity; peer, purpose, or PID
mismatches return `reservation_mismatch`. The gateway must close without sending
additional version or state frames after renewal failure. Renewal is accepted for
`version`, `receive`, and `sync-stream`, but only `sync-stream` is required to
renew routinely; short `version` and `receive` gateways normally release before
the first renewal.

These endpoints are local implementation details. They do not relax the local
API security model and are not reachable off-host after the loopback migration.
Gateway processes authenticate to loopback APIs with the same config v2 local
request auth as new app and CLI clients: the `shared_key` is read from the
user-owned clipfan config, the request carries
`X-Clipfan-Auth-Version: clipfan-v1/request-hmac`, and the signature uses the
HKDF-derived `clipfan-v1/request-hmac` key plus canonical request/response form
defined in Safe Mode. Config directories are mode `0700`, config files and
private keys are mode `0600`. A compromised same-Unix-user process can read the
same config and is outside the security boundary; other unprivileged users on a
multi-user host should be blocked by file permissions and loopback request
signing.

The local current APIs return protocol payloads, not wire frames. The
gateway/session writer wraps each payload as an SSH `state` frame and assigns
the outbound-direction `seq` immediately before writing to the SSH channel.

`POST /v1/current/receive` calls a single daemon method, `ApplyEnvelope`, that
is also used by the legacy HTTP `/v1/clip` handler until that off-host path is
removed. After the SSH cutover, any remaining `/v1/clip` handler is
loopback/local-test only; off-host use is unsupported and release-gated against.
`ApplyEnvelope` returns:

```json
{
  "status": "applied",
  "id": "9f3a1c7b8e2d4a06b15c0f9e7d2a4b13",
  "current_changed": true,
  "origin": "magic-kingdom",
  "message": ""
}
```

Allowed statuses are:

- `applied`: the envelope advanced the ordering barrier, became local current
  state, and is eligible for relay.
- `ignored_seen`: the clip ID was already processed.
- `ignored_echo`: the payload is an echo of the daemon's own local write or an
  image-store path representation that must not clobber the real image.
- `ignored_older`: the envelope's ordering key is not greater than the
  persisted ordering barrier.
- `ignored_concealed`: the envelope has `concealed: true`.
- `rejected`: the envelope is malformed, addressed to the wrong recipient, has
  an invalid timestamp, cannot be decrypted, exceeds payload limits, or cannot
  be applied.

Every early return in the existing receive path must map to one of these
statuses. The SSH ack status is copied from this result; gateway code must not
infer or fabricate ack statuses from logs or side effects.

The watch endpoint streams newline-delimited events. With
`include_snapshot=true`, watch registration and the initial snapshot are
performed under the same daemon ordering lock/revision used for local clipboard
changes and inbound apply. The first event is always a `snapshot` event for the
current state at the registered watch sequence, including `clip:null` when no
visible current exists. Null payloads always include `null_reason`, one of
`no_visible_current`, `concealed_clear`, or `user_cleared_current`. Any local
current change after that sequence is emitted as a later `current` event. There
is no gap between snapshot and subscription. This is the local daemon API
contract: clients that read directly from the watch stream see the snapshot
event first. When the snapshot has `clip:null`, the daemon uses the persisted
`current_null_reason` loaded through transport-current recovery. If the
persisted field was absent, the recovery rule derives `no_visible_current` only
for an empty ordering barrier; a non-empty barrier recovers to non-qualifying
`user_cleared_current` and records `transport_current_null_reason_recovered`.

```json
{"type":"snapshot","watch_seq":7,"ts":1780257600,"payload":{"sender":"m4","clip":null,"null_reason":"no_visible_current"},"sig":"hex-hmac"}
```

The request is authenticated with the normal local request signature. Each
event signature is lower-case hex HMAC-SHA256 using the
`clipfan-v1/ssh-hello-hmac` key over:

```text
clipfan-local-current-watch-v1
event_type=<snapshot|current>
watch_seq=<decimal sequence>
ts=<unix seconds>
payload_sha256=<hex sha256 of canonical current payload JSON>

```

Canonical current payload JSON is the compact object encoding with fixed field
order: `sender`, `clip`, and `null_reason` only when `clip` is null. When `clip`
is non-null its fields are ordered `id`, `origin`, `recipient`, `ts`, `kind`,
`body`, `nonce`, `concealed`. Timestamps are RFC3339Nano UTC strings. The SSH
writer converts this payload into a `state` frame by adding `type:"state"` and a
fresh outbound-direction SSH `seq`; the local `watch_seq` is not forwarded on the
SSH wire. Event `type` is either `snapshot` for the initial atomic snapshot or
`current` for later changes.

The gateway verifies each event before writing the corresponding SSH state
frame. Watch streams use a per-peer single-slot latest queue for SSH writes. If
the initial snapshot has been queued but no SSH state frame has been written yet,
a newer `current` event may supersede that queued snapshot; the peer receives
only the newer state. If the snapshot has already been written to SSH, later
`current` events are written or coalesced as later updates. When a peer is slow,
a newer current event replaces an older queued current event because the
transport promises latest-state convergence, not history replay. If a write is
blocked longer than the stream write timeout, the gateway closes the stream and
the session manager reconnects or falls back to on-demand delivery.
Each replacement increments per-peer `superseded_state_drops` and records the
dropped/replacement relationship in logs. Default copyable logs and diagnostic
exports expose counters, peer ID, and stream ID but not clip IDs or clipboard
contents; only explicit developer logs may include dropped and replacement clip
IDs according to the redaction matrix. Status exposes counters, not clipboard
contents, so the app can explain "newer clip replaced queued clip" without
leaking data.

Default peer and daemon status exposes these numeric observability fields:
`active_ssh_processes`, `ssh_process_limit`, `active_ssh_processes_by_purpose`,
`large_frame_decoders_in_use`, `large_frame_decoder_limit`,
`superseded_state_drops`, `dropped_remote_states_by_reason`,
`reservation_active`, `reservation_expired`, `reservation_failures_by_code`,
`on_demand_attempts`, `on_demand_successes`, and `on_demand_failures_by_phase`.
None of these counters include clip IDs, plaintext clipboard content, encrypted
body bytes, signatures, or nonce values. The UI may show aggregate counts and
stable failure codes; copyable logs keep the same redaction policy.

Inbound gateway processes reserve session capacity before sending hello. The
reservation includes peer ID, purpose, PID, and a short lease expiration. The
gateway releases the lease on clean exit, and the daemon expires abandoned
leases so crashed gateway processes do not permanently consume capacity.
Reservations are required for `version`, `receive`, and `sync-stream`; a forced
command that cannot reserve capacity returns `too_many_sessions` before hello.
Long-lived `sync-stream` gateways renew the signed lease every 10 seconds
through `PATCH /v1/ssh/sessions/<token>`. A gateway that misses renewal or
receives renewal failure closes before sending more protocol frames. Expiration
releases capacity; it does not authorize the old gateway to continue.

If the forced-command gateway cannot reach the local loopback daemon, cannot
authenticate its loopback reservation request, or receives an unexpected local
daemon response, it writes one best-effort error frame to stdout and exits
without emitting hello, version, state, ack, or signed material. Stable codes
are `daemon_unavailable`, `local_auth_failed`, and `daemon_protocol_error`.
These errors apply to command-locked `version` as well as clipboard purposes.
If stdout is already unusable, the gateway exits non-zero and writes only a
redacted stderr summary. Remote callers surface the stable code when present and
otherwise show `gateway_failed_before_hello`.

Managed `authorized_keys` is necessary but not sufficient for inbound sync. For
`ssh_keys_ready`, the daemon accepts an inbound gateway reservation only when
local config contains a matching `ssh.peers[].id`, that peer has `enabled:true`,
`accept:true`, the peer migration state is `ssh_keys_ready`, and current local
inbound material validates against the peer record: managed authorized_keys line,
`proof.accept_key_id`, gateway path, owner/mode, and supported option set all
match. Missing,
disabled, removed, or unconfigured peers get
`{"type":"error","code":"peer_not_configured"}` before hello.
`ssh_material_staged` and other configured-but-unready states get
`{"type":"error","code":"peer_not_ready"}` before hello. Removing a peer
locally therefore stops inbound sync even if a stale remote authorized_keys line
cannot be removed immediately.
Local proof or permission drift for an `ssh_keys_ready` accept peer also returns
`peer_not_ready`, records `permission_drift` or the more specific proof failure
warning, and does not reserve capacity.

`shared_key_written_unverified` is the only exception to the `ssh_keys_ready`
reservation rule, and only for purpose `version`. A peer in that state may
reserve a command-locked `version` gateway based only on the forced-command
peer ID, purpose, capacity, `enabled:true`, `accept:true`, and persisted
`migration_state:"shared_key_written_unverified"`. The daemon deliberately does
not require persisted local proof validation in this state because the
forced-command authorized_keys line has already selected the peer identity, and
it does not know whether the remote has the right `shared_key` at reservation
time.
After reservation succeeds, the gateway still sends no version data until the
client-first hello HMAC verifies with `clipfan-v1/ssh-hello-hmac`. A wrong or
missing `shared_key` therefore fails during hello with `bad_auth`, not during
reservation. The same peer must be rejected for `receive` and `sync-stream` with
`{"type":"error","code":"peer_not_ready"}` before hello. Clipboard sync never
starts from `shared_key_written_unverified`; successful command-locked version
verification plus promotion is what moves the peer to `ssh_keys_ready`.

The gateway process does not read or write clipboard state directly. It talks
to the daemon so existing dedup, echo suppression, image handling, tmux loading,
history recording, and relay semantics remain centralized.

## SSH Frame Protocol

All stream frames are newline-delimited JSON objects. The maximum decrypted
clipboard payload is 64 MiB. Because state frames carry base64 ciphertext and
JSON overhead, the maximum encoded frame line is 90 MiB. Decoders enforce both
limits: reject an encoded line over 90 MiB before JSON decoding, and reject a
decrypted payload over 64 MiB before writing local clipboard state.

Frame readers use bounded buffering and backpressure. Each session may buffer at
most 1 MiB without holding the process-wide large-frame semaphore. If the
current encoded line would grow past 1 MiB, the reader must acquire the
large-frame semaphore before reading more bytes from the SSH pipe. While waiting
for the semaphore, the reader stops draining that SSH channel and the normal SSH
channel/window backpressure applies. Once the semaphore is held, that session may
grow one encoded frame buffer up to the 90 MiB limit, decode it, apply/reject it,
and then release the semaphore. The default semaphore capacity is 2, and no
reader uses an unbounded scanner or allocates a second encoded frame buffer.
Sessions waiting on the semaphore remain subject to the normal read and write
deadlines.

Large-frame work runs on daemon sync workers, not on the Mac app main actor or
the clipboard polling loop. The app receives only status snapshots and bounded
logs over loopback. While a large image frame is decoding or applying, clipboard
watch sends for that peer continue to coalesce through the single-slot latest
queue; they do not accumulate an unbounded backlog and they do not block local
UI refresh. If a large frame holds the semaphore long enough to miss write,
read, or ping/pong deadlines, the session is closed and normal reconnect or
on-demand fallback handles the next latest state.

Default resource budget for large payloads: at most two sessions may hold
maximum-size encoded frame buffers or encrypt maximum-size outbound frames at
once. Total encoded-frame memory is therefore bounded by two 90 MiB large-frame
buffers plus up to 1 MiB per other active session waiting for the semaphore, plus
decrypted payload buffers for the active semaphore holders and Go runtime
overhead. Persistent streams and on-demand sends share this same semaphore and
payload budget; on-demand fallback does not get a separate large-frame pool.
Low-resource peers may lower the configured payload limit, but they must
advertise the same protocol version and reject oversized frames with
`payload_too_large` rather than truncating or partially applying them. The
sender records orange runtime health for that peer with `payload_too_large` and
keeps the local current state; smaller future clips may still sync.

Protocol JSON interoperability rules:

- Senders emit compact UTF-8 JSON with no insignificant whitespace and object
  fields in the order shown in this spec for deterministic fixtures.
- Receivers accept any JSON object member order for protocol frames.
- Duplicate JSON object keys are rejected with `duplicate_field`.
- Unknown top-level frame fields are rejected in protocol v1 with
  `unknown_field`; future extensibility requires a new protocol version.
- Numeric fields that appear in HMAC signing inputs are encoded as decimal text
  in the signing input, not as raw JSON bytes.
- Raw JSON bytes are never HMAC input except where this spec explicitly says a
  SHA-256 hash of canonical current payload JSON is signed for local watch
  events.

### Hello

The SSH client side always sends the first `hello` for `version`, `receive`,
and `sync-stream`. The forced-command gateway verifies that initiator hello and
then sends its own `hello`. No mode uses simultaneous hellos.

Each `hello` frame has this shape:

```json
{
  "type": "hello",
  "protocols": [1],
  "purpose": "sync-stream",
  "host_id": "m4",
  "peer_id": "fsck",
  "ts": 1780257600,
  "nonce": "8f4d4f4b4d9847d5a6b7c8d9e0f1a2b3",
  "sig": "hex-hmac"
}
```

The JSON `protocols` array must contain sorted, unique positive integers. The
canonical signing input encodes that array as comma-separated decimal text with
no spaces. The hello signature is lower-case hex HMAC-SHA256 using the
`clipfan-v1/ssh-hello-hmac` key over these exact UTF-8 bytes:

```text
clipfan-ssh-hello-v1
host_id=<host_id>
peer_id=<peer_id>
purpose=<purpose>
protocols=<comma-separated protocols>
ts=<unix seconds>
nonce=<nonce>

```

The receiver verifies:

- the hello timestamp is within 2 minutes before or after the receiver's clock,
- `host_id` is allowed by config and by the forced-command
  `--authorized-peer` when running on the gateway side,
- nonce has not been seen recently for the authenticated remote host ID and
  purpose,
- HMAC verifies with the `clipfan-v1/ssh-hello-hmac` key derived from
  `shared_key`.

The SSH hello freshness window is 2 minutes. The nonce replay cache is in-memory
and keyed by `(authenticated_remote_host_id, purpose, nonce)`, where
`authenticated_remote_host_id` is the hello `host_id` after config,
forced-command, and HMAC checks prove it is the sender of that hello. The hello
`peer_id` is the receiver/local host ID and is not used as the replay-cache peer
dimension. Entries are retained for 4 minutes. Default caps are 256 live nonces
per `(authenticated_remote_host_id, purpose)` bucket and 8192 live nonces
process-wide. If a bucket or process cap is reached, the oldest expired entries
are removed first; if the cap is still full, the oldest entries in that bucket
are evicted and the eviction is counted in status as
`nonce_replay_cache_evictions`. A daemon restart loses the nonce cache; replay
resistance after restart relies on the timestamp window and the fact that a
replayed hello alone cannot submit clipboard state without a live hello-verified
SSH channel.

After both hellos are valid, each side selects the highest protocol number that
appears in both `protocols` lists. If no common protocol exists, the session is
not established.

If protocol versions do not overlap, the receiver sends
`{"type":"error","code":"unsupported_protocol"}` and closes the session. The app
surfaces this as "peer requires clipfan update" with the local and remote daemon
versions when available.

### State

After hello, each side sends its current latest state:

```json
{
  "type": "state",
  "seq": 42,
  "sender": "m4",
  "clip": {
    "id": "9f3a1c7b8e2d4a06b15c0f9e7d2a4b13",
    "origin": "magic-kingdom",
    "recipient": "fsck",
    "ts": "2026-06-01T12:34:56.789Z",
    "kind": "text",
    "body": "BASE64_CIPHERTEXT",
    "nonce": "BASE64_12_BYTE_AES_GCM_NONCE",
    "concealed": false
  }
}
```

`seq` is an unsigned state sequence scoped to one sender direction within one
SSH session. Each side starts its own outbound sequence at 1 and increments by 1
for every `state` frame it sends on that session. The two directions do not
share a sequence namespace, so a full-duplex stream can have both peers send
`seq:1` without conflict. `clip` is the existing encrypted envelope. The sender
rewrites only
`recipient` for the next hop. `origin` remains the true origin so existing
dedup and history attribution keep working.

`sender` is the immediate authenticated transport peer for this SSH channel, not
necessarily the clip's true origin. After hello, the receiver binds the channel
to the authenticated peer ID. Every `state.sender` on that channel must equal
that authenticated peer ID. A mismatch writes one `error` frame with code
`sender_mismatch`, records a protocol error, closes the SSH channel, and does not
submit the frame to `ApplyEnvelope`. Relay and status use the authenticated
channel peer as `last_recv_peer_id`; they must not trust a mismatched
`state.sender`.
Receivers reject missing, zero, repeated, decreasing, or skipped `seq` values by
writing one `error` frame with code `protocol_error`, recording the stable reason
`bad_sequence`, closing the SSH channel, and not submitting the frame to
`ApplyEnvelope`. Receivers track only the inbound sequence from the authenticated
peer for that SSH session. Each new SSH session starts a new sequence space at 1
for each direction.

If no current clip exists, the sender sends:

```json
{"type":"state","seq":43,"sender":"m4","clip":null,"null_reason":"no_visible_current"}
```

When `clip` is null, `null_reason` is required and must be one of
`no_visible_current`, `concealed_clear`, or `user_cleared_current`. Null state
frames bypass `ApplyEnvelope` because there is no encrypted envelope to apply,
but they are still well-formed `state` frames and must be acknowledged with
`status:"no_state"` after sender, sequence, and null-reason validation.
`no_visible_current` means the sender currently has no sync-eligible visible
clip and may count for readiness when acked. `concealed_clear` and
`user_cleared_current` are no-op sync markers; they are acked but must not mark
`ssh_sync_ready`, update last successful push timestamps, or clear warning state.
Senders choose the null reason from the persisted transport-current
`current_null_reason`; they must not synthesize `no_visible_current` when a
non-empty local ordering barrier exists.

### Ack

Receivers acknowledge each state frame:

```json
{
  "type": "ack",
  "seq": 42,
  "id": "9f3a1c7b8e2d4a06b15c0f9e7d2a4b13",
  "status": "applied",
  "reason": ""
}
```

Ack `seq` must equal the state frame's `seq`. For non-null state, `id` is the
clip ID. For null state, `id` is empty and `seq` is the correlation key. Ack
frames never include `null_reason`; the sender computes readiness and
last-push effects by looking up the in-flight outbound `state` frame by `seq`
and using that frame's validated `null_reason`.

Allowed statuses are:

- `no_state`
- `applied`
- `ignored_seen`
- `ignored_echo`
- `ignored_older`
- `ignored_concealed`
- `rejected`

The ack is used for peer health and logs; correctness does not depend on
exactly-once delivery. `no_state` is used for `clip:null` frames and has an
empty `id`. Because the ack schema omits `null_reason`, an unknown or already
retired `seq` cannot qualify for green readiness even if its status is
`no_state`; it is logged as an uncorrelated ack and treated as neutral or
warning according to the session state. `reason` is empty for `applied` and
`no_state`, and a stable short code for other non-applied statuses, such as
`seen`, `echo`, `older`,
`concealed`, `wrong_recipient`, `bad_timestamp`, `decrypt_failed`,
`payload_too_large`, or `apply_failed`. Detailed text stays in redacted local
logs.

Ack health effects are normative:

| Ack status | Readiness effect | Last-push effect | Notes |
|------------|------------------|------------------|-------|
| `applied` | qualifies | success | Receiver accepted a non-null latest state. |
| `no_state` with `null_reason:"no_visible_current"` | qualifies | success | Sender has no visible sync-eligible current state and receiver acknowledged that fact. |
| `no_state` with `null_reason:"concealed_clear"` or `user_cleared_current` | does not qualify | neutral | Valid no-op marker, but not a successful latest-state exchange for green readiness. |
| `ignored_seen` | qualifies | success | Receiver already had the same non-null state ID. |
| `ignored_echo` | qualifies | success | Receiver identified the state as an echo of its own current state. |
| `ignored_older` | does not qualify | neutral | Sender's state is stale; wait for the peer's newer state or another local current event. |
| `ignored_concealed` | does not qualify | failure/warning | Concealed state should not have been sent as sync current. |
| `rejected` | does not qualify | failure/warning | Includes decrypt failure, wrong recipient, payload-too-large, bad timestamp, and apply failure. |

Only qualifying statuses may set `transport_health:"ssh_sync_ready"` or update
last successful push timestamps. Non-qualifying statuses may still prove the
channel is alive, but they leave health at
`transport_connected_no_clip_exchange`, `backoff`, or a warning state according
to the failure.

Ack frames are only for well-formed `state` frames. Non-null state frames must
reach `ApplyEnvelope` before ack. Null state frames bypass `ApplyEnvelope` and
are acked as `no_state` after validation. Protocol violations before apply,
including sender mismatch, bad sequence, malformed JSON, duplicate JSON fields,
invalid frame order, unknown frame type, invalid or missing `null_reason`, or any
state before successful hello, return one `error` frame with a stable `code` and
close the SSH channel. They do not return `ack` with `status:"rejected"`.

### Ping

Long-lived streams exchange keepalives:

```json
{"type":"ping","ts":1780257600}
{"type":"pong","ts":1780257600}
```

Missing pongs close the session and trigger reconnect backoff.

Default timing constants:

- SSH connect timeout: 5 seconds.
- Hello deadline after SSH channel open: 5 seconds.
- Per-frame idle/progress timeout: 10 seconds without read or write progress on
  the current frame. Any successful read or write progress resets this idle
  timeout.
- Per-frame absolute transfer deadline:
  `max(10 seconds, ceil(encoded_frame_bytes / 1 MiB/s) + 5 seconds)`, capped at
  120 seconds from the start of that frame. Progress does not reset this
  absolute deadline. Links slower than the modeled 1 MiB/s budget may time out by
  design and show orange health with a copyable `state_frame_timeout` log rather
  than silently lowering payload limits.
- Ping interval on idle streams: 30 seconds.
- Pong timeout: 10 seconds after ping write completes.
- On-demand latest-state ack freshness window for readiness without a live
  writable persistent stream: 60 seconds after the ack.
- On-demand command total deadline is
  `connect timeout + hello deadline + local state frame absolute deadline +
  remote state frame absolute deadline + 20 seconds`, capped at 300 seconds.
  Null-state frames use the 10 second absolute-deadline minimum. The per-frame
  idle/progress timeout still applies inside the on-demand command, but the
  command total deadline is the wall-clock cap.
- Session reservation lease: 30 seconds, renewed by active gateways every
  10 seconds.

Large-frame performance target: a 64 MiB binary image payload, which can encode
to a frame near the 90 MiB line limit, must complete over a sustained 5 MiB/s
SSH channel without hitting protocol deadlines in persistent or on-demand mode.

### Error

Protocol errors are structured:

```json
{"type":"error","code":"bad_auth","message":"hello signature failed"}
```

The Mac app may include sanitized `message` text in copyable peer logs, but UI
labels and actions are driven only by stable `code` values. Messages longer than
256 bytes are truncated with a visible marker; invalid UTF-8 and control
characters are escaped before display or export.

### Post-Hello Integrity

SSH transport v1 does not add per-frame signatures for `state`, `ack`, `ping`,
`pong`, or `error` frames. After the mutual hello succeeds, those frames are
trusted only because they arrive on the same SSH channel that authenticated the
sync key and completed the `shared_key` handshake. Ack, ping, pong, and error
frames are health signals, not clipboard authority; clipboard authority still
comes from `ApplyEnvelope` validation of encrypted state frames.

If a frame arrives before hello, after a failed hello, or on a channel whose
session reservation failed, the gateway ignores it and closes the channel.
Per-frame signatures can be added in a later protocol version, but v1
implementations must not invent incompatible ad hoc signatures.

## Persistent Session Lifecycle

Each daemon has an SSH session manager.

For every configured peer with `connect: true` and `persistent: true`, it runs:

```text
ssh
  -i ~/.config/clipfan/ssh/sync_ed25519
  -o BatchMode=yes
  -o IdentitiesOnly=yes
  -o ControlMaster=no
  -F /dev/null
  -o StrictHostKeyChecking=yes
  -o UserKnownHostsFile=~/.config/clipfan/ssh/known_hosts
  -o GlobalKnownHostsFile=/dev/null
  -o UpdateHostKeys=no
  -o ConnectTimeout=5
  -p <ssh_port>
  <ssh_user>@<ssh_host>
  sync-stream
```

The remote forced command handles the request; the requested command exists so
`SSH_ORIGINAL_COMMAND` is explicit and auditable.

SSH control multiplexing is disabled for clipfan-managed sync sessions. Each
persistent stream and on-demand send is its own SSH process/channel with its own
lifecycle, logs, timeout, and forced-command execution.

The session manager owns SSH child-process lifecycle. It drains stdout and
stderr concurrently, redacts stderr before adding it to peer logs, cancels child
processes on daemon shutdown or session timeout, sends SIGTERM before SIGKILL
when available, waits for every child to avoid zombies, and starts SSH with a
sanitized environment containing only `HOME`, `USER`, `LOGNAME`, `PATH`,
`SSH_AUTH_SOCK` when regular SSH operations explicitly need the user's agent,
and `TERM=dumb` for commands that require a terminal value. Runtime sync-key SSH
does not need `SSH_AUTH_SOCK`.

Session startup:

1. Open SSH.
2. Complete mutual `hello`.
3. Start read and write loops concurrently.
4. Open `GET /v1/current/watch?recipient=<peer-id>&include_snapshot=true` after
   the read loop is already draining remote frames.
5. Send the watch `snapshot` event as the first local `state` frame.
6. Send each later watch `current` event as a `state` frame with recipient set to
   the stream peer.
7. Receive peer `state` frames and submit them to the local daemon.

Implementations must not write the initial current state synchronously before
starting the read loop, and must not fetch a snapshot before registering the
watch. Large frames can be up to 90 MiB, so both sides need to drain while
writing. Acks include the clip ID for non-null state and `no_state` for null
state; health code correlates acks by state ID where present and by explicit
`seq` for null state.

Reconnect behavior:

- Initial retry after 1 second.
- Exponential backoff with jitter up to 5 minutes.
- Manual app action can request immediate reconnect.
- On reconnect, both sides send current latest state. No backlog is replayed.

Duplicate persistent sessions are allowed only transiently during connection
races before arbitration completes. They are not a content correctness problem
because clip IDs, tuple ordering, and echo suppression already deduplicate the
content path, but they are still a resource and status problem. Peer health
coalesces transient duplicates under the same peer ID and reports only the
retained stream after arbitration. Each daemon starts at most one outbound
persistent session per peer, runs at most one on-demand send per peer, and caps
total SSH child processes at 16 by default. Extra inbound gateway processes fail
session reservation and return `{"type":"error","code":"too_many_sessions"}`
before hello. If two persistent sessions survive hello for the same peer pair,
the reciprocal-stream arbitration rule closes the non-retained stream with
`duplicate_stream_replaced`; implementation must not keep multiple established
long-lived streams for the same peer pair.

`max_sessions_per_peer` counts every active SSH child process or inbound gateway
reservation for that peer across `version`, `receive`, `sync-stream`,
persistent streams, and on-demand sends, regardless of direction. The default
value 2 permits one long-lived sync stream plus one short-lived version or
on-demand operation. Reciprocal persistent peers use the arbitration rule above
to retain at most one long-lived stream. A persistent stream that has lost
writable latest-state capability must be closed before starting on-demand
fallback for the same peer, so fallback does not exceed the per-peer cap. If the
cap is still full after duplicate-stream arbitration, the new operation fails
with `too_many_sessions`, records orange health, and does not spawn another SSH
process.

Persistent writers keep at most one queued state frame per peer. A new local
current state replaces an older queued state for the same peer because the
protocol only promises latest-state convergence. If the SSH write blocks longer
than the write timeout, the session is closed, health turns orange, and the
normal reconnect or on-demand path carries the newest available state later.
Latest-only replacement is observable through the same
`superseded_state_drops` counter used by watch streams.

Startup performance target: with 50 configured peers, daemon startup should load
config/state, compute peer status snapshots, and answer local loopback
health/status within 500 ms on a typical Mac. Connection attempts are
rate-limited and jittered; they do not all spawn at once. The large-frame
semaphore is idle at startup and does not affect health/status until a frame is
actually sent or received.
The release-blocking success criterion starts with Milestone 13a: a deterministic
startup test with 50 configured peers and fake SSH/session dependencies must
answer local health/status within the 500 ms budget and prove zero outbound SSH
processes are spawned before the jittered connection scheduler starts.

## On-Demand Fallback

When a local current clip changes and no persistent stream to a peer has
writable latest-state capability, the session manager may use on-demand delivery
only if the peer has `connect:true`, `on_demand:true`, persisted
`ssh_keys_ready`, a valid outbound locator tuple, this host's sync private key,
well-formed persisted `connect` proof for that key ID/gateway path, and a pinned
known-host entry for the exact host/port tuple. A stale
`remote_authorized_key_unknown` warning does not block on-demand delivery by
itself; it does keep the row orange until regular-SSH repair/check refreshes
proof. Missing or malformed `connect` proof still blocks on-demand and
persistent outbound attempts. A stream has writable latest-state capability only
after hello succeeds, the write loop is alive, the watch snapshot has been
accepted into the single-slot queue, no write timeout/backpressure failure is
pending, and the stream is not closing. Plain connection liveness, hello
success, or ping/pong without a writable state path is not enough to suppress
on-demand fallback. Accept-only `connect:false` peers are not on-demand
candidates and do not record on-demand failures in this release. In
session-manager, status, and UI code,
"stream down or not configured" means there is no writable persistent stream for
an otherwise outbound-eligible peer; it never means "try fallback for an
accept-only peer" or "try fallback before outbound SSH material is complete".
Accept-only peers render as able to receive inbound sync when their local
authorized_keys proof is valid, with no local fallback path. In the default
one-way Add Peer topology, only the `connect:true` side can originate on-demand
delivery. The `accept:true, connect:false` side can send its local clipboard
changes only while an active persistent stream is writable, until a later
reciprocal setup explicitly gives that side its own `connect:true` locator and
proofs.

Fallback is not limited to future clipboard changes. If a persistent stream
closes, times out, or loses writable latest-state capability while the peer has a
queued or unacked newest current state, the session manager closes that stream,
checks the newest sync-eligible visible local current state against the peer's
last acked ordering key, and schedules on-demand delivery for the newest state if
the peer is otherwise outbound-eligible. The reconnect scheduler may race this
fallback: if a reconnect becomes writable and accepts the same or newer state
before the on-demand process starts, the on-demand attempt is canceled. If
on-demand starts first, reconnect continues on its normal backoff and later
successful streams exchange latest state again. The fallback sends only the
newest current state at send time; it never replays older queued or failed frames.
If on-demand also fails, status records the failure and the newest state remains
eligible for the next reconnect or explicit local current change. No current
state may be stranded waiting for another copy event solely because a persistent
write failed.

Each peer has an in-memory delivery cursor for the current daemon process. It is
not persisted; after daemon restart, the next stream or on-demand attempt may
send the latest current state again and the receiver's seen/order checks handle
duplicate or older state. The cursor records the greatest outbound visible-state
ordering key for which this daemon observed that peer's ack as `applied`,
`ignored_seen`, `ignored_echo`, or `ignored_older`. `ignored_older` advances the
cursor for that sent key because the peer proved it already has an equal or
newer ordering barrier. `rejected`, `ignored_concealed`, malformed frames,
protocol errors, SSH process failures, write timeouts, and failed final acks do
not advance the cursor. Null state acks are tracked separately as
`last_acked_null_reason` plus the sender's current ordering barrier for health
correlation; they do not advance the visible-state delivery cursor and do not
make a later visible state look delivered. Reconnect and on-demand races compare
the candidate visible state's ordering key with this cursor: if a reconnect
accepts the same or a newer visible state before on-demand starts, on-demand is
canceled; otherwise on-demand sends the newest visible state at process start.

```text
ssh
  -i ~/.config/clipfan/ssh/sync_ed25519
  -o BatchMode=yes
  -o IdentitiesOnly=yes
  -o ControlMaster=no
  -F /dev/null
  -o StrictHostKeyChecking=yes
  -o UserKnownHostsFile=~/.config/clipfan/ssh/known_hosts
  -o GlobalKnownHostsFile=/dev/null
  -o UpdateHostKeys=no
  -o ConnectTimeout=5
  -p <ssh_port>
  <ssh_user>@<ssh_host>
  receive
```

On-demand frame order is fixed:

1. Initiator writes `hello` to stdin.
2. Receiver verifies the hello.
3. Receiver writes its own `hello` to stdout.
4. Initiator verifies the receiver hello.
5. Initiator writes one `state` frame to stdin.
6. Receiver submits the state to its local daemon through `ApplyEnvelope`.
7. Receiver writes one `ack` with the returned status or one `error` frame to
   stdout.
8. Receiver writes its own current `state` frame for the initiator, or a null
   state when it has no visible current state. This receiver-to-initiator frame
   uses the receiver's outbound-direction sequence and normally starts at
   `seq:1`; it does not continue the initiator's sequence.
9. Initiator submits that receiver state through local `ApplyEnvelope`.
10. Initiator writes one final `ack` or `error` frame to stdin.
11. Both sides close the SSH channel.

`receive` never accepts `state` before a valid initiator hello. If either hello
fails, the failing side writes an `error` frame when possible and closes without
submitting clipboard state.

On-demand sends use the same known-hosts file, sync key, shared-key hello,
envelope encryption, recipient binding, and frame size limits as persistent
streams.

On-demand delivery is best-effort bidirectional latest-state exchange. If it
fails before both state directions reach an ack without protocol error, the peer
is marked unhealthy with the last completed phase. Any state already applied by
either side remains applied; there is no rollback. The next successful
persistent or on-demand connection exchanges latest visible state again.

On-demand readiness is direction-aware. Each completed exchange records two ack
statuses relative to the local host:

- `outbound_ack_status`: the peer's ack for the local host's state frame.
- `inbound_ack_status`: the local host's ack for the peer's state frame, after
  the local daemon applies or rejects that peer state and the final ack is
  successfully written to the SSH channel.

For the host that was the receiver, `inbound_ack_status` is the ack it wrote in
step 7 and `outbound_ack_status` is the final ack it reads from the initiator in
step 10. For the host that was the initiator, `outbound_ack_status` is the ack
it reads in step 7 and `inbound_ack_status` is the final ack it writes in step
10. An on-demand exchange may set local `transport_health:"ssh_sync_ready"` for
the 60 second on-demand freshness window only when both direction ack statuses
are known, neither direction ended in `error`, no direction produced a
green-blocking failure/warning ack, and at least one of the two ack statuses
qualifies under the ack health matrix (`applied`, qualifying
`no_state/no_visible_current`, `ignored_seen`, or `ignored_echo`). Benign
non-qualifying statuses, currently `ignored_older` and valid non-qualifying
`no_state` null reasons, do not qualify by themselves but do not block readiness
when the opposite direction qualified and both directions completed.
Failure/warning statuses such as `ignored_concealed`, `rejected`, protocol
errors, validation failures, and malformed frames keep the peer orange
regardless of opposite-direction success. For example, if the initiator sends an
older local state and receives `ignored_older`, then applies the peer's newer
state and successfully writes the final `applied` ack, the completed exchange
qualifies for on-demand readiness because convergence succeeded through the
inbound direction. If the initiator's local state is rejected as
`wrong_recipient`, `decrypt_failed`, or `payload_too_large`, the exchange does
not qualify even if the peer's opposite-direction state was applied.

## Relay Semantics

SSH transport supports transitive relay across the connected overlay. A daemon
that accepts a non-concealed state frame submits it through `ApplyEnvelope`. If
the result status is `applied` and `current_changed` is true, the session
manager offers the new current state to every configured peer except:

- the true clip origin,
- the peer that delivered the frame, as an optimization,
- peers with no persistent stream that has writable latest-state capability and
  no eligible on-demand outbound path. Ineligible peers include `connect:false`,
  `on_demand:false`, missing outbound locator fields, missing local sync key,
  missing known-host pin, or missing/malformed `connect` proof. A stale
  `remote_authorized_key_unknown` warning does not make the peer ineligible, but
  the warning remains visible until regular-SSH repair/check refreshes proof.

For every next hop, the sender rewrites `recipient` to that next peer ID and
keeps `origin` as the true origin. Existing clip IDs, seen-set dedup, tuple
ordering, and echo suppression remain the loop-prevention mechanism. Duplicate
relay through multiple paths is allowed and should result in `ignored_seen` or
`ignored_older` acks, not a user-visible error.

The relay topology guarantee is reachability-based, not global. A latest visible
clip is expected to propagate from its origin to hosts reachable through a
sequence of currently writable latest-state paths. A writable latest-state path
is either a hello-verified persistent SSH stream whose local send side is
writable for that peer, or an eligible on-demand outbound path. Full convergence
inside a fleet requires the writable latest-state graph to be connected strongly
enough over time for every host to reach every other host; offline hosts,
disabled peers, `connect:false` peers without an active writable stream, and
peers missing outbound material are outside the current convergence component.
The minimum three-host relay acceptance topology is a line `A <-> B <-> C` with
no direct `A <-> C` stream or on-demand path: a clip from A must reach C through
B, and a later clip from C must reach A through B. Tests must not count a host
that is unreachable from the origin as a relay failure.

Latest-state ordering is deterministic. The ordering key is:

```text
(clip timestamp, origin host ID, clip ID)
```

Timestamp comparison is primary. Equal timestamps are broken by lexicographic
origin host ID, then lexicographic clip ID. Receivers use this ordering for
local current-state decisions and for tests with fixed clocks.

When a daemon mints a local visible or concealed clip, it chooses a monotonic
timestamp relative to its persisted ordering barrier:

```text
local_ts = max(current_utc_time, ordering_barrier.timestamp + 1 nanosecond)
```

If `ordering_barrier` is empty, `current_utc_time` is used. This prevents a
local clock rollback from making a new user copy look older than the host's
previously accepted state. If the selected `local_ts` is more than 30 seconds
ahead of the host's current wall clock, the daemon records
`clock_skew_warning` and logs `local_clock_rollback_adjusted` with the skew.
The envelope still uses `local_ts`; sync correctness prefers monotonic latest
state over wall-clock display fidelity.
All local sources that mint clips, including clipboard polling, manual copy
commands, history restores, and local copy API submissions, go through one daemon
sequencer guarded by the current-state lock. The sequencer reads and updates
`ordering_barrier` in the same critical section as clip ID minting, so two
simultaneous local events cannot receive the same `(timestamp, origin host ID,
clip ID)` ordering key. If the wall clock and prior barrier would produce the
same timestamp for two queued local events, the later event receives
`previous_local_ts + 1 nanosecond`. The receive bridge
`POST /v1/current/receive` and any remaining legacy receive handler never mint a
new clip ID for a peer envelope; they pass the sender's existing envelope into
`ApplyEnvelope` so deduplication, relay loop prevention, and ordering convergence
use the original clip identity.

Peer status records both true origin and transport peer when useful:

```json
{
  "last_recv_origin": "magic-kingdom",
  "last_recv_peer_id": "fsck",
  "last_recv_transport": "ssh-stream"
}
```

## Latest-State Convergence

The protocol does not replay history. Among clips that are eligible for sync,
the fleet converges on latest visible state. Concealed clips are intentionally
local-only and can create local divergence until the concealed host copies or
receives a newer visible clip. Each host maintains one current visible clip:

- clip ID
- origin
- timestamp
- kind
- encrypted envelope body
- envelope nonce
- concealed flag

Each host also persists one ordering barrier. The barrier is the greatest
ordering key seen locally, even when there is no visible current clip to send.

On every connection, each side sends only this current clip. A receiver applies
it only when `ApplyEnvelope` returns `applied`. If two disconnected components
each create a different latest clip, they converge by the deterministic ordering
key `(timestamp, origin host ID, clip ID)` when connectivity returns. A clip
with a lower or equal ordering key returns `ignored_older` unless it was already
seen, in which case it returns `ignored_seen`.

Clock skew behavior remains explicit: clips too far in the future are rejected
with the existing peer-envelope timestamp bound. A clip envelope is rejected as
`bad_timestamp` when its clipboard timestamp is more than 2 minutes ahead of the
receiver's clock at apply time. There is no lower-bound freshness rejection for
old clip timestamps; old clips are handled by the ordering barrier and normally
return `ignored_older` unless they are genuinely newer than the local barrier.
This is intentionally different from request and hello freshness, which require
timestamps within 2 minutes in either direction. The one-sided clip bound
prevents a trusted but broken peer from poisoning ordering state with arbitrary
future timestamps without making disconnected/offline hosts unable to reconnect
with an older current visible clip.

A clip whose timestamp is in the future but within the 2 minute bound is valid
and may temporarily dominate later real-time clips from slower-clock hosts until
wall clocks catch up or a newer ordering key appears. That is an intentional
last-write-wins tradeoff inherited from the existing protocol, not a conflict
resolution bug. When a daemon accepts or rejects repeated peer clips with
timestamps more than 30 seconds ahead of local time, peer health records
`clock_skew_warning` with the maximum observed skew and the peer ID. The UI
shows this as an orange diagnostic, not as a sync failure, and copyable logs use
the stable codes `future_clip_accepted` or `future_clip_rejected`.
The warning clears when either the local wall clock reaches the maximum accepted
future timestamp for that peer or a newer ordering key from any host supersedes
the skewed peer state. Rejected future clips do not poison the ordering barrier;
their warning clears when the peer stops sending future timestamps for one
successful health interval or a later accepted state from that peer is no more
than 30 seconds ahead.

History remains local. A received clip may be recorded in that host's local
history exactly as it is today, but history is not used for protocol catch-up.

Concealed clips are not sent by the transport and no concealed barrier is sent
to peers in SSH v1. A local concealed clip clears transport-current state and
advances only the local ordering barrier. Other peers may keep their previous
visible clip and may offer that older visible clip back to the concealed host;
the concealed host rejects it as `ignored_older` using its local barrier. The
null current event sent on active streams is only a no-op marker that prevents
the sender from queueing the previous visible clip; it does not advance remote
barriers and does not update peer health, last successful push time, or delivery
logs as a successful clipboard sync. Peers ack the null state as `no_state`, but
the wire `null_reason:"concealed_clear"` prevents it from being treated as
startup no-current readiness. Because the null reason is persisted with the
barrier, daemon restart preserves the same non-readiness marker instead of
emitting `no_visible_current`. If a malformed or older peer sends a state frame
whose envelope has `concealed: true`, the receiver returns `ignored_concealed`,
does not apply it locally, and does not relay it.

## Peer Status and UI

The daemon's peer snapshot includes SSH transport state:

```json
{
  "peer_id": "fsck",
  "transport": "ssh",
  "ssh_host": "fsck.com",
  "persistent": true,
  "on_demand": true,
  "last_session_started_at": "2026-06-01T12:00:00Z",
  "last_session_ended_at": "",
  "last_push_ts": "2026-06-01T12:01:00Z",
  "last_push_ok": true,
  "last_push_err": "",
  "last_recv_ts": "2026-06-01T12:01:03Z",
  "last_recv_origin": "magic-kingdom",
  "last_recv_peer_id": "fsck",
  "last_recv_transport": "ssh-stream",
  "remote_version": "v0.4.0"
}
```

The menubar fleet list keeps green/orange/gray rendering, but color comes only
from the UI color table in Peer States. Fresh stream activity, hello success,
and successful ping/pong within the last 75 seconds are liveness only and feed
`transport_health:"transport_connected_no_clip_exchange"` until a latest-state
exchange succeeds. `transport_health:"ssh_sync_ready"` is set only by a
qualifying persistent stream state/ack exchange while the same stream remains
hello-verified, writable, and ping/pong healthy, or by a completed on-demand
bidirectional latest-state exchange that satisfies the direction-aware ack-status
rule within the 60 second on-demand freshness
window. On-demand readiness uses the direction-aware `outbound_ack_status` and
`inbound_ack_status` contract from the on-demand protocol section; a single
non-qualifying `ignored_older` does not block green when the opposite direction
qualifies and both directions completed without error or failure/warning ack,
but `rejected`, `ignored_concealed`, protocol errors, validation failures, and
malformed frames block green regardless of opposite-direction success. A live
writable persistent stream does not need periodic readiness state frames after
its first qualifying latest-state exchange. If that stream closes, becomes unwritable,
misses pong deadlines, or hits a protocol error, persistent readiness ends and
the peer enters reconnect backoff,
`transport_connected_no_clip_exchange`, or `never_connected` according to the
normal state machine. Current SSH/protocol/version errors, reconnect backoff,
`loopback_unprovisioned`, `provision_failed`, idle, and never-connected states
feed runtime status/warnings and then render through that table.

Status snapshots expose the reconnect attempt count, next retry time, last
successful ack time, and last failure phase. After a local copy, on-demand
delivery either produces an ack within the size-aware on-demand deadline or the
peer turns orange with a copyable error. A manual reconnect action resets the
backoff and records a new attempt immediately.

Peer detail views include:

- sync key path,
- known-host status,
- last SSH error,
- last protocol error,
- copyable logs for version/update/sync failures,
- actions to reconnect, rotate this host's sync key, remove key from peer, and
  update using regular SSH credentials.

The app reads peer health from canonical signed loopback `GET /v1/status`.
Existing `GET /v1/peers` remains a signed compatibility alias for the peer slice
while the Swift app migrates. It reads redacted per-peer logs from
`GET /v1/ssh/logs?peer=<peer-id>&limit=<bytes>`, also signed and loopback-only.

Copyable logs use one redacted entry schema with an explicit `source` and
`durable` flag. Daemon-owned runtime stream/version/receive logs are bounded
in-memory rings and may disappear after daemon restart; daemon log responses mark
them `source:"runtime_ring"` and `durable:false`. Daemon-owned provisioning,
listener repair, `shared_key_written_unverified` remediation history, pre-secret
SSH-material cleanup records, and post-secret delete tombstones are bounded
durable local state records in the user-owned clipfan state directory; daemon log
responses mark them with their specific source and `durable:true`. App-owned
legacy regular-SSH update/check operation logs are also durable local records,
but they are returned by the app's update/check sheets and diagnostic export,
not by daemon `GET /v1/ssh/logs` unless a later milestone explicitly imports an
operation into a daemon-owned peer remediation record. Safe-mode signed repair
exposes daemon-owned durable logs plus whatever runtime-ring entries still exist
in the current daemon process. `safe_mode_health_only` exposes only
unauthenticated health; it exposes no status or log endpoint because the daemon
cannot authenticate signed local requests. If a runtime-only ring was lost on
restart, the app must show that no prior runtime log is available instead of
implying cleanup or success.

Logs shown in the app must redact secrets. Default UI logs may include peer
display names, exit statuses, coarse command phase names, protocol error codes,
and sanitized stderr/stdout summaries from SSH. They must not include
`shared_key`, private key material, HMAC values, encrypted envelope bodies,
decrypted clipboard content, full frame payloads, raw SSH argv, home-directory
paths, or clip IDs. Copyable diagnostic logs may include hostnames, usernames,
and installed binary paths only after a visible "include diagnostic details"
choice; even then they still redact keys, HMACs, frame bodies, and clipboard
content. Debug logs may include clip IDs only behind an explicit developer
setting. Per-peer copyable logs retain the newest 64 KiB by default and
truncate older lines with a visible `[truncated]` marker. Sync logs are
in-memory only by default, are not written to disk, and are never uploaded. A
user-initiated diagnostic export writes only the selected redacted log snapshot.
clipfan does not send telemetry for SSH transport, provisioning, or clipboard
sync events.
Every log line, remote stderr/stdout summary, and protocol message shown or
exported by the app is UTF-8 validated, capped to 1024 bytes per line after
redaction, escapes control characters, and is rendered as data rather than
markup. Longer remote output is summarized with stable phase/code fields and a
visible `[truncated]` marker.

Security-relevant remediation history for `shared_key_written_unverified`,
pre-secret SSH-material cleanup, host-key mismatch, failed cleanup, remote
demotion/removal, and downgrade blocking is persisted as redacted audit events
in the user-owned clipfan state directory with mode `0600`. The persisted
remediation log keeps the newest 256 events or 256 KiB, whichever is smaller,
and follows the same redaction matrix as copyable logs. It survives app/daemon
restart so the UI can explain why a peer remains in a remediation state.

Diagnostic exports and timestamped config backups are local files only, created
with mode `0600` in the user-owned clipfan directory or a user-selected export
location. They are not encrypted by clipfan and are never uploaded; the UI labels
them as containing sensitive operational metadata even after redaction.

Protocol examples and checked-in unit-test fixtures may contain literal `sig`
fields and fixed expected HMAC values because they are synthetic test vectors.
Runtime logs, UI diagnostics, and exported logs must never include a raw HMAC,
HMAC prefix, request signature, response signature, hello signature, version
signature, watch-event signature, nonce, encrypted body, or full signed frame.
Authentication failures log only structured facts:

- `auth_phase`: `request`, `hello`, `version`, or `watch_event`,
- `auth_error`: `missing_signature`, `malformed_signature`, `bad_signature`,
  `stale_timestamp`, `future_timestamp`, `replayed_nonce`, or
  `wrong_peer_identity`,
- `signature_present`: boolean,
- `timestamp_skew_seconds`: integer when a timestamp was parseable,
- `peer_id`, `purpose`, and protocol version when those fields already passed
  validation and are not themselves the rejected secret material.

OpenSSH process failures map to stable phase codes before they enter logs:
`host_key_untrusted`, `host_key_mismatch`, `ssh_auth_failed`,
`ssh_connect_failed`, `ssh_timeout`, `ssh_process_signaled`, `scp_upload_failed`,
`remote_command_failed`, `remote_json_invalid`, `remote_phase_failed`, and
`gateway_protocol_failed`. Logs may include the numeric exit status or signal
name, but user-facing status is driven by the stable phase code plus redacted
stderr.

Field-level redaction rules:

| Field class | Default UI | Diagnostic opt-in copy/export | Developer logs |
|-------------|------------|-------------------------------|----------------|
| Peer display name, peer ID, phase code, protocol error code | show | show | show |
| Host key type and SHA256 fingerprint for TOFU/mismatch | show when needed for trust decision | show | show |
| Hostname, username, SSH port | show in peer settings and mismatch/update flows | show | show |
| Installed binary path, gateway path | hide except peer settings/update details | show | show |
| Clipfan-managed sync key path and dedicated known_hosts path | show only in peer settings/key detail with the user home collapsed to `~`; hide in default failure logs | show with the user home collapsed to `~`; never include key contents | show normalized path only; no key contents |
| Home-directory paths unrelated to install/gateway path | hide | redact | redact unless explicit local debug build |
| Raw SSH argv | hide | redact to tool name, phase, host/port, and option categories | redact values; no private key path unless local debug build |
| SSH stderr/stdout | sanitized summary only | sanitized lines with paths/secrets redacted | sanitized lines; raw only in explicit local debug build |
| Host key old/new fingerprints | show for mismatch repair | show | show |
| `shared_key`, private keys, public-key private comments beyond key ID | redact | redact | redact |
| HMAC/signature values, nonces, encrypted bodies, full signed frames | redact | redact | redact |
| Clipboard plaintext, image bytes, frame bodies, clip IDs | redact | redact | clip IDs only behind explicit developer setting; contents never |
| Timestamps, exit status, signal name, retry count, duration | show | show | show |

Installed and gateway paths appear in three surfaces only: peer settings/update
details may show them because they are the editable target of repair; default
failure summaries show only the phase code plus "installed path available in
diagnostics"; diagnostic opt-in export may include the paths after redacting
unrelated home-directory paths. Default copyable failure logs do not include
full install/gateway paths.

The peer detail "sync key path" field is a local key-material settings field,
not a log field. It displays only clipfan-managed paths with the user home
collapsed to `~`, such as `~/.config/clipfan/ssh/sync_ed25519`, and never shows
private key contents. Default fleet rows and copyable failure logs hide it.

Known-host mismatches are hard failures. The app shows the old and new host key
fingerprints and requires regular SSH credential confirmation before replacing
the pinned key in clipfan's known-hosts file.

Known hosts are stored in OpenSSH `known_hosts` format in clipfan's dedicated
known-hosts file. Provisioning records the exact host/port tuple used for the
peer and accepts Ed25519, ECDSA, or RSA host keys as OpenSSH reports them.
Ed25519 is preferred when present. ECDSA is accepted when reported by OpenSSH.
RSA host keys are accepted only with SHA-2 RSA signatures supported by the local
OpenSSH build; `ssh-rsa`/SHA-1 signatures are not enabled or added back through
`HostkeyAlgorithms` overrides. Runtime SSH always uses
`StrictHostKeyChecking=yes`, `UserKnownHostsFile=<dedicated-file>`,
`GlobalKnownHostsFile=/dev/null`, and `UpdateHostKeys=no` with this file.

On first install, if clipfan has no known-host entry for the target host/port,
the app performs explicit trust-on-first-use bootstrap with:

```text
ssh-keyscan -T 5 -p <ssh_port> <ssh_host>
```

The app parses the returned OpenSSH host key lines, displays each key type and
SHA256 fingerprint, and requires user confirmation before writing the selected
host/port entry into clipfan's dedicated known_hosts file. No install, version
probe, or sync command runs until the user-confirmed key is present. All
subsequent SSH commands use `StrictHostKeyChecking=yes`, the dedicated
known_hosts file, `GlobalKnownHostsFile=/dev/null`, and `UpdateHostKeys=no`;
runtime code never uses `StrictHostKeyChecking=no`, `accept-new`, global
known_hosts, or OpenSSH host-key auto-update as a substitute for clipfan's
dedicated pin. clipfan stores exact host/port entries, using `[host]:port` for
non-default ports, and stores every user-confirmed key type for that tuple.
clipfan's dedicated known_hosts file does not use hashed hostnames or aliases;
aliases must be separate peer configs with their own confirmed host/port tuple.
This first-contact flow is TOFU: it records the key observed on the network and
does not prove host identity unless the user verifies the fingerprint out of
band. The confirmation UI must say: "This fingerprint was observed from the
network. Confirm it matches the host you intend to trust." If `ssh-keyscan`
returns no keys, provisioning stops. If multiple key types are returned, the UI
shows all returned fingerprints and stores the selected confirmed set; later SSH
must use one of the stored keys. A DNS name or IP change is a new host/port tuple
and requires fresh confirmation.
Security prompts for TOFU, host-key mismatch, duplicate host ID, and
`shared_key_written_unverified` remediation must be keyboard reachable, expose
fingerprints/host IDs/peer names to VoiceOver labels, avoid color-only risk
communication, and provide copy buttons for fingerprints and remediation logs.
The destructive or trust-changing action must be the non-default button when
macOS conventions allow it.
On mismatch repair, no install, update, config, authorized_keys, or daemon
mutation runs before the replacement key is confirmed and pinned. The exact
repair protocol is:

1. Run `ssh-keyscan` for the configured host/port and parse returned keys.
2. Display the stored fingerprints and newly observed fingerprints with the same
   TOFU warning text used for first contact.
3. Require the user to confirm that the new fingerprint matches the intended
   host out of band.
4. Write a temporary mode `0600` known_hosts file containing only the selected
   new key lines for that exact host/port tuple.
5. Run one fixed, non-mutating regular-SSH credential probe with
   `StrictHostKeyChecking=yes`, `-F /dev/null`,
   `UserKnownHostsFile=<temporary-file>`, `GlobalKnownHostsFile=/dev/null`, and
   `UpdateHostKeys=no`. The remote command is the fixed string
   `printf clipfan-host-key-repair-ok`; it contains no user-controlled data.
6. If the probe authenticates and returns the expected marker, atomically
   replace only that host/port entry in clipfan's dedicated known_hosts file,
   then resume the user-requested provisioning or update operation.
7. If the probe fails, discard the temporary file, leave the permanent pinned
   key unchanged, record the failure in the peer log, and do not mutate the
   remote host.

Runtime sync keys never run mismatch repair and never use a temporary
known_hosts file. They continue to fail closed with `StrictHostKeyChecking=yes`,
`GlobalKnownHostsFile=/dev/null`, and `UpdateHostKeys=no` until the app repairs
the pinned host key through the regular-SSH flow.

Removing a peer deletes that peer's exact host/port entry from clipfan's
dedicated known-hosts file only when no remaining peer config references the
same host/port tuple. If another peer uses the same tuple, the known-host entry
stays. Disabled peer records still count as references for retention, because
they may be re-enabled and still represent an explicit user-configured trust
decision. A separate user-confirmed cleanup action may prune known_hosts entries
that are not referenced by any enabled or disabled peer record. The user's normal
SSH known_hosts files are never modified. Updating a known-host entry after a
mismatch requires regular SSH credential confirmation and records the old/new
fingerprints in the peer log.

## Install, Update, and Key Provisioning

Install/update remain explicit regular SSH operations from the app.

Prerequisites and SSH config policy:

- Regular install/update requires `ssh`, `scp`, and `ssh-keyscan` on the Mac.
  For legacy check/update actions that do not yet have a persisted
  `ssh.peers[]` record, missing tools or unsupported options produce an
  operation-local `regular_ssh_prerequisite_failed` log entry before any
  `regular_ssh_install_update_mutation`; they do not create or mutate a peer
  migration state. In Add/Repair Peer provisioning, the same pre-secret failure
  is operation-local until the flow already has a persisted local peer record.
  Only retries/repairs for an existing persisted peer may record the failure as
  `provision_failed` through the transition endpoint with pre-secret absence
  proof.
- Regular install/update in this release uses explicit `ssh_user`, `ssh_host`,
  and `ssh_port`, passes `-F /dev/null`, and does not honor user SSH config,
  Host aliases, ProxyJump, or ProxyCommand. The user's SSH agent may be used for
  regular SSH authentication.
- Regular SSH authentication supports the app's explicit optional identity-file
  field for install, update, host-key-confirmed provision commands, and cleanup
  only. It is never used for runtime `version`, `receive`, or `sync-stream`
  transport. Runtime SSH uses only clipfan-managed command-locked sync keys.
- If the user supplies an identity file, the app expands a leading `~/`, rejects
  empty paths, relative paths, paths containing NUL/newline/control characters,
  non-regular files, files not owned by the current user, and files that are
  group- or world-writable. Accepted identity files are passed as argv
  `-i <absolute path>` with `IdentitiesOnly=yes`; they are never interpolated
  into shell text.
- If the user leaves the identity-file field empty, regular SSH may use the
  user's already-running SSH agent and OpenSSH default identity lookup. The app
  still passes `-F /dev/null` and explicit user/host/port, so SSH config aliases,
  `IdentityFile`, `ProxyJump`, and `ProxyCommand` entries are not honored.
- The app runs regular SSH/SCP non-interactively with `BatchMode=yes`. Password
  prompts and passphrase UI are unsupported in this release. Passphrased keys
  work only when an agent has already unlocked them. If OpenSSH would need a
  password or passphrase prompt, the operation fails with `ssh_auth_required` or
  `ssh_auth_failed` and tells the user to load the key into an agent, choose a
  usable explicit key, or use a direct account that already accepts key auth.
- Runtime sync-key SSH uses explicit host/user/port/key/known_hosts settings and
  ignores the user's SSH config by passing an empty SSH config file. Runtime
  ProxyJump/ProxyCommand is unsupported in this release.
- Product impact: a host that is reachable only through a user's SSH config
  alias, ProxyJump/bastion, ProxyCommand, SSH-config-only `IdentityFile` entry,
  or other client-side SSH config cannot be added for sync in this release. Add
  Peer fails before `add_peer_provision_mutation` with
  `unsupported_ssh_topology` and tells the user to use a direct DNS name,
  VPN/Tailscale/MagicDNS address, the explicit identity-file field, a key already
  available through the agent or default OpenSSH identity lookup, or wait for a
  later explicit topology feature.
- Local process invocations for `ssh`, `scp`, and `ssh-keyscan` are constructed
  as argv arrays, not local shell strings. Host, user, port, identity file, local
  staged path, and peer ID values are validated before argv construction and are
  never interpolated into a local shell command.
- Legacy regular install/update may keep the existing fixed remote shell
  templates for continuity with released app behavior: the `uname -s; uname -m`
  probe, remote staging directory creation with `mktemp`, upload into that
  remote-generated stage, running `install.sh --with-tmux` or
  `install.sh --no-tmux` from that stage, restarting the user service as part of
  the installer, and verifying the installed binary with `version --json`. The
  stage path must be generated by the remote template, validated against the
  `/tmp/clipfan-install.*` prefix before reuse, and shell-quoted when referenced
  in the fixed template. User-controlled values such as `ssh_user`, `ssh_host`,
  `ssh_port`, identity file, local paths, `DEST`, peer ID, install path, config
  path, state path, and any config field must not be interpolated into these
  remote shell templates. Any path returned by the remote side is treated as
  untrusted output and must pass explicit absolute-path validation before a later
  fixed verification command may reference it.
- Public pre-17d3b regular SSH update/check templates may upload binaries,
  restart the user service, and verify the installed binary only. They must not
  stage or install `config.json`, `shared_key`, `static_peers`, `ssh.peers[]`,
  peer config, sync keys, dedicated known_hosts, managed authorized_keys,
  migration state, or any file that enrolls the target into sync. The current
  Mac app install/enroll path that stages a remote `config.json` containing the
  fleet `shared_key` and `static_peers` is `add_peer_provision_mutation` even
  though it uses regular SSH credentials and legacy shell templates.
- Remote sync provisioning mutations after Milestone 6 use
  `clipfan provision` JSON stdin/stdout, not inline shell edits. This includes
  config, sync key, dedicated known_hosts, managed authorized_keys, service
  metadata, and migration-state writes. Keeping the legacy install/update shell
  templates above does not permit ad hoc remote shell mutation for provisioning.
- Remote Linux/macOS peers need OpenSSH server support for forced commands and
  the explicit authorized_keys options listed in this design.
- Supported OpenSSH server targets for this release are the macOS bundled
  OpenSSH used by the current supported macOS release and Ubuntu LTS OpenSSH
  8.9/9.x. Release CI must exercise at least one macOS OpenSSH server and one
  Ubuntu LTS OpenSSH server. If a target server rejects any required
  authorized_keys option or cannot run forced commands, provisioning fails with
  `openssh_unsupported`; clipfan does not silently install a weaker
  authorized_keys line.
- If `sshd` is present but too old, lacks required authorized_keys options,
  lacks forced-command support, or only works by enabling deprecated algorithms,
  Add Peer fails before `add_peer_provision_mutation` with
  `openssh_unsupported` and a copyable diagnostic. There is no compatibility mode
  that removes restrictions or enables public HTTP fallback.
- Forced-command authorized_keys lines use the absolute installed `clipfan`
  path and do not depend on login shell startup files, user `PATH`, tmux, or
  systemd/launchd service environment. Integration fixtures cover macOS sshd
  and Ubuntu sshd with non-login forced commands and a minimal environment.
- Remote path discovery does not trust `HOME` alone. Regular SSH
  `clipfan version --json` reports `uid`, `effective_user`, `home_dir`,
  `config_path`, `state_dir`, and `install_path` after resolving the account
  through the OS user database and falling back to `HOME` only when the OS
  lookup is unavailable. The provisioner persists absolute `config_path` and
  `state_dir` in service metadata. A forced-command gateway with no usable home
  directory or absolute config path fails closed with `home_unavailable`; it
  never creates config under the current working directory.

Public milestone language uses two distinct mutation families:

- `regular_ssh_install_update_mutation`: user-initiated binary upload,
  install/update verification, and user-service restart using regular SSH
  credentials, with no staged config payload and no sync enrollment side effects.
  Public builds may offer this family before 17d3b for explicit update/check
  flows only when the operation does not write `config.json`, `shared_key`,
  `static_peers`, `ssh.peers[]`, sync keys, dedicated known_hosts, managed
  authorized_keys, migration state, fleet secrets, or peer config.
- `add_peer_provision_mutation`: any command or API that writes sync keys,
  dedicated known_hosts, managed authorized_keys, remote peer config, migration
  state, `shared_key`, `static_peers`, staged `config.json`, or command-locked
  sync readiness. Public builds must not run this family before 17d3b. The
  current Swift `Installer.install` Add Peer path that stages config is in this
  family until it is replaced by the gated provisioning flow. The provision
  subcommands `sync-key`, `known-hosts`, `write-config`, `authorized-key`, and
  `transition` are in this family.

Regular update/check operation errors are not persisted peer states unless the
user has explicitly entered Add/Repair Peer provisioning. For transient legacy
suggestions, the Mac app stores redacted operation-local logs keyed by action,
host, port, and operation ID, shows them in the update/check sheet, and provides
the same copy/export affordance as peer update logs. The bounded durable log
store is app-side local state under the user's clipfan state directory, not
`ssh.peers[]`; it contains stable phase/error codes and redacted command
summaries, never `shared_key`, private key paths, HMACs, clipboard payloads, or
full SSH argv containing user secrets. If the user later chooses Add/Repair Peer,
that flow starts a new provisioning record and may reference the prior operation
`log_id`, but it must not convert the legacy operation log into
`provision_failed` without user-confirmed provisioning.

Public Add Peer before Milestone 17d3b:

- Public builds may show unsupported/coming-soon SSH transport state, listener
  repair, user-initiated `regular_ssh_install_update_mutation`, and read-only
  diagnostics allowed by the public behavior table.
- Public builds must not invoke any `add_peer_provision_mutation` context before
  17d3b. This includes `sync-key`, `known-hosts`, `write-config`,
  `authorized-key`, `transition`, `service`, and `cleanup`; all return
  `unsupported_command` from the provision client before SSH execution when the
  operation context is Add Peer/provisioning. The later provision subcommand
  matrix defines the only non-Add-Peer public exceptions for regular
  install/update and explicit cleanup/remediation.
- Public UI must not show Add Peer success/green. It may render
  "provisioned; sync unavailable" only for internal/test builds. Public builds
  before 17d3b use "SSH setup required" or "SSH setup pending" for non-mutating
  and 17d3a local-cutover states.

Internal gated fixture flow before 17d3b and public released flow after 17d3b.
Before the 17d3b public Add Peer/sync acceptance bundle passes, the mutating
steps below may run only against explicit internal fixtures as described in
Milestones 10b1-10b4. After 17d3b, the same sequence is the public Add Peer flow:

1. User enters regular SSH target or picks a discovered host.
2. App performs host-key TOFU bootstrap or mismatch repair, then uses
   `StrictHostKeyChecking=yes`, the dedicated or operation-scoped known_hosts
   file, `GlobalKnownHostsFile=/dev/null`, and `UpdateHostKeys=no` for all
   regular SSH commands.
3. App connects with regular SSH credentials.
4. App uploads/install payload and records the installed clipfan path returned
   by the remote installer, but does not write the fleet `shared_key` yet.
5. App runs regular SSH `clipfan version --json` at the installed path and reads
   the remote clipfan `host_id`, daemon version, effective Unix user, install
   path, home/config/state paths, and supported SSH protocols. This identity
   probe must work without the fleet `shared_key`.
6. App takes the remote config lock through regular SSH and classifies any
   existing remote `shared_key`. If the key is missing, provisioning continues
   as a clean pre-secret install. If it matches the local fleet key, the attempt
   records `legacy_shared_key_present` and may continue as a legacy HTTP peer
   migration. If it is valid but different, malformed, unreadable, or the lock
   cannot be acquired, the flow fails before any remote mutation or local peer
   record is written.
7. App validates that the remote `host_id` matches the intended peer ID or asks
   the user to accept/rename before writing final config. It also validates that
   the effective Unix user is the same account that will own config,
   authorized_keys, and the daemon. Duplicate peer IDs are rejected before any
   fleet secret is written.
8. App stops any existing remote user daemon before writing remote config or key
   material, verifies a pre-SSH/public-listening daemon cannot restart, and
   records the stop phase in logs. If the old daemon cannot be stopped, the flow
   fails before writing non-secret SSH config or the fleet `shared_key`.
9. App writes the non-secret remote config skeleton before any remote sync key
   or proof artifact is created: loopback listen, persisted remote host ID,
   remote peer record for the local host, service metadata, and no
   `shared_key`. For `legacy_shared_key_present`, this is the only step that
   removes the previously present fleet credential from the remote staged config,
   and it may run only after step 8 proves the old daemon cannot restart with a
   public peer HTTP listener. The write runs under the remote config lock and
   records the resulting config revision. Any peer records written in this phase
   use `migration_state: "loopback_unprovisioned"` with no proof yet, which
   rejects every gateway purpose and leaves the staged transition unavailable
   until managed authorized_keys proof is patched.
10. App optionally attempts to create or verify the remote host's local sync key
   pair with `clipfan provision sync-key` so future outbound setup and rotation
   of that remote host's own key are easier. This is best-effort only when the
   remote peer record starts as `accept:true, connect:false`; it is not required
   for the one-way local-to-remote Add Peer success path. The command receives
   the expected remote host ID and config revision from step 9 and fails before
   touching key files if the persisted config is missing that host ID, contains a
   different host ID, or has changed revision.
   For a remote that starts as `accept:true, connect:false`, failure to create
   the remote local sync key records `remote_accept_only_missing_sync_key`,
   blocks future remote outbound setup and rotation of that remote host's own
   sync key, but it does not block the one-way local-to-remote Add Peer flow,
   public green/success after latest-state exchange, or this host's sync-key
   rotation because inbound accept-only runtime and this host's rotation do not
   use the remote local private key. It also does not transition the peer to
   `provision_failed`; the notice remains visible until a later regular-SSH
   repair creates the remote host's local sync key.
11. App ensures the local host has a sync key pair.
12. App verifies and, if needed, updates the remote `ssh.peers[]` record created
   in step 9 so the local host's record has `enabled: true`, `accept: true`,
   `connect: false`, `persistent: false`, `on_demand: false`, and
   `migration_state: "loopback_unprovisioned"`. This is the same remote peer
   record, not a second peer-record write. The remote daemon rejects gateway
   reservations while it remains unprovisioned or staged.
13. App appends or replaces one managed authorized_keys line on the peer for the
    local host's sync public key and forced command, reads back or otherwise
    verifies the managed marker, then runs the pre-secret offline
    `clipfan provision transition` mode. That command takes the remote config
    lock while the remote daemon is still stopped and unauthenticated, verifies
    the expected host ID and config revision, applies the `accept` proof patch,
    and transitions the remote record from `loopback_unprovisioned` to
    `ssh_material_staged` in one atomic revisioned write with the same audit
    rules as the signed transition endpoint. It is not a signed loopback daemon
    API and it cannot write `shared_key` or promote beyond
    `ssh_material_staged`.
14. App creates the local `ssh.peers[]` record for the remote host as
    `loopback_unprovisioned` with `enabled: true`, `accept: false`,
    `connect: true`, `persistent: true`, and `on_demand: true`, then patches the
    staged `connect` proof from step 13 and transitions the local record to
    `ssh_material_staged`. The peer row may be visible between these revisioned
    writes only as unprovisioned/provisioning, never as staged or green without
    matching proof. Default Add Peer does not accept inbound forced commands
    from the remote because the remote's sync public key has not been installed
    locally.
15. Reciprocal outbound setup is skipped in this release. The app does not
    collect the local host's SSH endpoint for remote-to-local dialing, does not
    install the remote sync public key in the local authorized_keys file, does
    not change the local peer record to `accept:true`, and does not change the
    remote peer record to `connect:true`. One established persistent stream is
    already bidirectional for latest-state exchange.
16. App verifies that the local app build includes command-locked version
    gateway support before writing the fleet secret.
17. App writes final remote config containing the fleet `shared_key` and
    promotes only the remote peer record for the local host to
    `migration_state: "shared_key_written_unverified"`. This enables
    command-locked `version` only.
18. App writes the local peer record to
    `migration_state: "shared_key_written_unverified"` and restarts the local
    daemon as needed. Clipboard sync is still stopped.
19. App starts or restarts the remote daemon through its user service.
20. App immediately verifies command-locked gateway version using the sync key
    and `shared_key`.
21. If verification succeeds, app runs regular SSH
    `clipfan provision transition` `mode:"daemon_loopback"` on the remote host.
    That remote command signs the peer's local loopback proof/transition APIs,
    writes current `accept` proof for the remote peer record, and promotes the
    remote peer record to `ssh_keys_ready`. Only after the remote transition
    returns success does the app promote the local peer record to
    `ssh_keys_ready` through the local
    signed loopback config API after local `connect` proof is current. Local
    promotion is last, so a failed remote promotion cannot make the local daemon
    start sync against an unready peer.
22. If local promotion fails after remote promotion succeeds, the peer remains
    orange locally and retry resumes the local promotion or uses regular SSH to
    demote/remove the remote record. No automatic rollback is attempted across
    hosts.
23. Daemons establish the persistent stream only after both sides relevant to
    the enabled directions are `ssh_keys_ready`.

Remote daemon/service lifecycle:

- Install may place the binary and service files, but it must not start or
  restart the daemon before identity/account validation and final config write.
- Before final config, no placeholder config containing public listen defaults
  or fleet `shared_key` is written.
- On macOS, the app manages the user launchd agent. On Linux, it prefers the
  user systemd service when available and falls back to the existing user-level
  service/start command documented by the installer. No sudo/root service is
  used.
- Service activation success means the process starts, answers signed loopback
  `/v1/version` locally or command-locked gateway version remotely as
  appropriate, and reports the expected daemon version and host ID.
- Before the final remote `shared_key` write, failure to start, stop, or
  restart is `provision_failed` with service phase, platform, and redacted
  stderr captured in logs. After the final remote `shared_key` write, daemon
  start/restart or command-locked version verification failure is
  `shared_key_written_unverified`.

Remote config/key writes create directories as the target `ssh_user` with mode
`0700` and files with mode `0600`. Updates preserve the existing owner and
private mode when the file already exists. A file owned by a different Unix user
or writable by group/other is a provisioning failure unless the app created and
fixed it in the same regular-SSH session before writing secrets.

Add Peer provision mutation mechanism:

- After the binary is uploaded, `add_peer_provision_mutation` is performed only
  through fixed `clipfan provision ...` subcommands invoked over regular SSH with
  JSON stdin.
- The local `ssh` process is started with an argv array. The remote command
  itself is still parsed by the remote login shell, so clipfan builds it only
  from fixed subcommand names plus a validated canonical `install_path` or
  `gateway_path` that is absolute, POSIX-clean, has no `..` component, matches
  `[A-Za-z0-9._/@+-]+`, and has passed owner/mode checks. No peer names,
  hostnames, JSON, user-entered paths, or arbitrary strings are interpolated into
  the remote command; all variable data is passed through JSON stdin.
- Supported subcommands are `sync-key`, `known-hosts`, `write-config`,
  `authorized-key`, `transition`, `service`, and `cleanup`. They perform path
  validation, mode checks, sync-key creation/verification, known_hosts writes,
  authorized_keys locking, config writes, daemon-local proof/state transitions,
  and service control on the remote host. Identity probing is not a provision
  subcommand; it is always regular SSH
  `clipfan version --json`.
- Provision subcommands are gated by operation context as well as subcommand
  name. Public builds before Milestone 17d3b reject any invocation whose
  operation context is `add_peer_provision_mutation`, `remote_secret_write`,
  `sync_key_rotation`, or `reciprocal_sync_setup`, even when the subcommand is
  `service` or `cleanup`. Before 17d3b, `service` is allowed in public builds
  only for user-initiated `regular_ssh_install_update_mutation` that uploads or
  restarts the user's own binary without writing fleet secrets, peer records,
  sync keys, dedicated known_hosts, managed authorized_keys, or green sync
  state. Before 17d3b, `cleanup` is allowed in public builds only for an
  explicit user-confirmed regular-SSH remediation that removes stale install or
  update artifacts and does not write or promote fleet material. After 17d3b,
  Add Peer may use the full provision set only when the release manifest and
  runtime gates in this spec are true.

Provision subcommand public gate matrix:

| Subcommand | Add Peer before 17d3b | Regular update/install before 17d3b | Explicit cleanup/remediation before 17d3b | Add Peer after 17d3b |
|------------|------------------------|-------------------------------------|-------------------------------------------|----------------------|
| `sync-key` | blocked `unsupported_command` | blocked | blocked except user-confirmed stale sync-key removal that does not create a new key | allowed |
| `known-hosts` | blocked `unsupported_command` | blocked; legacy update uses operation-scoped temporary known_hosts, not persisted clipfan peer storage | blocked except user-confirmed stale dedicated-entry removal | allowed |
| `write-config` | blocked `unsupported_command` | blocked | blocked except user-confirmed removal/demotion of a staged record that already exists | allowed, with the separate secret-write gate for `shared_key` |
| `authorized-key` | blocked `unsupported_command` | blocked; public update/check before 17d3b may report stale managed paths but must not rewrite managed authorized_keys | allowed only for user-confirmed stale managed-line removal | allowed |
| `transition` | blocked `unsupported_command` | blocked | allowed only to demote/remove an existing unverified staged state; never to promote green | allowed |
| `service` | blocked when the operation context is Add Peer/provisioning | allowed for user-initiated install/update restart without peer sync mutation | allowed for user-confirmed stop/restart needed to clean stale install/update artifacts | allowed |
| `cleanup` | blocked when the operation context is Add Peer/provisioning | allowed only for install/update artifact cleanup | allowed for user-confirmed cleanup of stale artifacts or stale staged fleet material; must not create or promote material | allowed |

Post-17d3b public remediation contexts are explicitly gated by operation
context as well as subcommand. The same generated release manifest that enables
public Add Peer must enable these contexts; there is no environment-variable or
ad hoc UI bypass in release builds.

| Public operation context after 17d3b | Required gates | Allowed purpose |
|--------------------------------------|----------------|-----------------|
| `sync_key_rotation` | transport gates all true, runtime gates all true, peer already `ssh_keys_ready`, regular SSH credentials available, exact host-key pin valid | install pending managed keys, verify command-locked `version`, promote the host-global key, and clean old managed lines |
| `shared_key_written_unverified_cleanup` | transport gates all true and peer in `shared_key_written_unverified` | demote/remove the unverified remote fleet credential over regular SSH; never promote green without command-locked verification |
| `removed_peer_cleanup` | transport gates all true and durable tombstone or stale cleanup status exists | remove stale managed authorized_keys lines, dedicated known_hosts entries, or install/update artifacts without creating new peer material |
| `proof_or_host_key_repair` | transport gates all true, existing peer record, regular SSH credentials available, exact host-key confirmation complete | refresh known-host pins, directional proof, gateway path, or managed-line path and then use the scoped transition API only if proof is current |

Gate tests instantiate the provision client directly and assert the stable
decision before any remote SSH process is spawned. They cover every subcommand,
operation context, and public/internal build profile, so `service` and `cleanup`
cannot be accidentally left reachable as Add Peer mutations before 17d3b, and
post-17d3b remediation contexts cannot remain blocked or bypass the generated
gate manifest.
- `sync-key` JSON stdin includes absolute `key_path`, expected `host_id`, and
  requested key type `ed25519`. It creates the private/public key pair and
  `<key_path>.clipfan.json` metadata sidecar atomically when absent, verifies an
  existing key pair against the sidecar contract, returns public key and key ID,
  and never prints the private key. A missing or mismatched sidecar returns
  `sync_key_identity_mismatch` and requires explicit repair or key rotation.
- `known-hosts` JSON stdin includes absolute `known_hosts_path`, exact
  `ssh_host`, `ssh_port`, confirmed host key lines, and expected host-key
  fingerprints. It atomically creates or replaces only the exact host/port tuple
  in clipfan's dedicated known_hosts file, preserves unrelated entries, and
  never edits the user's normal OpenSSH known_hosts file.
- `write-config` JSON stdin includes `expected_host_id`,
  `expected_config_revision`, absolute `config_path`, a scoped config patch, and
  `allow_shared_key_write`. It takes the remote config lock, verifies owner/mode
  and host ID, preserves unknown config v2 fields, rejects stale revisions with
  `config_revision_conflict` plus the current revision, writes atomically, and
  increments the revision exactly once on success. If the remote config changes
  between identity probe and write, retry must reread the remote config and
  rebuild the scoped patch; it must not overwrite the full file from a stale
  local snapshot.
- `transition` JSON stdin includes `mode`, `expected_host_id`,
  `expected_config_revision`, `peer_id`, optional `proof_patch`, `from_state`,
  `to_state`, `reason`, and `log_id`. It has two legal modes:
  `pre_secret_offline` and `daemon_loopback`.
- `transition` with `mode:"pre_secret_offline"` is allowed only for
  `loopback_unprovisioned` to `ssh_material_staged` before any remote
  `shared_key` write. It requires `allow_shared_key_write:false`, proves the
  remote daemon is stopped, takes the remote config lock, verifies owner/mode,
  expected host ID, expected config revision, persisted `shared_key` absence,
  valid staged proof shape, and the exact transition-table requirements, then
  applies the proof patch and transition in one atomic config write. It appends
  the same redacted audit event shape as the signed transition endpoint and
  increments the config revision exactly once. It cannot write `shared_key`,
  start the daemon, promote to `shared_key_written_unverified` or
  `ssh_keys_ready`, or perform cleanup/demotion.
- `transition` with `mode:"daemon_loopback"` runs on the remote host over
  regular SSH after the remote daemon is started and local auth material exists.
  The remote command reads the local config, signs loopback requests with
  `clipfan-v1/request-hmac`, applies the scoped proof patch when present, then
  calls the loopback transition endpoint. If the daemon is unavailable, loopback
  auth fails, the revision is stale, or
  the proof does not match the enabled direction, it returns structured JSON with
  `ok:false`, `phase:"transition"`, a stable error code such as
  `daemon_unavailable`, `local_auth_failed`, `config_revision_conflict`, or
  `proof_mismatch`, and leaves the peer in its previous migration state.
- The app does not generate inline remote shell for config, key, service, or
  authorized_keys edits. The only shell script in the flow is the existing
  installer payload before the trusted `clipfan` binary is available.
- Each provision subcommand returns structured JSON with `ok`, `phase`,
  `changed`, `retryable`, and redacted `message` fields. Tests exercise these
  subcommands with fake filesystem/service dependencies rather than matching
  generated shell text.
- Each `add_peer_provision_mutation` appends a redacted local audit event before
  and after the operation: timestamp, local host ID, peer ID, SSH target tuple,
  phase code, key ID, config revision when known, changed boolean, result code,
  and retryable boolean. Audit events never include secrets, raw HMACs, clipboard
  content, private key paths, or raw argv. They are stored in the same bounded
  per-peer log ring by default and are included in user-initiated diagnostic
  export only when selected.
- File-writing provision subcommands write temporary files in the target
  directory, set final mode/owner before publish where the platform permits,
  atomically rename into place, and fsync the file and parent directory where
  supported. If a crash or disconnect occurs, retry must probe actual remote
  file contents, owner/mode, managed authorized_keys markers, service state, and
  command-locked gateway behavior before deciding which phase to resume. No
  retry trusts only the last app-side phase label. A crash may leave either the
  old complete file or the new complete file; temporary files are ignored and
  cleaned best-effort on the next regular-SSH repair.

Update flow:

1. User authenticates with regular SSH credentials.
2. App uploads the current payload and runs install/update as today.
3. App does not use the command-locked sync key for update.
4. App verifies the installed binary through the same regular SSH session by
   running `clipfan version --json` at the installed path.
5. If sync keys are already provisioned, app then verifies runtime sync
   readiness through the command-locked `version` gateway. Failure of this
   second check marks sync unhealthy but does not imply the binary install
   failed.

Version verification by persisted state and runtime health:

| Persisted state | Runtime health | Version behavior |
|-----------------|----------------|------------------|
| transient `legacy_http` suggestion from `static_peers` | any | user-prompted regular SSH `clipfan version --json` or update only, using operation-scoped host-key pinning; no background probe and no public HTTP fallback |
| `loopback_unprovisioned` | any | regular SSH `clipfan version --json` only during user-initiated Add/Repair/Update; no public HTTP fallback |
| `ssh_material_staged` | any | regular SSH retry or cleanup only; command-locked gateway purposes return `peer_not_ready` |
| `provision_failed` | any | regular SSH retry resumes from failed phase |
| `shared_key_written_unverified` | any | command-locked `version` gateway may be attempted for retry verification; `receive` and `sync-stream` remain rejected until this succeeds |
| `ssh_keys_ready` | not runtime `ssh_sync_ready` | command-locked `version` gateway may be attempted for runtime version readiness; failures show as orange runtime health and do not by themselves determine latest-state sync readiness |
| `ssh_keys_ready` | runtime `ssh_sync_ready` | command-locked `version` gateway is the normal peer version probe and may clear version warnings |
| any | install/update in progress | regular SSH verifies the installed binary first, then command-locked gateway verifies runtime version readiness when keys exist |

Outbound session start and inbound gateway reservation are separate checks:

| Persisted state on checking host | Outbound start from this host to peer | Inbound reservation on this host from peer |
|----------------------------------|---------------------------------------|-------------------------------------------|
| `legacy_http`, `loopback_unprovisioned`, `provision_failed` | not allowed | not allowed |
| `ssh_material_staged` | not allowed | not allowed |
| `shared_key_written_unverified` | `version` only, and only when this host's peer record has `connect:true`, sync private key, known-host pin, and connect proof shape; `receive` and `sync-stream` are rejected | `version` only, and only when this host's peer record has `accept:true`, `enabled:true`, and capacity. Persisted local managed authorized_keys proof validation is deliberately bypassed only for this state/purpose because the forced-command line already selected the peer identity and hello HMAC verifies the fleet `shared_key`; `receive` and `sync-stream` are rejected |
| `ssh_keys_ready` | `version`, `receive`, and `sync-stream` according to enabled runtime gates, and only when this host's record has `connect:true` plus required local outbound material | `version`, `receive`, and `sync-stream` according to enabled runtime gates, and only when this host's record has `accept:true` plus required local inbound material |

The reservation API is gateway-side/inbound. It must not be reused as the
initiator's outbound readiness check because the default one-way Add Peer record
is `accept:false, connect:true` locally and `accept:true, connect:false`
remotely. In that default flow, local retry verification starts an outbound
`version` session using `connect:true`; the remote gateway accepts the inbound
`version` reservation using its `accept:true` peer record for the local host.

Safe mode overrides this table. While the local daemon is in
`public_listen_requires_confirmation`, every command-locked gateway purpose,
including `version`, returns `public_listen_requires_confirmation` before
migration-state-specific behavior is evaluated.

Key rotation is host-global because a host has one configured sync private key
path. It is not per-peer rotation.

1. Generate a pending local sync key at a separate pending path, for example
   `sync_ed25519.next`, with pending sidecar metadata. Do not replace the current
   key or proof records yet.
2. Build the required participant set from every enabled `connect:true` peer
   whose outbound proof uses the current local sync key. Disabled and removed
   peers are not required participants; re-enable requires regular-SSH repair so
   the peer learns the current key.
3. Install a new forced-command authorized_keys line for the pending public key
   alongside the old line on every required participant using regular SSH
   credentials.
4. Verify command-locked `version` to every required participant with the pending
   key and the existing `shared_key`.
5. Only after every required participant verifies, atomically promote the pending
   key to the configured current key path and update local proof/key IDs under
   the config lock. Keep the old private key only as a mode-`0600` cleanup
   artifact until old remote managed lines are removed.
6. If install or verification fails before promotion, keep the old key and proof
   active, mark rotation failed with a copyable log, and best-effort remove
   pending remote lines.
7. After promotion, remove old managed lines over regular SSH. Cleanup failures
   leave sync using the new key but surface stale-key cleanup pending until
   regular-SSH cleanup succeeds.

Compromise response:

- If the local sync private key is lost but `shared_key` is intact, the daemon
  marks affected `connect:true` peers `missing_sync_key`, blocks outbound
  sessions, and requires regular-SSH reprovisioning or key rotation before those
  peers can return to runtime `ssh_sync_ready`. The app does not regenerate a
  key and pretend existing remote authorized_keys proof is still valid.
- If the host currently has only accept-only peers, losing the local sync
  private key does not invalidate existing inbound `accept:true` authorized_keys
  proof for remote peers, but the row remains orange with `missing_sync_key`
  until the user regenerates a local sync identity. Regeneration is local-only
  for accept-only operation; any future outbound setup still requires
  regular-SSH reprovisioning so peers learn the new public key.
- If only a sync private key is suspected compromised, run host-global staged
  rotation across every enabled outbound peer or disable/remove unavailable
  peers before promotion. Until rotation completes, disabling a peer locally
  stops local outbound and inbound sync for that peer because the daemon requires
  matching enabled peer config before accepting gateway reservations.
- If regular SSH access to a peer is unavailable, the app marks remote key
  removal as pending and keeps the peer disabled locally. The stale remote
  authorized_keys line is reported as a remaining risk until SSH access is
  restored.
- If `shared_key` is suspected compromised, the fleet application credential is
  compromised. This release does not provide automated `shared_key` rotation;
  use regular SSH credentials to reinstall/reconfigure affected peers or remove
  them from the fleet. A sync-key-only rotation is not sufficient.

Managed authorized_keys lines have stable comments:

```text
clipfan-sync:<local-host-id>:<key-id>
```

The supported managed authorized_keys target is exactly
`<target-home>/.ssh/authorized_keys` for the target `ssh_user`. The app creates
`<target-home>/.ssh` with mode `0700` when absent, creates `authorized_keys`
with mode `0600` when absent, and preserves an existing private owner/mode when
the file already exists. Group- or world-writable `.ssh` directories or
`authorized_keys` files are provisioning failures unless repaired in the same
regular-SSH session before writing managed lines.
This release does not support sshd configurations whose effective
`AuthorizedKeysFile` excludes `<target-home>/.ssh/authorized_keys`. When the app
can query `sshd -T` for the target account, it fails before mutation with
`unsupported_authorized_keys_file` if the default file is not honored. When sshd
configuration cannot be queried, the app performs a pre-secret forced-command
reachability probe after installing the managed line and before any remote
`shared_key` write. If the probe proves the managed line is not effective, the
peer stays `provision_failed` with `authorized_keys_not_effective`, and no fleet
secret is written.

`key-id` is the first 16 lowercase hex characters of SHA256 over the canonical
OpenSSH public-key blob: parse the public key line, take the second
whitespace-delimited field, base64-decode it, and hash those decoded bytes
exactly. Do not hash the full authorized_keys line, key type text, comment,
base64 text, or private key bytes. Fixed vector:

```text
public key line: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f clipfan-test-vector
decoded blob hex: 0000000b7373682d6564323535313900000020000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f
sha256: 66402c9468c58941dd19ffd650bf2b42f9226f83d3bd06ad515d0e5104a77020
key-id: 66402c9468c58941
```

Go, Swift, provision subcommands, and gateway validation must share this vector.
The app updates only lines with the `clipfan-sync:<local-host-id>:` prefix. It
never rewrites unrelated user SSH keys. Remote edits are serialized with a lock
file adjacent to the target `authorized_keys` file, write a temporary
authorized_keys file in the same directory as the target, preserve file mode,
validate that the resulting file still parses as OpenSSH authorized_keys lines,
compare the target file's mtime, size, and inode/content hash against the fresh
read taken under the lock, then atomically rename it into place. If the
target changed, the app discards the temp file, rereads, reapplies only the
managed-line edit, and retries. Forced-command paths are shell-quoted when
generated, but the final authorized_keys line is parsed as OpenSSH options, not
as shell text. Quoting is defense in depth only; it does not expand the
supported `gateway_path` character set.

If provisioning finds an existing managed line for the same host ID with a
different key ID, it treats the line as either rotation or a duplicate identity.
Rotation is allowed only when the local config records the old key ID being
replaced. Otherwise the app refuses to install another key for that host ID and
shows a duplicate-host-ID warning with a repair flow to rename the local host,
remove the stale remote key, or explicitly rotate from the recorded old key.
Two active machines with the same clipfan `hostname` are unsupported because
peers cannot distinguish their application identity after the forced command
maps both keys to the same `--authorized-peer`.

Provisioning is idempotent and has explicit partial-failure behavior:

Failure persistence is phase-specific:

| Phase family | Durable peer state | Log/remediation behavior |
|--------------|--------------------|--------------------------|
| pre-Add Peer diagnostics, legacy update/check, unsupported topology, missing local tools, host-key confirmation cancellation or mismatch before user confirmation, regular SSH auth/connect failure before remote identity is accepted, remote `shared_key` preflight mismatch/malformed/unreadable/locked, identity/account mismatch, duplicate host ID not accepted, or payload install failure before any remote config/key/authorized_keys mutation | no persisted peer record; operation-local log/remediation only | show the failure in the Add/Repair/Update sheet with copy/export; retry restarts from user-entered target and confirmed host-key state; no `ssh.peers[]` row, migration state, or green/orange fleet row is created; if remote pre-secret artifacts may exist, write the durable operation-scoped remediation record defined below |
| pre-secret phase after a persisted local peer record exists, including failed non-secret config write, required sync-key provision for this host or a `connect:true` peer, dedicated known_hosts provision, managed authorized_keys write, pre-secret forced-command probe, proof patch, staged transition, or local/remote cleanup of those pre-secret artifacts | persisted `provision_failed` through the transition endpoint | keep the peer row orange with failed phase, `remote_secret_absence_proof`, copyable log, and retry/removal actions |
| optional remote accept-only sync-key creation fails for a peer that remains `accept:true, connect:false` | no persisted migration-state change; keep current staged/promotion state | record notice `remote_accept_only_missing_sync_key`, keep copyable operation log, and allow one-way Add Peer to continue; future reciprocal outbound setup or remote host-key rotation requires regular-SSH repair |
| command capable of writing a config containing `shared_key` spawned, but absence is not proven by a locked regular-SSH remote config read | persisted `shared_key_written_unverified` | show security-relevant remediation that the remote may have the fleet credential |
| remote `shared_key` write absence is proven after a secret-capable command was attempted | persisted `provision_failed` only if the transition includes locked absence proof | keep peer orange with cleanup proof and retry/removal actions |

- Automatic provisioning retries are bounded to 3 attempts per phase with
  exponential backoff of 1s, 2s, and 4s plus jitter. After that, the app records
  the state dictated by the failure matrix, then requires an explicit user retry.
  User retries resume from the last idempotent successful durable phase when one
  exists, or restart from the operation-local Add/Repair sheet when no persisted
  peer record exists.
- Operation-local remediation records are durable app-side state, not
  `ssh.peers[]`. They are written before a persisted local peer row exists
  whenever remote pre-secret artifacts may have been created, including uploaded
  payloads, service files, reusable installed binaries, pre-secret managed
  authorized_keys lines, temporary staging directories that cleanup could not
  verify removed, or operation-scoped known_hosts pins. The record is keyed by
  operation ID plus target tuple and contains only redacted cleanup locators:
  operation kind, SSH user/host/port, confirmed host-key fingerprint reference,
  remote install/gateway path when known, artifact categories, managed marker IDs
  or key IDs when known, last cleanup attempt, retryable flag, and `log_id`. It
  never contains `shared_key`, private key material, HMACs, clipboard content,
  full SSH argv, or unredacted home-directory listings. These records survive app
  restart, appear in the Add/Repair/Update sheet as stale pre-secret cleanup
  items, and can be cleaned only through explicit user-confirmed regular SSH.
  Starting Add/Repair Peer later may reference the operation `log_id`, but it
  still creates a fresh `ssh.peers[]` record only after the normal durable
  peer-record phase.
- If host-key verification fails or is cancelled before confirmation, no config
  or authorized_keys changes are attempted and no peer row is created.
- If any remote config, key, state, authorized_keys, or service file write fails
  with disk-full or permission-denied after a persisted peer record exists, the
  peer enters `provision_failed` with the failing path category and operation
  recorded in redacted logs. Before a peer record exists, the same failure stays
  operation-local. The UI must not show the peer as `ssh_keys_ready`.
- If the remote `shared_key` preflight fails with `mismatched_valid`,
  `invalid_malformed`, or `unreadable_or_locked`, provisioning records a copyable
  regular-SSH diagnostic and makes no remote or local peer mutation. If
  preflight recorded `legacy_shared_key_present` and a later pre-final-write
  phase fails after a peer record exists, the peer remains `provision_failed`
  with `legacy_shared_key_present` in remediation history so cleanup copy does
  not imply that the remote never held the fleet credential.
- If payload install fails before any remote config/key/authorized_keys mutation,
  the app removes the local transient provisioning record, leaves remote
  authorized_keys untouched, and keeps the operation-local log. If the failed
  install may have left uploaded payloads, stage directories, service files, or a
  reusable installed binary, the app also writes the durable operation-scoped
  remediation record above.
- If payload install succeeds but identity/account validation fails before any
  remote config, key, or authorized_keys mutation, the app does not write
  `shared_key` and does not start the daemon. It marks the installed binary as a
  reusable pre-secret artifact for retry when the same target/user/path is used,
  and offers best-effort regular-SSH cleanup of the uploaded payload and service
  files. Cleanup failure is logged but is not a fleet-secret exposure because no
  fleet credential or managed key was written. No peer row is created unless the
  user accepts the remote identity and provisioning reaches the first durable
  peer-record phase.
- If authorized_keys update fails after install succeeds but before a local peer
  row exists, the failure remains operation-local and writes or updates the
  durable operation-scoped remediation record for any remote pre-secret artifacts
  already written. If the same failure occurs after a local peer row exists
  during retry/repair, the peer enters `provision_failed` with the install marked
  successful and the key step marked failed.
- If daemon restart, user-service activation, or command-locked gateway
  verification fails after writing final config with `shared_key`, the peer
  enters `shared_key_written_unverified`. The UI shows that the remote may have
  the fleet credential and offers retry verification, regular-SSH cleanup, peer
  removal, or fleet shared-key rotation guidance.
- If command-locked version verification succeeds but remote promotion from
  `shared_key_written_unverified` to `ssh_keys_ready` fails, the local peer stays
  `shared_key_written_unverified`; retry resumes remote promotion over regular
  SSH before any local promotion.
- If remote promotion succeeds but local promotion to `ssh_keys_ready` fails,
  the app keeps the local peer orange, records `local_promotion_failed`, and
  offers retry promotion or regular-SSH remote demotion/removal. This is a
  bounded inconsistency, not green sync: local promotion is last, so the local
  daemon does not start outbound sync from an unpromoted record.
- If failure occurs after writing remote config containing `shared_key`, cleanup
  is best-effort: stop the remote user service if it was started, remove the
  managed authorized_keys line if one was written, remove the remote
  `ssh.peers[]` record for the local host if this attempt created it, and delete
  the remote config file only when it was created by this provisioning attempt.
  The app reports that reliable secure deletion of an already-written secret is
  not guaranteed.
- Retrying Add Peer replaces the managed line with the same
  `clipfan-sync:<local-host-id>:<key-id>` marker and does not duplicate keys.

## Compatibility Matrix

| Local app/daemon | Remote daemon | Expected behavior |
|------------------|---------------|-------------------|
| new SSH app | old HTTP daemon | Version check uses regular SSH or reports `peer update required`; no silent HTTP fallback. |
| old app or old CLI | new loopback daemon with config v2 | Unsupported for signed local APIs. Config v2 uses HKDF-derived `clipfan-v1/request-hmac`; old binaries that sign with raw `shared_key` receive `auth_version_mismatch` or `bad_signature` on signed endpoints. The unauthenticated loopback health endpoint may still return `ok`, but clipboard/history/config/repair/update APIs require the new app or CLI. No dual request-signature validation is implemented. |
| SSH cutover daemon with listener gate enabled | old config with generated wildcard listen | Daemon migrates listen to loopback and exposes old `static_peers` only as transient `legacy_http` suggestions until the user starts Add/Repair Peer and reaches the scoped peer-record write phase. Public builds before 17d3a do not enable this row. |
| SSH cutover daemon with listener gate enabled | old config with explicit custom public listen | Daemon binds loopback safe mode only and requires user confirmation before enabling sync. Public builds before 17d3a do not enable this row. |
| SSH-provisioned new peers | new SSH daemon | Persistent stream and on-demand fallback are available. |
| mixed protocol versions | any | Gateway returns `unsupported_protocol`; UI labels peer as requiring update. |

Release notes for the SSH transport release must say that peer updates are
required and that public `7853` is no longer supported.

Old app/CLI recovery is intentionally outside the signed API. The app release
must update the Mac app and local CLI before or with any daemon that writes
config v2. If a user ends up with a config v2 daemon and only an old client, the
supported recovery is to install the current app/CLI or use the current CLI's
offline config repair. Old clients are not granted a special unauthenticated
repair API.

Compatibility decision: the SSH transport release intentionally does not
provide a peer-HTTP compatibility transport. Migration states exist to label
old peers, guide regular-SSH updates, and avoid showing broken sync as healthy.
Adding a runnable compatibility transport later would be a separate design
decision and would require explicit approval because it reintroduces the public
daemon exposure this design removes.

## Peer States

Each persisted SSH peer has an explicit migration state so the app never
presents broken sync as healthy. Old `static_peers` that have not been converted
to `ssh.peers[]` are not persisted peers; they appear only as transient
`legacy_http` status suggestions with `source:"static_peers"`.

- `ssh_material_staged`: non-secret SSH material has been written for the peer,
  such as host identity, loopback config skeleton, peer records, known-host
  entries, sync keys, and managed authorized_keys lines, and no fleet
  `shared_key` has been written for this provisioning attempt. The daemon
  rejects all gateway reservations for this peer with `peer_not_ready`. Once a
  `shared_key` is written remotely, the peer must be
  `shared_key_written_unverified` until cleanup/removal or promotion.
- `ssh_keys_ready`: the peer was successfully provisioned for its enabled
  directions and passed command-locked gateway verification at promotion time,
  but runtime sync may or may not be connected now. For `accept:true`, promotion
  required local config allowing the peer, the matching managed authorized_keys
  line on this host, and matching `proof.accept_*` fields. For `connect:true`,
  promotion also required a sync private key, known-host entry, and
  `proof.connect_*` record for the outbound SSH target. Accept-only peers with
  `connect:false` did not need outbound known-host records or `connect` proof.
- `loopback_unprovisioned`: local daemon has migrated away from network HTTP,
  but this peer has no working SSH transport. Sync is intentionally stopped for
  this peer.
- `provision_failed`: the app attempted SSH provisioning and captured a
  copyable failure log before any remote fleet `shared_key` write occurred.
  Failure after a remote `shared_key` write is never represented by this state;
  it remains `shared_key_written_unverified` until verified cleanup, promotion,
  or explicit delete/remediation audit.
- `shared_key_written_unverified`: final config containing the fleet
  `shared_key` was written to the remote host, but service start or
  command-locked gateway verification failed. The UI must treat this as a
  security-relevant remediation state, not a harmless retry. The only
  command-locked gateway purpose allowed in this state is `version` for retry
  verification; clipboard `receive` and `sync-stream` are stopped.

`ssh_sync_ready` is derived runtime health, not persisted config. The daemon
reports it when the persisted state is `ssh_keys_ready` and at least one
authorized latest-state exchange has succeeded. For a persistent `sync-stream`,
readiness remains true while the same stream remains hello-verified, writable,
and ping/pong healthy. Green UI is the layer that requires an empty
green-blocking warning set; `transport_health:"ssh_sync_ready"` may coexist with
warnings such as `remote_authorized_key_unknown`. For on-demand `receive`
without a live writable persistent stream, readiness lasts for the 60 second on-demand
freshness window only after the completed bidirectional exchange satisfies the
direction-aware ack-status rule in the on-demand protocol section. A
latest-state exchange is either a command-locked `sync-stream` state frame with a
qualifying ack, a completed command-locked `receive` on-demand exchange with
qualifying direction-aware ack status, or a `clip:null` frame acked as
qualifying `no_state` with `null_reason:"no_visible_current"`. Hello,
ping/pong, standalone command-locked `version` success, `rejected` acks,
`ignored_older`, `ignored_concealed`, `concealed_clear`, and
`user_cleared_current` null markers do not set `ssh_sync_ready` by themselves.
`ssh_keys_ready` records the last successful app provisioning and gateway
verification result. It is not the same as live connectivity. For accept-only
peers, the app can locally verify the managed authorized_keys line and config
record, but it cannot prove the remote private key still exists or that the
remote will connect until an inbound gateway succeeds. Accept-only peers in
`ssh_keys_ready` but with no successful inbound gateway show runtime
`never_connected` with wording "ready to accept; no connection seen", not green.
During promotion, missing or mismatched `accept:true` proof prevents
`ssh_keys_ready`. After a valid promotion, later missing local authorized_keys
material or local proof mismatch does not silently demote persisted
`ssh_keys_ready`; it sets orange runtime health and blocks the affected inbound
reservation until repair succeeds. If persisted `connect:true` proof becomes
missing or malformed after a previous valid promotion, outbound attempts are
blocked until repair succeeds. If the last known-good remote proof is merely
stale because it has not been refreshed by a newer regular-SSH check, runtime
health is orange `remote_authorized_key_unknown` until a regular-SSH
repair/check refreshes proof.
Implementation invariant: no session-start, status, or UI code may treat
persisted `ssh_keys_ready` alone as live readiness. Operational readiness always
requires persisted state, the relevant outbound/inbound runtime gate,
successful transport health, and an empty green-blocking warning set. UI green
is only the explicit table row below.

Formal proof invariant:

- Promotion invariant: a transition into `ssh_keys_ready` requires current proof
  for every enabled direction at the time of promotion. Missing, stale, or
  mismatched proof blocks promotion.
- Config-load invariant: loading persisted `ssh_keys_ready` validates schema,
  required fields, and proof field shape. Missing files or local material drift
  are reported as runtime health and action blocks, not as config parse failure
  or silent persisted demotion.
- Drift invariant: after a peer was validly promoted, later local drift or
  unavailable remote proof does not silently demote persisted
  `migration_state`. The daemon/app reports orange runtime health such as
  `permission_drift`, `missing_sync_key`, `missing_known_host`, or
  `remote_authorized_key_unknown`. A remote accept-only host missing its own
  unused local sync key is reported separately as
  `remote_accept_only_missing_sync_key` and does not prove local drift for this
  host's active transport path.
- Drift action policy: missing local material (`missing_sync_key`,
  `missing_known_host`, or `permission_drift`) blocks the affected outbound or
  inbound action until local repair succeeds. Missing or malformed persisted
  `connect` proof blocks outbound command-locked version, stream, and on-demand
  attempts. Stale remote proof (`remote_authorized_key_unknown`) does not block
  those outbound attempts by itself; the daemon may attempt them with normal
  backoff using the last verified config, and success updates runtime health but
  does not refresh persisted proof or clear the stale-proof warning. Only a
  regular-SSH repair/check clears `remote_authorized_key_unknown`. Reservations
  for inbound peers continue to use local config and local authorized_keys
  checks.
- Demotion invariant: persisted demotion from `ssh_keys_ready` to
  `ssh_material_staged`, `loopback_unprovisioned`, or removed state happens only
  through explicit user repair/removal, migration repair, or a signed config
  write that records the reason in logs.
Runtime health detects local drift after provisioning. The daemon can report
missing local sync private keys, missing local known-host entries for
`connect:true` peers, and local permission drift as `missing_sync_key`,
`missing_known_host`, or `permission_drift`. Remote authorized_keys drift for
outbound peers cannot be checked silently by the daemon; the app reports
`remote_authorized_key_unknown` until a user-initiated regular-SSH repair/check
verifies or fixes it. The persisted migration state remains `ssh_keys_ready`
until repair succeeds or the user disables/removes the peer.

Runtime status is composite. `transport_health` records the active connection
state such as `ssh_sync_ready`, while `warnings` records green-blocking security
or repair conditions such as `remote_authorized_key_unknown`. `notices` records
non-green-blocking future-topology facts such as
`remote_accept_only_missing_sync_key`. A successful latest-state stream exchange
or completed direction-aware on-demand exchange may set
`transport_health:"ssh_sync_ready"`. Command-locked `version` success records
runtime version readiness and may clear version warnings, but it is not a
separate prerequisite for setting `ssh_sync_ready`.
The UI remains orange while `version_mismatch`, `remote_authorized_key_unknown`,
or any higher-precedence warning is present. Green requires working latest-state
transport and an empty green-blocking warning set; non-blocking notices remain
visible in peer details and repair flows without changing the row color.

State model:

| Layer | Values | Persistence | Owner |
|-------|--------|-------------|-------|
| transient migration display | `legacy_http` for old `static_peers` suggestions | not persisted | daemon derives from old config/status |
| persisted migration state | `loopback_unprovisioned`, `ssh_material_staged`, `shared_key_written_unverified`, `ssh_keys_ready`, `provision_failed` | daemon config | app writes through signed config API; daemon reads/enforces |
| derived runtime status | `transport_health` values: `never_connected`, `connecting`, `transport_connected_no_clip_exchange`, `ssh_sync_ready`, `backoff`; `warnings` values: `version_mismatch`, `protocol_error`, `clock_skew_warning`, `missing_sync_key`, `missing_known_host`, `permission_drift`, `remote_authorized_key_unknown`; `notices` values: `remote_accept_only_missing_sync_key` | daemon memory plus app repair checks | daemon/app |
| UI color | green, orange, gray | not persisted | app renders from persisted state plus runtime status |

UI color is determined only by this table; earlier status summaries defer to it.
Disabled/removed is a peer lifecycle state, not a warning, and has highest
precedence:

| Condition | Color | Label family |
|-----------|-------|--------------|
| peer removed or explicitly disabled by the user | gray | disabled/removed |
| daemon safe mode | orange | repair listener |
| transient `legacy_http` suggestion or persisted `loopback_unprovisioned` | orange | SSH setup required |
| `ssh_material_staged` | orange | provisioning |
| `shared_key_written_unverified` | orange | verification/remediation |
| `provision_failed` | orange | failed with copyable log |
| `ssh_keys_ready` plus `transport_health:"ssh_sync_ready"` and empty green-blocking warnings | green | running |
| `ssh_keys_ready` plus `never_connected`, `connecting`, `transport_connected_no_clip_exchange`, or `backoff` | orange | ready/connecting/degraded |
| any warning: `version_mismatch`, `protocol_error`, `clock_skew_warning`, `missing_sync_key`, `missing_known_host`, `permission_drift`, `remote_authorized_key_unknown`, host-key mismatch, stale remote proof, missing/malformed directional proof, repeated SSH failure, or security warning | orange | repair/check required |

After a daemon migrates its listener to loopback, any old `static_peers` entry
is shown as transient `legacy_http` with the explicit label "SSH setup required;
HTTP sync disabled" and offers the regular-SSH provisioning flow. The app writes
a real `ssh.peers[]` record with persisted `loopback_unprovisioned` only when
the user starts Add/Repair Peer. It must not show the peer as green until
runtime `ssh_sync_ready`.

Descriptive provisioning/runtime state summary:

This table explains user-visible flow and runtime outcomes. It is not the
validator for `POST /v1/config/ssh/peers/<peer_id>/transition`; that endpoint's
persisted-state validator is the normative table in the scoped config API
section.

| From | Trigger | Probe/update behavior | To |
|------|---------|-----------------------|----|
| transient `legacy_http` suggestion | user starts Add/Repair Peer with regular SSH credentials | app writes the first real `ssh.peers[]` record with `loopback_unprovisioned`; no automatic public HTTP probe; no background regular-SSH probe | persisted `loopback_unprovisioned` |
| `loopback_unprovisioned` | user starts Add/Repair Peer with regular SSH credentials | run regular SSH `clipfan version --json`; if old or missing, offer update/install over regular SSH | `ssh_material_staged` after successful install, host-key pin, non-secret config write, and managed key write |
| `loopback_unprovisioned` | regular SSH target unreachable or authentication fails after a persisted peer record exists | capture copyable failure log and remote secret absence proof | `provision_failed` |
| `provision_failed` | user retries with regular SSH credentials | resume from the failed pre-secret step using idempotent install/key/config actions | `ssh_material_staged` on successful pre-secret staging, otherwise `provision_failed` |
| `ssh_material_staged` | app writes final config with `shared_key` and enables version-only gateway verification | capture remediation log and warn that remote may have fleet credential if verification fails | `shared_key_written_unverified` |
| `shared_key_written_unverified` | command-locked version gateway succeeds and app promotes remote then local peer config | retry remote/local promotion with regular SSH if either write fails | `ssh_keys_ready` only after both promotion writes succeed |
| `shared_key_written_unverified` | user retries verification or cleanup with regular SSH credentials | retry gateway verification or best-effort remove remote secret/key material | `ssh_keys_ready` on verification and promotion success, otherwise `shared_key_written_unverified` or removed |
| `ssh_keys_ready` | command-locked version gateway succeeds | record runtime version readiness and clear version warning when versions are compatible; do not mark sync ready | persisted `ssh_keys_ready`, runtime version ready |
| `ssh_keys_ready` | command-locked persistent latest-state stream exchange or eligible completed on-demand exchange qualifies under the direction-aware ack-status rule | mark runtime sync ready | persisted `ssh_keys_ready`, runtime `ssh_sync_ready` |
| runtime `ssh_sync_ready` | version mismatch, protocol error, or repeated SSH failure | keep persisted provisioning state but show orange runtime health | persisted `ssh_keys_ready`, runtime orange |

Regular-SSH version probes are user-initiated through Add/Repair/Update flows or
explicit "Check Version" actions. The app does not silently try arbitrary SSH
credentials for old peers in the background.

Fleet upgrades are per peer. A peer that can be reached with regular SSH can
move from `loopback_unprovisioned` through `ssh_material_staged` and
`shared_key_written_unverified` to `ssh_keys_ready` while another peer stays
`loopback_unprovisioned`, staged, unverified, or `provision_failed`. The fleet
remains multi-master among the peers with working SSH transport. Unreachable
peers do not receive updates until regular SSH repair succeeds, and they never
receive a temporary public HTTP fallback.

## Implementation Surfaces

The plan should keep ownership close to existing modules:

- Local HTTP auth, loopback handlers, version/history/config endpoints:
  `internal/transport/auth.go`, `internal/transport/server.go`, and
  `internal/transport/*_test.go`.
- Daemon sync state, receive/apply behavior, peers/status snapshots, and session
  manager integration: `internal/daemon/daemon.go` and focused tests under
  `internal/daemon/`.
- Config schema, config-version migration, path/mode validation, and
  `shared_key` generation/loading: `internal/config/config.go` and
  `internal/config/config_test.go`.
- CLI signing and local commands: `internal/cli/cli.go` and `cmd/clipfan/`.
- SSH transport, gateway parser, frame protocol, provision subcommands, and
  OpenSSH integration tests should live in new focused packages under
  `internal/sshtransport/` or equivalently named small packages chosen during
  implementation; they should not bloat `internal/transport/server.go`.
- Mac app signing, daemon client, add/update peer flows, status/log UI, and
  offline repair: `apps/mac/Clipfan/Sources/Clipfan/Signing.swift`,
  `DaemonClient.swift`, `Installer.swift`, `AddPeerSheet.swift`,
  `UpdatePeerSheet.swift`, `PeerUpdateLog.swift`, `PeerHealth.swift`, and
  `FleetRow.swift`.
- Installer/service defaults and release gates: `dist/install.sh`,
  `dist/clipfan.service`, launchd plist files, GitHub workflow files, and
  release scripts.

## Migration Plan

These milestones are implementation slices, not release boundaries. No public
release may advertise or enable SSH peer sync, remote `shared_key` writes,
public Add Peer success, public-green peer rows, or command-locked clipboard
runtime until the loopback security baseline, SSH sync path, and no-off-host-HTTP
tests are all green. Milestone 17d3a is the explicit exception for local
security hardening: it may ship a public build that disables legacy off-host peer
HTTP sync and moves generated listeners to loopback before public SSH sync is
enabled, but it must keep Add Peer/sync orange or unsupported and must not claim
SSH sync success. If work is merged incrementally, incomplete paths stay behind
an internal feature flag and are excluded from public release builds until the
matching cutover slice is complete. Internal builds that expose provisioning
before end-to-end sync exists must render the peer as orange with an explicit
"provisioned; sync unavailable" state, not green `ssh_sync_ready`.

Public HTTP test modes:

- Tracking mode runs during incremental milestones and may contain temporary
  owner-commented allowlist entries for known pre-replacement public peer HTTP
  callsites. Tracking mode must stay green and the allowlist must shrink or stay
  flat.
- Release-blocking mode runs in release CI and after Milestone 9d3 for
  version/update paths. It fails if app or daemon peer runtime code can call
  `http://<peer>:7853`, including version probes, fanout, update verification,
  retry paths, and migrated old configs. Only loopback HTTP calls and test-only
  fixtures may remain.

Every milestone that touches peer version, provisioning, update, relay,
fallback, or migration logic must run the appropriate mode. Each milestone that
introduces a new failure phase must also expose a redacted status/log field for
that phase before the feature is user-visible.

The suite has four named sub-gates, all blocking release:

- `daemon-peer-http-runtime`: daemon fanout, relay, receive, retry, and migrated
  config paths cannot construct or dial off-host peer HTTP URLs.
- `app-peer-version-update`: Mac app version checks, update verification, and
  failure retry paths cannot call public peer HTTP.
- `migration-no-http-fallback`: old config migration, `legacy_http`, and
  `loopback_unprovisioned` states never probe public peer HTTP in the
  background.
- `fixture-allowlist`: tests and fixtures may contain public HTTP examples only
  in explicitly allowlisted legacy fixtures; production packages fail the scan.
  The allowlist is stored in the test suite with owner comments and must shrink
  or stay flat in every milestone; adding a new public peer HTTP allowlist entry
  requires an explicit test failure message naming the package and reason.
  Security/release engineering owns allowlist removal, and release CI fails if
  any temporary allowlist entry remains past its declared milestone.

Public HTTP callsite disposition:

- Milestone 1c blocks off-host daemon peer HTTP runtime in
  `internal/daemon/daemon.go`, `internal/discovery/static.go`,
  `internal/discovery/tailscale.go`, and `internal/transport/client.go` when
  the build-level `PeerHTTPRuntimeDisabled` gate is true. This gate is
  independent of config schema and is not derived from `transport:"ssh"`.
  Tracking-mode tests may allow existing production callsites only as
  owner-commented temporary entries that name the replacement milestone.
- Milestone 9d3 removes or rewires public HTTP peer version/update construction
  in `apps/mac/Clipfan/Sources/Clipfan/PeerVersion.swift`,
  `apps/mac/Clipfan/Sources/Clipfan/RemoteUpdateOfferController.swift`,
  `apps/mac/Clipfan/Sources/Clipfan/UpdatePeerSheet.swift`,
  `apps/mac/Clipfan/Sources/Clipfan/PeerUpdateLog.swift`, and their tests
  `PeerVersionProbeTests.swift`, `PeerUpdateVerifierTests.swift`,
  `PeerUpdateAdvisorTests.swift`, and `PeerUpdateLogTests.swift`.
- Milestone 16c removes or rewires public HTTP relay/fanout callsites in
  `internal/daemon/daemon.go`, `internal/discovery/static.go`,
  `internal/discovery/tailscale.go`, `internal/transport/client.go`, and the
  related daemon/discovery/transport tests. After 16c, those production packages
  may construct only loopback HTTP URLs or SSH transport commands for peer
  runtime behavior.
- Milestone 17d3b0 is the release-blocking peer-HTTP inventory prerequisite:
  no production off-host peer HTTP runtime path may remain unclassified or
  reachable before 17d3b packaging. Optional post-17d3b cleanup is limited to
  deleting test-only fixtures/helpers already classified by that inventory. It
  is not allowed to be the first milestone that removes a production public HTTP
  runtime path.

Public Add Peer success gate: public builds must not show a peer-add operation
as successful/green until the 17d3b public Add Peer/sync acceptance bundle passes
for persistent latest-state SSH exchange and direction-aware on-demand
latest-state SSH fallback. For the default one-way Add Peer flow, the
`connect:true` side must support on-demand fallback when its persistent stream is
down. The `accept:true, connect:false` side is not expected to originate
on-demand until reciprocal setup exists; it can send local outbound clipboard
changes only while an active persistent stream is writable. Milestones before
17d3b may expose only internal provisioning UI or public orange states labeled
"SSH setup required" / "SSH setup pending". Public builds must not use
"provisioned; sync unavailable"; that wording is reserved for internal/test
builds. This is one release gate, not a per-milestone judgment call. The 17d3b
acceptance bundle is proven with ephemeral integration hosts under the internal
secret-write flag and with the real remediation controls enabled. Public builds
never use the internal flag at runtime.

Public local listener/config cutover is a separate gate from public remote
secret writes. Milestone 17d3a may ship a public security hardening release that
persists config v2 and migrates generated peer-HTTP listeners to loopback after
the local hardening, no-peer-HTTP, backup, rollback export, downgrade-blocking,
and regular-SSH install/update gates pass. Internal real-OpenSSH sync acceptance
may run as a sentinel before or during 17d3a, but it is a public 17d3b blocker,
not a 17d3a packaging blocker. The 17d3a release intentionally keeps public
remote `shared_key` writes disabled and keeps Add Peer rows orange. Milestone
17d3b is the later public peer-add enablement gate that permits real remote
`shared_key` writes and green/success UI.

Release gate source of truth:

| Build context | Remote `shared_key` write | Add Peer green/success UI | Required gate |
|---------------|---------------------------|---------------------------|---------------|
| public/release before Milestone 17d3a | no; provision client returns `unsupported_command` before invoking remote `write-config` with `shared_key` | no; render orange "SSH setup required" or hide unfinished action | all generated gates false |
| public/release at Milestone 17d3a | no; provision client still returns `unsupported_command` before invoking remote `write-config` with `shared_key` | no; render orange "SSH setup pending" or "SSH setup required" | `PeerHTTPRuntimeDisabled=true`, `ConfigV2WriteEnabled=true`, `RemoteSecretWriteReleaseEnabled=false`, `ssh_public_add_peer_success_enabled=false` |
| non-release developer/test before Milestone 17d3b | yes only with `ALLOW_REMOTE_SECRET_WRITE` and explicit ephemeral fixture target | no public-green UI; internal UI must label state as not release-ready | internal fixture test flag |
| public/release after Milestone 17d3b acceptance | yes | yes, only after runtime `ssh_sync_ready`, verified on-demand fallback support, and host-global sync-key rotation support are all available in the release; a specific peer row may turn green from a qualifying persistent latest-state ack while that stream remains live/writable, or from a completed direction-aware on-demand exchange inside the on-demand freshness window | transport gates all true and runtime gates `ssh_receive_primitive_enabled=true`, `ssh_sync_stream_enabled=true`, `ssh_persistent_current_enabled=true`, and `ssh_sync_key_rotation_enabled=true` in the same release bundle |

`PeerHTTPRuntimeDisabled`, `ConfigV2WriteEnabled`,
`RemoteSecretWriteReleaseEnabled`, and `ssh_public_add_peer_success_enabled` are
generated from the same release manifest. `PeerHTTPRuntimeDisabled=true` plus
`ConfigV2WriteEnabled=true` with the two public peer-add gates false is valid
only for the Milestone 17d3a local cutover. A public build is invalid if
`PeerHTTPRuntimeDisabled` and `ConfigV2WriteEnabled` differ, if
`RemoteSecretWriteReleaseEnabled` and `ssh_public_add_peer_success_enabled`
differ, if either peer-add gate is true while `ConfigV2WriteEnabled` is false,
or if any peer-add gate is true while the public Add Peer success UI remains
disabled.
This table is normative for public Add Peer behavior. Milestone text and flow
steps are derived summaries and must not enable `add_peer_provision_mutation`,
remote `shared_key` writes, or green/success UI earlier than this table allows.

Release manifest contract:

- Source artifact: `release/ssh-transport-gates.json`.
  Milestone 0a1 owns creating the `release/` directory when it does not already
  exist. Outside that initial creation slice, a missing or malformed source
  artifact is a release-blocking `release_gate_manifest_missing` or
  `release_gate_manifest_invalid` failure, not an instruction for consumers to
  use defaults.
- Generated Go output: `internal/releaseflags/ssh_transport_gates.go`.
- Generated Swift output:
  `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift`.
- Release CI regenerates both outputs, fails on diff, and asserts
  `PeerHTTPRuntimeDisabled`, `ConfigV2WriteEnabled`,
  `RemoteSecretWriteReleaseEnabled`, and
  `ssh_public_add_peer_success_enabled` match the milestone gate matrix.
- Public 17d3b enablement is invalid unless the same release bundle also sets
  `ssh_receive_primitive_enabled`, `ssh_sync_stream_enabled`, and
  `ssh_persistent_current_enabled`, and `ssh_sync_key_rotation_enabled` true in
  `release/ssh-runtime-gates.json` and passes the release-blocking persistent,
  on-demand latest-state, and host-global sync-key rotation tests. The transport
  gate table controls Add Peer and remote `shared_key` authority; the runtime
  gate manifest controls whether the runtime commands can actually exchange
  clipboard state and rotate command-locked sync keys.
- `PeerHTTPRuntimeDisabled` is false in public builds until the Milestone 17d3a
  local listener/config cutover. When true, daemon peer runtime code cannot
  construct or dial off-host peer HTTP URLs, regardless of config schema,
  missing `transport`, old `static_peers`, or generated wildcard listener
  values.
- `ConfigV2WriteEnabled` is false in public builds until the Milestone 17d3a
  local listener/config cutover. Milestone 0 and 1 code may exist before then,
  and Milestone 1e may enable config v2 writes for internal/test manifests after
  Milestones 1a-1d pass, but public app/daemon/updater paths must not persist
  `config_version:2` or migrate generated peer-HTTP listeners before the local
  cutover release is ready.
- Go provision-client tests and Swift Add Peer UI tests consume only generated
  constants, never ad hoc environment checks, for public-build behavior.

Runtime gate contract:

- Source artifact: `release/ssh-runtime-gates.json`.
  Missing or malformed runtime manifests fail with the same named manifest
  errors as the transport manifest after Milestone 0a2 creates the file.
- Generated Go output: `internal/releaseflags/ssh_runtime_gates.go`.
- Generated Swift output:
  `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHRuntimeGates.swift`.
- This separate manifest owns runtime slice gates such as
  `ssh_receive_primitive_enabled`, `ssh_sync_stream_enabled`, and
  `ssh_persistent_current_enabled`, plus the release-blocking rotation gate
  `ssh_sync_key_rotation_enabled`. These gates may be enabled for internal
  fixture builds slice-by-slice, but public release profiles keep them false
  until the matching release-blocking acceptance slice passes. At Milestone 17d3b
  public enablement, these four runtime gates flip true in the same release
  artifact as the public Add Peer/secret-write transport gates.
- Public release CI regenerates both the public release-gate outputs and the
  runtime-gate outputs. The public release-gate manifest still contains
  exactly the four public gates above; adding runtime gates to that file
  is invalid. Adding ad hoc runtime flags outside the generated runtime-gate
  outputs is also invalid.
- `ssh_public_add_peer_success_enabled` remains a public release gate in
  `ssh-transport-gates.json`; it is not duplicated in the runtime-gate manifest.

Release gate checklist:

- `.github/workflows/release.yml` contains a `release-gates-generate` job that
  runs the gate generators, fails on any diff to
  `internal/releaseflags/ssh_transport_gates.go` or
  `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift`,
  `internal/releaseflags/ssh_runtime_gates.go`, or
  `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHRuntimeGates.swift`; asserts the
  public release manifest contains exactly `PeerHTTPRuntimeDisabled`,
  `ConfigV2WriteEnabled`, `RemoteSecretWriteReleaseEnabled`, and
  `ssh_public_add_peer_success_enabled`; and asserts the runtime manifest owns
  all runtime gates referenced by daemon or Swift code. The job fails if
  `ssh_public_add_peer_success_enabled` or `RemoteSecretWriteReleaseEnabled` is
  true while any required runtime latest-state or sync-key rotation gate is
  false.
- `.github/workflows/release.yml` contains a `public-http-runtime-scan` job that
  runs the four named sub-gates `daemon-peer-http-runtime`,
  `app-peer-version-update`, `migration-no-http-fallback`, and
  `fixture-allowlist` in release-blocking mode.
- `.github/workflows/release.yml` contains phase-aware
  `openssh-fixtures-macos` and `openssh-fixtures-ubuntu` jobs. Before 17d3b,
  including the 17d3a local hardening release, these jobs run as non-blocking
  sentinels when configured; missing macOS fixture runners record
  `macos_ssh_fixture_unavailable` in CI artifacts but do not block 17d3a
  packaging. When a branch, tag, or manifest enables
  `RemoteSecretWriteReleaseEnabled` or `ssh_public_add_peer_success_enabled`,
  the same jobs become release-blocking and fail closed if unavailable or
  failing.
- In blocking 17d3b mode, the macOS job runs on the hosted `macos-26` runner or
  an explicitly configured self-hosted `clipfan-macos-ssh-fixture` runner,
  creates two temporary non-root local users on the runner, starts the macOS
  bundled OpenSSH server on high localhost ports for those users, disables
  inbound daemon TCP port 7853 in the fixture, uses regular SSH credentials for
  install/update, uses command-locked clipfan keys for `version`, `receive`, and
  `sync-stream`, and verifies one-hop persistent latest-state exchange,
  on-demand reconnect latest-state exchange, successful host-global sync-key
  rotation, failed verification rollback, and stale old-line cleanup reporting.
- In blocking 17d3b mode, the Ubuntu job runs on `ubuntu-latest`, starts at least
  two Ubuntu LTS OpenSSH targets as containers or local non-root fixture users
  with forced-command authorized_keys enabled, exposes only sshd fixture ports,
  blocks daemon TCP port 7853 between peers, and runs the same regular-SSH
  install/update and command-locked runtime checks as the macOS job.
- The fixture may add a third ephemeral peer only for relay acceptance. That
  third peer is not required before Milestone 16, but Milestone 16 release gates
  must prove three-host tuple ordering and no public HTTP relay/fanout.
- Required Go test selectors before the 17d3a public local hardening release are
  `TestConfigV2WriteGate`, `TestNoPublicSecretWriteSurfaceBeforeProvisionClient`,
  `TestRegularSSHMutationRequiresPinnedHostKey`,
  `TestDaemonPeerHTTPRuntimeBlocked`, and `TestMigrationNoHTTPFallback`.
  If provision-client stubs already exist before 17d3a, the same release
  candidate must also prove they reject public secret writes and public Add Peer
  provision mutation; 17d3a does not require creating those stubs solely to test
  rejection.
- Additional required Go test selectors before 17d3b public Add Peer/sync
  enablement are `TestProvisionClientRejectsPublicSecretWrite`,
  `TestProvisionClientRejectsMutationWithoutPinnedHostKey`,
  `TestPersistentSSHLatestStateExchange`,
  `TestSSHRelayLatestStateConvergenceNoPeerHTTP`,
  `TestOnDemandSSHLatestStateExchange`, `TestHostGlobalSyncKeyRotation`, and
  `TestHostGlobalSyncKeyRotationRollback`.
- The full Swift test suite remains release-blocking for each public release
  candidate. Required Swift test selectors before the 17d3a public local
  hardening release include, at minimum,
  `AddPeerReleaseGateTests`, `PeerVersionProbeTests`,
  `PeerUpdateVerifierTests`, `PeerUpdateLogTests`, `FleetRowTests`,
  `HostKeyTrustTests`, and `SSHIdentityFileValidationTests`.
- Additional required Swift test selectors before 17d3b public Add Peer/sync
  enablement include `RemoteSecretWriteGateTests`,
  `SharedKeyRemediationTests`, `AddPeerTopologyValidationTests`, and
  `SyncKeyRotationTests`. Renaming one of these tests requires updating this
  checklist in the same change, not silently dropping coverage.
- Internal/test CI may exercise `ConfigV2WriteEnabled=true` only in Milestone 1e
  after Milestones 1a-1d pass. Release CI permits public
  `PeerHTTPRuntimeDisabled=true` and
  `ConfigV2WriteEnabled=true` only in the Milestone 17d3a local cutover change,
  after the 17d3a1-17d3a3 local hardening, backup, rollback, downgrade-blocking,
  and no-peer-HTTP gates pass. Real OpenSSH latest-state acceptance may run as a
  non-blocking sentinel before or during 17d3a, but it is not a packaging blocker
  for this local listener/config cutover. Release CI permits public
  `RemoteSecretWriteReleaseEnabled=true` and
  `ssh_public_add_peer_success_enabled=true` only in Milestone 17d3b, after the
  17d3a build has passed local migration/rollback acceptance, Milestone 10c3c
  host-global sync-key rotation acceptance has passed, and Milestone 17d1
  latest-state acceptance has passed in both real OpenSSH fixture jobs. A public
  branch or tag with either peer-add gate true while the public Add Peer success
  UI remains disabled is invalid.

Public build behavior by milestone:

| Milestone range | Public config v2/listener migration | Public regular SSH install/update | Public Add Peer behavior | Public `add_peer_provision_mutation` | Public remote `shared_key` write | Success/green UI |
|-----------------|-------------------------------------|-----------------------------------|--------------------------|------------------------------------|---------------------------|------------------|
| 0-7 | no; implementation is internal/test only | disabled in SSH-transport public packages unless the existing shipped action already has pinned host-key enforcement | unsupported/hidden | no | no | no |
| 8a-10a3 | no; implementation is internal/test only | user-initiated update/check may use regular SSH only after Milestones 4e3b, 4e3c, and 9d1a host-key enforcement tests pass | read-only diagnostics may probe identity/version with regular SSH after host-key confirmation | no in public builds | no | no |
| 10b1-16 | no; implementation is internal/test only | user-initiated update/check may use regular SSH only with the same host-key enforcement boundary | internal/test provisioning may write non-secret config and, only behind internal fixture gates, `shared_key`; public builds still reject Add Peer provision mutation | no in public builds | no in public builds | no |
| 17a-17c | no; implementation is internal/test only | user-initiated update/check may use regular SSH only with the same host-key enforcement boundary | internal/test end-to-end sync acceptance; public builds remain Add Peer provision-mutation disabled until the release gate flips | no in public builds | no in public builds | no |
| 17d3a | yes; public builds disable peer HTTP runtime, may persist config v2, and migrate generated peer-HTTP listeners to loopback | user-initiated update/check may use regular SSH only with the same host-key enforcement boundary | unsupported/hidden or orange "SSH setup pending"; no public Add Peer success | no in public builds | no in public builds | no |
| 17d3b and later | yes | user-initiated update/check may use regular SSH only with the same host-key enforcement boundary | full Add Peer flow may run only when required runtime gates are also true | yes | yes, after non-secret identity/account checks and runtime-gate bundle validation | yes, only after the release includes persistent latest-state exchange, on-demand latest-state exchange, host-global sync-key rotation, required runtime gates are true, and this peer has a live writable persistent stream after a qualifying latest-state ack or a completed direction-aware on-demand exchange inside the freshness window |

Public/interim release policy:

- Security/release engineering owns the decision to ship Milestone 17d3a as an
  interim public hardening release. Milestones before 17d3a may be merged and
  tested, but they must not be packaged as public SSH-transport releases.
- Milestone 17d3a is allowed to break legacy off-host peer HTTP sync by moving
  generated listeners to loopback. The public UI must label affected peers
  orange as SSH setup pending and must not claim sync success until 17d3b.
- Public rollback after `config_version:2` is written is forward-first: ship a
  fixed current build, do not restart a pre-SSH daemon, and do not silently
  downgrade config files. A user-requested emergency offline rollback command may
  write a timestamped backup and export a detached config-v1-compatible
  loopback-only file only when no peer has been promoted beyond
  `loopback_unprovisioned`. That export is a diagnostic/current-tool recovery
  artifact, not an active downgrade: it does not replace the active config by
  default, is not consumed by a pre-SSH daemon, and the app/updater continues to
  block old-daemon service start or reinstall afterward. The command must warn
  that legacy off-host peer HTTP sync remains unsupported.
- If any peer has reached `shared_key_written_unverified` or `ssh_keys_ready`,
  rollback is not a config downgrade. Recovery is a current-version repair,
  cleanup/removal, or shared-key rotation flow so remote fleet secrets and
  managed authorized_keys lines remain accounted for.

Review-slice rule: a milestone may be implemented as multiple PRs, and large
milestones must be split before coding. Host-key provisioning, Milestones
10b1-10b4, Milestones 10c1-10c3, Milestone 11e, and Milestones 14-17 are split
into protocol/data model, daemon enforcement, app UI/log surfacing, cleanup, and
real OpenSSH integration acceptance slices. No slice may expose a public-green UI
or remote secret write before its release gate.

Milestone 0a1: transport gate manifest schema.

- Create `release/` if needed and add `release/ssh-transport-gates.json` with
  exactly
  `PeerHTTPRuntimeDisabled`, `ConfigV2WriteEnabled`,
  `RemoteSecretWriteReleaseEnabled`, and
  `ssh_public_add_peer_success_enabled`, all false for public/release builds.
- Add schema validation tests for legal gate combinations, invalid public
  enablement ordering, missing manifest, malformed manifest, and unexpected
  extra gate names.

Milestone 0a2: runtime gate manifest schema.

- Add `release/ssh-runtime-gates.json` in the `release/` directory created by
  Milestone 0a1, with the internal/runtime slice gates used by later milestones,
  including `ssh_receive_primitive_enabled`,
  `ssh_sync_stream_enabled`, `ssh_persistent_current_enabled`, and
  `ssh_sync_key_rotation_enabled`.
- Add schema validation tests proving public 17d3b transport enablement is
  invalid unless the required runtime latest-state and sync-key rotation gates
  are true in the same release bundle, and proving missing or malformed runtime
  manifests fail with the named manifest errors rather than falling back to
  permissive defaults.

Milestone 0a3: Go gate generation and consumers.

- Generate `internal/releaseflags/ssh_transport_gates.go` and
  `internal/releaseflags/ssh_runtime_gates.go`.
- Add Go tests proving existing daemon and CLI/provision-client consumers that
  exist in this slice consume generated flags instead of ad hoc environment
  checks before any config-v2 write path can run. Swift installer/updater, Add
  Peer, status UI, dist script, and release CI consumers are covered by
  Milestones 0a4-0a5. Provision-client gate tests belong to the milestone that
  introduces the provision client and to the later secret-write enforcement
  milestone.

Milestone 0a4: Swift gate generation and consumers.

- Generate `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift`
  and `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHRuntimeGates.swift`.
- Add Swift tests proving Add Peer, peer update/version UI, and status rendering
  consume generated flags instead of ad hoc environment checks.

Milestone 0a5: release CI gate regeneration.

- Add CI regeneration checks for both manifests and all generated Go/Swift
  outputs, fail on generated diff, and fail when Add Peer/secret-write transport
  gates are true without the required runtime latest-state and sync-key rotation
  gates.
- No config-v2 write path may merge before the generated transport artifact
  exists and is consumed by the code that could persist `config_version:2`.

Milestone 0b: config v2 revision parser and gated scoped-writer plumbing.

- Add the minimal config v2 envelope needed for this migration before adding
  the full SSH schema: parse `config_version:2`, preserve unknown v2 fields on
  scoped updates, maintain the config revision, reject stale revisions, and keep
  atomic lock/rename semantics.
- The config v2 persistence entry point is generated-gate controlled and fails
  closed with `config_v2_writes_disabled` while `ConfigV2WriteEnabled=false`.
  Milestone 0b may land writer code and disabled-path tests, but no
  app/daemon/updater path may actually persist `config_version:2` in this
  milestone.
- Public/release builds must not write `config_version:2` until the Milestone
  17d3a local cutover.

Milestone 0c: HKDF request auth.

- Add dormant HKDF-derived `clipfan-v1/request-hmac` signing helpers in the Go
  daemon/client libraries, CLI, Mac app daemon client, and a shared local
  request-signing helper that the later forced-command gateway loopback bridge
  will use.
- Add the shared local-daemon discovery helper used by the Mac app, CLI, and
  forced-command gateway. It reads config-derived loopback ports, falls back to
  7853 only with signed config/state identity proof for signed endpoints, and
  keeps health-only safe-mode probing separate from signed status or mutation
  calls.
- Add fixed canonical request/response fixtures and helper tests, but do not
  route live config v2 endpoints through the new helper and do not reject raw-key
  clients in this slice.
- Keep unauthenticated loopback health limited to `ok`.

Milestone 0d: client rollout preparation.

- Add dormant tests for new app/CLI signed request success, old app/CLI signed
  endpoint rejection, shared local request-signing helper behavior, and scoped
  config updates preserving config v2 fields.
- Wire the app, CLI, and forced-command gateway to the shared local request
  helper on dormant code paths only. This slice must not switch live config v2
  endpoints and must not reject raw-key clients.
- Add app/CLI startup error-path scaffolding so the later live-auth cutover can
  report old client failures clearly instead of attempting whole-config writes
  or raw-key signed requests against config v2.

Milestone 0e: config v2 recovery scaffolding.

- Add dormant recovery fixtures proving a config v2 daemon plus old client is
  not a trap once signed listener repair and offline listener repair exist.
- Keep the fixtures behind helper paths only. This slice must not switch live
  config v2 endpoints, must not reject raw-key clients, and must not enable
  public config v2 writes.
- Finish local app/CLI startup behavior so old clients fail clearly instead of
  attempting whole-config writes or raw-key signed requests against config v2.
- Milestone 0e may merge only recovery scaffolding and old-client diagnostics.
  Live raw-key rejection and public config v2 write enablement are blocked until
  Milestone 1b5, after Milestone 1b4 implements signed and offline listener
  repair.

Milestone 0f: OpenSSH fixture infrastructure readiness.

- Add non-release CI skeleton jobs for `openssh-fixtures-macos` and
  `openssh-fixtures-ubuntu` before protocol milestones depend on those jobs.
- Prove the macOS runner can create temporary non-root local users, start the
  bundled OpenSSH server on high localhost ports, install forced-command
  `authorized_keys` fixtures, run regular SSH with `BatchMode=yes`, and block
  daemon TCP port 7853 between fixture peers. This milestone does not run the
  clipfan sync protocol; it proves runner capability and fixture control.
- If hosted `macos-26` cannot satisfy those fixture operations, provision and
  document the self-hosted `clipfan-macos-ssh-fixture` runner before the first
  real OpenSSH-dependent protocol/provisioning acceptance milestone proceeds.
  Milestone 1 listener/safe-mode work may proceed without this fixture. In
  OpenSSH-dependent milestone and 17d3b blocking modes, CI must fail closed with
  `macos_ssh_fixture_unavailable` rather than silently skipping the fixture; in
  17d3a local-hardening mode, the unavailable fixture is a recorded non-blocking
  sentinel result.
- Add `docs/ci/clipfan-macos-ssh-fixture.md` before any protocol milestone
  depends on the macOS fixture. The document names the repository release
  maintainer as runner owner, describes hosted-runner eligibility and the
  fallback self-hosted runner setup, requires dedicated non-admin fixture users
  created per run or cleaned after each run, forbids storing fleet `shared_key`
  or private sync keys outside ephemeral test directories, and defines the
  cleanup/isolation checks that must pass before the fixture-available gate is
  considered green.

Milestone 0g: stable host-ID migration.

- Add the scoped host-ID migration write for configs that lack `hostname`. It
  derives once, validates, persists under the config lock, and blocks sync-key
  generation, managed authorized_keys writes, proof records, transport-current
  state, command-locked gateway identity, and SSH provisioning promotion until
  the persisted host ID exists.
- Add tests proving host ID is derived once, persisted before later SSH material
  can be generated, and treated as immutable unless a later explicit identity
  reset flow runs. Cleanup of sync-key metadata, managed authorized_keys, proof
  records, and transport-current state is owned by the milestones that introduce
  those artifacts.

Milestone 0h is split because storage locality affects platform detection,
daemon startup, app repair UI, and updater safety. Milestones 0h1-0h4 must all
pass before the 17d3a listener/config cutover can enable.

Milestone 0h1: runtime storage locality classifier.

- Implement the shared storage locality classifier for the existing config and
  daemon state roots required before config v2 writes. This slice must not
  create, guess, or validate future sync-key, dedicated known_hosts, or
  transport-current roots.
- Add platform tests proving known NFS/SMB/cloud-sync/network-home
  classifications fail closed with no user override, inconclusive filesystems
  return `storage_check_inconclusive`, local-classified roots still require
  owner/mode/lock/atomic-rename smoke tests, and no code claims cross-host lock
  coordination for shared homes.

Milestone 0h2: daemon storage fail-closed behavior.

- Run the classifier before SSH transport, config v2 writes, safe-mode
  classification, or the 17d3a listener/config cutover can bind any socket.
- Add daemon tests proving post-17d3a unsupported or inconclusive storage
  failures bind no socket because the daemon lock cannot be trusted, including
  health-only loopback safe mode.

Milestone 0h3: app/offline storage repair UI.

- Add status/error surfaces for `unsupported_runtime_storage` and
  `storage_check_inconclusive`, plus the app and CLI offline repair prompt to
  move clipfan runtime storage to local per-host paths.
- Add app/CLI tests proving repair guidance comes from offline preflight when
  the daemon cannot bind, and that no daemon endpoint is required for this
  recovery.

Milestone 0h4: updater/service storage abort.

- Add updater/service tests for the 17d3a unsupported-storage abort path: old
  public-listen services are stopped/disabled or the update fails with
  `public_listener_service_still_active`; the updater does not write config v2,
  start the new daemon, report success, or mark the peer green.
- Milestones 4a1, 4b, and 11b add their own locality guard tests before enabling
  sync-key generation, dedicated known_hosts writes, and transport-current
  persistence respectively.

Milestone 1a: gated loopback-default plumbing and internal generated-listen migration.

- Add loopback default generation and generated-listen migration code paths, but
  keep public/release behavior gated. While `ConfigV2WriteEnabled=false`, public
  builds must not rewrite existing generated peer-HTTP listeners and must not
  package generated/installer defaults that silently break legacy off-host peer
  HTTP sync.
- Internal/test manifests may exercise generated config and installer config
  writing loopback listen, plus migration of old wildcard `":7853"`,
  `"0.0.0.0:7853"`, and `"[::]:7853"` configs to loopback on daemon start.
- Milestone 17d3a is the first public release milestone that may enable these
  loopback default and generated-listen migration behaviors.

Milestone 1b1: safe-mode listener classification and bind behavior.

- Implement `public_listen_requires_confirmation` classification for explicit
  custom non-loopback listens, including the documented port derivation order.
- Bind only the derived loopback repair address, never the unsafe configured
  address, and fail closed on loopback bind failure.
- Keep SSH session manager startup, legacy peer sync, on-demand sends, and
  gateway reservations stopped while safe mode is active.

Milestone 1b2a: safe-mode endpoint allowlist and rejection map.

- Implement the safe-mode endpoint allowlist for unauthenticated health and the
  signed local version/status/log endpoint families, with every non-allowlisted
  endpoint rejected by stable error code. Listener repair read/update remains
  unavailable until Milestone 1b4 and returns `listener_repair_unavailable`
  without mutation if called.
- Add routing tests proving the daemon exposes only the allowlisted endpoint
  families while in safe mode and never falls through to normal peer, current,
  clipboard, gateway, or mutation handlers.

Milestone 1b2b: safe-mode HKDF auth enforcement.

- Enforce HKDF `clipfan-v1/request-hmac` auth and auth-version headers for every
  signed safe-mode endpoint introduced in 1b2a; reject old raw-key signatures.
- Add canonical auth fixture tests for success, missing auth-version, old
  raw-key signatures, malformed signatures, stale timestamps when applicable,
  and wrong derived key context.

Milestone 1b2c: minimal safe-mode status/log schema.

- Define and implement the minimal safe-mode schema for `GET /v1/status`,
  compatibility `GET /v1/peers`, and `GET /v1/ssh/logs`, including safe-mode
  fields, existing legacy/static peer suggestions, listener/global log IDs, and
  redacted copyable log entries. Milestone 1b2c must not parse or expose
  `ssh.peers[]`, persisted SSH migration states, or SSH runtime health; those
  fields are defined by Milestone 3 and later runtime milestones. Milestone 2
  may add app UI handling, but it must not invent a different safe-mode schema.
- Add schema tests proving peer-specific SSH logs return the documented
  unavailable status before the SSH peer schema exists, while listener/global
  logs remain copyable.

Milestone 1b2d: safe-mode no-peer-work negative behavior.

- Prove safe-mode endpoints never trigger peer version probes, SSH sessions,
  current APIs, clipboard APIs, on-demand sends, reconnect scheduling, or gateway
  reservations.
- Add negative tests with fake peer/session/current dependencies that would fail
  if any safe-mode status/log route starts peer work.

Milestone 1b3: daemon lock and port-conflict handling.

- Take the daemon lock before binding, write lock diagnostics only after the
  lock is acquired, reject concurrent daemons, and report
  `daemon_lock_timeout` when restart cannot release the lock.
- Detect loopback port conflicts before falling back to any alternate bind, and
  fail closed with `daemon_port_conflict`.

Milestone 1b4: signed and offline listener repair.

- Implement signed `GET/PATCH /v1/config/listener` with
  `expected_config_revision`, `expected_revision_state`,
  stale-revision-state errors, and field allowlisting.
- Add those listener repair read/update endpoints to the safe-mode allowlist only
  in this milestone.
- Implement offline listener repair for missing/invalid `shared_key` or
  unavailable signed repair, including service stop when a health-only daemon is
  running, daemon-lock release verification, config-lock ownership, timestamped
  backup, listener-only edits, and no pre-SSH daemon restart.

Milestone 1b5: config v2 live-auth cutover.

- Add recovery tests proving a config v2 daemon plus old client is not a trap:
  installing the current app/CLI restores signed access, and current CLI offline
  repair can fix listen/port without a running daemon.
- Switch config v2 signed endpoints to require
  `X-Clipfan-Auth-Version: clipfan-v1/request-hmac` and reject raw-`shared_key`
  signatures with `auth_version_mismatch` or `bad_signature`; do not add dual
  validation.
- Prove current-client success, old-client rejection, signed listener repair,
  and offline listener repair all pass in the same acceptance branch before
  merging live raw-key rejection.
- Keep public config v2 write enablement blocked until the Milestone 17d3a
  release gate completes.

Milestone 1c: no-peer-HTTP daemon enforcement.

- Add a release-build peer HTTP kill switch independent of the final SSH schema:
  when `PeerHTTPRuntimeDisabled` is true, discovery/static off-host HTTP fanout
  cannot run even before full `transport:"ssh"` config parsing lands in
  Milestone 3. The gate is build-level and generated; it is not inferred from
  peer config.
- Add a mechanical non-loopback HTTP kill switch/test harness that fails any
  daemon peer runtime attempt to call `http://<peer>:7853`.
- Add tests proving the kill switch blocks old configs with no `transport`, old
  `static_peers`, and generated wildcard listeners.
- This is the 17d3a public HTTP sync prerequisite: production relay/fanout code
  may still exist behind the generated kill switch, but public release builds
  cannot construct or dial off-host peer HTTP for sync. Milestone 16c later
  deletes or rewires the replaced relay/fanout callsites after SSH relay exists.

Milestone 1d: updater downgrade blocking.

- Implement updater blocking so config v2 is written only after the new binary
  is installed and old user services are stopped, and so pre-SSH daemons cannot
  be restarted after migration. This is a blocking release acceptance criterion
  owned by the release engineer, not a follow-up task after public rollout.

Milestone 1e: internal config v2 write enablement.

- After Milestones 1a-1d pass, allow only internal/test manifests to set
  `ConfigV2WriteEnabled=true` and exercise real config v2 writes. This milestone
  is the first slice where daemon/app/updater tests may persist
  `config_version:2`, and it must prove loopback listener migration, peer HTTP
  runtime blocking, and updater downgrade blocking are active in the same test
  run.
- Public/release manifests keep `ConfigV2WriteEnabled=false` until Milestone
  17d3a.

Milestone 1f: confirmed local fleet reset recovery.

- Implement the CLI/app minimal local fleet-reset flow for invalid or lost
  `shared_key` recovery before public config v2 cutover can ship.
- This milestone may land code and tests before 17d3a, but public builds keep
  the reset write path disabled while `ConfigV2WriteEnabled=false`. Public reset
  availability is part of the 17d3a cutover profile, not a separate bypass of
  the config v2 write gate.
- Require owner/mode validation, adjacent config lock acquisition, explicit
  destructive confirmation, timestamped backup, fresh 32-byte `shared_key`,
  preserved or newly-derived local host identity from Milestone 0g, loopback
  listener, no `static_peers`, no peer sync runtime, and fresh config v2
  revision.
- The 1f reset is intentionally pre-SSH-material only. It refuses to run when the
  config or state contains `ssh.peers[]`, sync-key paths, dedicated known_hosts,
  managed authorized_keys metadata, or transport-current files; those cleanup
  extensions land after the corresponding primitives exist and are completed by
  Milestone 11e before public Add Peer/sync enablement.
- Add tests proving reset refuses unparseable, wrong-owner, unsafe-mode, locked,
  or SSH-material configs; never contacts remote hosts; never edits
  authorized_keys; and leaves old remote hosts outside the new fleet until
  regular-SSH re-enrollment or separate reset.
- Add public-profile tests proving the reset write path returns
  `config_v2_writes_disabled` or equivalent recovery guidance before 17d3a, then
  succeeds only in the 17d3a release profile where `ConfigV2WriteEnabled=true`.
- Public 17d3a release CI must include these tests before enabling
  `ConfigV2WriteEnabled`.

Milestone 2: app safe-mode repair and status skeleton.

- Add Mac app safe-mode detection, repair action, daemon restart, and
  post-restart verification that peer sync stays stopped until loopback config
  is confirmed.
- Extend the Milestone 1b2 status/log schema with app-facing rendering for
  listener safe-mode repair status, last failure phase, and copyable safe-mode
  log availability. This milestone must not invent or render SSH peer persisted
  migration-state fields or SSH runtime health; those app surfaces land only
  after Milestone 3 defines the schema and later runtime milestones define the
  corresponding health fields.

Milestone 3: SSH config schema and migration state.

- Add SSH config schema shape validation, migration-state persistence,
  duplicate ID rejection, path expansion, and explicit transport selection.
  Runtime SSH enablement remains blocked until Milestones 4a1-4a2 create local
  sync key/key-ID primitives and strict validation can check real material.
- Add directional proof schema and validation helpers for `accept` and
  `connect`, including key ID, gateway path, verification timestamp, and
  host-perspective semantics. Later reservation and promotion milestones must
  call this helper instead of duplicating readiness checks.
- Add tests proving no off-host HTTP sync fanout is made in SSH transport mode.

Milestone 3b1: scoped peer read/upsert APIs.

- Implement signed loopback `GET /v1/config/ssh/peers/<peer_id>` with secret
  redaction and `PUT /v1/config/ssh/peers/<peer_id>` for non-secret peer record
  creation/update.
- Add tests for peer ID validation, scoped merge semantics, unknown-field
  rejection, stale revision conflicts, migration-state change rejection, and
  unknown config v2 peer-field preservation for read/upsert only.

Milestone 3b2: scoped proof patch API.

- Implement signed loopback `PATCH /v1/config/ssh/peers/<peer_id>/proof`.
- Add tests for proof shape validation, host-perspective semantics, stale
  revision conflicts, rejection of unknown proof fields, and preservation of
  unrelated peer/config fields.
- Add identity-reset proof tests proving proofs tied to an old local host ID or
  old peer ID are marked stale and cannot promote a peer to `ssh_keys_ready`.

Milestone 3b3: scoped transition validator.

- Implement signed loopback `POST /v1/config/ssh/peers/<peer_id>/transition`
  using the normative persisted-state transition table in this spec.
- Add tests for legal transitions, illegal runtime/UI `to_state` rejection,
  required proof/log fields, stale revision conflicts, and exactly-one config
  revision increment.

Milestone 3b4: scoped disable/delete APIs.

- Implement signed loopback `POST /v1/config/ssh/peers/<peer_id>/disable` and
  `DELETE /v1/config/ssh/peers/<peer_id>` as config-level mutations. This slice
  writes disable/delete audit entries, pre-secret SSH-material cleanup records
  and post-secret tombstones when required, local cleanup intent, and
  stale-remediation status, but it does not claim live session or gateway
  enforcement before those systems exist.
- Add tests proving config-level disable/delete preserve unrelated peer records,
  write required SSH-material cleanup records or post-secret tombstones before
  removing the peer row, reject stale revisions, retain disabled key material
  for repair, and expose pending cleanup status.

Milestone 3b5a: app daemon-client scoped API wrappers.

- Add Swift daemon-client helpers for the scoped peer read/upsert, proof,
  transition, disable, and delete endpoints, with typed request/response models
  and redaction tests.
- Add stale revision retry tests against fake daemon responses; no provisioning
  flow is rewired in this slice.

Milestone 3b5b: app provisioning state-write cutover.

- Rewire Add/Repair Peer state writes for peer records, proof, promotion,
  demotion, disable, and removal to use the scoped daemon-client helpers when a
  daemon is available.
- Add tests proving no whole-config peer writes remain in those flows and unknown
  config v2 fields survive the full peer lifecycle.

Milestone 3b5c: scoped retry/log/UI behavior.

- Add UI and retry behavior for scoped stale-revision conflicts, tombstones,
  redacted logs, and operation failure messages.
- Add tests proving scoped API failures render orange with copyable logs and do
  not silently fall back to whole-config mutation.

Milestone 4a1: sync key creation model.

- Add local sync key generation, key-ID tracking, and
  `<sync_key>.clipfan.json` sidecar creation for the local host ID.
- Add sync-key root locality checks before creating or writing key material.
- Add tests for first creation, existing-key refusal, sidecar creation, host ID
  binding, unsupported/inconclusive sync-key storage refusal, and no private key
  disclosure in logs.

Milestone 4a2: sync key load and validation.

- Validate sidecar schema, host ID binding, public key digest/key ID matching,
  owner/mode on private key, public key, and sidecar files, and refusal to use
  group/world-writable material.
- Refuse to overwrite existing private keys or mismatched sidecars
  automatically; report `sync_key_identity_mismatch` or `missing_sync_key` and
  require explicit repair or rotation.
- Add tests for load, mode repair where safe, wrong host ID, digest mismatch,
  missing sidecar, stale sidecar, and no private key disclosure in logs.

Milestone 4a3: sync key reset and identity integration.

- Extend confirmed local fleet reset to regenerate the local sync key only after
  these primitives exist, and add reset tests proving stale or mismatched sync
  key material is cleared without logging private key contents.
- Add host-identity reset tests proving sync-key sidecars bound to the old host
  ID become stale and cannot be reused after rename/reset.

Milestone 4b: known-hosts storage.

- Add dedicated known-hosts storage and exact host/port tuple handling.
- Add dedicated known_hosts root locality checks before writing the store.
- Add known-host retention tests for already-implemented disable/delete APIs:
  disabled peers retain pins for repair/re-enable, while deleted peers prune only
  pins no other peer references.
- Extend confirmed local fleet reset to clear dedicated known_hosts entries only
  after this storage primitive exists.

Milestone 4d: explicit SSH target and auth policy.

- Add explicit regular-SSH target policy: user/host/port only, no SSH config
  aliases, ProxyJump, or ProxyCommand in this release.
- Add regular-SSH auth policy: validated optional identity-file argv support,
  agent/default-identity support when no explicit key is supplied,
  `BatchMode=yes`, clear `ssh_auth_required`/`ssh_auth_failed` errors, and tests
  proving runtime sync-key SSH ignores the user's regular SSH credentials.

Milestone 4e1: host-key scan and parse.

- Add `ssh-keyscan`, host-key line parsing, fingerprint calculation, supported
  algorithm filtering, and no-mutation failure behavior.

Milestone 4e2: host-key confirmation UI.

- Add fingerprint display, keyboard/VoiceOver accessibility, explicit user
  confirmation, cancellation behavior, and copyable fingerprints.

Milestone 4e3a: persisted known-host enforcement.

- Add the shared enforcement object that returns a confirmed pinned host key for
  an exact host/port tuple from dedicated known_hosts storage.
- Add tests proving failed or cancelled TOFU confirmation leaves the remote
  untouched and no persisted peer mutation can proceed without the confirmed pin.

Milestone 4e3b: operation-scoped legacy host-key enforcement.

- Add operation-scoped temporary known_hosts files for non-persisted legacy
  version/update actions.
- Add tests proving legacy actions use `StrictHostKeyChecking=yes`, the
  operation-scoped pinned known_hosts file, `GlobalKnownHostsFile=/dev/null`, and
  `UpdateHostKeys=no`; a matching global known_hosts entry must not satisfy
  clipfan's dedicated pin requirement. Legacy actions never persist SSH peer
  records or green sync state.

Milestone 4e3c: SSH/SCP argv enforcement boundary.

- Route mutating SSH/SCP command construction through the enforcement object.
  This milestone proves the boundary for the mutating SSH/SCP paths that already
  exist at the time it lands, especially install/update upload and verification.
  It also exposes a test helper used by later milestones.
- Every later milestone that creates or changes a mutating SSH/SCP path must add
  a focused test proving that path imports and uses this boundary rather than
  constructing OpenSSH argv directly. That includes install/update,
  `clipfan provision sync-key`, `clipfan provision known-hosts`, remote config
  writes, authorized_keys edits, cleanup/removal, sync-key rotation, transition,
  and repair flows.
- No remote install, upload, provision, or mutation milestone may run SSH/SCP
  before this boundary has a confirmed pinned host key for the exact host/port
  tuple.
- Provision-client boundary tests fail if a mutating upload, install, provision,
  update, cleanup, or repair command can be constructed before the enforcement
  boundary returns a confirmed pinned host key for the exact host/port tuple.

Milestone 4f: host-key mismatch repair and removal.

- Add host-key mismatch handling, peer removal, and known-host update
  confirmation on top of the scan/parse, confirmation UI, and enforcement layer.
- Add tests proving mismatch repair cannot run install, update, config,
  authorized_keys, or daemon mutation before the replacement key is confirmed and
  pinned for the exact host/port tuple.

Milestone 5: gateway parser and managed authorized_keys rendering.

- Add minimal `ssh-gateway` command parser, requested-command validation, TTY
  rejection, and `--authorized-peer` plus `--authorized-key-id` extraction
  without implementing version/receive/stream behavior yet. Milestone 5 owns
  the full verb allowlist: `version`, `receive`, `sync-stream`, and
  `probe-authorized-key`. Later milestones may enable behavior for a parsed verb
  behind gates, but must not change parser acceptance semantics.
- Add managed authorized_keys render/parse code using the portable explicit
  option set.
- Add key-ID marker handling, duplicate-host-ID detection, and unit tests for
  command path quoting and rejected unsupported commands.
- Add the `probe-authorized-key` pre-secret forced-command verb. It must work
  without daemon auth, config, or `shared_key`, emit only the documented probe
  JSON, and be unable to run clipboard, version, install, update, config,
  service, or sync-stream behavior.

Milestone 6: remote provision subcommand framework.

- Add `clipfan provision` JSON stdin/stdout framework with fake filesystem and
  service dependencies.
- Add direct provision-client gate tests proving generated release flags are used
  before any provision command can construct or execute a remote mutation.
- Add fixed remote provision command construction using validated `gateway_path`.
  Provision/config/key/authorized_keys/service mutations must go through
  `clipfan provision` JSON stdin/stdout rather than inline shell edits. This
  milestone does not rewrite the legacy regular install/update remote templates
  that remain explicitly allowed in the install/update policy section.
- Add boundary tests proving provision command construction cannot run without
  the Milestone 4e3c pinned host-key enforcement object.

Milestone 7a1: remote installed-path capture.

- Add the `install.sh --json-result` producer contract: stdout is exactly one
  JSON object with `install_path`, `config_path`, `state_dir`, `uid`,
  `effective_user`, and `version`; human logs are stderr-only.
- Capture the remote installed binary path during regular-SSH install/update,
  validate it, verify it with `<install_path> version --json`, and persist it in
  service metadata before any managed authorized_keys rewrite needs it.
- Add tests proving path capture rejects missing JSON, extra stdout text,
  malformed or relative paths, mismatched version-reported paths/accounts, and
  human-output parsing; accepted paths are persisted and available to later
  authorized_keys rendering without rerunning install.

Milestone 7a2: managed authorized_keys edit and locking.

- Add remote authorized_keys locking adjacent to the target, atomic replacement,
  concurrent-edit retry, idempotent retry, and rotation behavior for the
  supported `<target-home>/.ssh/authorized_keys` path.
- Add tests proving unrelated user keys are preserved, only clipfan-managed lines
  are replaced, lock contention retries from a fresh read, and output parses as
  OpenSSH authorized_keys before rename.
- Add host-key boundary tests proving the authorized_keys edit path cannot
  construct SSH/SCP commands without the confirmed exact host/port pin.

Milestone 7a3: pre-secret forced-command reachability probe.

- Wire the Milestone 5 `probe-authorized-key` contract into provisioning so the
  app proves the managed line can execute before any `shared_key` write.
- This slice must not invent a daemon API or command-locked version exception
  before `shared_key` exists.

Milestone 7a4: AuthorizedKeysFile detection.

- Add unsupported `AuthorizedKeysFile` detection and fail provisioning with
  `unsupported_authorized_keys_file` or `authorized_keys_not_effective` before
  writing `shared_key` when the managed line is not effective.

Milestone 7a5: update-flow managed-line path rewrite.

- Add update-flow handling that rewrites only managed authorized_keys lines when
  the installed binary path changes after update or `DEST` changes.
- This rewrite is internal/test-only until 17d3b. In public builds before 17d3b,
  regular update/check may verify the installed path and report stale managed
  material if it already exists, but it must not rewrite managed authorized_keys
  or run any `add_peer_provision_mutation`. After 17d3b, the rewrite is allowed
  only for already-provisioned peers and must not create new peer material.
- Add tests proving this rewrite uses regular SSH credentials, preserves
  unrelated keys, and does not write config, `shared_key`, or sync-key material.
- Add host-key boundary tests proving path rewrite uses the Milestone 4e3c
  enforcement object for every SSH/SCP command.

Milestone 7b: local authorized_keys proof validation.

- Add daemon/app helpers that inspect this host's managed authorized_keys lines
  for an inbound peer, parse the portable option set, validate key ID,
  `--authorized-peer`, forced-command path, and marker ownership, and report
  permission drift without rewriting the file.
- Milestone 9b gateway reservations must call this helper for inbound
  `accept:true` proof instead of using remote-editing code or ad hoc parsing.

Milestone 7c: remote sync-key and known-hosts provision commands.

- Add `clipfan provision sync-key` with fake filesystem tests for create,
  verify, idempotent retry, mode repair, wrong-host-ID rejection, and no private
  key disclosure. The command requires an expected persisted host ID and config
  revision, loads the target config before touching key files, and fails closed
  if the config is missing that host ID, contains a different host ID, or has
  changed revision.
- Add `clipfan provision known-hosts` with fake filesystem tests for exact
  host/port replacement, unrelated entry preservation, confirmed fingerprint
  validation, atomic writes, and refusal to edit the user's normal OpenSSH
  known_hosts file.
- Add host-key boundary tests for both provision subcommands; neither may
  construct SSH/SCP commands without the confirmed exact host/port pin.
- Milestone 8b2 non-secret remote config write must happen before Add Peer
  invokes `provision sync-key`. Add Peer invokes `provision sync-key` as a
  required phase only for this host's outbound material or a peer that will be
  `connect:true`; when the remote starts as `accept:true, connect:false`, the
  remote sync-key creation attempt is optional best-effort and failure records
  only `remote_accept_only_missing_sync_key`. Milestone 10b2 shared-key write
  depends on these commands. No milestone may use ad hoc remote shell mutation
  for remote sync keys or dedicated known_hosts.

Milestone 7d: managed key cleanup on disable/delete/removal.

- Add local managed authorized_keys cleanup helpers that remove only clipfan
  accept lines for the deleted peer under the local authorized_keys lock, retain
  lines for disabled peers, and record `stale_local_authorized_key_line` when
  cleanup fails.
- Add remote connect-line cleanup status for removed peers. Remote cleanup uses
  regular SSH credentials; when those credentials are unavailable, the peer stays
  removed locally and the UI reports stale remote key cleanup pending.
- Add tests proving disable retains managed lines while reservations reject,
  delete retries local cleanup safely, cleanup failures persist stale remediation
  status, and remote cleanup warnings survive restart. Forced-command
  removed-peer rejection is tested later when the reservation gateway exists.
- Extend confirmed local fleet reset to remove only clipfan-managed
  authorized_keys lines for the old local host identity after these cleanup
  helpers exist, and prove reset still never edits unmarked authorized_keys lines.

Milestone 8a: remote identity probing.

- Implement and test the no-config regular-SSH `clipfan version --json`
  contract used for identity probing.
- Probe remote `host_id`, effective Unix user, daemon version, and supported SSH
  protocols before writing the fleet `shared_key`.
- Extend peer status/log fields for identity probe, account mismatch, and
  duplicate host ID before exposing this flow.

Milestone 8b1: non-secret remote config payload.

- Define and test the non-secret remote daemon config payload: loopback listen,
  peer host ID, remote peer record for the local host initially in
  `loopback_unprovisioned`, service metadata, and no `shared_key`. The payload
  persists the remote host ID before any sync-key sidecar, managed
  authorized_keys proof, or transport-current artifact can be created. It must
  not write `ssh_material_staged` or proof before managed authorized_keys proof
  exists.
- Add fake provision-command tests for schema validation, path validation,
  owner/mode preservation, and parseability. No real SSH write occurs in this
  slice.

Milestone 8b2: non-secret remote config write over regular SSH.

- Write the Milestone 8b1 payload over regular SSH only after identity/account
  checks pass.
- This milestone is internal/test-only for public builds. Public Add Peer remote
  mutation still returns `unsupported_command` until Milestone 17d3b release gate
  enablement.
- Verify config file creation, ownership, mode, and parseability over regular
  SSH, and verify the remote peer record remains `loopback_unprovisioned` until
  the later authorized_keys proof patch plus transition succeeds. Tests prove Add
  Peer invokes any required or optional `provision sync-key` attempt only after
  this write returns the persisted host ID and config revision, and that optional
  remote accept-only sync-key failure records only
  `remote_accept_only_missing_sync_key`.
- Add host-key boundary tests proving the remote config write path cannot
  construct SSH/SCP commands without the confirmed exact host/port pin.

Milestone 8b3: remote pre-secret proof patch and staged transition.

- Implement `clipfan provision transition` `mode:"pre_secret_offline"` for the
  remote `loopback_unprovisioned` to `ssh_material_staged` transition after
  Milestone 7a3 proves forced-command reachability and Milestone 8b2 writes the
  non-secret remote config.
- The command takes the remote config lock, verifies expected host ID, expected
  config revision, persisted `shared_key` absence, daemon-stopped proof, staged
  accept proof shape, and the exact transition table requirements, then applies
  the proof patch and transition in one atomic config write with one redacted
  audit event.
- Add retry and failure tests for stale revision, missing host ID, daemon still
  running, malformed proof, existing `shared_key`, forbidden target state,
  cleanup/demotion attempts, and disconnect after write. Tests must prove the
  command cannot write `shared_key`, cannot promote beyond
  `ssh_material_staged`, and cannot call a signed loopback transition endpoint.

Milestone 8b4: non-secret remote config status/log behavior.

- Add status/log fields for non-secret config write and remote parse/mode
  failures before exposing this flow.

Milestone 8c: staged retry/status coverage.

- Add Swift tests for each partial provisioning failure branch and retry
  behavior.
- Add staged-state retry behavior and UI tests proving `ssh_material_staged`
  never renders green.

Milestone 8d: duplicate-host-ID repair UX.

- Add Add Peer and Repair UI for duplicate host IDs found during identity probe
  or managed authorized_keys inspection.
- Supported actions are: rename the local host before provisioning, remove the
  stale remote managed key when regular SSH access is available, or cancel
  without writing fleet secrets. Regenerating a host identity is allowed only
  before the local host has been promoted into any peer's `ssh_keys_ready`
  record.
- Logs must identify the local host ID, remote target, and managed key marker
  involved without printing private keys, shared keys, HMACs, or clipboard
  content.
- Tests prove duplicate IDs block remote `shared_key` writes, keep the peer
  orange, and make the successful repair path explicit before provisioning can
  resume.

Milestone 9a: public HTTP version/update regression harness.

- Add green CI tests and temporary allowlist plumbing that identify every
  current app and daemon version/update path that can call public peer HTTP.
  These entries are marked `temporary_until: milestone-9d` with owner comments.
  Do not remove the production path until Milestone 9d3 supplies the replacement,
  and do not merge red expected-failing tests.

Milestones 9d1a-9d3 are an independent 17d3a regular-SSH update/check track.
They may be implemented immediately after Milestones 4e3b-4e3c host-key
enforcement and Milestone 9a inventory. They do not depend on Milestones 9b or
9c gateway reservation, local proof integration, or command-locked version
protocol work. Milestone numbering reflects the broader transport roadmap, not a
requirement that 9b/9c land before the local hardening release can replace public
HTTP version/update checks.

Milestone 9b1: reservation schema and capacity.

- Add the local SSH session reservation API schema and the version-only
  reservation implementation. Milestone 11 extends the same compatible contract
  to `receive` and `sync-stream`; it must not replace the version contract.
- The schema in this milestone already includes the full purpose enum
  (`version`, `receive`, `sync-stream`), lease token shape, renewal endpoint,
  renewal success/error codes, capacity errors, safe-mode errors, and peer-state
  errors.
- Add tests for signed renewal, unknown tokens, expired tokens, peer/purpose/PID
  mismatch, and capacity release after expiration. Later `sync-stream` milestones
  must call this renewal path rather than inventing keepalive capacity.

Milestone 9b2: inbound peer policy.

- Add inbound reservation policy for peer existence, `enabled:true`,
  `accept:true`, disabled/removed rejection, state-specific errors, and
  `shared_key_written_unverified` version-only acceptance. `ssh_keys_ready`
  reservations remain `peer_not_ready` unless the local proof helper from 9b3 is
  wired and passes; 9b2 may test only schema, capacity, and state classification
  for `ssh_keys_ready`. `receive` and `sync-stream` reserve policy may be
  unit-tested here but gateway commands still return `unsupported_command` until
  their later milestones.
- Add policy tests proving disabled or removed peers return
  `peer_not_configured` for reservation purposes before any gateway behavior is
  exposed, and that peers with active SSH-material cleanup records or
  post-secret tombstones cannot reserve `version`, `receive`, or `sync-stream`.

Milestone 9b3: local proof integration.

- Integrate Milestone 7b local authorized_keys proof validation and reject
  `ssh_keys_ready` reservations with `peer_not_ready` when local proof helpers
  are unavailable or fail. This is the first milestone where `ssh_keys_ready`
  inbound reservations may pass. `shared_key_written_unverified` version-only
  reservations deliberately bypass persisted proof validation and rely on
  forced-command peer identity plus hello HMAC verification.

Milestone 9b4: gateway reservation wiring.

- Wire the forced-command gateway to reserve version sessions before hello and
  release/expire leases on exit. After successful reservation, the version
  gateway still returns `unsupported_command` until Milestone 9c implements
  hello/version.
- Wire the forced-command gateway loopback bridge to the Milestone 0c shared
  local request-signing helper and add bridge-signing tests here, after the
  gateway parser exists.
- Inbound version reservations enforce capacity, safe-mode rejection, peer
  existence, `enabled:true`, `accept:true`, disabled/removed rejection,
  `ssh_keys_ready` acceptance, and `shared_key_written_unverified` version-only
  acceptance before any gateway behavior is exposed. Outbound session-start
  checks use `connect:true` and the separate eligibility table above.
- Add gateway tests proving a forced command for a removed peer returns
  `peer_not_configured` before hello.
- Milestone 9b depends on the Milestone 3 directional proof helper, the daemon
  scoped peer config APIs from Milestones 3b1-3b4, the Milestones 4a1-4a2 sync
  key/key-ID primitives, Milestone 5 gateway path parser, and Milestone
  7a2-7a4/7b managed authorized_keys render/parse and local proof-validation
  helpers. It does not depend on the Swift app wrappers, provisioning cutover,
  or UI retry behavior in Milestones 3b5a-3b5c. If the daemon-side helpers are
  unavailable, reservations for `ssh_keys_ready` peers return `peer_not_ready`
  outside narrow unit tests for parser/capacity behavior.

Milestone 9c: command-locked hello/version protocol.

- Add hello HMAC verification, protocol negotiation, and command-locked version
  gateway support.
- Add fixed HKDF, nonce, canonical signing, and version-response fixtures.

Milestone 9c2a: host-global sync-key rotation data model.

- Add pending host sync key metadata, all-required-peer staging lists, old-key
  cleanup state, and proof update semantics for one configured sync private key
  per host.
- Add tests proving disabled/removed peers are excluded from the required
  participant set, enabled outbound peers using the current key are required,
  and re-enable requires regular-SSH repair when a peer missed rotation.

Milestone 9c2b: rotation pre-promotion guardrails.

- Add read-only status and app affordance plumbing for host-global sync-key
  rotation, but keep every install, verification, promotion, rollback, and
  cleanup command disabled until the post-10b3 rotation milestones.
- Add tests proving rotation commands cannot run when no enabled peer is
  persisted `ssh_keys_ready`, when command-locked version is unavailable, or
  when the release/runtime gates are false. These tests use the Milestone 9c2a
  data model only; they do not manufacture artificial promoted peers to exercise
  real rotation.
- Document in the app copy that rotation is unavailable until at least one peer
  has completed provisioning and command-locked verification.

Milestone 9d1a: regular-SSH version/update command path.

- Use regular SSH `clipfan version --json` before provisioning and during
  install/update binary verification.
- Add host-key boundary tests proving version/update commands use the
  operation-scoped or persisted host-key enforcement object, never ad hoc SSH
  argv construction.

Milestone 9d1b: legacy static-peers version/update UI and logs.

- Implement the 17d3a legacy `static_peers` version/update replacement: a
  user-prompted regular-SSH check/update action with explicit user/host/port,
  optional identity-file validation, operation-scoped TOFU/known_hosts pinning,
  copyable logs, and no persisted SSH peer record or green sync UI.
- Add operation-local durable log storage and copy/export UI for legacy
  check/update errors.

Milestone 9d1c: legacy static-peers no-peer-write enforcement.

- Add tests proving legacy suggestions never run background public HTTP probes,
  successful legacy updates verify only the installed binary over regular SSH,
  and the row remains orange "SSH setup required" until Add/Repair Peer
  provisions sync.
- Add tests proving failed legacy check/update actions write only transient or
  bounded app-side operation logs, never `ssh.peers[]`, migration state, or
  `provision_failed`, unless the user explicitly starts Add/Repair Peer.

Milestone 9d2: command-locked readiness version path.

- Use command-locked gateway version checks after provisioning for runtime
  readiness against already-provisioned fixtures/internal peers. Real public
  provisioning reaches this path only after Milestones 10b1-10b4 secret-write
  gates and Milestone 17 release gates allow it.

Milestone 9d3: public HTTP version/update removal.

- Switch peer version probes and update verification away from public HTTP,
  remove the replaced public HTTP version/update paths, and make the Milestone
  9a tests pass with the temporary version/update allowlist entries removed.
- Delete the replaced public HTTP version/update client callsites in this
  milestone; do not defer those specific deletions to Milestone 18.
- This milestone may not merge until Milestones 9d1a-9d1c legacy regular-SSH
  version/update replacement tests are green.
- The expected app callsites are
  `apps/mac/Clipfan/Sources/Clipfan/PeerVersion.swift`,
  `apps/mac/Clipfan/Sources/Clipfan/RemoteUpdateOfferController.swift`,
  `apps/mac/Clipfan/Sources/Clipfan/UpdatePeerSheet.swift`, and
  `apps/mac/Clipfan/Sources/Clipfan/PeerUpdateLog.swift`; their tests must prove
  peer version/update behavior uses regular SSH or command-locked version only.

Milestone 9e: version/update logs and UI surfacing.

- Add minimal daemon/app log capture for version, update, and provisioning
  failures.
- Show clear UI states for `legacy_http`, `loopback_unprovisioned`,
  `ssh_material_staged`, `shared_key_written_unverified`, `ssh_keys_ready`,
  runtime `never_connected`, runtime `ssh_sync_ready`, and `provision_failed`.
  Before Milestone 17, public builds render `ssh_keys_ready` without end-to-end
  sync as orange "SSH setup pending" or "SSH setup required". Only internal/test
  builds may use orange "provisioned; sync unavailable" while exercising gated
  provisioning before clipboard exchange exists.

Milestone 10a1: shared-key retry verification.

- Implement and test retry verification for `shared_key_written_unverified`
  behind an internal feature flag. No flow in this milestone writes the remote
  `shared_key`.

Milestone 10a2: shared-key cleanup and demotion.

- Implement and test regular-SSH cleanup, remote peer removal/demotion, and
  local staged-record cleanup for `shared_key_written_unverified`. No flow in
  this milestone writes the remote `shared_key`.
- Add host-key boundary tests proving cleanup and demotion commands cannot run
  without the confirmed exact host/port pin.

Milestone 10a3: shared-key remediation UI guidance.

- Implement and test user-facing shared-key rotation guidance and copyable
  remediation logs. No flow may write the remote `shared_key` until 10a1-10a3
  controls and tests pass.

Milestone 10a4: secret-write release gate verification.

- Extend the Milestone 0a1-0a5 manifest, generation, and consumer tests to prove
  `PeerHTTPRuntimeDisabled` and `ConfigV2WriteEnabled` remain false in public
  builds until 17d3a, and that `RemoteSecretWriteReleaseEnabled` plus
  `ssh_public_add_peer_success_enabled` remain false in public builds until
  17d3b. Go provision-client and Swift Add Peer UI consume the same generated
  public-build gate values.

Milestone 10b1: secret-write gate enforcement.

- Add the lower-level provision-client gate for `write-config` requests whose
  JSON stdin includes `shared_key`.
- Public/release builds compile with `RemoteSecretWriteReleaseEnabled=false`,
  and no environment variable or UI feature flag can override it. Tests
  instantiate the provision client directly, bypass the UI, and assert
  `unsupported_command` before invoking the remote provision subcommand.

Milestone 10b2: internal shared-key write.

- Enable only internal/provisioning test builds to write final remote config
  containing `shared_key`, and only after Milestone 9 command-locked version
  support exists and Milestone 10a1-10a4 remediation and release-gate controls
  pass.
- Before Milestone 17d3b, secret writes require both an internal/test/developer
  build with `ALLOW_REMOTE_SECRET_WRITE` and an explicit fixture-target
  predicate: named test fixture, local ephemeral OpenSSH integration host, or
  owner-commented development fixture record. The environment variable alone is
  never sufficient to write a real fleet `shared_key` to an arbitrary host, and
  release CI rejects the flag.
- Add host-key boundary tests proving the final secret write path cannot
  construct SSH/SCP commands without the confirmed exact host/port pin.

Milestone 10b3a: command-locked verification after secret write.

- Immediately verify command-locked gateway version after writing `shared_key`.
- Keep both local and remote records in `shared_key_written_unverified` when
  verification fails; do not attempt promotion in this slice.

Milestone 10b3b: remote post-secret transition command and API.

- Implement regular-SSH `clipfan provision transition`
  `mode:"daemon_loopback"`, which runs on the peer after the remote daemon has
  local auth material and calls that peer's signed loopback proof/transition
  APIs.
- Prove remote transition handles `daemon_unavailable`, `local_auth_failed`,
  `config_revision_conflict`, `proof_mismatch`, and stale revision without
  changing migration state.
- Add host-key boundary tests proving transition command construction uses the
  Milestone 4e3c enforcement object.

Milestone 10b3c: local promotion ordering.

- Keep both local and remote records in `shared_key_written_unverified` until
  remote then local promotion succeeds. Local promotion uses the local signed
  loopback config API only after remote transition succeeds, so a failed remote
  promotion cannot make the local daemon start sync against an unready peer.

Milestone 10b3d: promotion failure and retry coverage.

- Add tests and UI/log handling for verification success with remote transition
  failure, remote transition success with local promotion failure, retry after
  each failure, and regular-SSH remote demotion/removal when local promotion
  cannot complete.

Milestone 10b4: unverified-secret UI/log recovery.

- Add `shared_key_written_unverified` UI/log handling, retry, cleanup/removal,
  demotion, and rotation guidance.
- No later polish milestone may be required to recover from
  `shared_key_written_unverified`.

Milestone 10c1: rotation install, verification, and promotion.

- Implement the peer-detail "rotate this host's sync key" action as a
  host-global regular-SSH repair flow after command-locked version verification,
  remote transition, and local promotion exist from Milestones 10b3a-10b3d.
- Generate a pending local sync key/key ID, install the pending managed
  forced-command authorized_keys line alongside the old line on every required
  `ssh_keys_ready` participant using regular SSH, verify command-locked
  `version` to every participant with the pending key and existing `shared_key`,
  and atomically promote the pending key only after all required participants
  verify.
- Failed install or verification before promotion keeps the old key and proof
  active, marks rotation failed with copyable logs, and best-effort removes
  pending managed lines.
- Add host-key boundary tests proving every regular-SSH install, verify,
  promotion, rollback, and pending-line cleanup command in rotation uses the
  Milestone 4e3c enforcement object.

Milestone 10c2: rotation UI, logs, and cleanup.

- Show host-global copy in peer detail/settings, not per-peer rotation copy.
- After promotion, remove old managed lines over regular SSH. Cleanup failure
  keeps peers enabled on the new key but shows stale-key cleanup pending until
  regular-SSH cleanup succeeds.
- Add copyable logs for install, verify, promotion, rollback, and stale cleanup.

Milestone 10c3a: fake-SSH rotation acceptance.

- Add daemon/provision-client tests and fake-SSH tests proving successful
  host-global rotation, failed install rollback, failed verification rollback,
  stale old-line cleanup, exclusion of disabled/removed peers, re-enable repair
  after missed rotation, and no shell/install authority through the sync key.

Milestone 10c3b: rotation app acceptance.

- Add app UI acceptance tests for the same rotation outcomes from Milestone
  10c3a, including copyable logs and stale-cleanup rendering. This slice does
  not add real OpenSSH fixtures.

Milestone 10c3c: real OpenSSH rotation acceptance.

- Add one real OpenSSH fixture proving successful host-global rotation, failed
  install rollback, failed verification rollback, stale old-line cleanup,
  exclusion of disabled/removed peers, re-enable repair after missed rotation,
  and no shell/install authority through the sync key.

Milestone 11a: `ApplyEnvelope` receive refactor.

- Refactor daemon receive handling into `ApplyEnvelope` with structured
  results.
- Add explicit envelope crypto key selection by authenticated endpoint/protocol:
  legacy peer HTTP callers use the existing `SHA256(raw shared_key)` body key
  while that transport remains in the pre-cutover compatibility window, and
  SSH/current callers use `clipfan-v1/body-aead`. The receive path must not try
  both keys.
- Before wiring legacy `/v1/clip` to `ApplyEnvelope`, make `/v1/clip`
  loopback-only or local-test-only in SSH transport builds and add a release
  test proving off-host `/v1/clip` receive is unreachable.
- Make legacy `/v1/clip` call `ApplyEnvelope` while it still exists.

Milestone 11b: transport-current persistence.

- Introduce the persisted current transport state and ordering barrier.
- Add transport-current root locality checks before creating or writing transport
  state.
- Add deterministic tuple ordering, unsupported/inconclusive transport-current
  storage refusal, and restart recovery tests.
- Implement confirmed `reset_corrupt_transport_state_clear_barrier` recovery for
  `transport_state_corrupt`: it takes the transport-current lock, writes a
  timestamped backup of corrupt files when readable, clears current plus ordering
  barrier only after explicit user confirmation, emits no peer state until a new
  local or received visible clip exists, and records a copyable recovery event.
  Tests must prove the normal `clear_visible_current_preserve_barrier` operation
  does not call this reset path and never clears the ordering barrier.
- Extend confirmed local fleet reset to clear transport-current and pending
  transport state encrypted under the old key only after transport-current
  persistence exists.
- Add host-identity reset tests proving transport-current and ordering barriers
  tied to the old host ID are cleared or blocked before peers can return to
  `ssh_keys_ready`.

Milestone 11c: local current APIs.

Milestone 11c1: current API schema and auth.

- Add signed loopback route registration, request/response schemas, recipient
  validation, payload limits, response signing, and safe-mode rejection for
  `GET /v1/current`, `GET /v1/current/watch`, and
  `POST /v1/current/receive`.
- Add tests for auth-version enforcement, loopback-only access, unsupported
  methods, malformed recipients, payload size limits, and safe-mode rejection.

Milestone 11c2: receive bridge and status mapping.

- Add `ApplyEnvelope` as the single local receive method used by
  `POST /v1/current/receive` and the legacy loopback/test-only `/v1/clip`
  handler.
- Add tests mapping every existing receive early return to exactly one allowed
  `ApplyEnvelope` status, including malformed input, replay/seen clips, echo
  suppression, concealed payloads, older ordering keys, invalid timestamps,
  decrypt failures, wrong recipient, successful apply, and relay eligibility.

Milestone 11c3: current watch atomicity and signatures.

- Add atomic watch registration with `include_snapshot=true`, initial snapshot
  semantics, null reasons, canonical current payload JSON, and per-event HMAC
  verification fixtures shared by Go and Swift.
- Add tests proving direct watch clients receive the initial snapshot before
  later current events and that no change can occur between snapshot capture and
  subscription registration. SSH writer queueing, snapshot supersession, and
  latest-only drop counters remain owned by Milestone 15a.

Milestone 11d: receive/stream reservation expansion.

- Extend the daemon reservation policy library from `version` to `receive` and
  `sync-stream`, but keep forced-command gateway behavior for those purposes as
  `{"type":"error","code":"unsupported_command"}` until Milestones 12 and 13
  implement them.
- Enforce reservation policy from daemon-readable peer config before any
  gateway `receive` or `sync-stream` implementation uses it.

Milestone 11e1: local fleet reset discovery and dry-run model.

- Define the confirmed local fleet reset plan for configs that contain SSH
  material. This slice depends on the peer schema/proof validators, sync-key
  primitives, dedicated known_hosts storage, managed authorized_keys cleanup
  helpers, and transport-current persistence/recovery existing.
- Add a dry-run command/model that reads config and state under lock, classifies
  reset actions, reports required daemon-stop/lock status, lists local artifacts
  to remove or tombstone, and reports remote artifacts that cannot be cleaned
  automatically. This slice performs no mutation.
- Add tests proving lost/invalid `shared_key` plus existing `ssh.peers[]`
  produces a complete reset plan and that remote cleanup is reported as guidance,
  not claimed as performed.

Milestone 11e2: offline config/state reset.

- Implement the offline reset mutation after stopping the local user daemon and
  proving the daemon lock is clear. It takes the config and state locks, writes
  timestamped backups, generates a fresh 32-byte fleet `shared_key`, preserves
  or explicitly re-derives the local host identity according to the reset
  confirmation copy, removes legacy `static_peers`, tombstones or clears
  `ssh.peers[]`, directional proof, migration state, provisioning/remediation
  records tied to the old fleet credential, transport-current state, and pending
  stream/on-demand state.
- Add tests proving the new fleet starts as single-host loopback config v2 with
  no public HTTP sync, old peers cannot remain or become `ssh_keys_ready`, and
  transport-current/order barriers tied to the old fleet are cleared only through
  the confirmed reset path.

Milestone 11e3: local SSH material cleanup.

- Remove local sync-key sidecars, dedicated known_hosts entries, pending key
  rotation files, and local SSH transport state selected by the reset plan.
  Cleanup uses backups and redacted audit events and does not touch the user's
  normal SSH known_hosts files.
- Add tests proving stale remote cleanup warnings survive restart, local cleanup
  records are durable, and unrelated local SSH files are not edited.

Milestone 11e4: local managed authorized_keys cleanup.

- Remove only clipfan-managed authorized_keys lines for the old local host
  identity, using the managed marker parser and atomic file replacement. This
  slice must not edit unmarked authorized_keys lines.
- Add parser and mutation tests proving malformed marker comments fail closed,
  unmarked lines are preserved byte-for-byte, and cleanup failure leaves a
  durable local remediation record.

Milestone 11e5: reset UI/log surfacing.

- Show the confirmed reset dry-run, destructive action summary, local cleanup
  results, stale remote cleanup warnings, and re-enrollment guidance for every
  removed/tombstoned peer. The UI must say the reset does not contact remote
  hosts and does not prove remote cleanup of old managed authorized_keys lines or
  the old fleet credential.
- Add app/CLI tests for copyable logs, confirmation copy, retry after partial
  local cleanup failure, and re-enrollment guidance.

Milestone 11e6: local fleet reset acceptance.

- Add end-to-end fake-filesystem acceptance proving a lost/invalid `shared_key`
  with existing `ssh.peers[]` has a supported offline reset path before public
  Add Peer/sync enablement; reset never edits unmarked authorized_keys lines;
  stale remote cleanup warnings survive restart; old peers cannot remain or
  become `ssh_keys_ready`; and the new fleet starts as single-host loopback
  config v2 with no public HTTP sync.

Milestones 10c through 17 are guarded by explicit runtime gates from
`release/ssh-runtime-gates.json`:

- `ssh_receive_primitive_enabled`: before Milestones 12a-12c pass, `receive`
  reservations may be parsed for tests but the gateway returns
  `unsupported_command` and the UI hides on-demand send.
- `ssh_sync_stream_enabled`: before Milestones 13a-13d pass, `sync-stream`
  reservations may be parsed for tests but the daemon refuses to start stream
  processes and the gateway returns `unsupported_command`.
- `ssh_persistent_current_enabled`: before Milestones 14a-16c pass,
  established streams may reach hello/status tests but must not apply or relay
  clipboard state outside internal test builds.
- `ssh_sync_key_rotation_enabled`: before Milestones 10c1-10c3 pass,
  host-global sync-key rotation status may render as unavailable, but install,
  verification, promotion, rollback, and stale old-line cleanup commands return
  `unsupported_command`.

Public Add Peer success and real public remote `shared_key` writes are governed
only by the public release manifest table above:
`ssh_public_add_peer_success_enabled` and `RemoteSecretWriteReleaseEnabled` live
in `release/ssh-transport-gates.json`, not in `release/ssh-runtime-gates.json`.
Commands that depend on missing gates return `unsupported_command` or
`peer_not_ready`; hiding a button is not the security boundary.

Milestones 13a through 17d2, Milestones 17d3a1-17d3a4, and Milestones
17d3b0-17d3b4 are mandatory implementation review slices. Each slice lands with
its own acceptance tests and must not be combined with another behavior slice
unless the combined change is documentation-only or test-only and the reviewer
explicitly accepts the narrower risk. Aggregate Milestones 17d3a and 17d3b are
release sequences/gate flips composed from those slices, not single
implementation PRs.

Milestones 14a through 17d3b also declare a primary implementation category.
Each implementation PR must stay inside one category unless the extra changes
are tests/docs needed to prove that category:

| Slice | Primary category |
|-------|------------------|
| 14a | daemon data model and counters |
| 14b | stream writer protocol |
| 14c | stream reader and daemon apply |
| 14d | UI/status/log surfacing |
| 14e | real OpenSSH acceptance |
| 15a | stream watch writer |
| 15b | stream remote apply loop |
| 15c | UI/status/log acceptance |
| 16a | daemon relay routing |
| 16b | daemon relay fanout/apply |
| 16c | public HTTP removal/security |
| 17a | on-demand protocol sender |
| 17b | fallback trigger and health |
| 17c | UI/status/log surfacing |
| 17d1 | real OpenSSH release acceptance; 17d3b blocker and non-blocking 17d3a sentinel |
| 17d2 | local-cutover release manifest dry-run |
| 17d3a | local listener/config cutover |
| 17d3b | public Add Peer enablement |

Milestone 12a: receive gateway behavior gate.

- Wire the already-parsed `receive` verb to reservation and feature-gate checks.
  Parser rejection for TTY, shell, scp, sftp, install, update, and arbitrary
  command strings remains owned by Milestone 5.

Milestone 12b: receive primitive.

- Add the internal `receive` primitive using the fixed hello/state/ack sequence.
  This is not user-visible on-demand fallback yet.

Milestone 12c: receive logs and acceptance.

- Extend minimal logs to include one-shot receive failures.
- Verify against a real SSH peer with command-locked keys.
- Run the `daemon-peer-http-runtime` suite before enabling this primitive outside
  internal builds.

Milestone 13a: persistent process and argv lifecycle.

- Add daemon-managed outbound persistent SSH sessions.
- Add `sync-stream` process lifecycle, SSH argv construction, timeout handling,
  and fake-ssh tests.
- Add tests proving disabling or deleting a peer closes any daemon-managed
  outbound persistent process for that peer before the config mutation returns.

Milestone 13b: stream hello negotiation.

- Complete stream hello/protocol negotiation and health transitions without
  sending clipboard state.

Milestone 13c: reconnect health model.

- Render connected streams that have not exchanged clipboard state as
  `transport_connected_no_clip_exchange`, not `ssh_sync_ready`.

Milestone 13d: stream status/log surfacing.

- Extend peer status/log fields for persistent connect, hello negotiation,
  reconnect backoff, and protocol errors before exposing stream health in UI.

Milestone 14a: persistent current data model.

- Wire persisted transport-current, ordering barrier, local sequencer, and
  latest-only drop counters into the stream session manager without sending
  frames yet.

Milestone 14b: stream writer initial-snapshot path.

- Register the local current watch with `include_snapshot=true` after hello and
  send only the initial snapshot event to one configured peer, with bounded queue
  behavior and fake SSH tests. This is the same production startup path that
  Milestone 15a extends for continuous watch events; 14b must not introduce a
  separate one-off current read that bypasses atomic watch registration.

Milestone 14c: stream reader apply path.

- Receive one remote state over an established stream, call `ApplyEnvelope`,
  return ack, and update runtime status without relay.

Milestone 14d: reconnect/backoff and status.

- Add reconnect/backoff fake-clock tests, connected-without-clip status,
  copyable stream failure logs, and UI/log fields before exposing stream status.

Milestone 14e: real OpenSSH current exchange acceptance.

- Verify one-peer persistent current exchange against real OpenSSH and rerun the
  `daemon-peer-http-runtime` suite before enabling the internal stream gate.

Milestone 15a: stream local watch writer.

- Add local current watch sends over established streams using the single-slot
  latest queue and drop counters.
- Add a snapshot coalescing test that registers the watch, blocks the SSH writer
  after the initial snapshot is queued but before the first SSH state frame is
  written, injects a newer local current event, and proves the stream sends the
  newer state once instead of the stale queued snapshot. A separate local watch
  API test proves direct watch clients receive the initial snapshot first.

Milestone 15b: stream remote apply loop.

- Add continuous remote receive/apply loop over established streams, ack
  handling, and bounded frame-size enforcement.

Milestone 15c: stream UI/log acceptance.

- Add UI/log tests for stream success, superseded drops, write timeout,
  protocol errors, and health transitions.

Milestone 16a: SSH relay routing.

- Add relay eligibility and next-hop selection over established SSH streams
  while public HTTP relay/fanout remains test-blocked.

Milestone 16b: SSH relay apply/fanout.

- Relay accepted remote state to eligible peers, preserve true origin, suppress
  echoes, and verify tuple ordering across the minimum `A <-> B <-> C`
  three-host line topology with no direct A-C path.

Milestone 16c: relay HTTP removal.

- Delete replaced public HTTP relay/fanout callsites once SSH relay tests pass;
  Milestone 17d3b0 inventories all peer-HTTP symbols before public Add Peer
  packaging, and Milestone 18 removes only classified leftovers, fixtures,
  helpers, and docs.
- This milestone is not a 17d3a prerequisite. The 17d3a hardening release relies
  on Milestone 1c's generated kill switch and public HTTP runtime scan to block
  old relay/fanout behavior without requiring SSH relay to be implemented first.
- The expected Go callsites are in `internal/daemon/daemon.go`,
  `internal/discovery/static.go`, `internal/discovery/tailscale.go`, and
  `internal/transport/client.go`; after this milestone, production runtime code
  in those files may not construct off-host peer HTTP URLs for relay, fanout,
  receive, retry, or migrated old configs.

Milestone 17a: on-demand protocol send path.

- Add user-visible on-demand delivery over the receive primitive, including
  daemon enforcement and fake SSH tests.
- This remains internal/test-only in public builds until Milestone 17d3b flips the
  release manifest.

Milestone 17b: fallback trigger and health.

- Add fallback trigger policy when streams are unhealthy, retry/backoff
  behavior, and runtime health transitions for success/failure.
- This remains internal/test-only in public builds until Milestone 17d3b flips the
  release manifest.

Milestone 17c: fallback UI/log surfacing.

- Add copyable failure logs, user-visible retry state, and UI readiness for
  on-demand fallback.
- This remains internal/test-only in public builds until Milestone 17d3b flips the
  release manifest.

Milestone 17d1: real OpenSSH release acceptance.

This milestone is a 17d3b public Add Peer/sync blocker, not a dependency for
the 17d3a local hardening release. It may run before, during, or after 17d3a as
a non-blocking sentinel for the interim hardening release; it must pass before
any public remote `shared_key` write, command-locked clipboard runtime, or green
Add Peer success UI ships.

- Prove persistent SSH sync and on-demand SSH fallback both exchange latest
  state on ephemeral real OpenSSH hosts, with remediation controls enabled.

Milestone 17d2: local-cutover release manifest dry-run.

- Regenerate the release manifest in dry-run mode for the 17d3a local cutover
  only and prove `PeerHTTPRuntimeDisabled=true` and `ConfigV2WriteEnabled=true`
  would flip while `RemoteSecretWriteReleaseEnabled=false`,
  `ssh_public_add_peer_success_enabled=false`, and all runtime sync gates remain
  false.
- Keep the committed public/release manifest values false in this milestone.
  Add CI assertions that public 17d3a may have only
  `PeerHTTPRuntimeDisabled=true` and `ConfigV2WriteEnabled=true`, and that any
  public branch with either peer-add gate true and public Add Peer success UI
  disabled fails before packaging. Full peer-add/runtime manifest dry-run is
  Milestone 17d3b1 after rotation and latest-state fixture acceptance pass.

Milestone 17d3a: local listener/config public cutover.

This is a release sequence, not one implementation PR. The final public
manifest flip may land only after the prerequisite slices below are green on the
same release candidate. Milestone 17d3a is intentionally limited to public
listener/config hardening, no-peer-HTTP runtime enforcement, backup/remediation,
downgrade blocking, rollback export, and regular-SSH install/update continuity.
End-to-end OpenSSH clipboard sync acceptance remains a Milestone 17d3b public
Add Peer/sync blocker, not a blocker for shipping the interim 17d3a hardening
release.

The 17d3a final flip prerequisite set is explicit: Milestones 0h1-0h4 storage
locality, Milestones 1a-1f local listener/config hardening and reset recovery,
Milestone 1c public peer-HTTP sync kill-switch enforcement, Milestones 4e3b,
4e3c, and 9d1a host-key-enforced regular-SSH install/update, Milestones
9d1b-9d1c legacy static-peer regular-SSH UI/log behavior, Milestone 9d3 public
HTTP version/update removal, the four `public-http-runtime-scan` sub-gates
(`daemon-peer-http-runtime`, `app-peer-version-update`,
`migration-no-http-fallback`, and `fixture-allowlist`) running in release-blocking
mode, and Milestones 17d3a1-17d3a3 below. If any prerequisite is not present in
the same release candidate, `PeerHTTPRuntimeDisabled` and
`ConfigV2WriteEnabled` stay false for public builds. Milestone 16c deletion of
replaced relay/fanout callsites is deliberately later and remains tied to SSH
relay acceptance.

Milestone 17d3a1: backup and remediation history.

- Before any public config v2 write can be enabled, implement timestamped backup
  creation for the prior config, redacted remediation-history recording of the
  backup path, owner/mode preservation checks, and failure handling that stops
  before mutation when backup fails.
- Add tests proving the backup is created exactly once for the first cutover
  write, is not overwritten on retry, is available in diagnostics, and never
  includes unredacted secrets in copyable logs.

Milestone 17d3a2: downgrade block and offline rollback.

- Implement downgrade blocking for pre-SSH daemons that would read config v2 or
  restored loopback-only configs incorrectly.
- Implement the emergency offline rollback command for
  `loopback_unprovisioned` fleets only. It may export a detached
  config-v1-compatible loopback config for current-tool diagnostics or manual
  local recovery, but it does not replace the active config by default, is not an
  old-daemon restart path, never re-enables off-host peer HTTP, and refuses to
  run after any peer reaches `shared_key_written_unverified` or
  `ssh_keys_ready`.
- Add local migration, rollback, and downgrade-blocking tests before the final
  manifest flip.

Milestone 17d3a3: cutover UI and release CI dry-run.

- Add the updater/app preflight notice and first post-upgrade UI explanation:
  legacy off-host peer HTTP sync is being disabled, affected peers remain orange
  until SSH setup is completed, regular SSH install/update remains available,
  and the backup path is available in diagnostics.
- Update README, architecture docs, and release notes for the 17d3a boundary:
  local daemon HTTP is loopback-only, public peer HTTP sync is no longer
  supported, public SSH Add Peer/sync remains pending, and regular SSH
  install/update remains available.
- Run release CI in dry-run mode for local migration, downgrade blocking,
  emergency offline rollback export, no-public-peer-HTTP, gate regeneration, and
  regular-SSH install/update continuity. Internal OpenSSH sync acceptance may run
  as a non-blocking sentinel in this slice, but public 17d3a packaging is blocked
  only by the local hardening criteria above. The committed public manifest
  values remain unchanged in this slice.

Milestone 17d3a4: final local listener/config manifest flip.

- Flip `PeerHTTPRuntimeDisabled` and `ConfigV2WriteEnabled` only, in one final
  manifest PR after the full 17d3a prerequisite set above passes. Public builds
  disable peer HTTP runtime, may persist config v2, migrate generated peer-HTTP
  listeners to loopback, and render affected peers orange as SSH setup pending.
- Public remote `shared_key` writes, public Add Peer success UI, and green
  peer-add rows remain disabled. Release CI must rerun the 17d3a3 gates and the
  Milestone 1f reset-recovery tests before packaging this cutover.

Milestone 17d3b: public Add Peer enablement.

The public behavior flip is atomic at packaging time, but review artifacts are
split so the risky pieces are inspected separately before the generated gate
values change.

Milestone 17d3b0: peer HTTP runtime negative inventory.

- Before any 17d3b public packaging or final gate flip, produce the negative
  inventory and remove or classify every production peer-HTTP-related symbol.
  Release CI fails if any off-host runtime peer HTTP path remains unclassified
  or reachable.
- The inventory covers every remaining peer-HTTP-related symbol, fixture, and
  test helper. Each entry is classified as removed, loopback-only, or test-only
  with owner comments. Test-only entries must include the milestone that will
  delete or retain them.
- This prerequisite must not be the first slice that removes a live production
  runtime fallback; 9d and 16c already remove or rewire version/update and
  relay/fanout runtime paths. 17d3b0 proves that no production off-host peer
  HTTP runtime path escaped those earlier slices.

Milestone 17d3b1: manifest and generator verification.

- Regenerate transport and runtime gates in dry-run mode and prove
  `RemoteSecretWriteReleaseEnabled`, `ssh_public_add_peer_success_enabled`,
  `ssh_receive_primitive_enabled`, `ssh_sync_stream_enabled`,
  `ssh_persistent_current_enabled`, and `ssh_sync_key_rotation_enabled` would
  move together from the same release-candidate acceptance bundle covering
  Milestone 10c3c rotation and Milestone 17d1 latest-state fixtures while
  `ConfigV2WriteEnabled` is already true.
- The committed public manifest values remain unchanged in this slice.

Milestone 17d3b2: daemon/provision-client enforcement verification.

- Add release-profile tests proving remote `shared_key` writes, Add Peer
  provision mutations, command-locked `receive`, command-locked `sync-stream`,
  persistent current exchange, on-demand fallback, and host-global sync-key
  rotation stay blocked while any one required public/runtime gate is false.
- Prove these checks instantiate daemon/provision-client enforcement directly,
  bypassing UI button visibility.

Milestone 17d3b3: UI consumption verification.

- Add Swift release-profile tests proving Add Peer success UI, green fleet rows,
  retry/remediation actions, sync-key rotation actions, and peer detail actions
  consume only generated transport/runtime gates and cannot render success when
  daemon/provision-client enforcement would still reject the operation.

Milestone 17d3b4: final public gate flip.

- In the final generated-gates PR after 17d3b0-17d3b3 pass, flip
  `RemoteSecretWriteReleaseEnabled`, `ssh_public_add_peer_success_enabled`,
  `ssh_receive_primitive_enabled`, `ssh_sync_stream_enabled`,
  `ssh_persistent_current_enabled`, and `ssh_sync_key_rotation_enabled`; then
  enable public Add Peer success UI. Release CI must rerun the manifest,
  enforcement, UI, persistent latest-state, on-demand fallback, and sync-key
  rotation gates before packaging.
- After this milestone, a peer may render green only when it can become runtime
  `ssh_sync_ready` from persistent latest-state exchange or complete verified
  on-demand latest-state exchange.
- Update README, architecture docs, and release notes for the 17d3b SSH sync
  boundary: command-locked sync/version keys, regular SSH install/update,
  latest-state-only convergence, local-only history, host-key/forced-command
  troubleshooting, unsupported SSH config aliases/ProxyJump/ProxyCommand, and
  backup/restore caveats for config v2, host identity, sync keys, known_hosts,
  and managed authorized_keys markers.

Milestone 18: post-cutover peer HTTP fixture cleanup.

- Delete dead off-host peer HTTP leftovers not already deleted in 9d
  version/update removal, 16 relay/fanout removal, or 17d3b0 production runtime
  inventory. This milestone is limited to test-only fixtures/helpers or docs
  already classified by 17d3b0 and must not remove the first reachable
  production runtime callsite for any peer HTTP behavior.
- Keep or delete classified legacy fixtures according to their owner comments.
  If a fixture remains, it must be named as loopback-only or legacy-test-only
  and must not appear in production package scans.

Milestone 19: status polish, logs polish, and cleanup docs.

- Polish peer SSH status and copyable redacted logging APIs added by the
  milestones that introduced each failure phase. This milestone may not add the
  first user-visible status/log field for any failure mode; those are required
  in the milestone that introduces the failure.
- Keep loopback HTTP only for local app/CLI/gateway integration.
- Reference the Milestone 17d3b0 peer-HTTP inventory in troubleshooting/docs
  only; dead peer-HTTP fixture/helper deletion is owned by Milestone 18 and must
  not be deferred to this polish milestone.
- Polish README, architecture docs, and troubleshooting wording after the
  release-blocking 17d3a and 17d3b documentation has already shipped. Milestone
  19 must not be the first milestone that documents the public security boundary,
  SSH-only peer sync, host-key behavior, forced commands, reconnect state, or
  backup/restore caveats.

## Testing Strategy

Go unit tests:

- Listener default and generated-listen migration tests are profile-scoped:
  `public_pre_17d3a` proves config defaults, installer output, and first-run
  generated listens remain legacy-compatible and do not persist loopback
  migration; `internal_loopback_enabled` proves internal/test defaults produce
  `127.0.0.1:7853` and old generated `":7853"`, `"0.0.0.0:7853"`, and
  `"[::]:7853"` configs migrate to loopback; `public_17d3a_cutover` proves the
  same loopback behavior plus required backup/remediation history before any
  persisted public config write.
- Listen migration table tests cover empty listen, exact legacy defaults,
  empty listen with valid non-default `port`, malformed listen with and without
  valid `port`, exact legacy defaults, accepted loopback forms
  (`127.0.0.1:<port>`, `localhost:<port>`, `127.x.y.z:<port>`, and
  `[::1]:<port>`), explicit custom public listens, wildcard non-default listens
  (`:<non-default-port>`, `0.0.0.0:<non-default-port>`, and
  `[::]:<non-default-port>`), effective bind address, actor/gate-specific
  persistence, config revision behavior, peer sync state, and UI state.
- App safe-mode discovery tests use the daemon's port derivation order: valid
  `listen` port, otherwise valid `port`, otherwise 7853.
- Host-ID migration tests prove old configs without `hostname` derive one stable
  ID, validate it with peer-ID rules, persist it before sync keys/proofs/current
  state can be created, and use the persisted ID for `clipfan version --json`,
  hello identity, gateway reservation, and sync-key metadata even after the OS
  hostname changes.
- Host rename/reset tests prove renaming after SSH material exists clears
  transport-current plus ordering barrier state tied to the old host ID, marks
  old proofs and managed authorized_keys lines stale, requires regular-SSH
  reprovisioning, and does not allow a peer to remain or become
  `ssh_keys_ready` from old identity material.
- Config version 2 is written only after the new binary is installed and old
  services are stopped; downgrade/restart attempts for pre-SSH daemons are
  blocked with manual recovery instructions.
- Old configs without `transport` load through the migration path, never start
  off-host HTTP, surface transient `legacy_http` peer suggestions with
  `source:"static_peers"`, and do not create persisted `ssh.peers[]` records,
  `loopback_unprovisioned` records, or config revision increments until the app
  writes a real peer during provisioning.
- Public/release gate tests prove generated-listen migration and generated
  loopback defaults do not run while `ConfigV2WriteEnabled=false`; the same
  behavior is exercised only in internal/test manifests before 17d3a.
- Explicit custom non-loopback listens require confirmation before sync starts.
- Explicit custom non-loopback listens start safe-mode loopback repair traffic,
  bind the configured port on loopback, do not bind the public address, and do
  not start the SSH session manager.
- Safe mode rejects gateway `version`, `receive`, and `sync-stream`
  reservations with `public_listen_requires_confirmation` while preserving
  local loopback repair APIs.
- Safe mode rejects gateway `version` before evaluating
  `shared_key_written_unverified` or `ssh_keys_ready` migration-state
  exceptions.
- Safe-mode endpoint schema tests prove `GET /v1/version`, `GET /v1/status`,
  compatibility `GET /v1/peers`, `GET /v1/ssh/logs`, and
  `PATCH /v1/config/listener` expose only the documented repair/status/log fields
  and never trigger peer version probes, SSH sessions, current APIs, clipboard
  APIs, or gateway reservations. Log endpoint fixtures include `source` and
  `durable` on every entry, reject unknown sources, and prove
  `safe_mode_health_only` exposes no signed status or log endpoint.
- Pre-v2 safe-mode repair tests prove the new app signs listener repair with
  HKDF-derived `clipfan-v1/request-hmac` over the existing valid `shared_key`
  before `config_version:2` is persisted, and raw-key old-client signatures are
  rejected.
- Listener repair revision tests cover pre-v2, missing-revision v2, and
  valid-revision v2 configs; null revision expectations write revision 1 only
  for still-unversioned configs, while stale or newly-versioned files return
  `config_revision_conflict`.
- Canonical URI signing tests cover duplicate query rejection, lexicographic
  query ordering, uppercase percent encoding, `+` rejection, path-token slash and
  dot-segment rejection, raw request-target mismatch returning
  `non_canonical_uri`, and byte-identical Go/Swift fixtures for signed local
  endpoints with query strings.
- Shared local-daemon discovery tests prove the Mac app, CLI, and
  forced-command gateway use the same helper for derived loopback ports,
  non-default safe-mode ports, 7853 fallback, signed config/state identity proof,
  and health-only safe-mode liveness. CLI copy/paste/status/repair paths and
  gateway loopback bridge tests must fail if they hardcode
  `http://127.0.0.1:7853` instead of using the helper.
- Safe-mode validation-tier tests prove unsafe listeners with a valid
  `shared_key` enter `safe_mode_signed_repair` and still start signed loopback
  repair/status APIs when sync key paths, known_hosts, or proof are missing,
  while strict runtime validation keeps SSH sync stopped.
- Invalid-`shared_key` safe-mode tests prove only minimal unauthenticated health
  is exposed in `safe_mode_health_only`; no signed status/log endpoint and no
  unauthenticated config mutation endpoint exists. The app/CLI recovery test
  stops the health-only user service, waits for daemon-lock release, then uses
  offline listener repair under the config lock; stop failure or lock timeout
  returns `daemon_lock_timeout` with copyable lock metadata and no config
  mutation.
- Daemon startup fails closed on daemon-lock conflicts or loopback port
  conflicts and never falls back to a public bind.
- Daemon lock tests prove the daemon takes `<state_dir>/daemon.lock` with a
  non-blocking exclusive advisory lock before binding, writes diagnostic
  metadata only after acquiring the lock, rewrites stale unlocked lock files,
  refuses to start when the advisory lock is held, and reports
  `daemon_lock_timeout` instead of starting a second daemon when restart does not
  release the lock.
- Offline repair tests prove config repair first proves the daemon advisory lock
  is not held, then takes the adjacent config lock. With a running
  `safe_mode_health_only` daemon, repair attempts must stop the user service and
  observe daemon-lock release before editing. If the lock remains held, offline
  repair refuses mutation even when signed safe-mode repair is unavailable.
- Recovery-ordering tests prove unsafe listeners offer signed or offline
  listener repair before fleet reset, invalid/lost `shared_key` with a safe
  listener offers confirmed local fleet reset, daemon-unavailable safe-listener
  cases prefer restart/diagnostics, and held locks block both repair and reset
  mutation.
- SSH transport mode does not use `discovery/static_peers` for runtime HTTP
  fanout.
- SSH config parser expands key and known-host paths safely.
- Managed key/state/config path handling rejects symlinked managed directories,
  final symlink writes, and traversal outside the managed clipfan directory.
- Config load rejects duplicate peer IDs and peers whose ID equals local
  hostname.
- Config load rejects peer IDs outside the allowed character set and length.
- Config validation allows accept-only peers with `connect:false` and no
  outbound locator fields, but requires host/user/port for `connect:true`.
- `ssh_keys_ready` validation is directional: accept-only peers need enabled
  config plus managed authorized_keys acceptance, while outbound peers also need
  sync key and known-host records.
- SSH transport validation requires a local sync key pair before outbound
  `connect:true` actions, key publication, or key rotation. Accept-only inbound
  reservations with valid `accept:true` proof do not require the local sync
  private key, but status remains orange with `missing_sync_key` until local key
  regeneration succeeds. Minimal safe-mode load does not require sync keys.
- Add Peer tests distinguish remote accept-only missing sync keys from local
  missing sync keys: `remote_accept_only_missing_sync_key` remains visible in
  peer details, blocks future reciprocal outbound setup and rotation of that
  remote host's own sync key, but does not block this host's latest-state
  exchange, public green/success UI, or this host's host-global sync-key
  rotation.
- Directional proof validation requires matching key IDs, gateway paths, and
  verification timestamps before promotion to `ssh_keys_ready`.
- Local authorized_keys proof-validation tests parse this host's managed lines
  without rewriting them and reject wrong key ID, wrong `--authorized-peer`,
  wrong gateway path, unsupported options, and permission drift.
- Inbound `ssh_keys_ready` reservation tests require current local managed
  authorized_keys proof/material validation and return `peer_not_ready` on proof
  or permission drift; the only bypass is `shared_key_written_unverified` plus
  purpose `version`.
- Outbound session-start tests are separate from inbound reservation tests:
  default one-way `shared_key_written_unverified` can start outbound `version`
  with `connect:true` locally while the remote gateway reserves inbound
  `version` with `accept:true`; neither side allows `receive` or `sync-stream`
  in that state.
- Inbound reservation tests prove the `shared_key_written_unverified` version
  exception succeeds without persisted local managed authorized_keys proof, then
  still fails during hello when the fleet `shared_key` is wrong, and rejects
  `receive` plus `sync-stream` with `peer_not_ready`.
- Runtime proof drift tests prove successful command-locked sync can set
  `transport_health:"ssh_sync_ready"` while `remote_authorized_key_unknown`
  remains in warnings and keeps the UI orange until regular-SSH repair clears
  it.
- `ssh_material_staged` rejects every gateway purpose with `peer_not_ready` and
  never renders green.
- Promotion tests prove local peer state is not promoted to `ssh_keys_ready`
  until remote command-locked version verification and remote promotion both
  succeed.
- Config update APIs reject stale revisions and require reload/merge/retry.
- Scoped peer-config endpoint tests cover the 3b1-3b5 slices separately:
  redacted `GET`/non-secret `PUT`, proof `PATCH`, persisted-state transition
  `POST`, disable/delete, auth-version header enforcement, stale revision
  conflicts, unknown-field rejection, legal transition enforcement, and unknown
  config v2 field preservation. They prove `PUT` cannot change migration state,
  including writing `ssh_material_staged`, `provision_failed`, or promotion
  states; staged and failed states require proof plus transition endpoint
  evidence; and deleting states with cleanup obligations writes the required
  SSH-material cleanup record or post-secret tombstone before the peer row is
  removed.
- Config v2 revision tests cover missing revision as external
  `config_revision:null` plus internal virtual revision 0, rejection of
  `expected_config_revision:0` for missing-revision configs, first v2 write
  storing 1, exactly-one increment per successful write, integer revision return
  from read/status APIs only after the field exists, and authenticated
  stale-write errors returning the current external revision representation.
- Config v2 passthrough tests cover unknown top-level fields and future nested
  `ssh` fields surviving Milestone 0 scoped updates without being parsed into
  zero-value typed structs or dropped.
- Local config writes take the adjacent lock, preserve mode `0600`, atomically
  rename, reject stale revisions, and recover cleanly from temp files after
  crashes.
- Runtime storage tests and documentation checks classify NFS, SMB,
  cloud-synced folders, network homes, and shared home directories as
  unsupported for config, state, SSH keys, known_hosts, and transport-current
  storage; known unsupported roots fail closed with `unsupported_runtime_storage`
  before SSH transport or 17d3a listener/config cutover can enable. Post-17d3a
  tests prove unsupported or inconclusive storage with an old generated public
  listener binds no socket, leaves no public listener active, and relies on
  app/CLI offline diagnostics instead of daemon health. Tests also cover
  `storage_check_inconclusive`, the owner/mode/lock/atomic-rename smoke test, app
  status surfacing, no user override for known unsupported storage, and no claim
  of cross-host lock coordination for shared homes.
- Offline config repair works when the daemon cannot bind, takes the config
  lock, writes a timestamped backup, edits only listener repair fields
  (`listen`, `port`, and `previous_listen`), and never restarts a pre-SSH
  daemon.
- Disabled peers do not start outbound sessions, do not accept inbound
  reservations, and render gray with highest precedence. Disabled warnings or
  reasons may appear in labels/details/logs but do not override the gray color.
- Disable retains local managed authorized_keys lines and dedicated known_hosts
  pins while reservations reject and the UI says the peer is disabled. Delete
  attempts local managed-line cleanup, records `stale_local_authorized_key_line`
  on cleanup failure, exposes a cleanup retry action, and reports stale remote
  connect lines when regular SSH cleanup is unavailable.
- Gateway command parser rejects unknown commands, TTY use, shell metacharacter
  command strings, and missing `--authorized-peer` or `--authorized-key-id`.
- Gateway command parser tests cover direct local test mode separately from
  production forced-command mode. Production mode ignores argv verbs and accepts
  only exact `SSH_ORIGINAL_COMMAND` values: `version`, `receive`,
  `sync-stream`, and `probe-authorized-key`.
- Pre-secret probe tests prove `probe-authorized-key` succeeds without daemon
  auth, config, or `shared_key`; emits only the documented probe JSON on stdout;
  emits empty stderr on success; rejects TTY and unknown command strings; and
  cannot run version, receive, sync-stream, install, update, config, service, or
  clipboard behavior.
- Authorized peer is enforced during hello.
- Hello HMAC rejects stale timestamps, replayed nonces, wrong peer IDs, and bad
  signatures.
- Runtime gateway verbs emit no hello, version, state, ack, or signed material
  before verifying the client-first hello. The pre-secret probe emits no
  hello/version/state/ack or signed material at all.
- Gateway daemon-unavailable tests prove `version`, `receive`, and `sync-stream`
  return or surface `daemon_unavailable`, `local_auth_failed`,
  `daemon_protocol_error`, or `gateway_failed_before_hello` before hello without
  leaking signed material.
- Initiator/gateway hello validation tests cover `host_id` as sender and
  `peer_id` as receiver from both sides of a stream.
- Hello nonce replay cache retention, caps, eviction counters, and restart
  behavior match the 2 minute freshness, 4 minute retention, 256 entries per
  `(authenticated_remote_host_id,purpose)` bucket, and 8192 process-wide entry
  contract. Tests prove two inbound peers to the same daemon use separate replay
  buckets even though both hellos name the local host as `peer_id`.
- Hello and version HMAC tests use fixed canonical byte fixtures.
- HKDF-derived request, SSH HMAC, and body AEAD keys are domain separated and
  covered by fixed test vectors.
- Envelope crypto boundary tests prove SSH/current envelopes use
  `clipfan-v1/body-aead`, legacy peer HTTP bodies keep the existing
  `SHA256(raw shared_key)` key only during the pre-cutover compatibility window,
  and receivers choose by authenticated endpoint/protocol without trying both
  keys.
- Config v2 signed local endpoints reject raw-`shared_key` old app/CLI
  signatures, require `X-Clipfan-Auth-Version`, include
  `auth_version=clipfan-v1/request-hmac` in request and response canonical
  fixtures, require `X-Clipfan-Ts`, `X-Clipfan-Nonce`, `X-Clipfan-Sig`, and
  `X-Clipfan-Response-Sig` on the existing signed request/response paths, and
  accept only HKDF-derived `clipfan-v1/request-hmac`.
- Shared-key validation permits missing `shared_key` only for pre-secret
  regular-SSH staging states, and rejects missing, malformed,
  non-standard-base64, and non-32-byte values before signed local APIs,
  command-locked sync/version, final secret write, or promotion.
- Shared-key staging tests prove the remote `ssh_material_staged` config may
  lack `shared_key`, the scoped final write payload must include a valid local
  fleet credential, and promotion validates the post-write config rather than
  rejecting the pre-write staged file.
- Protocol challenge nonces are 16-byte lowercase hex; body AEAD nonces are
  12-byte standard base64; tests reject cross-use and malformed encodings.
- Protocol JSON tests cover compact sender output, arbitrary receiver field
  order, duplicate-key rejection, unknown-field rejection, and canonical watch
  state hashing.
- Protocol error message tests cap remote-controlled `message` at 256 bytes,
  escape invalid UTF-8/control characters, and prove UI state is driven by
  stable `code` values rather than remote text.
- Local current API tests prove `GET /v1/current` returns current payloads
  without any sequence, `GET /v1/current/watch?include_snapshot=true` registers
  the watch and emits the initial snapshot atomically under the daemon ordering
  lock, null payloads include the persisted `null_reason`, `no_visible_current`
  is emitted only with an empty ordering barrier, current watch events carry
  signed local `watch_seq` but no SSH `state.seq`, and gateway/session writers
  add outbound-direction SSH `seq` only when constructing SSH `state` frames.
- Receive bridge tests prove `POST /v1/current/receive` and the legacy
  loopback/test-only receive handler never mint a new clip ID for a peer
  envelope and always pass the sender's original envelope identity to
  `ApplyEnvelope`.
- Local watch atomicity tests prove direct watch clients receive the initial
  snapshot first and that changes after registration are delivered as later
  `current` events with no subscription gap.
- SSH writer snapshot-coalescing tests block the writer after a snapshot event is
  queued but before any SSH state frame is written, inject a newer current event,
  and prove the queued snapshot is superseded while preserving latest-state
  delivery.
- State/ack protocol tests require monotonically increasing per-direction `seq`,
  independent full-duplex sequence namespaces, ack `seq` echo, required
  `null_reason` for null state, empty `id` for null-state `no_state`, null frames
  bypassing `ApplyEnvelope`, and null-state health correlation by `seq` plus
  the sender's in-flight `null_reason`. They also prove ack frames never carry
  `null_reason`.
- Ack health matrix tests prove only `applied`, qualifying
  `no_state/no_visible_current`, `ignored_seen`, and `ignored_echo` can set
  `ssh_sync_ready` or last successful push timestamps; `ignored_older`,
  `ignored_concealed`, non-qualifying null reasons, and `rejected` cannot.
  Direction-aware readiness tests separately prove
  `ignored_older` plus opposite-direction `applied` may qualify, while
  `rejected` plus opposite-direction `applied` stays orange.
- Protocol violation tests prove sender mismatch, missing/repeated/decreasing or
  skipped `seq`, malformed JSON, duplicate JSON fields, invalid frame order, and
  unknown frame type emit one `error` frame with a stable code, close the
  channel, and never call `ApplyEnvelope` or return an ack. Missing or invalid
  `null_reason` on `clip:null` frames is a protocol violation.
- Stream readiness tests prove hello success and ping/pong liveness set
  `transport_connected_no_clip_exchange`, not `ssh_sync_ready`; only a
  qualifying latest-state ack can set `ssh_sync_ready`. A persistent stream that
  has produced a qualifying ack remains `ssh_sync_ready` while it stays
  hello-verified, writable, and ping/pong healthy even if green-blocking warnings
  keep the UI orange; it does not need periodic readiness frames. Startup
  `clip:null` `no_state` ack qualifies only when
  `null_reason:"no_visible_current"`, and `concealed_clear` plus
  `user_cleared_current` null markers never qualify as successful sync. Closing
  or making the stream unwritable decays health to reconnect/backoff or
  `transport_connected_no_clip_exchange`.
- Runtime auth failure logs include structured failure codes and signature
  presence, but never raw HMACs, HMAC prefixes, nonces, encrypted bodies, or
  full signed frames.
- SSH log cursor tests prove `GET /v1/ssh/logs` returns bounded redacted pages,
  opaque peer-scoped and source-scoped cursors, `next_cursor` only when older
  entries remain, `cursor_expired` when a runtime ring overwrite/restart or
  durable-store compaction drops the referenced page, the newest available page
  after cursor expiry, and no raw log text or secrets in the cursor.
- Log durability tests prove daemon runtime-ring entries are
  `source:"runtime_ring"`, marked `durable:false`, and may disappear after
  daemon restart. They separately prove daemon-owned provisioning, listener
  repair, `shared_key_written_unverified` remediation, `ssh_material_cleanup`,
  and `post_secret_tombstone` records use their specific sources, are marked
  `durable:true`, and survive daemon restart until documented durable-store
  compaction. App-owned legacy regular-SSH update/check logs are tested through
  app update/check sheets and diagnostic export, not daemon `GET /v1/ssh/logs`,
  unless imported into daemon-owned remediation records by a later milestone.
- Frame decoder enforces newline framing, 90 MiB encoded frame limit, and 64
  MiB decrypted payload limit.
- Protocol timing tests cover per-frame idle/progress timeout, size-aware
  absolute frame deadline, on-demand command wall-clock cap, null-state absolute
  minimum, and the 64 MiB payload over a sustained 5 MiB/s fake link performance
  target.
- Large-frame decoding uses bounded buffers and the process-wide large-frame
  semaphore.
- Low-resource payload-limit tests reject oversized frames with
  `payload_too_large` and never partially apply clipboard state.
- Persistent streams and on-demand sends share the same large-frame semaphore;
  tests prove on-demand cannot exceed the configured large-frame concurrency.
- Payload-limit mismatch tests show the sender records orange
  `payload_too_large` health for that peer and later smaller clips can still
  sync.
- `state` frames with null clip do not mutate current state.
- `ApplyEnvelope` returns the correct structured status for applied, seen,
  echo, older, concealed, bad recipient, bad timestamp, decrypt failure, and
  payload-too-large cases.
- Clip envelope timestamp tests prove future timestamps more than 2 minutes
  ahead return `bad_timestamp`, future timestamps within 2 minutes are eligible
  for ordering, and old past timestamps have no freshness rejection beyond the
  ordering barrier.
- Repeated accepted or rejected peer clips more than 30 seconds ahead of local
  time set `clock_skew_warning` with maximum observed skew and stable log codes.
- Clock-skew warning clear tests cover local wall clock catch-up, superseding
  newer ordering keys, rejected future clips that do not advance the barrier,
  and a later healthy interval with no future timestamps.
- Locally minted clips use a monotonic timestamp greater than the persisted
  ordering barrier after wall-clock rollback and record
  `local_clock_rollback_adjusted` when adjusted more than 30 seconds.
- Simultaneous local source tests run clipboard polling, manual copy, history
  restore, and local copy API submissions through the daemon sequencer and prove
  unique, strictly increasing local ordering keys.
- Equal-timestamp conflicts converge by `(timestamp, origin host ID, clip ID)`.
- Persisted current transport state and ordering barrier are recovered after
  daemon restart and can be emitted to a peer.
- Transport-current persistence tests prove writes use the
  `transport-current.lock`, mode `0600` regular files, temporary file plus fsync
  plus atomic rename, last-known-good backup recovery, and fail-closed
  `transport_state_corrupt` behavior when both primary and backup are invalid.
- Corrupt transport-current tests prove the daemon does not start SSH sync or
  local current/watch/gateway state endpoints with an empty ordering barrier
  when a prior transport state file exists but cannot be recovered.
- Pre-SSH state migration does not backfill transport-current from `store.State`,
  `current.txt`, or image-store paths; the next visible local or received clip
  creates transport-current.
- A local concealed clip clears visible transport-current state, persists the
  ordering barrier, survives restart, and rejects stale visible peer state.
- Restart after a concealed clear loads `current:null` with
  `current_null_reason:"concealed_clear"`, emits watch and SSH null markers with
  `null_reason:"concealed_clear"`, and does not set readiness, peer health
  green, last successful push, or delivery-success logs.
- Clear-current retention tests prove clearing local history alone does not clear
  transport-current, clearing current clipboard/sync state clears
  transport-current and emits null events, disabling a peer stops sends without
  deleting global transport-current, and re-enrollment with a new fleet
  credential deletes transport-current encrypted under the old key.
- Restart after user clear-current loads `current:null` with
  `current_null_reason:"user_cleared_current"`, preserves the ordering barrier,
  rejects stale visible peer state, and does not qualify as successful sync.
- First-run and post-reset startup with no transport-current files emits
  `null_reason:"no_visible_current"` with an empty barrier and can qualify for
  readiness only through the normal no-visible-current ack path.
- Legacy-field recovery tests cover `current:null` transport-current files that
  omit `current_null_reason`: empty barrier recovers to `no_visible_current`,
  non-empty barrier recovers to non-qualifying `user_cleared_current` and logs
  `transport_current_null_reason_recovered`, and unknown null reasons remain
  invalid/corrupt state.
- Local fleet-reset tests are phased with reset implementation milestones. The
  1f tests prove invalid or lost `shared_key` recovery requires explicit
  confirmation, refuses unparseable/wrong-owner/unsafe-mode/SSH-material configs,
  writes only the minimal pre-SSH single-host recovery state, and leaves remote
  cleanup to regular SSH re-enrollment. Later extension tests prove reset clears
  peers, sync keys, known_hosts, managed authorized_keys lines, and
  transport-current only after those primitives exist.
- Concealed-generated null events use `null_reason:"concealed_clear"`, receive
  `no_state` acks, and do not update peer health or last successful push
  timestamps.
- Image SSH state frames decrypt to image bytes, write a receiver-local image
  store path, never depend on sender `local_image_path`, and reject or
  `ignored_echo` the same image-store-path cases as the existing receive path.
- Recipient rewrite tests prove the encrypted body is unchanged, recipient is
  validated as an outer envelope field, and future resealing requirements would
  fail a fixture if AEAD associated data changes.
- State frames whose `sender` differs from the authenticated channel peer emit
  `sender_mismatch`, close the channel, and are not submitted to `ApplyEnvelope`.
- Watch stream events have valid per-event HMACs and use a single-slot
  latest-only queue.
- Session reservation tests cover reserve, signed renewal, release, expiry,
  unknown token, expired token, peer/purpose/PID mismatch, `sync-stream` renewal
  every 10 seconds, gateway close on renewal failure, and capacity release after
  expiration.
- Session reservation rejects missing, disabled, removed, or unprovisioned peer
  IDs with `peer_not_configured`.
- Session reservation applies to `version`, `receive`, and `sync-stream`.
- Per-peer session capacity tests prove `max_sessions_per_peer` counts inbound
  and outbound `version`, `receive`, `sync-stream`, persistent, and on-demand
  sessions together; a closed unhealthy stream releases capacity before fallback;
  and a full cap returns `too_many_sessions` without spawning another SSH
  process.
- Session reservation permits only `version` for
  `shared_key_written_unverified` and rejects `receive`/`sync-stream` with
  `peer_not_ready`.
- `shared_key_written_unverified` version reservation succeeds before hello
  based on forced-command peer ID and persisted state, then returns no version
  data until hello HMAC verifies.
- Timeout tests cover hello deadline, write deadline, ping/pong timeout, and
  on-demand total deadline.
- Runtime drift detection reports missing sync key, missing known host, and
  permission drift without changing persisted `ssh_keys_ready`; remote
  authorized_keys drift is reported only by app-initiated regular-SSH checks.
- On-demand eligibility tests prove `connect:false` accept-only peers, peers
  without outbound locator fields, peers without a local sync key, peers without
  pinned known-host entries, and peers with missing or malformed `connect` proof
  do not spawn fallback SSH or record noisy on-demand failures. Separate stale
  remote-proof tests prove `remote_authorized_key_unknown` does not block
  outbound on-demand by itself but keeps the row orange until regular-SSH repair
  refreshes proof.
- Direction-aware fallback tests cover both sides of the default one-way Add
  Peer topology: the `connect:true` side falls back on-demand when its persistent
  stream is down and can qualify for green readiness through the completed
  direction-aware exchange, while the `accept:true, connect:false` side does not
  attempt on-demand, does not record an on-demand failure, and remains
  orange/not-ready for local outbound changes unless a live persistent stream is
  writable. Tests also prove absent reciprocal setup means no remote-to-local
  on-demand path is inferred.
- On-demand fallback trigger tests prove a connected stream with hello or
  ping/pong liveness but no writable latest-state capability does not suppress
  fallback, while a stream with a live write loop and accepted watch snapshot
  does suppress fallback. Write timeout or stream close with a queued or unacked
  newest current state schedules on-demand without waiting for another copy
  event; reconnect winning the race cancels the redundant on-demand attempt.
- Runtime readiness tests prove qualifying persistent latest-state acks set
  `transport_health:"ssh_sync_ready"` for the life of that healthy writable
  stream, completed direction-aware on-demand exchanges set it only inside the
  60 second on-demand freshness window when no persistent stream is
  live/writable, standalone command-locked `version` success does not, and
  version/protocol warnings keep the row orange even after a qualifying
  latest-state exchange. They include the asymmetric case where
  `outbound_ack_status:"ignored_older"` plus `inbound_ack_status:"applied"`
  qualifies only after both directions complete and the final ack is written,
  while either direction ending in `error`, either direction producing
  `rejected` or `ignored_concealed`, or both directions having only
  non-qualifying benign statuses stays orange.
- Accept-only lost-sync-private-key tests prove inbound accept proof remains
  valid, UI stays orange with `missing_sync_key`, local regeneration is allowed
  for accept-only operation, and future outbound setup requires regular-SSH
  reprovisioning.
- Remote accept-only missing-sync-key tests prove
  `remote_accept_only_missing_sync_key` renders as a non-green-blocking notice,
  survives restart, blocks only future reciprocal outbound setup and that remote
  host's own sync-key rotation, and does not block this host's Add Peer
  green/success or host-global sync-key rotation.
- Redacted remediation history persists `shared_key_written_unverified`,
  host-key mismatch, cleanup, demotion/removal, and downgrade-blocking events
  across app/daemon restarts.
- On-demand receive returns structured ack/error frames.
- On-demand exchange sends receiver current state back after the receiver ack,
  and the initiator returns the final ack/error.
- Null state frames receive `no_state` acks with empty IDs and require a valid
  `null_reason`.
- On-demand partial failures keep any successfully applied state and record the
  last completed phase; no rollback is attempted.
- Forced-command gateway rejects requested `install`, `update`, shell, scp, and
  arbitrary command strings.
- Session manager reconnect backoff is deterministic under a fake clock.
- Concealed local clips are not sent; concealed inbound frames are ignored and
  not relayed.

Go integration tests with fake `ssh`:

- Persistent session starts the expected `ssh` argv with `BatchMode`,
  `IdentitiesOnly`, strict host checking, the configured key, and clipfan's
  known-hosts file, plus `GlobalKnownHostsFile=/dev/null` and
  `UpdateHostKeys=no`. A matching global known_hosts entry must not satisfy a
  missing clipfan dedicated pin.
- Release gate tests fail if any peer runtime path can call
  `http://<peer>:7853`.
- Release gate subtests cover `daemon-peer-http-runtime`,
  `app-peer-version-update`, `migration-no-http-fallback`, and
  `fixture-allowlist`.
- SSH child lifecycle drains stdout/stderr, redacts stderr, cancels on daemon
  shutdown, and waits for process exit.
- Bidirectional persistent startup with large current-state frames does not
  deadlock because read/write loops run concurrently.
- Persistent stream memory pressure test starts the configured maximum outbound
  streams, injects frames at the maximum accepted frame size plus rejected
  oversized frames, and asserts bounded latest-only queue depth, bounded active
  stream goroutines/processes, and no unbounded heap growth after GC. The
  acceptance metric is "one latest pending frame per peer, at most two
  large-frame encoded buffers, and at most 1 MiB pre-semaphore buffer per other
  active session"; a test failure must report queue depth, active stream count,
  semaphore holders/waiters, encoded buffer bytes, and heap delta.
- Latest-only queue tests prove superseded sends increment
  `superseded_state_drops`, expose counters in default status/logs, hide clip
  IDs in default and diagnostic logs, and include dropped/replacement clip IDs
  only in explicit developer logs without clipboard body data.
- Observability tests prove default status includes active SSH process counts,
  large-frame semaphore usage, latest-only drop counts, dropped remote state
  counts, reservation active/expired/failure counters, and on-demand
  attempt/success/failure counters without clip IDs, nonces, signatures, frame
  bodies, or clipboard content.
- Local current state is sent once after hello.
- A remote state frame is applied locally.
- A local clip change is sent over an existing stream.
- Stream failure triggers on-demand send when configured.
- Delivery cursor tests prove `applied`, `ignored_seen`, `ignored_echo`, and
  `ignored_older` outbound acks advance only the in-memory per-peer visible-state
  cursor; rejected/error/malformed/write-timeout outcomes do not; null-state acks
  update only null-state correlation; daemon restart forgets the cursor and may
  safely resend latest state; reconnect acceptance of the same or newer visible
  state cancels a pending on-demand send.
- On-demand failure marks peer status unhealthy with a copyable error.
- No off-host HTTP peer calls are made in `ssh_keys_ready`, runtime
  `ssh_sync_ready`, `loopback_unprovisioned`, or `provision_failed` states.
- Version/update code makes no public HTTP calls after the version migration.

Real OpenSSH integration tests:

- A local ephemeral OpenSSH server accepts the managed authorized_keys option
  line and runs forced-command `probe-authorized-key` before any config or
  `shared_key` exists.
- `SSH_ORIGINAL_COMMAND` parsing accepts only `version`, `receive`,
  `sync-stream`, and `probe-authorized-key`; bad commands, TTY allocation, scp,
  sftp, and shell strings are rejected.
- `no-pty`, no forwarding options, known_hosts pinning, host-key mismatch
  failure, and non-default port known_hosts formatting are exercised against
  real OpenSSH.
- The real-server fixture covers pre-secret `probe-authorized-key`,
  command-locked `version`, receive primitive handshake rejection before hello,
  and authorized_keys path rendering.
- AuthorizedKeysFile tests prove clipfan creates and manages only
  `<target-home>/.ssh/authorized_keys`, repairs private `.ssh` and file modes
  before writing, fails with `unsupported_authorized_keys_file` when sshd
  reports that the default file is not honored, and fails with
  `authorized_keys_not_effective` before any remote `shared_key` write when the
  pre-secret forced-command reachability probe cannot execute the managed line.
- Real OpenSSH fixtures cover macOS sshd and Ubuntu LTS sshd forced commands
  with absolute clipfan paths, non-login shells, and minimal environments.
- Forced-command fixtures with missing or unusual `HOME` verify absolute
  `config_path`/`state_dir` discovery and `home_unavailable` failure instead of
  creating config in the working directory.

Swift tests:

- Mac app local daemon discovery uses the shared helper, probes loopback in safe
  mode, accepts 7853 fallback only after signed identity proof, separates
  health-only liveness from signed status/mutation, and can repair a persisted
  explicit non-loopback config.
- Add-peer flow writes loopback config for remotes.
- First-install host-key bootstrap displays key type and SHA256 fingerprint and
  stores the `ssh-keyscan` result only after confirmation.
- Host-key bootstrap enforcement tests prove no upload, install, provision,
  update, or config mutation command is constructed before TOFU confirmation
  succeeds for the exact host/port tuple.
- Security prompt accessibility tests cover keyboard navigation, VoiceOver
  labels for fingerprints/host IDs/peer names, copy buttons, and non-color-only
  risk communication for TOFU, host-key mismatch, duplicate host ID, and
  `shared_key_written_unverified`.
- Host-key mismatch repair uses a temporary known_hosts file for one fixed
  non-mutating regular-SSH credential probe, leaves the permanent pinned key
  unchanged on failure, and replaces only the exact host/port tuple on success.
- Host-key race tests prove every SSH/SCP command after TOFU confirmation uses
  the pinned known_hosts file and fails closed if the host key changes between
  `ssh-keyscan`, install upload, regular-SSH probe, and runtime gateway checks.
- Host-key algorithm tests prefer Ed25519, accept OpenSSH-reported ECDSA, accept
  RSA only with SHA-2 RSA signatures, and never add `ssh-rsa`/SHA-1
  compatibility overrides.
- Regular install/update passes explicit host/user/port, ignores user SSH config
  aliases/proxies, and fails clearly when the explicit target is unreachable.
- Public pre-17d3b regular install/update boundary tests prove update/check
  operations never stage or install `config.json`, `shared_key`, `static_peers`,
  `ssh.peers[]`, sync keys, dedicated known_hosts, managed authorized_keys,
  migration state, or peer config. The current Swift `Installer.install` path
  that stages config is classified as `add_peer_provision_mutation` and is
  rejected in public builds before 17d3b.
- Installed-path capture tests prove `install.sh --json-result` is the only
  machine-readable installer stdout contract, human installer output is ignored,
  `<install_path> version --json` verifies the path/account metadata, and stale
  or mismatched paths are not persisted.
- Regular install/update accepts a validated explicit identity-file path, expands
  `~/`, rejects relative paths, control characters, non-regular files, wrong
  owner, and group/world-writable files, and passes the accepted file as argv
  `-i <absolute path>` with `IdentitiesOnly=yes`.
- Regular install/update without an explicit identity file may use an already
  running SSH agent or OpenSSH default identity lookup, but every invocation uses
  `BatchMode=yes`; tests prove a password/passphrase requirement returns
  `ssh_auth_required` or `ssh_auth_failed` without showing an interactive prompt
  or hanging the app.
- Runtime command-locked `version`, `receive`, and `sync-stream` argv
  construction never includes the user's explicit identity-file path, never uses
  the user's SSH agent as the runtime identity, and uses only the clipfan-managed
  sync key plus dedicated known_hosts file.
- Add Peer rejects SSH config aliases, ProxyJump-only hosts, ProxyCommand-only
  hosts, and SSH-config-only `IdentityFile` dependencies with
  `unsupported_ssh_topology` before `add_peer_provision_mutation`, and the
  UI/release notes label those topologies as unsupported for this release.
- SSH/SCP/ssh-keyscan construction uses argv arrays and rejects invalid
  host/user/port/path/peer ID values before execution.
- Host canonicalization tests cover DNS lower-casing, exactly-one trailing dot
  stripping, invalid empty/doubled labels, punycode A-label lower-casing,
  canonical IPv4 dotted decimal, RFC 5952 IPv6 storage without brackets,
  known_hosts tuple rendering for default and non-default ports, and
  duplicate-target rejection using `ssh_user`, canonical `ssh_host`, and
  `ssh_port`.
- SCP destination tests cover DNS, IPv4, IPv6 bracketed target rendering,
  `-P <port>`, absolute remote path validation, and no shell interpolation.
- Remote provision command construction rejects install/gateway paths with
  spaces, quotes, shell metacharacters, control characters, relative forms,
  lexical non-canonical forms, any `..` component, symlink final components,
  wrong owner, or group/world-writable mode before command construction.
- Remote provision subcommands receive variable data only through JSON stdin;
  hostile peer IDs, hostnames, and paths are never interpolated into remote
  shell command text.
- Provision-client gate matrix tests call the gate directly for every
  subcommand and operation context, proving public builds before 17d3b reject
  Add Peer `service` and `cleanup` mutations as well as key/config/proof
  mutations before any SSH process is spawned.
- Remote `sync-key` provision tests cover new key creation, sidecar creation,
  sidecar owner/mode validation, key ID/public-key digest matching, expected
  host-ID mismatch, missing sidecar, stale sidecar, idempotent retry, mode
  repair where safe, and no private-key disclosure.
- Remote `write-config` provision tests cover `expected_config_revision`, stale
  revision conflicts with current revision reporting, unknown config v2 field
  preservation, host-ID mismatch, owner/mode rejection, atomic writes, and retry
  after remote config changes between identity probe and write.
- Remote `transition` provision tests run the command on the remote host with a
  fake loopback daemon, prove it signs `proof` and `transition` requests with
  `clipfan-v1/request-hmac`, handles `daemon_unavailable`, `local_auth_failed`,
  `config_revision_conflict`, and `proof_mismatch`, and never promotes the local
  peer before remote transition succeeds.
- Add-peer flow reads remote `host_id` from regular SSH `clipfan version --json`
  and rejects mismatched or duplicate peer IDs before final config write.
- Backup/restore tests cover restoring the same host identity onto two live
  machines. The second live machine is rejected as `duplicate_host_id` during
  identity probe or hello, remains orange, and requires explicit host identity
  repair before it can reserve sessions.
- Backup/restore tests cover restoring `shared_key` without the matching sync
  private key and restoring a sync private key under a different host identity;
  both paths require reprovisioning or key rotation before `ssh_keys_ready`.
- `clipfan version --json` returns host ID, uid, effective user, home dir,
  config path, state dir, install path, version, protocols, and
  configured=false without requiring daemon config or `shared_key`.
- Add-peer flow installs/probes identity without writing `shared_key`, writes
  final config only after identity/account validation, and does not start the
  remote daemon before final config exists.
- Add-peer flow writes the local peer record as `accept:false, connect:true` and
  the remote peer record for the local host as `accept:true, connect:false`, both
  staged. This release never writes local `accept:true` during Add Peer; that
  requires a future reciprocal outbound feature with the remote sync public key
  installed locally and proof recorded.
- Add-peer promotion writes `shared_key_written_unverified`, verifies the
  command-locked version gateway, promotes the remote record, then promotes the
  local record last.
- Add-peer promotion refuses `ssh_keys_ready` when directional proof is missing,
  stale, or for the wrong key ID/gateway path.
- Two-host proof tests verify the exact local `connect_key_id` and remote
  `accept_key_id` fields for the default one-way-outbound setup, and prove
  reciprocal outbound proof fields remain absent unless a future explicit
  reciprocal setup milestone adds them.
- Add-peer provisioning appends only the managed authorized_keys line.
- Remote `clipfan provision` subcommands are behavior-tested with fake
  filesystem/service dependencies and JSON stdin/stdout.
- Remote `known-hosts` provision tests cover exact host/port replacement,
  unrelated entry preservation, confirmed fingerprint validation, atomic writes,
  and refusal to edit the user's normal OpenSSH known_hosts file.
- Production-build tests prove public Add Peer cannot invoke remote `sync-key`,
  `known-hosts`, `write-config`, `authorized-key`, `transition`, `service`, or
  `cleanup` mutation before the Milestone 17d3b release manifest enables public
  Add Peer, while user-initiated regular SSH update/check actions remain
  available where the public behavior table allows them.
- Operation-local remediation tests prove remote pre-secret artifacts created
  before a local peer row exists produce durable app-side cleanup records, those
  records survive restart, they are copyable from the Add/Repair/Update sheet,
  and they never create an `ssh.peers[]` row or `provision_failed` transition
  without a later user-confirmed Add/Repair flow reaching the durable
  peer-record phase.
- Managed authorized_keys rendering always uses the explicit portable option set
  and does not depend on OpenSSH `restrict` support.
- Add-peer provisioning serializes authorized_keys edits, preserves unrelated
  keys, and reports partial failures with copyable logs.
- Authorized_keys updates retry from a fresh read when mtime, inode, or content
  hash changes before rename.
- Managed authorized_keys cleanup tests prove disable retains managed lines,
  delete removes only the deleted peer's local accept line when possible, cleanup
  failures persist stale local remediation state, stale remote connect lines are
  reported when regular SSH is unavailable, and removed-peer forced commands
  return `peer_not_configured`.
- After 17d3b, update flow detects installed binary path changes and rewrites
  only managed authorized_keys lines for affected already-provisioned peers.
  Public builds before 17d3b report stale managed paths without rewriting them.
- Add-peer provisioning rejects unrecorded duplicate host IDs with different
  key IDs.
- Duplicate-host-ID repair tests cover local rename before provisioning, stale
  managed-key removal over regular SSH, cancellation with no fleet-secret write,
  and rejection of host identity regeneration after any peer has recorded this
  host as `ssh_keys_ready`.
- Host-global key rotation stages the new key ID for every required enabled
  outbound peer, verifies all required peers before promotion, excludes
  disabled/removed peers from the required participant set, keeps the old
  key/proof active on failed install or verification, requires re-enable repair
  for peers that missed rotation, and leaves visible cleanup pending on failed
  old-line cleanup instead of silently succeeding.
- Host removal deletes only the peer's exact entry from clipfan's dedicated
  known-hosts file when no enabled or disabled remaining peer config references
  the same host/port tuple.
- Host removal preserves a known_hosts entry when another enabled or disabled
  peer config references the same host/port tuple, and user-confirmed cleanup
  prunes only unreferenced entries.
- Partial failure tests cover host-key verification failure, payload install
  failure, authorized_keys update failure, daemon restart failure, local config
  write failure, and retry idempotency.
- Failure persistence matrix tests prove pre-peer failures create only
  operation-local logs, pre-secret failures after a local peer row transition to
  `provision_failed`, secret-capable unknown outcomes transition to
  `shared_key_written_unverified`, and locked absence proof is required before a
  secret-capable attempt can return to `provision_failed`.
- Provisioning retry tests enforce 3 automatic attempts per phase with 1s, 2s,
  and 4s jittered backoff, then require explicit user retry from the last
  idempotent successful phase.
- Post-install identity/account validation failure leaves no `shared_key`, no
  managed authorized_keys mutation, no daemon start, and offers reusable-artifact
  retry or best-effort cleanup.
- `shared_key_written_unverified` remediation tests cover retry verification,
  regular-SSH cleanup, remote demotion/removal, local staged-record cleanup, and
  shared-key rotation guidance before any flow can write a remote `shared_key`.
- Release-build tests reject `ALLOW_REMOTE_SECRET_WRITE` and prove real remote
  `shared_key` writes stay disabled until Milestone 17d3b acceptance is
  satisfied.
- Release-manifest tests regenerate `release/ssh-transport-gates.json` and
  `release/ssh-runtime-gates.json` outputs for Go and Swift, fail on generated
  diff, and prove Go provision-client and Swift Add Peer UI consume the same
  public-build values. They accept exactly three public transport states: all
  transport gates false before 17d3a; only `PeerHTTPRuntimeDisabled` plus
  `ConfigV2WriteEnabled` true at 17d3a; and all transport gates true at 17d3b
  only when `ssh_receive_primitive_enabled`, `ssh_sync_stream_enabled`, and
  `ssh_persistent_current_enabled` plus `ssh_sync_key_rotation_enabled` are
  also true in the runtime manifest. They reject `PeerHTTPRuntimeDisabled`
  without `ConfigV2WriteEnabled`, peer-add gates without `ConfigV2WriteEnabled`,
  mismatched `RemoteSecretWriteReleaseEnabled` /
  `ssh_public_add_peer_success_enabled`, and peer-add/secret-write gates without
  the required runtime latest-state and sync-key rotation gates.
- Runtime-gate manifest tests prove `ssh_receive_primitive_enabled`,
  `ssh_sync_stream_enabled`, `ssh_persistent_current_enabled`, and
  `ssh_sync_key_rotation_enabled` live only in
  `release/ssh-runtime-gates.json`, and that
  `ssh_public_add_peer_success_enabled` lives only in
  `release/ssh-transport-gates.json`.
- Local cutover rollback tests prove 17d3a emergency offline rollback writes a
  timestamped backup, refuses to run after any peer reaches
  `shared_key_written_unverified` or `ssh_keys_ready`, exports only a
  detached config-v1-compatible loopback file for `loopback_unprovisioned`
  fleets, does not replace the active config by default, keeps old-daemon restart
  blocked, and never re-enables off-host peer HTTP.
- Provisioning fails when install/update, daemon config, and authorized_keys
  target different Unix accounts.
- Copyable logs redact raw SSH argv, home-directory paths, frame payloads, clip
  IDs, keys, HMACs, and clipboard content unless diagnostic details are
  explicitly enabled where allowed.
- Redaction matrix tests cover default UI, diagnostic opt-in export, developer
  logs, host-key fingerprints, install/gateway paths, stderr summaries, HMACs,
  nonces, frame bodies, and clip IDs.
- Managed sync-key path redaction tests prove peer settings may show only the
  normalized clipfan-managed path with the home prefix collapsed to `~`, default
  fleet rows and failure logs hide the path, diagnostic exports keep it
  normalized, and no surface includes private key contents.
- Copyable log and remote stderr/stdout tests cap lines at 1024 bytes after
  redaction, escape control characters, render text as data, and show
  `[truncated]` markers for longer remote output.
- New-app scoped config updates preserve config v2 fields, while old app/CLI
  whole-config or raw-key signed writes are rejected with
  `config_schema_mismatch`, `auth_version_mismatch`, or `bad_signature`.
- Update flow keeps using regular SSH/SCP credentials.
- Update SSH option construction never selects the clipfan sync key.
- Pre-provisioning version verification uses regular SSH, post-provisioning
  version verification uses the command-locked gateway, and neither path uses
  public HTTP.
- Provisioning writes the fleet `shared_key` into the remote daemon config with
  private permissions and command-locked gateway verification fails when it is
  missing or wrong.
- Provisioning state tests prove `provision_failed` accepts only structured
  `remote_secret_absence_proof`: strictly pre-secret phase failures may set
  `secret_write_command_spawned:false`; any spawned shared-key-capable remote
  command requires a regular-SSH locked remote config read proving the fleet key
  absent, otherwise the peer enters `shared_key_written_unverified` with
  `reason:"secret_write_outcome_unknown"`.
- A failure after writing remote `shared_key` enters
  `shared_key_written_unverified` and shows retry, cleanup, removal, and
  rotation guidance.
- A local promotion failure after remote promotion leaves the local UI orange
  and does not start sync.
- Service lifecycle tests cover launchd, user systemd, fallback user start,
  restart failure reporting, and no sudo/root service usage.
- Fleet rows render SSH health and copyable errors.
- Peer status snapshots use `peer_id`, `last_recv_origin`,
  `last_recv_peer_id`, and `last_recv_transport` consistently.
- Fleet rows apply the Peer States UI color table for healthy streams,
  on-demand freshness, idle/never-connected, disabled, missed pongs, reconnect
  backoff, and failed provisioning.
- Host-key mismatch requires explicit regular-SSH reprovisioning.

Manual verification:

- Install on a public host with no inbound `7853` firewall rule.
- Confirm `nc -vz fsck.com 7853` fails externally while SSH sync works.
- Copy on Mac and see remote tmux buffer update over SSH.
- Copy in remote tmux and see Mac clipboard update over the same persistent SSH
  stream.
- Stop the persistent session, copy locally, and verify on-demand send works.
- Disconnect two components, copy different values, reconnect, and verify
  tuple-based latest-state convergence.
- Copy a concealed item on one host, reconnect, and verify other hosts may keep
  their previous visible clip until a newer sync-eligible visible clip appears.

## Documentation Updates

Documentation is release-blocking, not Milestone 19 cleanup. The 17d3a release
must ship the local security-boundary docs before `PeerHTTPRuntimeDisabled` and
`ConfigV2WriteEnabled` are public. The 17d3b release must ship the SSH sync docs
before remote secret writes, command-locked runtime sync, or public-green Add
Peer UI are enabled. Milestone 19 may polish wording and troubleshooting after
those release-blocking docs already exist.

README:

- Replace HTTP mesh explanation with SSH transport.
- State that the daemon listens on loopback only.
- Explain command-locked sync/version keys and regular SSH install/update.
- Document that history is local and protocol convergence is latest-state only.
- Update security guarantees and concerns.
- Document backup/restore guidance for config v2, `shared_key`, host identity,
  sync private keys, dedicated known_hosts, and managed authorized_keys markers.
  Restoring the same host identity onto two live machines is unsupported and
  should trigger duplicate-host-ID repair. Restoring only `shared_key` without
  the host sync private key requires sync-key reprovisioning. Restoring a sync
  private key without the matching host identity is rejected unless the user
  explicitly runs key rotation for the new host identity.
- Document lost `shared_key` recovery: there is no decrypt/recover path; the
  user must run confirmed local fleet reset to create a new fleet credential and
  re-enroll peers with regular SSH credentials or reinstall/reconfigure them.
  Existing encrypted in-flight envelopes from the old key are discarded.
- Document operational caveats: SSH config aliases, ProxyJump, and ProxyCommand
  are unsupported in this release; laptop sleep/wake or network changes may
  force reconnect/on-demand fallback; DNS/IP changes require host-key
  reconfirmation for the new host/port tuple.

Architecture doc:

- Replace peer HTTP API section with local daemon API plus SSH frame protocol.
- Describe `ssh-gateway`, session manager, and on-demand fallback.
- Keep existing envelope, dedup, echo suppression, image flow, and tmux details.

Release notes:

- Call out that this is a daemon transport change and requires peer update.
- Explain that public `7853` exposure is no longer part of the supported setup.

## Acceptance Criteria

Classification:

- Milestone 17d3a local listener/config cutover: a failure in the 17d3a
  subsection blocks public builds from enabling `PeerHTTPRuntimeDisabled` and
  `ConfigV2WriteEnabled`.
- Milestone 17d3b public Add Peer/sync enablement: a failure in the 17d3b
  subsection blocks public builds from enabling `RemoteSecretWriteReleaseEnabled`
  or `ssh_public_add_peer_success_enabled`.
- Internal milestone: per-milestone tests may pass earlier for hidden/internal
  paths, but they do not permit public-green UI or public remote secret writes.
- Post-cutover polish: only Milestone 19 status wording/docs and optional
  deletion of non-runtime test-only fixtures already classified by Milestone
  17d3b0 can remain after 17d3b public Add Peer/sync enablement; security
  behavior, sync behavior, peer-HTTP runtime inventory, and copyable failure logs
  must already satisfy the applicable release-blocking criteria.

Milestone 17d3a local listener/config cutover criteria:

- A freshly installed daemon binds only to loopback.
- An existing generated `":7853"` config binds loopback immediately and persists
  loopback only through the documented config-version/gate rules.
- Explicit custom public listens enter safe mode, expose only loopback repair
  APIs, and never start SSH peer sync until the user confirms loopback repair,
  but only when storage locality passes. Unsupported or inconclusive runtime
  storage fails before binding any socket and is repaired through offline app/CLI
  preflight, not daemon repair endpoints.
- No app, daemon, update, retry, or migration runtime path probes public peer
  HTTP for version or sync.
- Old `static_peers` expose only user-prompted regular-SSH check/update actions
  with operation-scoped host-key pinning; they do not run background probes,
  persist SSH peer records, or render green.
- Public Add Peer `add_peer_provision_mutation`, public remote `shared_key`
  write, and public-green success UI remain disabled.
- Config v2 writes are gated, revision-checked, atomic, preserve unknown fields,
  and cannot be downgraded by restarting a pre-SSH daemon.
- The first public cutover write creates a timestamped backup, records the
  backup path in redacted remediation history, and the app/updater shows
  preflight or first-launch post-upgrade copy explaining that legacy off-host
  peer HTTP sync was disabled and SSH setup is required before green sync.
- Minimal confirmed local fleet reset recovery is implemented and tested before
  `ConfigV2WriteEnabled` is public, so invalid/lost `shared_key` recovery for
  pre-SSH/config-v2 cutover state does not depend on an unauthenticated daemon
  API. Full SSH-material reset cleanup, including `ssh.peers[]`, proof,
  migration state, remediation records, and local SSH material, is completed by
  Milestone 11e before public Add Peer/sync enablement.
- Old app/CLI signed local APIs are rejected under config v2 rather than
  accepted through raw-key compatibility.
- Any SSH version/update probe exposed in this cutover uses explicit
  `ssh_user`, `ssh_host`, and `ssh_port`, requires host-key confirmation, uses
  pinned known_hosts for later SSH/SCP commands, and fails closed on key change.
- Install/update still work with regular user SSH credentials.
- README and architecture docs accurately describe the 17d3a security boundary:
  local HTTP is loopback-only, public peer HTTP sync is no longer supported, and
  public SSH Add Peer/sync remains pending until the later release gate.

Milestone 17d3b public Add Peer/sync enablement criteria:

- A public host with only direct explicit `ssh_user`, `ssh_host`, and `ssh_port`
  SSH exposed can sync clipboard state. SSH config aliases, ProxyJump, and
  ProxyCommand are unsupported in this release and are labeled that way in Add
  Peer UI and release notes.
- No app, daemon, update, retry, or migration runtime path probes public peer
  HTTP for version or sync.
- The 17d3b0 negative inventory has removed or classified every peer-HTTP
  symbol, and release CI fails if any production off-host peer HTTP runtime path
  remains unclassified or reachable.
- Host-key TOFU and mismatch repair require explicit fingerprint confirmation,
  use pinned known_hosts for every later SSH/SCP command, and fail closed on key
  change.
- Runtime sync/version with clipfan-managed keys cannot open a shell, allocate a
  TTY, forward ports, or run install/update.
- Install/update still work with regular user SSH credentials.
- A leaked sync key without `shared_key` cannot complete the sync handshake.
- Old app/CLI signed local APIs are rejected under config v2 rather than
  accepted through raw-key compatibility.
- `shared_key_written_unverified` has visible retry, cleanup, removal/demotion,
  and rotation guidance before any real remote `shared_key` write can ship.
- Any active persistent SSH stream carries state in both directions.
- On-demand SSH delivery works from each `connect:true` side when that side's
  persistent stream is down.
- In the default one-way Add Peer topology, `accept:true, connect:false` peers
  do not originate on-demand SSH delivery, do not record on-demand failures for
  local outbound changes, and render orange/not-ready for local outbound sync
  unless a live persistent stream is writable or a later reciprocal setup enables
  `connect:true`.
- Three-host relay/latest-state convergence works across the specified
  `A <-> B <-> C` line topology without public HTTP relay or public HTTP fanout.
- Offline hosts receive only the sender's current latest sync-eligible visible
  state when connectivity returns; no history is replayed.
- Concealed clips remain local-only and may intentionally leave other hosts on
  the previous visible clip until a newer visible clip is copied or received.
- Transport-current corruption fails closed with copyable recovery guidance and
  does not silently erase the ordering barrier.
- Host-global sync-key rotation succeeds end-to-end over regular SSH plus
  command-locked verification, and partial failures keep the old working key or
  show stale-key cleanup pending.
- The Mac app shows actionable SSH/key/protocol failures with copyable logs.
- README and architecture docs accurately describe the new security boundary.
