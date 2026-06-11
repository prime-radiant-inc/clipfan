# Documentation Index

One row per doc. `Reader` is the addressed reader (`user`, `operator`,
`contributor`, `adopter`, `+`-joined for genuinely sectioned docs, `—` for
point-in-time rows). `Class` is the confirmed evergreen/point-in-time
classification of record (the classify-and-confirm gate's output). `Owns` is
machine-readable: the path globs whose facts this doc owns — `docmaint stale`
diffs them; `—` for point-in-time docs. The fenced table is machine-maintained;
edit rows, never the sentinels.

<!-- doc-index:begin -->
| Doc | What | Reader | Class | Owns |
| --- | --- | --- | --- | --- |
| `README.md` | what clipfan is, install, getting started, daily use, tmux copy integration, config, menubar app, caveats | user+adopter | evergreen | `dist/install.sh`, `dist/tmux.conf.snippet`, `dist/com.primeradiant.clipfan.plist`, `internal/config/**`, `apps/mac/Clipfan/Sources/**` |
| `SECURITY.md` | trust boundary, protections, non-goals, multi-user Linux notes | user+adopter | evergreen | `internal/transport/auth.go`, `internal/transport/crypto.go`, `internal/transport/envelope.go`, `internal/store/**`, `internal/storagecheck/**`, `internal/tmux/**`, `internal/config/listener.go`, `internal/config/listener_repair.go` |
| `docs/ARCHITECTURE.md` | module layout, SSH sync payload, HTTP API, discovery, recirculation prevention, image flow, history, XDG paths, auth model | contributor | evergreen | `cmd/**`, `internal/**`, `dist/tmux.conf.snippet`, `dist/clipfan-pasteboard-helper.swift` |
| `docs/RELEASING.md` | release pipeline: repo secrets, tagging, daemon versioning, verification, local builds | contributor | evergreen | `.github/workflows/release.yml`, `dist/build-all.sh`, `scripts/extract-release-notes.sh`, `scripts/test-ssh-release-gates.sh`, `DAEMON_VERSION`, `apps/mac/Clipfan/Info.plist` |
| `docs/TROUBLESHOOTING.md` | daemon-down and peers-not-syncing recovery steps | user | evergreen | `dist/com.primeradiant.clipfan.plist`, `dist/clipfan.service`, `apps/mac/Clipfan/Sources/Clipfan/SettingsView.swift` |
| `docs/ROADMAP.md` | what's shipped and what's planned | user+contributor | evergreen | — |
| `docs/DICTIONARY.md` | project dictionary (normative terminology) | contributor | evergreen | — |
| `docs/development/building-from-source.md` | build the daemon and menubar app from a source clone; manual host install; tests | contributor | evergreen | `dist/build-all.sh`, `dist/install.sh`, `apps/mac/**` |
| `docs/ci/clipfan-macos-ssh-fixture.md` | CI readiness bar for the real macOS OpenSSH fixture | contributor | evergreen | `.github/workflows/openssh-fixtures.yml`, `scripts/openssh-fixture-readiness.sh` |
| `CHANGELOG.md` | append-only release record (entries true as of their release) | — | point-in-time | — |
| `docs/PLAN.md` | historical build log of the original daemon implementation (banner'd) | — | point-in-time | — |
| `docs/superpowers/**` | dated design specs, plans, handoffs, and research notes (records, never rewritten) | — | point-in-time | — |
<!-- doc-index:end -->

<!-- Owns `—` = the doc owns no code surface, so `stale` never flags it. Right
for point-in-time rows, the dictionary, and ROADMAP (their freshness comes from
`scan`, releases, and full audits). -->

<!-- Excluded from doc flows: ABOUT.md is generated and foreign-owned
(maintained by the maintaining-project-map skill — do not hand-edit);
LICENSE and catalog-info.yaml are non-markdown; docs/images/ holds a capture
script, not docs. -->

<!-- Decided gaps (confirmed 2026-06-10): no CLAUDE.md (declined at portfolio
confirm); no brochure/marketing artifacts (internal proprietary tool — adopters
are teammates served by README + ABOUT.md); no docs site or screenshot tutorial
(ROADMAP polish item, no current reader); no standalone API reference
(docs/ARCHITECTURE.md owns the loopback HTTP API; it has no external
consumers). -->
