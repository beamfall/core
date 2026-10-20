#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LINT="$ROOT/tools/gate/check-doc-links.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# The real current-doc set must stay clean (known gaps allowlisted, no new
# dangling references). Historical artifacts, ADR bodies, drafts, reviews, and
# plan bodies are intentionally outside this gate.
"$LINT" >/dev/null

# Default discovery must include durable operator docs, not only onboarding and
# architecture pages.
mkdir -p "$TMP/docs/operations"
cat >"$TMP/docs/operations/broken.md" <<'MD'
See [missing](nonexistent-runbook.md).
MD
if BEAMFALL_GATE_ROOT="$TMP" "$LINT" >/tmp/check-doc-links-default.out 2>/tmp/check-doc-links-default.err; then
  echo "expected default current-doc discovery to include docs/operations" >&2
  exit 1
fi
grep -q 'docs/operations/broken.md' /tmp/check-doc-links-default.err

# Historical/future debt is reported compactly without blocking the current-doc
# gate or dumping every stale path.
report="$(BEAMFALL_GATE_ROOT="$TMP" "$LINT" --report-all)"
grep -q '^doc-links report: 1 dangling candidate(s)' <<<"$report"
grep -q $'^dangling\tdocs/operations\t1$' <<<"$report"
grep -q $'^sample\tdocs/operations/broken.md:' <<<"$report"
rm "$TMP/docs/operations/broken.md"

# A dangling markdown link (relative to the doc's own directory) must fail.
mkdir -p "$TMP/docs"
cat >"$TMP/docs/dangling-link.md" <<'MD'
See [missing](nonexistent-sibling.md) for details.
MD
if BEAMFALL_GATE_ROOT="$TMP" "$LINT" "$TMP/docs/dangling-link.md" >/tmp/check-doc-links-1.out 2>/tmp/check-doc-links-1.err; then
  echo "expected dangling markdown link to fail" >&2
  exit 1
fi
grep -q 'dangling link reference: nonexistent-sibling.md' /tmp/check-doc-links-1.err

# A dangling backtick-quoted repo path (top-level dir prefix) must fail.
mkdir -p "$TMP/internal"
cat >"$TMP/docs/dangling-code.md" <<'MD'
The pin lives at `internal/nonexistent/thing.go`.
MD
if BEAMFALL_GATE_ROOT="$TMP" "$LINT" "$TMP/docs/dangling-code.md" >/tmp/check-doc-links-2.out 2>/tmp/check-doc-links-2.err; then
  echo "expected dangling code reference to fail" >&2
  exit 1
fi
grep -q 'dangling code reference: internal/nonexistent/thing.go' /tmp/check-doc-links-2.err

# A backtick span that isn't a repo path (Go stdlib import, no top-level-dir
# prefix) must be skipped, not flagged.
cat >"$TMP/docs/stdlib-import.md" <<'MD'
Uses `os/exec` and `encoding/json` internally.
MD
BEAMFALL_GATE_ROOT="$TMP" "$LINT" "$TMP/docs/stdlib-import.md" >/dev/null

# A link that resolves outside the repo root (cross-repo workspace
# reference, e.g. AGENTS.md's real `../DOCS.md`) must be skipped, not
# flagged. The doc must sit at the fake root for `..` to actually escape it.
cat >"$TMP/cross-repo.md" <<'MD'
See [governance](../DOCS.md) for details.
MD
BEAMFALL_GATE_ROOT="$TMP" "$LINT" "$TMP/cross-repo.md" >/dev/null

# The inline allowlist marker must suppress a real dangling reference.
cat >"$TMP/docs/allowed.md" <<'MD'
The pin lives at `internal/nonexistent/thing.go`. <!-- doc-links-allow: intentional fixture -->
MD
BEAMFALL_GATE_ROOT="$TMP" "$LINT" "$TMP/docs/allowed.md" >/dev/null

# A bare docs/adr/NNNN shorthand citation (no filename) resolves against any
# docs/adr/NNNN-*.md and must not be flagged.
mkdir -p "$TMP/docs/adr"
: >"$TMP/docs/adr/0042-some-decision.md"
cat >"$TMP/docs/adr-shorthand.md" <<'MD'
Per `docs/adr/0042` this is fine.
MD
BEAMFALL_GATE_ROOT="$TMP" "$LINT" "$TMP/docs/adr-shorthand.md" >/dev/null

# ...but a shorthand citation to a genuinely nonexistent ADR number still fails.
cat >"$TMP/docs/adr-shorthand-missing.md" <<'MD'
Per `docs/adr/9999` this is fine.
MD
if BEAMFALL_GATE_ROOT="$TMP" "$LINT" "$TMP/docs/adr-shorthand-missing.md" >/tmp/check-doc-links-3.out 2>/tmp/check-doc-links-3.err; then
  echo "expected unresolved ADR shorthand to fail" >&2
  exit 1
fi
grep -q 'dangling code reference: docs/adr/9999' /tmp/check-doc-links-3.err

echo "check-doc-links_test: OK"
