//go:build io_e2e

package ioe2e

// Filesystem io-ops NOT yet covered by an in-process apptest journey, and why.
// Per the I/O E2E integrity rule, an op earns its `// io:<id>` marker only when
// a journey genuinely drives the real code that performs the filesystem I/O and
// asserts the real on-disk effect. The job worker is now reachable via the
// App.RunPendingJobs test seam (internal/app/app.go) — which unblocked
// fs-scan-walk (see lifecycle_jobs_test.go) — but the four ops below each have a
// remaining, concrete blocker beyond running jobs. They are deliberately left
// MISSING rather than credited with a hollow test.
//
//   - fs-ingest-move (internal/ingest) — BLOCKED: the atomic os.Rename
//     (downloadEnclosure) fires only inside the subscription-poll job, which
//     performs REAL network egress to fetch the feed enclosure it then renames.
//     RunPendingJobs can now run the job, but covering this needs a subscription
//     configured against a loopback feed/enclosure double (an egress-double
//     setup like internal/ioe2e/egress_*) so the rename has a fetched file to
//     move. A worthwhile follow-up; out of this slice's scope.
//
//   - fs-transcode-output (internal/playback) — BLOCKED on one wire gap:
//     precomputePlaybackRenditions(mediaID) enqueues the segment-writing job
//     (RunPendingJobs runs it) and scan-walk now imports a Media backed by a
//     REAL file, so the transcode input exists. The remaining blocker is
//     obtaining the *discovered mediaID* over the public harness: there is no
//     simple list query — media browsing goes through the domain-agnostic
//     Source/Section `sourceitems` view, which the journey would have to
//     construct to read back the scanned Media's id and target the precompute
//     mutation. Follow-up: drive sourceitems (or add a manager media-list read)
//     to fetch the id, then precompute → RunPendingJobs → assert segments.
//
//   - fs-artwork-cache (internal/artwork) — BLOCKED: the content-addressed
//     artwork write into <DataDir>/artwork is reached via pinArtworkCandidate,
//     which first does FetchResource(candidate.SourceURL) — a REAL network fetch
//     — and needs a pre-existing artwork CANDIDATE from an identify/metadata
//     lookup. The local poster sweep does NOT generate for freshly-scanned,
//     unidentified Media (verified: <DataDir>/artwork is empty after a real scan
//     + artwork-sweep). So a real cache write needs an external image server +
//     a seeded candidate; no HTTP-only local path exists.
//
// Covered fs ops: fs-store-datadir, fs-trash-move, fs-secrets-0600,
// fs-store-backup, fs-libencrypt-atrest (lifecycle_fs_test.go) + fs-scan-walk
// (lifecycle_jobs_test.go).
