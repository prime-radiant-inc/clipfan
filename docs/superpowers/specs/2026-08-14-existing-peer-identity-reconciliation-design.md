# Existing Peer Identity Reconciliation

## Problem

When Add Peer installs Clipfan on a Mac that already has Clipfan state, the
installer intentionally preserves the existing config. The onboarding UI can
still derive a different host ID from the SSH/Tailscale name, however. The
remote sync-key sidecar is bound to the persisted Clipfan host ID, so
`ssh-ensure-sync-key` correctly rejects the guessed ID with
`sync_key_identity_mismatch` before the peer config is written.

## Decision

After the remote payload is installed, run `roster-read` over the already
trusted regular SSH connection. Use the report's non-secret `origin` as the
remote host's canonical ID when constructing the `ssh-provision-direct`
specification. The existing sync-key identity validation remains strict.

The authenticated SSH connection and pinned host key establish which machine
provided the roster report. The report's origin is the machine's persisted
Clipfan identity, which is the identity the sync-key sidecar and config use.

Reject an empty, malformed, invalid, or duplicate origin with a stable config
IO error rather than provisioning an ambiguous mesh.

## Alternatives considered

- Reset or rotate the remote sync key on mismatch: rejected because it can
  invalidate existing peer authorization and silently damage a working mesh.
- Require a manual identity-repair step: safer than auto-rotation, but it
  does not satisfy seamless onboarding for a Mac that is already enrolled.

## Verification

- Unit-test roster report decoding, including malformed and missing origins.
- Exercise onboarding with an existing remote identity that differs from the
  SSH-derived label and assert that provisioning uses the canonical origin.
- Run the complete Go and macOS test suites plus release gates before tagging
  the new release.
