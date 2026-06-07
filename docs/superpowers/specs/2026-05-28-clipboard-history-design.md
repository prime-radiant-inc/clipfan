# Clipboard History Browser — Design

**Status:** Approved (design). Ready for implementation planning.
**Date:** 2026-05-28
**Component:** clipfan daemon (Go) + Clipfan.app (SwiftUI)

## Summary

Add a clipboard **history browser** to the Clipfan macOS menubar app: a two-pane
window that lists recently-copied items (text, links, and images) with a large
preview pane, full keyboard navigation, search, type filters, pinning, and an
origin-host badge on every row. Each daemon records the clips that pass through
its own clipboard; the menubar app reads the local daemon's history over HTTP.
Picking an item re-copies it and syncs it to the fleet.

## Goals

- Browse the recent clipboard, including **multimedia** (real image previews).
- Premium, native, keyboard-first feel (Raycast/Paste-grade).
- Show where each item came from — items can originate on any fleet host.
- Reuse the existing daemon/store/transport architecture; add no new sync protocol.

## Non-goals (deferred, not in this version)

- Cross-fleet **merged** history (union of all hosts). History is local per-host.
- Auto-paste into the frontmost app via Accessibility (we re-copy; user presses ⌘V).
- Rich link cards with fetched favicons/social images.
- Paste-stack (sequential multi-item paste).
- OCR / text recognition on images.

## Architecture

History is **local per-host**. The daemon already sees every clip at two points:

- `pollOnce` (`internal/daemon/daemon.go`) — a clip copied locally.
- `onReceive` (`internal/daemon/daemon.go`) — a clip pushed by a peer.

Both already persist the current item via `store.SaveState`. We add one call,
`store.AppendHistory(content, origin)`, at each site, right after the existing
save. Because the Mac is the relay hub, its local history naturally sees ~every
clip on the fleet, each tagged with the host it originated on. No new wire
protocol is introduced.

New pieces:

- **Backend:** a `history.json` ring (newest-first, capped) reusing the existing
  `images/<sha>.png` files as thumbnails; a `GET /v1/history` endpoint; a
  `POST /v1/restore` endpoint; pin/delete/clear endpoints.
- **Frontend:** a SwiftUI two-pane history window, opened from the menubar icon
  and a configurable global hotkey.

## Data model

```
HistoryEntry {
  id          string   // sha256 hex of the content (stable identity, dedup key)
  kind        string   // "text" | "image" | "link"
  preview     string   // short display string: first ~140 chars, or image filename
  text        string   // full text payload, inline, for text/link (empty for image)
  image_path  string   // absolute path to images/<sha>.png, for image (empty otherwise)
  size_bytes  int
  dims        string   // e.g. "1440×900", images only ("" otherwise)
  origin      string   // host the clip originated on
  ts          time.Time
  pinned      bool
}
```

- Persisted as a JSON list in `$XDG_STATE_HOME/clipfan/history.json`, newest first.
- `kind == "link"` is text whose content matches a URL regex; it renders with a
  link icon. Detection is a backend concern so the API is self-describing.
- Short text is stored inline in the entry; images stay on disk by path. This
  keeps `history.json` small while letting the UI render thumbnails from disk.
- **Identity & dedup:** `id` is the content hash. Re-copying identical content
  moves the existing entry to the top (updates `ts`) rather than adding a duplicate.

## Retention / garbage collection

- Cap: **200 entries**, count-based. Configurable via a new `MaxHistory` field in
  `internal/config/config.go` (default 200).
- GC trims the oldest **unpinned** entries beyond the cap. **Pinned entries are
  exempt** from the cap.
- Image GC coupling: the existing image GC (`store.gc`, currently keeps the last
  50 images) must not delete an image still referenced by a retained or pinned
  history entry. The image-retention bound is raised to cover the history cap, and
  images referenced by pinned entries are always protected. The reference set is
  computed from `history.json` before trimming.

## HTTP API

All endpoints are HMAC-SHA256 signed with the shared key, exactly like the
existing `POST /v1/clip` (`X-Clipfan-Sig` header over the request body).

- `GET /v1/history?limit=<n>` → `{ "entries": [HistoryEntry, ...] }`
  Newest first, pinned floated to the top, capped at `limit` (default = config cap).
- `POST /v1/restore` `{ "id": "<sha>" }` → `200`
  Daemon loads that entry and makes it the current clipboard: writes the local OS
  clipboard (text → pbcopy; image → the multi-type pasteboard helper) and fanouts
  to peers so the whole fleet converges. The restored item also moves to the top
  of history.
- `POST /v1/history/pin` `{ "id": "<sha>", "pinned": <bool> }` → `200`
- `DELETE /v1/history` `{ "id": "<sha>" }` (one) or `{ "all_unpinned": true }`
  (clear) → `200`

## UI — two-pane window

`MenuBarExtra(.window)` hosting a SwiftUI view:

- **Left:** a search field pinned to the top, then a scrollable list. Each row:
  thumbnail (image) or type icon (text/link), one-line preview, an **origin-host
  badge**, and a relative timestamp.
- **Right:** a large preview pane — the full image, or the full text — with a
  metadata footer: type · size · dimensions · origin host · timestamp.

Interaction (keyboard-first):

- Type-to-filter: instant case-insensitive substring match on preview/text.
  (Fuzzy ranking is a later enhancement.)
- ↑/↓ move selection; **Enter = restore** (re-copy + sync); Esc dismisses.
- Type-filter chips: All / Text / Image / Link.
- Pin (⌘.), delete selected (⌘⌫), clear-unpinned. Pinned items float to the top.
- Opens via menubar click **and** a configurable global hotkey (default ⇧⌘V).

The Swift app fetches `GET /v1/history` (reusing `DaemonClient` + the existing
`JSONDecoder.clipfan`), renders the list, and calls `POST /v1/restore` /
pin / delete on user action. Thumbnails load from the on-disk image path,
downscaled and cached so rows don't decode full PNGs.

## Privacy

The daemon must not record concealed clips. macOS password managers mark
pasteboard items with `org.nspasteboard.ConcealedType`; the macOS clipboard
backend checks for that type and, when present, skips the `AppendHistory` call
(the item is still synced as the current clipboard if appropriate, but never
persisted to history). This keeps secrets out of `history.json`.

## Testing

- **Go (`internal/store`):** append; newest-first ordering; dedup-moves-to-top;
  count-cap GC; pinned-exempt GC; image-reference protection during image GC.
- **Go (`internal/transport`):** `GET /v1/history`, `POST /v1/restore`,
  pin, delete handlers — signed round-trips; restore writes clipboard + fanouts.
- **Go (link detection):** URL regex classification of text vs link.
- **Swift:** `HistoryEntry` decoding via `JSONDecoder.clipfan`; the
  search/filter/pinned-sort logic extracted into a testable view-model.
- **Live (fleet):** copy on a Linux host → entry appears in the Mac history with
  that host's origin badge → restore from the Mac → re-syncs to the fleet.

## Module touch list

- `internal/store/` — new `history.go` (`HistoryEntry`, `AppendHistory`,
  `LoadHistory`, `Pin`, `Delete`, history-aware image GC).
- `internal/transport/server.go` — register the new endpoints + handlers.
- `internal/daemon/daemon.go` — call `AppendHistory` in `pollOnce` and `onReceive`;
  add a `restore(id)` path that reuses the existing write + `fanout`.
- `internal/clipboard/clipboard_darwin.go` — concealed-type check feeding a
  "should record" signal.
- `internal/config/config.go` — `MaxHistory` field (default 200).
- `apps/mac/Clipfan/Sources/Clipfan/` — new `HistoryWindow.swift`,
  `HistoryViewModel.swift`, `HistoryRow.swift`, `HistoryEntry` model; extend
  `DaemonClient` with history fetch/restore/pin/delete; register the global
  hotkey; wire the window into `ClipfanApp` + the menubar.
- `docs/` — ARCHITECTURE, ROADMAP, README updated to reflect history (evergreen).
