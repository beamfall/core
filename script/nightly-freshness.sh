#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVIDENCE_DIR="${RELEASE_NIGHTLY_EVIDENCE_DIR:-$ROOT/.agent-evidence/nightly-last-green}"
SOURCE_REPO="${NIGHTLY_FRESHNESS_SOURCE_REPO:-$ROOT}"
MAX_AGE_HOURS="${NIGHTLY_FRESHNESS_MAX_HOURS:-24}"
TIERS="${NIGHTLY_FRESHNESS_TIERS:-fuzz-nightly mutation-nightly race-nightly ui-catalog-nightly vuln-nightly web-compat-nightly}"
WEB_UI_E2E_MANIFEST="$ROOT/internal/web/app/e2e/lib/required-visual-parity-states.mjs"

usage() {
  cat >&2 <<'USAGE'
usage:
  script/nightly-freshness.sh record --tier NAME [--status green|red] [--repo REPO] [--provenance FILE]
  script/nightly-freshness.sh check [--tier NAME ...]
  script/nightly-freshness.sh red-contract --tier NAME [--repo REPO] [--ticket ID]
  script/nightly-freshness.sh workflow-policy [--workflow FILE]
USAGE
}

web_ui_e2e_manifest_json() {
  [ -f "$WEB_UI_E2E_MANIFEST" ] || {
    echo "web-ui-e2e manifest missing: $WEB_UI_E2E_MANIFEST" >&2
    return 1
  }
  node --input-type=module - "$WEB_UI_E2E_MANIFEST" <<'NODE'
import { pathToFileURL } from "node:url";

const manifestPath = process.argv[2];
const manifest = await import(pathToFileURL(manifestPath).href);
const states = manifest.REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS;
if (!Array.isArray(states)) {
  throw new Error("web-ui-e2e manifest does not export REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS");
}
process.stdout.write(JSON.stringify(states));
NODE
}

record() {
  local tier="" status="green" repo="beamfall" provenance=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --tier) tier="$2"; shift 2 ;;
      --status) status="$2"; shift 2 ;;
      --repo) repo="$2"; shift 2 ;;
      --provenance) provenance="$2"; shift 2 ;;
      *) usage; exit 2 ;;
    esac
  done
  [ -n "$tier" ] || { usage; exit 2; }
  case "$status" in green|red) ;; *) usage; exit 2 ;; esac
  if [ "$status" = "green" ]; then
    case "$tier" in
      web-ui-e2e-smoke)
        echo "FAIL web-ui-e2e-smoke public green record disabled; script/web-ui-e2e-smoke.sh owns full-inventory publication" >&2
        exit 2
        ;;
      web-compat-nightly)
        echo "FAIL web-compat-nightly public green record disabled; script/web-compat-nightly.sh owns executed-test publication" >&2
        exit 2
        ;;
    esac
  fi
  [ -z "$provenance" ] || [ -f "$provenance" ] || {
    echo "provenance file missing: $provenance" >&2
    exit 2
  }
  mkdir -p "$EVIDENCE_DIR"
  sha="$(git -C "$SOURCE_REPO" rev-parse HEAD 2>/dev/null || echo unknown)"
  ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  python3 - "$EVIDENCE_DIR/$tier.json" "$sha" "$repo" "$status" "$tier" "$ts" "$provenance" <<'PY'
import json
import os
import sys
import tempfile

path, commit, repo, status, tier, timestamp, provenance_path = sys.argv[1:]
payload = {
    "commit": commit,
    "sourceCommit": commit,
    "repo": repo,
    "status": status,
    "tier": tier,
    "timestamp": timestamp,
}
if provenance_path:
    with open(provenance_path, encoding="utf-8") as source:
        provenance = json.load(source)
    if not isinstance(provenance, dict):
        raise SystemExit("provenance must be a JSON object")
    payload["provenance"] = provenance

directory = os.path.dirname(path)
fd, temporary = tempfile.mkstemp(dir=directory, prefix=f".{tier}.", suffix=".tmp")
try:
    with os.fdopen(fd, "w", encoding="utf-8") as destination:
        json.dump(payload, destination, indent=2)
        destination.write("\n")
    os.replace(temporary, path)
except BaseException:
    try:
        os.unlink(temporary)
    except FileNotFoundError:
        pass
    raise
PY
  echo "$EVIDENCE_DIR/$tier.json"
}

download_artifacts() {
  [ -n "${GITHUB_ACTIONS:-}" ] || return 0
  [ -n "${GITHUB_REPOSITORY:-}" ] || return 0
  command -v gh >/dev/null 2>&1 || return 0
  mkdir -p "$EVIDENCE_DIR"
  for tier in $TIERS; do
    [ -f "$EVIDENCE_DIR/$tier.json" ] && continue
    run_id="$(gh run list --repo "$GITHUB_REPOSITORY" --workflow "$tier" --status completed --limit 1 --json databaseId -q '.[0].databaseId' 2>/dev/null || true)"
    [ -n "$run_id" ] || continue
    gh run download "$run_id" --repo "$GITHUB_REPOSITORY" --name "nightly-last-green-$tier" --dir "$EVIDENCE_DIR" >/dev/null 2>&1 || true
  done
}

check() {
  local tiers=() web_ui_manifest=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --tier) tiers+=("$2"); shift 2 ;;
      *) usage; exit 2 ;;
    esac
  done
  [ "${#tiers[@]}" -gt 0 ] || read -r -a tiers <<<"$TIERS"
  download_artifacts
  for tier in "${tiers[@]}"; do
    if [ "$tier" = "web-ui-e2e-smoke" ]; then
      web_ui_manifest="$(web_ui_e2e_manifest_json)"
      break
    fi
  done
  python3 - "$EVIDENCE_DIR" "$MAX_AGE_HOURS" "$SOURCE_REPO" "$web_ui_manifest" "${tiers[@]}" <<'PY'
import datetime as dt
import hashlib
import json
import os
import re
import subprocess
import sys

root = sys.argv[1]
max_age = dt.timedelta(hours=int(sys.argv[2]))
source_repo = sys.argv[3]
web_ui_manifest_json = sys.argv[4]
max_future = dt.timedelta(minutes=5)
tiers = sys.argv[5:]
now = dt.datetime.now(dt.timezone.utc)
failed = False
try:
    current_head = subprocess.check_output(
        ["git", "-C", source_repo, "rev-parse", "HEAD"],
        stderr=subprocess.DEVNULL,
        text=True,
    ).strip()
except (OSError, subprocess.CalledProcessError) as exc:
    print(f"FAIL nightly-freshness current_head_unavailable repo={source_repo} error={exc}")
    raise SystemExit(1)

for tier in tiers:
    path = os.path.join(root, f"{tier}.json")
    if not os.path.isfile(path):
        print(f"FAIL {tier} missing evidence={path}")
        failed = True
        continue
    try:
        data = json.load(open(path, encoding="utf-8"))
        stamp = dt.datetime.fromisoformat(str(data["timestamp"]).replace("Z", "+00:00"))
        if stamp.utcoffset() is None:
            raise ValueError("timestamp lacks timezone")
    except Exception as exc:
        print(f"FAIL {tier} invalid evidence={path} error={exc}")
        failed = True
        continue
    if data.get("tier") != tier:
        print(f"FAIL {tier} tier={data.get('tier')} evidence={path}")
        failed = True
        continue
    if data.get("repo") != "beamfall":
        print(f"FAIL {tier} repo={data.get('repo')} evidence={path}")
        failed = True
        continue
    commit = str(data.get("commit", ""))
    if re.fullmatch(r"[0-9a-fA-F]{40}", commit) is None:
        print(f"FAIL {tier} commit={commit or 'missing'} evidence={path}")
        failed = True
        continue
    source_commit = str(data.get("sourceCommit", ""))
    if re.fullmatch(r"[0-9a-fA-F]{40}", source_commit) is None:
        print(f"FAIL {tier} source_commit={source_commit or 'missing'} evidence={path}")
        failed = True
        continue
    commit_exists = subprocess.run(
        ["git", "-C", source_repo, "cat-file", "-e", f"{commit}^{{commit}}"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    ).returncode == 0
    if not commit_exists:
        print(f"FAIL {tier} commit_not_found={commit} evidence={path}")
        failed = True
        continue
    source_exists = subprocess.run(
        ["git", "-C", source_repo, "cat-file", "-e", f"{source_commit}^{{commit}}"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    ).returncode == 0
    if not source_exists:
        print(f"FAIL {tier} source_not_found={source_commit} evidence={path}")
        failed = True
        continue
    if source_commit.lower() != current_head.lower():
        print(
            f"FAIL {tier} stale_source source_commit={source_commit} "
            f"current_head={current_head} evidence={path}"
        )
        failed = True
        continue
    is_ancestor = subprocess.run(
        ["git", "-C", source_repo, "merge-base", "--is-ancestor", commit, source_commit],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    ).returncode == 0
    if not is_ancestor:
        print(f"FAIL {tier} wrong_source commit={commit} source_commit={source_commit} evidence={path}")
        failed = True
        continue
    if tier == "web-ui-e2e-smoke" and data.get("status") == "green":
        try:
            required_ids = json.loads(web_ui_manifest_json)
        except (TypeError, ValueError) as exc:
            print(f"FAIL {tier} invalid_web_ui_e2e_manifest error={exc} evidence={path}")
            failed = True
            continue
        provenance = data.get("provenance")
        states = provenance.get("states") if isinstance(provenance, dict) else None
        canonical_states = [{"id": state_id, "status": "passed"} for state_id in required_ids]
        canonical_digest = hashlib.sha256(
            json.dumps(required_ids, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        ).hexdigest()
        valid_manifest = (
            isinstance(required_ids, list)
            and len(required_ids) == 33
            and len(set(required_ids)) == 33
            and all(isinstance(state_id, str) and state_id for state_id in required_ids)
        )
        valid_provenance = (
            valid_manifest
            and isinstance(provenance, dict)
            and set(provenance) == {
                "schemaVersion",
                "manifestSHA256",
                "requiredCount",
                "passedCount",
                "states",
            }
            and type(provenance.get("schemaVersion")) is int
            and provenance.get("schemaVersion") == 1
            and provenance.get("manifestSHA256") == canonical_digest
            and type(provenance.get("requiredCount")) is int
            and provenance.get("requiredCount") == 33
            and type(provenance.get("passedCount")) is int
            and provenance.get("passedCount") == 33
            and states == canonical_states
            and all(
                isinstance(state, dict) and set(state) == {"id", "status"}
                for state in states or []
            )
        )
        if not valid_provenance:
            print(f"FAIL {tier} invalid_web_ui_e2e_provenance evidence={path}")
            failed = True
            continue
    if tier == "web-compat-nightly":
        provenance = data.get("provenance")
        browsers = provenance.get("browsers") if isinstance(provenance, dict) else None
        browser_names = (
            {browser.get("name") for browser in browsers if isinstance(browser, dict)}
            if isinstance(browsers, list)
            else set()
        )
        valid_browsers = (
            isinstance(browsers, list)
            and len(browsers) == 2
            and browser_names == {"firefox", "webkit"}
            and all(
                isinstance(browser, dict)
                and browser.get("revision")
                and browser.get("browserVersion")
                for browser in browsers
            )
        )
        host = provenance.get("host") if isinstance(provenance, dict) else None
        valid_host = isinstance(host, dict) and all(host.get(key) for key in ("os", "release", "arch"))
        valid_provenance = (
            isinstance(provenance, dict)
            and valid_host
            and provenance.get("playwrightVersion")
            and provenance.get("source") == "package-lock.json + playwright-core/browsers.json"
            and valid_browsers
            and type(provenance.get("executedTestCount")) is int
            and provenance.get("executedTestCount") > 0
        )
        if not valid_provenance:
            print(f"FAIL {tier} invalid_web_compat_provenance evidence={path}")
            failed = True
            continue
    status = data.get("status")
    age = now - stamp
    if status != "green":
        print(f"FAIL {tier} status={status} evidence={path}")
        failed = True
        continue
    if age > max_age:
        print(f"FAIL {tier} stale age_hours={age.total_seconds()/3600:.1f} evidence={path}")
        failed = True
        continue
    if age < -max_future:
        print(f"FAIL {tier} future age_hours={age.total_seconds()/3600:.1f} evidence={path}")
        failed = True
        continue
    print(f"PASS {tier} age_hours={age.total_seconds()/3600:.1f} commit={commit}")

raise SystemExit(1 if failed else 0)
PY
}

red_contract() {
  local tier="" repo="beamfall" ticket=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --tier) tier="$2"; shift 2 ;;
      --repo) repo="$2"; shift 2 ;;
      --ticket) ticket="$2"; shift 2 ;;
      *) usage; exit 2 ;;
    esac
  done
  [ -n "$tier" ] || { usage; exit 2; }
  ticket="${ticket:-NIGHTLY-$(date -u +%Y%m%d%H%M%S)}"
  mkdir -p "$ROOT/.roadmap/fanout-pauses"
  printf 'tier=%s\nrepo=%s\nticket=%s\ncreated=%s\n' "$tier" "$repo" "$ticket" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$ROOT/.roadmap/fanout-pauses/$repo"
  record --tier "$tier" --status red --repo "$repo" >/dev/null
  block="$(mktemp)"
  cat > "$block" <<EOF
- [ ] **$ticket — Triage red contract-bearing nightly $tier.** Auto-filed because the contract-bearing nightly tier is red; fanout merges for **Repo** $repo are paused until this is triaged. **Primary Repo** $repo · **Repo** $repo · **Size** S · **Review** auto · **Launch-blocking** yes · **Depends** none.
  - **Do:** inspect the failed $tier run, fix or explicitly waive the contract regression, then remove .roadmap/fanout-pauses/$repo.
  - **Files:** failing tier evidence, affected contract paths.
  - **Verify:** $tier publishes a fresh green last-green evidence record.
EOF
  cat "$block"
  rm -f "$block"
}

# Least-privilege policy for the UI nightly workflow
# (SECAUDIT-20260817-UI-NIGHTLY-LEAST-PRIVILEGE-1). Fail-closed structural
# checks: dependency/browser jobs must run with contents: read and
# persist-credentials: false; exactly one isolated publisher job may hold
# contents: write, reachable only from scheduled or default-branch runs, and
# no test artifact may cross into it.
workflow_policy() {
  local workflow="$ROOT/.github/workflows/ui-catalog-nightly.yml"
  while [ $# -gt 0 ]; do
    case "$1" in
      --workflow) workflow="$2"; shift 2 ;;
      *) usage; exit 2 ;;
    esac
  done
  [ -f "$workflow" ] || {
    echo "FAIL workflow-policy workflow file missing: $workflow" >&2
    exit 1
  }
  python3 - "$workflow" <<'PY'
import re
import sys

path = sys.argv[1]
KEY_RE = re.compile(r"^([A-Za-z0-9_.\-]+):(?:\s+(.*))?$")


def strip_comment(line):
    out = []
    quote = None
    for idx, char in enumerate(line):
        if quote:
            if char == quote:
                quote = None
            out.append(char)
            continue
        if char in "'\"":
            quote = char
            out.append(char)
            continue
        if char == "#" and (idx == 0 or line[idx - 1] in " \t"):
            break
        out.append(char)
    return "".join(out).rstrip()


raw_lines = open(path, encoding="utf-8").read().splitlines()
tokens = []
for line in raw_lines:
    stripped = strip_comment(line)
    if not stripped.strip():
        continue
    indent = len(stripped) - len(stripped.lstrip(" "))
    tokens.append([indent, stripped.strip()])


def unquote(value):
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
        return value[1:-1]
    return value


def parse_value(index, parent_indent):
    if index >= len(tokens) or tokens[index][0] <= parent_indent:
        return None, index
    if tokens[index][1].startswith("- "):
        return parse_list(index, tokens[index][0])
    return parse_map(index, tokens[index][0])


def parse_map(index, indent):
    result = {}
    while (
        index < len(tokens)
        and tokens[index][0] == indent
        and not tokens[index][1].startswith("- ")
    ):
        match = KEY_RE.match(tokens[index][1])
        if not match:
            raise ValueError(f"unparseable line: {tokens[index][1]!r}")
        key, inline = match.group(1), match.group(2)
        index += 1
        if inline is None or inline == "":
            result[key], index = parse_value(index, indent)
        elif inline in ("|", "|-", "|+", ">", ">-", ">+"):
            body = []
            while index < len(tokens) and tokens[index][0] > indent:
                body.append(tokens[index][1])
                index += 1
            result[key] = "\n".join(body)
        else:
            result[key] = unquote(inline)
    if index < len(tokens) and tokens[index][0] > indent:
        raise ValueError(f"unexpected indent at: {tokens[index][1]!r}")
    return result, index


def parse_list(index, indent):
    items = []
    while (
        index < len(tokens)
        and tokens[index][0] == indent
        and tokens[index][1].startswith("- ")
    ):
        inner = tokens[index][1][2:]
        if KEY_RE.match(inner):
            tokens[index] = [indent + 2, inner]
            item, index = parse_map(index, indent + 2)
            items.append(item)
        else:
            items.append(unquote(inner))
            index += 1
    return items, index


try:
    workflow, final = parse_map(0, 0)
    if final != len(tokens):
        raise ValueError(f"trailing unparsed content at: {tokens[final][1]!r}")
except ValueError as exc:
    print(f"FAIL workflow-policy unparseable workflow ({exc})")
    raise SystemExit(1)

failures = []


def fail(message):
    failures.append(message)


def permission_writes(perms):
    if perms is None:
        return []
    if isinstance(perms, str):
        return [perms] if "write" in perms else []
    if isinstance(perms, dict):
        return [
            f"{scope}: {level}"
            for scope, level in perms.items()
            if level not in ("read", "none")
        ]
    return ["unparseable permissions"]


top_perms = workflow.get("permissions")
if top_perms is None:
    fail("workflow-level permissions block missing (must be contents: read)")
else:
    for grant in permission_writes(top_perms):
        fail(f"workflow-level permissions grant write ({grant}); only job-level "
             "write on the isolated publisher is allowed")

jobs = workflow.get("jobs")
if not isinstance(jobs, dict) or not jobs:
    fail("no jobs block found")
    jobs = {}

writers = {}
dependents = {}
for name, job in jobs.items():
    if not isinstance(job, dict):
        fail(f"job {name} is unparseable")
        continue
    if permission_writes(job.get("permissions")):
        writers[name] = job
    else:
        dependents[name] = job


def job_steps(job):
    steps = job.get("steps")
    return steps if isinstance(steps, list) else []


def needs_list(job):
    needs = job.get("needs")
    if isinstance(needs, str):
        return [needs]
    if isinstance(needs, list):
        return [entry for entry in needs if isinstance(entry, str)]
    return []


if len(writers) != 1:
    fail(f"exactly one isolated publisher job may hold write permission, found "
         f"{len(writers)} ({', '.join(sorted(writers)) or 'none'})")
if not dependents:
    fail("no read-only dependency job found")

for name, job in dependents.items():
    for step in job_steps(job):
        if not isinstance(step, dict):
            fail(f"job {name} has an unparseable step")
            continue
        uses = step.get("uses", "")
        if isinstance(uses, str) and uses.startswith("actions/checkout@"):
            with_block = step.get("with")
            persist = with_block.get("persist-credentials") if isinstance(with_block, dict) else None
            if persist != "false":
                fail(f"job {name} checkout must set persist-credentials: false "
                     "(dependency/browser jobs never hold credentials)")

REQUIRED_GUARD = (
    "always() && needs.{dep}.result == 'failure' && "
    "(github.event_name == 'schedule' || (github.event_name == 'workflow_dispatch' "
    "&& github.ref == format('refs/heads/{{0}}', github.event.repository.default_branch)))"
)

for name, job in writers.items():
    perms = job.get("permissions")
    if perms != {"contents": "write"}:
        fail(f"publisher job {name} permissions must be exactly contents: write")
    needs = needs_list(job)
    if not needs:
        fail(f"publisher job {name} must depend on the read-only job via needs")
    for dep in needs:
        if dep in writers:
            fail(f"publisher job {name} must not depend on another write-permission job")
    condition = job.get("if")
    normalized = " ".join(condition.split()) if isinstance(condition, str) else ""
    allowed_guards = {" ".join(REQUIRED_GUARD.format(dep=dep).split()) for dep in needs}
    if not needs or normalized not in allowed_guards:
        fail(f"publisher job {name} if-guard must be exactly: "
             + REQUIRED_GUARD.format(dep=needs[0] if needs else "<dependency>")
             + " (write permission only for scheduled/default-branch runs, never "
             "arbitrary dispatch refs)")
    ran_red_contract = False
    for step in job_steps(job):
        if not isinstance(step, dict):
            fail(f"publisher job {name} has an unparseable step")
            continue
        uses = step.get("uses", "")
        if isinstance(uses, str) and uses.startswith("actions/download-artifact"):
            fail(f"publisher job {name} must not download artifacts from the "
                 "browser job (no test artifact crosses the trust boundary)")
        if isinstance(uses, str) and uses.startswith("actions/setup-node"):
            fail(f"publisher job {name} must not install node toolchains "
                 "(dependency/browser work stays in the read-only job)")
        if isinstance(uses, str) and uses.startswith("actions/checkout@"):
            with_block = step.get("with")
            ref = with_block.get("ref") if isinstance(with_block, dict) else None
            if ref != "${{ github.event.repository.default_branch }}":
                fail(f"publisher job {name} checkout must pin "
                     "ref: ${{ github.event.repository.default_branch }} "
                     "(trusted default-branch code only)")
        run_body = step.get("run", "")
        if isinstance(run_body, str):
            lowered = run_body.lower()
            if "npm " in lowered or "npx " in lowered or "playwright" in lowered:
                fail(f"publisher job {name} runs dependency/browser work; that "
                     "belongs in the read-only job")
            if "red-contract" in lowered:
                ran_red_contract = True
    if not ran_red_contract:
        fail(f"publisher job {name} must file the red contract ticket via "
             "script/nightly-freshness.sh red-contract")

if failures:
    for message in failures:
        print(f"FAIL workflow-policy {message}")
    raise SystemExit(1)
print(f"PASS workflow-policy {path}")
PY
}

cmd="${1:-}"; [ $# -gt 0 ] && shift || true
case "$cmd" in
  record) record "$@" ;;
  check) check "$@" ;;
  red-contract) red_contract "$@" ;;
  workflow-policy) workflow_policy "$@" ;;
  -h|--help) usage ;;
  *) usage; exit 2 ;;
esac
