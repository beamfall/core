#!/usr/bin/env bash
# Doc-link hermeticity gate: catches docs that cite repository paths which no
# longer exist.
#
# Walks current onboarding, architecture, API, glossary, design, operations
# and security docs for two shapes of repo-path reference.
#   * markdown links `[text](path)`      — resolved relative to the doc's own
#     directory (CommonMark semantics; matches how docs/architecture/*.md
#     already link `../adr/...`).
#   * backtick-quoted code spans `` `path/like/this` `` — resolved relative to
#     the repo root (matches how these docs actually cite tree paths, e.g.
#     the table row that named `internal/ffmpeg/ffmpeg.pin`). Only spans
#     containing a `/` whose first segment is a real top-level repo dir are
#     considered, so Go stdlib imports (`os/exec`, `encoding/json`) and module
#     paths (`github.com/beamfall/core`) are never candidates.
#
# A candidate is skipped, not flagged, when it: is a URL/mailto/absolute
# filesystem path; is a pure same-doc anchor (`#foo`); resolves outside the
# repo root (`../DOCS.md`, `../beamfall-design-system` — cross-repo workspace
# references this repo-scoped gate cannot verify, and which legitimately
# don't exist inside an isolated worktree); or is `docs/adr/NNNN` shorthand
# (bare ADR number, no filename — a real citation convention in these docs)
# matching an existing `docs/adr/NNNN-*.md`.
#
# Two allowlist mechanisms for known, reviewed non-existent paths:
#   * a line containing the literal `doc-links-allow` is exempt entirely
#     (new false positives an author hits later — put it in an HTML comment
#     on that line);
#   * KNOWN_GAPS below, for the paths already audited during this gate's
#     introduction (2026-07-07 contributor-readiness follow-up) — each is
#     documented "designed, NOT YET implemented" future path or a gitignored
#     derived cache absent from a fresh clone. Allowlisted misses still print,
#     just don't fail the gate, so intentional future topology stays visible.
set -euo pipefail

ROOT="${BEAMFALL_GATE_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"

default_targets() {
  printf '%s\n' \
    "$ROOT/README.md" \
    "$ROOT/CONTRIBUTING.md" \
    "$ROOT/AGENTS.md" \
    "$ROOT/CONTEXT.md" \
    "$ROOT/docs/README.md" \
    "$ROOT/docs/design-system.md"
  for dir in architecture api glossary design operations security/checklists security/privacy security/runbooks; do
    for f in "$ROOT/docs/$dir"/*.md; do
      [ -e "$f" ] && printf '%s\n' "$f"
    done
  done
}

targets=()
report_all=0
if [ "${1:-}" = "--report-all" ]; then
  report_all=1
  shift
  while IFS= read -r f; do
    targets+=("$f")
  done < <(find "$ROOT/docs" -type f -name '*.md' -print | sort)
elif [ "$#" -gt 0 ]; then
  targets=("$@")
else
  while IFS= read -r f; do
    targets+=("$f")
  done < <(default_targets)
fi

python3 - "$ROOT" "$report_all" "${targets[@]}" <<'PY'
from collections import Counter
import glob
import os
import re
import sys

root = os.path.normpath(sys.argv[1])
report_all = sys.argv[2] == "1"
targets = sys.argv[3:]

TOP_LEVEL_DIRS = {
    "artifacts", "cla", "cmd", "contracts", "docs", "internal",
    "kotlinx", "packaging", "script", "testing", "tools", "ui-catalog",
}

# Known non-existent paths audited when this gate was introduced. Each is a
# path ARCHITECTURE.md/TESTING.md explicitly marks "designed, NOT YET
# implemented" (V-14/V-16 FFmpeg pin+fixture-corpus cluster; the "notably
# absent, on purpose" package-map row), or a gitignored derived cache absent
# from a fresh clone.
KNOWN_GAPS = {
    "internal/ffmpeg/fixturespec/corpus.go": "fixture-corpus cluster, NOT YET implemented",
    "internal/webhooks": "package-map row: intentionally absent",
    "internal/search": "package-map row: intentionally absent (FTS lives in internal/store)",
    "internal/util": "package-map row: intentionally absent",
    "internal/model": "package-map row: intentionally absent",
    "internal/serving": "package-map row: intentionally absent (superseded by internal/reveal)",
}

LINK_RE = re.compile(r'\]\(([^)]+)\)')
CODE_RE = re.compile(r'`([A-Za-z0-9_.][A-Za-z0-9_./-]*)`')
URL_RE = re.compile(r'^[a-zA-Z][a-zA-Z0-9+.-]*://')
ADR_SHORTHAND_RE = re.compile(r'^docs/adr/(\d{4})$')

findings = []
allowlisted = []
external = []


def check(raw, base_dir, rel_file, lineno, kind):
    candidate = raw.split(" ", 1)[0].split("#", 1)[0].strip()
    if not candidate:
        return  # pure same-doc anchor
    if URL_RE.match(candidate) or candidate.startswith("mailto:"):
        return
    if os.path.isabs(candidate):
        return  # not repo-relative (e.g. /Users/..., /tmp/...)
    if kind == "code":
        first_seg = candidate.split("/", 1)[0]
        if first_seg not in TOP_LEVEL_DIRS:
            return
    resolved = os.path.normpath(os.path.join(base_dir, candidate))
    if resolved != root and not resolved.startswith(root + os.sep):
        if report_all:
            external.append((rel_file, lineno, raw, kind))
        return  # escapes the repo root — cross-repo reference, unverifiable here
    if os.path.exists(resolved):
        return
    adr_m = ADR_SHORTHAND_RE.match(candidate)
    if adr_m and glob.glob(os.path.join(root, "docs", "adr", adr_m.group(1) + "-*.md")):
        return  # bare-ADR-number citation convention, resolved by prefix
    if candidate in KNOWN_GAPS:
        allowlisted.append((rel_file, lineno, raw, kind, KNOWN_GAPS[candidate]))
        return
    findings.append((rel_file, lineno, raw, kind))


for path in targets:
    if not os.path.isfile(path):
        continue
    doc_dir = os.path.dirname(path)
    rel_file = os.path.relpath(path, root)
    with open(path, encoding="utf-8") as fh:
        for lineno, line in enumerate(fh, start=1):
            if "doc-links-allow" in line:
                continue
            for m in LINK_RE.finditer(line):
                check(m.group(1), doc_dir, rel_file, lineno, "link")
            for m in CODE_RE.finditer(line):
                text = m.group(1)
                if "/" not in text:
                    continue
                check(text, root, rel_file, lineno, "code")

def category(rel_file):
    parts = rel_file.split("/")
    if parts[:3] == ["docs", "architecture", "drafts"]:
        return "docs/architecture/drafts"
    if parts[:3] == ["docs", "plans", "roadmap"]:
        return "docs/plans/roadmap"
    return "/".join(parts[:2]) if len(parts) > 1 else parts[0]


if report_all:
    finding_counts = Counter(category(row[0]) for row in findings)
    external_counts = Counter(category(row[0]) for row in external)
    print(f"doc-links report: {len(findings)} dangling candidate(s), {len(external)} cross-repo reference(s), {len(allowlisted)} allowlisted future path(s)")
    for name, count in sorted(finding_counts.items()):
        print(f"dangling\t{name}\t{count}")
    for name, count in sorted(external_counts.items()):
        print(f"cross-repo\t{name}\t{count}")
    for rel_file, lineno, raw, kind in findings[:10]:
        print(f"sample\t{rel_file}:{lineno}\t{kind}\t{raw}")
    sys.exit(0)

if allowlisted:
    for rel_file, lineno, raw, kind, reason in allowlisted:
        print(f"check-doc-links: {rel_file}:{lineno} known gap (allowlisted) {kind} reference {raw}: {reason}", file=sys.stderr)

if findings:
    for rel_file, lineno, raw, kind in findings:
        print(f"check-doc-links: {rel_file}:{lineno} dangling {kind} reference: {raw}", file=sys.stderr)
    print(f"==> check-doc-links gate FAILED: {len(findings)} dangling repo-relative reference(s)", file=sys.stderr)
    sys.exit(1)

print(f"check-doc-links gate: clean ({len(allowlisted)} known gap(s) allowlisted)")
PY
