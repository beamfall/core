#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/nightly-freshness-test.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
source_repo="$tmp/source-repo"
git init -q -b main "$source_repo"
git -C "$source_repo" config user.name "Nightly Freshness Test"
git -C "$source_repo" config user.email "nightly-freshness@example.invalid"
git -C "$source_repo" commit -q --allow-empty -m source
source_commit="$(git -C "$source_repo" rev-parse HEAD)"
git -C "$source_repo" switch -q --orphan wrong-source
git -C "$source_repo" commit -q --allow-empty -m wrong-source
wrong_source_commit="$(git -C "$source_repo" rev-parse HEAD)"
git -C "$source_repo" switch -q main

expect_check_failure() {
  local tier="$1" pattern="$2"
  if env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" NIGHTLY_FRESHNESS_TIERS="$tier" "$ROOT/script/nightly-freshness.sh" check > "$tmp/$tier.out" 2>&1; then
    echo "FAIL $tier planted-red evidence passed" >&2
    exit 1
  fi
  grep -q "$pattern" "$tmp/$tier.out"
}

expect_web_ui_record_failure() {
  local label="$1"
  shift
  rm -f "$tmp/web-ui-e2e-smoke.json"
  if env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" \
    "$ROOT/script/nightly-freshness.sh" record --tier web-ui-e2e-smoke "$@" \
    >"$tmp/web-ui-record-$label.out" 2>&1; then
    echo "FAIL web-ui-e2e-smoke $label record passed" >&2
    exit 1
  fi
  grep -Fqx \
    'FAIL web-ui-e2e-smoke public green record disabled; script/web-ui-e2e-smoke.sh owns full-inventory publication' \
    "$tmp/web-ui-record-$label.out"
  [ ! -e "$tmp/web-ui-e2e-smoke.json" ] || {
    echo "FAIL web-ui-e2e-smoke $label record published evidence" >&2
    exit 1
  }
}

expect_web_compat_record_failure() {
  local label="$1"
  shift
  rm -f "$tmp/web-compat-nightly.json"
  if env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" \
    "$ROOT/script/nightly-freshness.sh" record --tier web-compat-nightly "$@" \
    >"$tmp/web-compat-record-$label.out" 2>&1; then
    echo "FAIL web-compat-nightly $label record passed" >&2
    exit 1
  fi
  grep -Fqx \
    'FAIL web-compat-nightly public green record disabled; script/web-compat-nightly.sh owns executed-test publication' \
    "$tmp/web-compat-record-$label.out"
  [ ! -e "$tmp/web-compat-nightly.json" ] || {
    echo "FAIL web-compat-nightly $label record published evidence" >&2
    exit 1
  }
}

web_ui_provenance="$tmp/web-ui-e2e-provenance.json"
node --input-type=module - \
  "$ROOT/internal/web/app/e2e/lib/required-visual-parity-states.mjs" \
  "$web_ui_provenance" <<'NODE'
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const manifest = await import(pathToFileURL(process.argv[2]).href);
const required = manifest.REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS;
writeFileSync(
  process.argv[3],
  JSON.stringify({
    schemaVersion: 1,
    manifestSHA256: createHash("sha256").update(JSON.stringify(required)).digest("hex"),
    requiredCount: 33,
    passedCount: 33,
    states: required.map((id) => ({ id, status: "passed" })),
  }),
);
NODE

expect_web_ui_record_failure direct
expect_web_ui_record_failure copied-valid --provenance "$web_ui_provenance"

web_compat_provenance="$tmp/web-compat-provenance.json"
python3 - "$web_compat_provenance" <<'PY'
import json
import sys

json.dump(
    {
        "host": {"os": "darwin", "release": "test", "arch": "arm64"},
        "playwrightVersion": "1.60.0",
        "source": "package-lock.json + playwright-core/browsers.json",
        "browsers": [
            {"name": "firefox", "revision": "1", "browserVersion": "1"},
            {"name": "webkit", "revision": "2", "browserVersion": "2"},
        ],
        "executedTestCount": 4,
    },
    open(sys.argv[1], "w", encoding="utf-8"),
)
PY
expect_web_compat_record_failure direct
expect_web_compat_record_failure copied-valid --provenance "$web_compat_provenance"

env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" \
  "$ROOT/script/nightly-freshness.sh" record --tier web-ui-e2e-smoke --status red >/dev/null
expect_check_failure web-ui-e2e-smoke '^FAIL web-ui-e2e-smoke status=red '

python3 - "$web_ui_provenance" "$tmp/web-ui-e2e-smoke.json" "$source_commit" <<'PY'
import datetime as dt
import json
import sys

provenance = json.load(open(sys.argv[1], encoding="utf-8"))
payload = {
    "commit": sys.argv[3],
    "sourceCommit": sys.argv[3],
    "repo": "beamfall",
    "status": "green",
    "tier": "web-ui-e2e-smoke",
    "timestamp": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "provenance": provenance,
}
json.dump(payload, open(sys.argv[2], "w", encoding="utf-8"))
PY
env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" \
  "$ROOT/script/nightly-freshness.sh" check --tier web-ui-e2e-smoke \
  | grep -q '^PASS web-ui-e2e-smoke '

cp "$tmp/web-ui-e2e-smoke.json" "$tmp/web-ui-e2e-smoke.good.json"
python3 - "$tmp/web-ui-e2e-smoke.json" <<'PY'
import json
import sys

path = sys.argv[1]
payload = json.load(open(path, encoding="utf-8"))
payload["provenance"]["states"][0]["status"] = "skipped"
json.dump(payload, open(path, "w", encoding="utf-8"))
PY
expect_check_failure web-ui-e2e-smoke '^FAIL web-ui-e2e-smoke invalid_web_ui_e2e_provenance '

cp "$tmp/web-ui-e2e-smoke.good.json" "$tmp/web-ui-e2e-smoke.json"
python3 - "$tmp/web-ui-e2e-smoke.json" <<'PY'
import json
import sys

path = sys.argv[1]
payload = json.load(open(path, encoding="utf-8"))
payload["provenance"]["states"].pop()
json.dump(payload, open(path, "w", encoding="utf-8"))
PY
expect_check_failure web-ui-e2e-smoke '^FAIL web-ui-e2e-smoke invalid_web_ui_e2e_provenance '

env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" "$ROOT/script/nightly-freshness.sh" record --tier green >/dev/null
env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" NIGHTLY_FRESHNESS_TIERS=green "$ROOT/script/nightly-freshness.sh" check | grep -q '^PASS green '

rm "$tmp/green.json"
if env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" NIGHTLY_FRESHNESS_TIERS=green "$ROOT/script/nightly-freshness.sh" check > "$tmp/missing.out" 2>&1; then
  echo "FAIL missing evidence passed" >&2
  exit 1
fi
grep -q '^FAIL green missing ' "$tmp/missing.out"

env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" "$ROOT/script/nightly-freshness.sh" record --tier red --status red >/dev/null
if env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" NIGHTLY_FRESHNESS_TIERS=red "$ROOT/script/nightly-freshness.sh" check > "$tmp/red.out" 2>&1; then
  echo "FAIL red evidence passed" >&2
  exit 1
fi
grep -q '^FAIL red status=red ' "$tmp/red.out"

python3 - "$tmp/web-compat-nightly.json" "$source_commit" <<'PY'
import datetime as dt
import json
import sys
payload = {
    "commit": sys.argv[2],
    "sourceCommit": sys.argv[2],
    "repo": "beamfall",
    "status": "green",
    "tier": "web-compat-nightly",
    "timestamp": (dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=25)).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "provenance": {
        "host": {"os": "darwin", "release": "test", "arch": "arm64"},
        "playwrightVersion": "1.60.0",
        "source": "package-lock.json + playwright-core/browsers.json",
        "executedTestCount": 4,
        "browsers": [
            {"name": "firefox", "revision": "1", "browserVersion": "1"},
            {"name": "webkit", "revision": "2", "browserVersion": "2"},
        ],
    },
}
json.dump(payload, open(sys.argv[1], "w", encoding="utf-8"))
PY
if env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" "$ROOT/script/nightly-freshness.sh" check > "$tmp/stale.out" 2>&1; then
  echo "FAIL stale evidence passed" >&2
  exit 1
fi
grep -q '^FAIL web-compat-nightly stale ' "$tmp/stale.out"

python3 - "$tmp" "$source_commit" "$wrong_source_commit" <<'PY'
import datetime as dt
import json
import os
import sys

root = sys.argv[1]
source_commit = sys.argv[2]
wrong_source_commit = sys.argv[3]
now = dt.datetime.now(dt.timezone.utc)
base = {
    "commit": source_commit,
    "sourceCommit": source_commit,
    "repo": "beamfall",
    "status": "green",
    "timestamp": now.strftime("%Y-%m-%dT%H:%M:%SZ"),
}
provenance = {
    "host": {"os": "darwin", "release": "test", "arch": "arm64"},
    "playwrightVersion": "1.60.0",
    "source": "package-lock.json + playwright-core/browsers.json",
    "executedTestCount": 4,
    "browsers": [
        {"name": "firefox", "revision": "1", "browserVersion": "1"},
        {"name": "webkit", "revision": "2", "browserVersion": "2"},
    ],
}
cases = {
    "wrong-tier": {**base, "tier": "some-other-tier"},
    "wrong-repo": {**base, "tier": "wrong-repo", "repo": "other"},
    "bad-commit": {**base, "tier": "bad-commit", "commit": ""},
    "future": {
        **base,
        "tier": "future",
        "timestamp": (now + dt.timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ"),
    },
    "fabricated": {
        **base,
        "tier": "web-compat-nightly",
        "commit": "f" * 40,
        "provenance": provenance,
    },
    "wrong-source": {
        **base,
        "tier": "web-compat-nightly",
        "commit": wrong_source_commit,
        "provenance": provenance,
    },
    "missing-provenance": {**base, "tier": "web-compat-nightly"},
    "missing-executed": {
        **base,
        "tier": "web-compat-nightly",
        "provenance": {key: value for key, value in provenance.items() if key != "executedTestCount"},
    },
    "zero-executed": {
        **base,
        "tier": "web-compat-nightly",
        "provenance": {**provenance, "executedTestCount": 0},
    },
    "bool-executed": {
        **base,
        "tier": "web-compat-nightly",
        "provenance": {**provenance, "executedTestCount": True},
    },
}
for tier, payload in cases.items():
    with open(os.path.join(root, f"{tier}.json"), "w", encoding="utf-8") as destination:
        json.dump(payload, destination)
PY
expect_check_failure wrong-tier '^FAIL wrong-tier tier=some-other-tier '
expect_check_failure wrong-repo '^FAIL wrong-repo repo=other '
expect_check_failure bad-commit '^FAIL bad-commit commit=missing '
expect_check_failure future '^FAIL future future '
mv "$tmp/fabricated.json" "$tmp/web-compat-nightly.json"
expect_check_failure web-compat-nightly '^FAIL web-compat-nightly commit_not_found='
mv "$tmp/wrong-source.json" "$tmp/web-compat-nightly.json"
expect_check_failure web-compat-nightly '^FAIL web-compat-nightly wrong_source '
mv "$tmp/missing-provenance.json" "$tmp/web-compat-nightly.json"
expect_check_failure web-compat-nightly '^FAIL web-compat-nightly invalid_web_compat_provenance '
for planted in missing-executed zero-executed bool-executed; do
  mv "$tmp/$planted.json" "$tmp/web-compat-nightly.json"
  expect_check_failure web-compat-nightly '^FAIL web-compat-nightly invalid_web_compat_provenance '
done

env RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp" NIGHTLY_FRESHNESS_SOURCE_REPO="$source_repo" "$ROOT/script/nightly-freshness.sh" record --tier head-bound >/dev/null
git -C "$source_repo" commit -q --allow-empty -m advanced-head
expect_check_failure head-bound '^FAIL head-bound stale_source source_commit='

# --- workflow-policy (SECAUDIT-20260817-UI-NIGHTLY-LEAST-PRIVILEGE-1) ---
# The committed UI nightly workflow must satisfy the least-privilege policy.
"$ROOT/script/nightly-freshness.sh" workflow-policy > "$tmp/wp-real.out" 2>&1
grep -q '^PASS workflow-policy ' "$tmp/wp-real.out"

expect_policy_failure() {
  local file="$1"
  shift
  if "$ROOT/script/nightly-freshness.sh" workflow-policy --workflow "$file" > "$tmp/wp.out" 2>&1; then
    echo "FAIL workflow-policy accepted $file" >&2
    exit 1
  fi
  local pattern
  for pattern in "$@"; do
    grep -Fq "$pattern" "$tmp/wp.out" || {
      echo "FAIL workflow-policy on $file missing: $pattern" >&2
      cat "$tmp/wp.out" >&2
      exit 1
    }
  done
}

# Pre-split shape: workflow-level write, credentials persisted into the
# browser job, no isolated publisher. Every clause must be rejected.
cat > "$tmp/wp-legacy.yml" <<'YAML'
name: ui-catalog-nightly
on:
  schedule:
    - cron: "37 6 * * *"
  workflow_dispatch:
permissions:
  contents: write
jobs:
  ui-catalog:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd # v5
      - name: browser work
        run: |
          npm ci --no-audit --no-fund
          npx playwright install --with-deps chromium
      - name: File red contract ticket
        if: failure()
        run: script/nightly-freshness.sh red-contract --tier ui-catalog-nightly --repo beamfall
YAML
expect_policy_failure "$tmp/wp-legacy.yml" \
  'workflow-level permissions grant write (contents: write)' \
  'exactly one isolated publisher job may hold write permission, found 0' \
  'job ui-catalog checkout must set persist-credentials: false'

# Job-level write on the dependency/browser job itself must be rejected.
cat > "$tmp/wp-dep-write.yml" <<'YAML'
name: ui-catalog-nightly
on:
  schedule:
    - cron: "37 6 * * *"
permissions:
  contents: read
jobs:
  ui-catalog:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd # v5
        with:
          persist-credentials: false
      - name: browser work
        run: npx playwright install --with-deps chromium
      - name: File red contract ticket
        if: failure()
        run: script/nightly-freshness.sh red-contract --tier ui-catalog-nightly --repo beamfall
YAML
expect_policy_failure "$tmp/wp-dep-write.yml" \
  'publisher job ui-catalog runs dependency/browser work' \
  'publisher job ui-catalog must depend on the read-only job via needs' \
  'no read-only dependency job found'

# A publisher reachable from arbitrary dispatch refs must be rejected.
cat > "$tmp/wp-open-dispatch.yml" <<'YAML'
name: ui-catalog-nightly
on:
  schedule:
    - cron: "37 6 * * *"
  workflow_dispatch:
permissions:
  contents: read
jobs:
  ui-catalog:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd # v5
        with:
          persist-credentials: false
  file-red-ticket:
    needs: ui-catalog
    runs-on: ubuntu-latest
    if: always() && needs.ui-catalog.result == 'failure'
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd # v5
        with:
          ref: ${{ github.event.repository.default_branch }}
      - name: File red contract ticket
        run: script/nightly-freshness.sh red-contract --tier ui-catalog-nightly --repo beamfall
YAML
expect_policy_failure "$tmp/wp-open-dispatch.yml" \
  'publisher job file-red-ticket if-guard must be exactly:'

# A test artifact crossing into the publisher must be rejected.
cat > "$tmp/wp-artifact-cross.yml" <<'YAML'
name: ui-catalog-nightly
on:
  schedule:
    - cron: "37 6 * * *"
  workflow_dispatch:
permissions:
  contents: read
jobs:
  ui-catalog:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd # v5
        with:
          persist-credentials: false
  file-red-ticket:
    needs: ui-catalog
    runs-on: ubuntu-latest
    if: always() && needs.ui-catalog.result == 'failure' && (github.event_name == 'schedule' || (github.event_name == 'workflow_dispatch' && github.ref == format('refs/heads/{0}', github.event.repository.default_branch)))
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd # v5
        with:
          ref: ${{ github.event.repository.default_branch }}
      - uses: actions/download-artifact@abcdefabcdefabcdefabcdefabcdefabcdefabcd # v4
        with:
          name: nightly-last-green-ui-catalog-nightly
      - name: File red contract ticket
        run: script/nightly-freshness.sh red-contract --tier ui-catalog-nightly --repo beamfall
YAML
expect_policy_failure "$tmp/wp-artifact-cross.yml" \
  'publisher job file-red-ticket must not download artifacts'

echo "nightly-freshness tests: passed"
