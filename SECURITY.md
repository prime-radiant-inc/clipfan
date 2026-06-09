# Security

clipfan is designed for a fleet of hosts you already trust with one shared
clipboard. It is not a sandbox, password manager, or cross-tenant clipboard
service.

The `shared_key` is the fleet credential. Any host with that key can send
clipboard updates as a trusted peer, decrypt clipboard payloads carried over the
SSH sync stream, and use the signed local API when it can reach a local daemon.
Do not copy it to a host you would not trust with your clipboard.

## What clipfan protects

- **Clipboard payload confidentiality on the wire.** Peer clipboard bytes are
  encrypted with AES-GCM using a key derived from `shared_key` before they are
  carried over the authenticated SSH sync stream.
- **Request integrity and replay resistance.** Signed requests bind the method,
  request URI, timestamp, nonce, and body. Stale timestamps and repeated request
  nonces are rejected. Mixed-version fleets fail closed; upgrade all peers
  together.
- **Recipient binding.** Peer clip envelopes include the intended recipient in
  the encrypted payload, so a captured clip payload for one peer is rejected if
  replayed to another peer.
- **Local control endpoints.** History, config, restore, and peer-status
  endpoints require a valid signature and are loopback-only. Successful local
  control responses are also signed and bound to the request nonce, so GUI
  clients can reject a spoofed loopback listener that does not know
  `shared_key`. The unauthenticated health endpoint returns only `ok`.
- **Signed diagnostics.** `/v1/version` requires a valid signed request, returns
  a signed response, and exposes only the daemon version string.
- **Local file permissions.** Config, state, history, and image storage are kept
  under the current user's XDG config/state directories. clipfan creates and
  repairs those directories as `0700` and the files as `0600`.
- **Loopback listener boundary.** Supported generated configs bind the daemon's
  HTTP API to `127.0.0.1:7853`. Legacy generated wildcard listeners are migrated
  to loopback at daemon start. Explicit unsafe listener configs enter safe mode
  and stop peer sync until repaired.
- **User service scope.** The Linux service is a `systemd --user` unit and the
  macOS service is a LaunchAgent. The daemon is not intended to run as root.
- **Concealed pasteboard items.** Password-manager-style concealed or transient
  pasteboard items are not synced or recorded.
- **Peer timestamp bounds.** Sync envelopes are rejected if their clipboard
  timestamp is more than two minutes ahead of the receiver's clock. This
  prevents a trusted peer from poisoning local ordering state with an arbitrary
  future clip.
- **Release-time tooling.** CI builds Sparkle's `generate_appcast` from the
  pinned Sparkle revision in this repo, then checks out that exact revision
  before using the appcast signing key. Sparkle release notes are extracted from
  `CHANGELOG.md` and embedded in the signed appcast.

## What clipfan does not protect against

- **A compromised trusted peer.** A host with `shared_key` is inside the trust
  boundary. It can send clipboard contents and disrupt sync. HMAC and encryption
  protect against outsiders; they do not make an untrusted key holder safe.
- **The same Unix user on the same host.** A process running as the same user can
  read the config, state, history, image files, and process environment. clipfan
  does not try to sandbox the user's own processes from each other.
- **Root or physical access.** A root user, device owner, or someone with
  physical access to an unlocked desktop can read or change clipboard state.
- **Manual public listener exposure.** Peer HTTP sync has been removed and
  generated configs are loopback-only. Do not manually configure the daemon HTTP
  listener on a public or shared network address.
- **Traffic metadata.** Payload bytes are encrypted, but the network path can
  still see connection metadata such as peer addresses, timing, and approximate
  message sizes unless you run over an encrypted underlay such as Tailscale.
- **Large local clips.** The macOS app caps how much history text it searches and
  renders at once, but clipfan still stores clipboard history up to your
  configured history limit. A same-user process can still create large local
  clips and consume local disk or memory.

## Multi-user Linux notes

- Other Unix users should not be able to read clipfan config, history, state, or
  image files when the XDG directories are owned by the clipfan user and the
  permissions above are intact. If a backup job, shared home directory, unusual
  ACL, or admin policy makes those files readable to other users, that is outside
  clipfan's guarantees.
- Other Unix users may be able to connect to a local TCP listener, but they still
  need `shared_key` to read or change clipboard state. Without the key, the only
  intended unauthenticated endpoint is `/v1/health`.
- tmux integration verifies that the tmux socket directory is not a symlink, is
  owned by the clipfan user, and is not accessible by group or other users
  (normally `/tmp/tmux-$UID` mode `0700`). Discovered socket files must also be
  owned by the clipfan user and not group/world-writable before clipfan calls
  `tmux -S <socket> load-buffer -`.
- Local daemon identity is authenticated with signed responses, but the local
  HTTP API still uses separate loopback HTTP requests. That protects against a
  different local Unix user who cannot read `shared_key`; it is not a sandbox
  against the same user, root, or a host configuration that exposes the key.
- Deleting a history item or clearing unpinned history also removes image files
  that are no longer referenced by remaining history entries.
