// Package integrationhost is the server-side runtime for third-party
// Integration Plugin workers. It is domain-agnostic: it names
// nothing about any media domain (invariants #1/#2/#3) and needs no vocabulary
// exemption.
//
// A third-party Integration Plugin ships as an out-of-process Integration
// Worker Artifact — a native binary for a declared OS/architecture target. The
// server is the runtime authority: it installs and verifies the artifact by
// hash and/or signing key, launches it supervised and out-of-process, connects
// to it ONLY through a narrow local Integration Host API over a unix-domain
// socket, enforces the closed permission vocabulary at method granularity,
// stores plugin-scoped secrets in the 0600 secret store, runs server-owned
// Plugin Lifecycle Actions, and audits high-risk grants, updates, dead-letter
// retries, and file-mutation requests.
//
// The worker receives no database handle, no public admin token, no arbitrary
// GraphQL access, and no raw filesystem access. The only channel is the Host
// API, whose every method is gated by the plugin's APPROVED permission grant.
//
// What this package deliberately reuses rather than rebuilds:
//
//   - The declarative Plugin Action Descriptors, permission vocabulary
//     (plugin.PermissionKinds), risk levels, and Control Policies all live in
//     internal/plugin (plugin.Bundle) and are surfaced to Manager/Console by
//     internal/graph with no downloaded plugin UI. This package adds the
//     RUNTIME, not the descriptor model.
//   - Plugin-scoped secrets reuse the composition root's 0600 secret store via
//     the SecretStore seam (namespaced per plugin).
//   - Audit records reuse store.AppendActivity with the closed Activity*
//     constants this ticket adds.
//   - Reveal-filtered catalog reads and event-outbox tails reuse the existing
//     store reads under a store.LibraryFilter resolved from the plugin's grant
//     (the auth.Scope resolution in internal/auth is the reference for the
//     filter shape, not a dependency — this package does not import auth).
package integrationhost
