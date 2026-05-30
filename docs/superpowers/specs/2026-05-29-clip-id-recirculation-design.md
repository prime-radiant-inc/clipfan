# Stable clip-ID — end-to-end recirculation prevention

## Problem

Clips can recirculate through the clipfan mesh. A clip is identified only by its
**content hash** (`Envelope.SHA256`), and loop prevention rests on three guards:

- a bounded (cap 64) `seenSet` of recent content hashes,
- fanout skipping the clip's origin on relay,
- a `lastTS` "drop older than last applied" check.

None of these tracks a clip's **identity** across changes to its bytes. When a
host re-represents a clip — most visibly an image rewritten as a file-path string
on a text-only backend, but also `pngpaste` re-encoding image bytes, or any
backend drift — the re-represented clip has a *new* hash and `origin=self`, so
every guard treats it as brand-new content and it re-enters the mesh. PRI-1920
fixed the one observed instance (image→path) semantically; this change removes the
structural cause.

## Goal

A clip is processed **exactly once per host**, regardless of how many paths it
arrives by or how its bytes morph in transit. Clips never recirculate.

## Non-goals

- Backward compatibility with ID-less daemons. The fleet is updated atomically
  (see Rollout). An envelope without an ID is dropped, not guessed.
- Persisting clip-IDs or sequence state across daemon restarts.
- Changing the HMAC auth, discovery, or history subsystems.

## Approach

Two cooperating pieces — a wire-level identity and local echo suppression — both
required, because the OS clipboard carries no metadata of its own.

### 1. Clip-ID in the envelope (mesh dedup)

`Envelope` gains `ID string`: a random 128-bit nonce (`crypto/rand`, 32 hex
chars) assigned **once**, at the true origin of a clip, and preserved verbatim
through every relay. A random nonce (not `origin+seq`) keeps IDs globally unique
without coordination and survives daemon restarts, which a reset counter would
not.

The daemon dedups by **clip-ID**: the `seenSet` holds clip-IDs, not content
hashes. A clip relayed around the mesh — arriving by multiple paths, or after its
bytes change — is recognised by its ID and processed once.

Origins that mint an ID:
- **`pollOnce`** — a genuine local user copy detected on the clipboard.
- **`clipfan copy` (`RunCopy`/`pushToDaemon`)** — a CLI/tmux/OSC injection; the
  CLI stamps the ID in the envelope it POSTs to the local daemon.

Relays (`onReceive` → `fanout`) and the transport layer (`PushAs`, the `/v1/clip`
server handler) carry the ID through unchanged.

### 2. Current-clip tracking (local echo suppression)

Clip-ID alone cannot label what `pollOnce` reads back off the clipboard — the
clipboard has no ID slot. So when the daemon **writes** a clip to the clipboard
(applying a received clip in `onReceive`, or `Restore`), it records the
**current clip**: its ID plus the signatures of every representation it wrote —
the text-byte hash, the image-byte hash, and the image-store path.

`pollOnce` then classifies each detected change:

- matches a current-clip signature (a tracked hash, or the current clip's
  image-store path) → it is an **echo** of an already-broadcast clip →
  **suppress** (do not record, do not fan out);
- otherwise → a genuine new user copy → mint a fresh ID, set it as the current
  clip, broadcast.

Suppressing (rather than re-broadcasting under the same ID) is correct because
the clip already propagated under its original ID; re-emitting it is pure noise.

This subsumes the PRI-1920 image-path guard: the image path is simply one tracked
signature of the current clip. The `store.IsImageStorePath` check is retained as
defense-in-depth — an image-store path is never broadcastable text regardless of
whether current-clip state is set — but the clip-ID mechanism is the primary
guarantee.

## Components & data flow

```
USER COPY (gui)                          CLI / tmux / osc
   │ pollOnce reads new content             │ clipfan copy: stamp Envelope.ID
   │ not an echo → mint ID                   │ POST /v1/clip
   ▼                                         ▼
 fanout(ID) ───────────────────────────►  server → onReceive(ID)
                                              │ seen.has(ID)? drop
                                              │ else: seen.add(ID)
                                              │   apply to clipboard
                                              │   record currentClip{ID, sigs, path}
                                              │   relay fanout(ID, skipOrigin)
   pollOnce reads our own write/echo  ◄───────┘
   matches currentClip sig/path → suppress
```

State added to `Daemon`:
- `seen` keyed by clip-ID (string) instead of content hash.
- `currentClip`: `{ id string; textHash, imageHash [32]byte; imagePath string }`,
  guarded by the existing `mu`.

## Rollout

The whole fleet is updated together (the daemon was just deployed fleet-wide, so
the mechanism is proven). There is **no** ID-less compatibility path: a received
envelope with an empty `ID` is logged and dropped rather than guessed, which
cannot cause recirculation. Deploy order does not matter as long as all hosts end
on the new build; a brief window where a straggler's ID-less clips are dropped is
acceptable.

## Error handling

- **Empty/missing ID on receive** → log at debug, drop the envelope.
- **`crypto/rand` failure** minting an ID → log and skip the broadcast (a clip we
  can't identify is one we won't emit); extraordinarily unlikely.
- **Current-clip not yet set** (first clip after start) → no signatures to match,
  so a read is treated as new; correct, since nothing was written by us yet.

## Testing

All via the existing daemon test harness (`fakeBackend`, `fakePusher`) and store
tests; real behaviour, no mocks of the logic under test.

- **ID dedup:** the same clip-ID delivered twice (two relay paths) is applied and
  relayed once.
- **Distinct IDs:** two genuinely different clips both propagate.
- **Echo suppression after receive:** apply a received image clip, then a
  `pollOnce` whose clipboard read is the image path (PRI-1920 scenario) → no
  broadcast; and a `pollOnce` whose read is the same image bytes we wrote
  (matching the current-clip image hash) → no broadcast. (Re-encoded image bytes
  with a different hash are not signature-matched; per Risks they degrade to one
  redundant broadcast, deduped by ID downstream — not a loop.)
- **New-copy after a received clip:** a `pollOnce` read that is *not* a
  current-clip signature mints a new ID and broadcasts.
- **ID preserved through relay:** a relayed envelope carries the original ID, not
  a new one.
- **ID-less envelope dropped:** an empty-ID envelope is not applied or relayed.
- **Wire round-trip:** `Envelope` with `ID` encodes/decodes; `PushAs` carries it.
- Existing `TestImageReceiveDoesNotEchoPath`, `TestRelayDedup`,
  `TestPollDoesNotBroadcastImageStorePath`,
  `TestReceiveImageStorePathDoesNotClobber` continue to pass (the image-path
  guard remains).

## Risks

- **`seen` capacity.** Keyed by clip-ID, the bounded set could still evict under a
  sustained burst, re-admitting a very old clip-ID. Mitigation: raise the cap
  (e.g. 256) — IDs are 32 bytes, cheap — and rely on `lastTS` to reject clips
  older than the last applied. A clip can't loop indefinitely because relays skip
  the origin and `lastTS` is monotonic.
- **Current-clip signature drift.** If a backend returns bytes that match neither
  the tracked text hash, image hash, nor image path (an unforeseen
  representation), that read is treated as new and re-broadcast under a new ID —
  one extra hop, then deduped by ID downstream. The image-path is the known case
  and is covered; other drift degrades to a single redundant broadcast, not a
  loop.

## Addendum — as-built refinements

The whole-implementation review surfaced two recirculation holes this design
under-specified. Both were fixed; `currentClip` ended up doing more than "set on
write" above:

1. **`pollOnce` adopts its own broadcast.** The design set `currentClip` only on
   the *write* paths (`onReceive`, `Restore`). But once content-hash dedup was
   removed, `pollOnce` had nothing to stop it from re-broadcasting the *same local
   clip* on every 250 ms poll. Fix: after broadcasting a local copy, `pollOnce`
   records it as the `currentClip`, so the next poll of the unchanged clipboard is
   an echo and is suppressed.

2. **`onReceive` consults `isEcho`.** The design consulted `isEcho` only in
   `pollOnce`. But the tmux `after-load-buffer` hook re-submits a received *text*
   clip through `clipfan copy` under a **fresh** clip-ID, which clip-ID dedup
   cannot catch. Fix: `onReceive` checks `isEcho` (right after the
   `IsImageStorePath` guard) and drops an inbound clip whose content matches what
   we just wrote. Dropping a content-duplicate is safe: applying identical bytes
   is a no-op and the fleet is already converged on that content.

Net rule: `currentClip` tracks the clipboard's *current logical content* — set on
every write **and** every local broadcast — and both `pollOnce` and `onReceive`
suppress anything matching it. Clip-ID dedup is the mesh-identity layer;
`currentClip`/`isEcho` is the content-echo layer. Covered by
`daemon/clipid_test.go` (`TestPollDoesNotRebroadcastSameLocalClip`,
`TestReceiveSuppressesReoriginatedText`).
