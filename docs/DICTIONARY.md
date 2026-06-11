# Project Dictionary

Normative: docs, code identifiers, commit messages, and UI strings use these
terms as defined. Divergences live in Exceptions, nowhere else. Maintained by
the superpowers-docs documentation skill; `docmaint scan` enforces the
`Use instead of:` lines mechanically (this file is excluded from its own sweep).

<!--
Entry format (parsed by docmaint — keep the grammar exact):
  ### term                 — the heading IS the canonical term/replacement
  1–2 sentence definition: what kind of thing + what distinguishes it.
  Distinct from: *neighbor* (one clause on the difference).      [optional]
  Use instead of: syn1, homograph [manual] (reason).             [optional]
    - comma-separated plain terms, no markup
    - [manual] = homograph; agents check it in audits, scan skips it.
      Applies per-term — tag each homograph individually.
    - one trailing (parenthetical) reason allowed; scan strips it
      before parsing the synonyms
Inclusion bar: project-specific, ambiguous, or non-standard usage only.
The dictionary defines; it never explains — link to the owning doc instead.
-->

## Terms

### clip
One logical clipboard item as it moves through the fleet: a random clip ID is
minted at its true origin and preserved through every relay — the identity the
mesh dedups on.
Distinct from: *history entry* (content-hash identity, local to one host).

### clip-ID dedup
The mesh-identity recirculation guard: a bounded set of recently seen clip IDs;
a clip whose ID was already seen is dropped before it is applied or relayed.
Distinct from: *echo suppression* (content identity — catches re-representations
that carry no ID).

### clipboard panel
The keyboard-driven clipboard history UI summoned with ⇧⌘V (search, type chips,
preview, pin/delete).
Distinct from: *menubar dropdown* (the menu on the menu bar icon).
Use instead of: clipboard history browser, history browser.

### clipfan
The project, and the daemon/CLI binary (`cmd/clipfan`) — lowercase, including
at sentence start. The Swift product/module and its type prefix are capitalized
`Clipfan`, the bundle is `Clipfan.app`, and ALL-CAPS `CLIPFAN_` prefixes
environment variables.
Distinct from: *Clipfan.app* (the menubar app bundle).

### concealed clip
A pasteboard item marked concealed or transient (password managers); never
synced, recorded, or relayed.

### daemon
The Go background service (`clipfan`) that polls the local clipboard, serves the
signed loopback API, and syncs with peers.
Distinct from: *menubar app* (the Swift control surface that supervises it).

### echo suppression
The content-identity recirculation guard: the daemon records the current clip
(id, kind, hash, image path) on every clipboard write or local broadcast and
suppresses a later read or inbound clip whose content matches — including an
image coming back as its store-path text.
Distinct from: *clip-ID dedup* (mesh identity).

### fleet
The set of hosts sharing one `shared_key` — the trust boundary, and the unit a
clip converges across.

### history entry
A clip recorded in a host's local `history.json`, identified by content hash
(re-copying identical content floats the existing entry instead of duplicating
it).
Distinct from: *clip* (mesh identity via clip ID).

### menu bar
The macOS UI strip at the top of the screen — the clipfan icon lives in the
menu bar. Two words when naming the OS surface.
Distinct from: *menubar app* (the app itself).

### menubar app
Clipfan.app, the macOS control surface: clipboard panel, fleet view, settings,
peer installer. One word, as the README writes it.
Use instead of: menu bar app.

### menubar dropdown
The menu shown by clicking the clipfan menu bar icon: Open Clipboard, Install
Update… (when an update is available), Settings…, Quit, and the Fleet section
with health dots.
Distinct from: *clipboard panel* (⇧⌘V).

### peer
Another host's daemon this daemon is configured to sync with (a provisioned
SSH edge).
Distinct from: *fleet* (the whole set).

### relay hub
The topology role of a host holding edges to peers that cannot see each other —
typically the Mac, which installs and keeps an edge to every peer. Any host
with an edge relays; the hub role is about edges, not special code.

### send
The ↑ direction in fleet rows and peer status: this host delivering a clip to a
peer (↓ is recv).
Use instead of: push [manual] (ordinary English elsewhere; only the
fleet-row/peer-status direction is governed).

### SSH sync stream
The persistent, authenticated SSH stream carrying newline-delimited frames of
encrypted clip envelopes between peers — the only inter-host sync transport
(peer HTTP sync was removed in 1.0.0).

## Names

<!--
Names also state exact spelling/capitalization and a location (path, command,
or upstream URL). scan flags case-variants of the canonical spelling
automatically; list spacing/hyphenation variants in Use instead of:.
-->

### Clipfan.app
The macOS menubar app bundle (`apps/mac/Clipfan`). Capitalized only as the
bundle/product name.

### clipfan-pasteboard-helper
The bundled Darwin helper that writes multi-type pasteboard items and detects
concealed/transient items (`dist/clipfan-pasteboard-helper.swift`).

### clipfan-shim
The Linux `xclip` / `wl-paste` replacement that answers clipboard queries from
clipfan state, no display server required (`cmd/clipfan-shim`).

## Exceptions

<!--
Format (parsed by docmaint):
  - `term` — `glob`[, `glob`…]; reason, tracking pointer. [temporary|permanent]
Scopes are path globs only — never prose predicates.
[temporary] needs a tracking pointer; scan reports it as a removal candidate
when the term has zero matches inside its glob-matched files (confirm via
git log -S before removing).
[permanent] is never flagged; zero current matches doesn't expire it.
-->

- `clipboard history browser` — `CHANGELOG.md`, `docs/PLAN.md`, `docs/superpowers/**`; historical records are never rewritten. [permanent]
- `history browser` — `CHANGELOG.md`, `docs/PLAN.md`, `docs/superpowers/**`; historical records are never rewritten. [permanent]

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-10 · commit `5ed989c` · verified against code._
