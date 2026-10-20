#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-secret-scan-test.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

fail() {
  echo "secret-scan_test: $*" >&2
  exit 1
}

python3 - "$ROOT/Makefile" "$ROOT/tools/gate/gate-entry.sh" <<'PY'
from pathlib import Path
import re
import shlex
import sys

makefile = Path(sys.argv[1]).read_text()
entry = Path(sys.argv[2]).read_text()


def gate_recipe(source: str) -> str:
    try:
        return source.split("\ngate:", 1)[1].split("\n\n", 1)[0]
    except IndexError as exc:
        raise ValueError("ordinary make gate target is missing") from exc


def validate_make_recipe(recipe: str) -> None:
    commands = [
        line[1:].strip().removeprefix("@").strip()
        for line in recipe.splitlines()
        if line.startswith("\t")
    ]
    if len(commands) != 1:
        raise ValueError(f"make gate must have one guarded recipe command: {commands}")
    words = shlex.split(commands[0])
    while words and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*=.*", words[0]):
        words.pop(0)
    if words != ["tools/gate/gate-entry.sh"]:
        raise ValueError(f"make gate must invoke only tools/gate/gate-entry.sh: {commands[0]}")


guard = 'exec python3 script/gate-fixture-guard.py -- "$0" "$@"'
coordinator = 'exec python3 tools/gate/gate_latest.py -- tools/gate/gate.sh "$@"'
guard_condition = 'if [ "${BEAMFALL_GATE_FIXTURE_GUARDED:-0}" != 1 ]; then'
preamble = [
    "set -euo pipefail",
    'cd "$(dirname "${BASH_SOURCE[0]}")/../.."',
]
prerequisites = [
    'if [ "${GATE_PREFLIGHT:-0}" != 1 ]; then',
    "make seeded-red-canary-controls",
    "make relay-vector-freshness-prove relay-vector-freshness",
    "python3 script/consumer_pin_freshness_test.py",
    "python3 script/consumer-pin-freshness.py",
    "fi",
]
canonical_entry = [*preamble, guard_condition, guard, "fi", *prerequisites, coordinator]


def executable_lines(source: str) -> list[str]:
    return [
        line.strip()
        for line in source.splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    ]


def validate_entry(source: str) -> None:
    lines = executable_lines(source)
    if lines[: len(preamble) + 3] != [*preamble, guard_condition, guard, "fi"]:
        raise ValueError("gate-entry must enter the fixture guard as its outermost command block")
    if lines != canonical_entry:
        raise ValueError(
            "gate-entry executable lines must be exactly the guarded prerequisites then the coordinator: "
            f"{lines}"
        )


recipe = gate_recipe(makefile)
validate_make_recipe(recipe)
validate_entry(entry)

invalid_recipes = {
    "stale direct coordinator": recipe.replace(
        "tools/gate/gate-entry.sh",
        "python3 tools/gate/gate_latest.py -- tools/gate/gate.sh",
        1,
    ),
    "entry only in a comment": recipe.replace("\t@", "# ", 1),
    "entry pointing at another script": recipe.replace("gate-entry.sh", "other-entry.sh", 1),
    "second recipe command": recipe + "\n\t@true",
}
for description, invalid_recipe in invalid_recipes.items():
    try:
        validate_make_recipe(invalid_recipe)
    except ValueError:
        continue
    raise SystemExit(f"{description} Makefile recipe escaped the chain check")
try:
    gate_recipe(makefile.replace("\ngate:", "\ngate-preflight-only:", 1))
except ValueError:
    pass
else:
    raise SystemExit("missing ordinary make gate target escaped the chain check")

invalid_entries = {
    "commented commands with executable bypasses": "\n".join(
        [
            "#!/usr/bin/env bash",
            *preamble,
            f"# {guard_condition}",
            f"# {guard}",
            "# fi",
            guard_condition,
            'python3 script/gate-fixture-guard.py -- "$0" "$@"',
            "fi",
            f"# {coordinator}",
            coordinator.removeprefix("exec "),
        ]
    ),
    "dead outer guard": entry.replace(guard_condition, f"if false; then\n{guard_condition}", 1),
    "non-exec coordinator": entry.replace(coordinator, coordinator.removeprefix("exec "), 1),
    "command after coordinator": entry + "\ntrue\n",
    "diverted command before coordinator": entry.replace(
        coordinator, f"exec bash tools/gate/other-gate.sh\n{coordinator}", 1
    ),
    "dropped prerequisite": entry.replace("  make seeded-red-canary-controls\n", "", 1),
    "prerequisite only in a comment": entry.replace(
        "  python3 script/consumer-pin-freshness.py", "  # python3 script/consumer-pin-freshness.py", 1
    ),
}
for description, invalid_entry in invalid_entries.items():
    try:
        validate_entry(invalid_entry)
    except ValueError:
        continue
    raise SystemExit(f"{description} escaped the executable gate-chain check")
PY
echo "secret-scan_test: RED-PROOF stale/commented/redirected Makefile recipes and dead, non-exec, diverted, trailing-command, prerequisite-dropping gate-entry chains were rejected"
echo "secret-scan_test: GREEN-PROOF make gate enters fixture-guarded canonical gate coordinator"

if ! awk '
  /^validate_gate_mode$/ { main = 1; next }
  !main { next }
  /^[[:space:]]*(fi|done|esac)([[:space:];]|$)/ { if (depth > 0) depth--; next }
  /^[[:space:]]*(if|case|for|while|until|select)([[:space:]]|$)/ { depth++; next }
  /^[[:space:]]*[[:alpha:]_][[:alnum:]_]*\(\)[[:space:]]*\{[[:space:]]*$/ { depth++; next }
  /^[[:space:]]*\}[[:space:]]*$/ { if (depth > 0) depth--; next }
  /^run_step "[^"]+" tools\/gate\/secret-scan\.py[[:space:]]*$/ && depth == 0 { registered++ }
  END { if (registered != 1) exit 1 }
' "$ROOT/tools/gate/gate.sh"; then
  fail "Core scan is no longer registered in the canonical gate"
fi
echo "secret-scan_test: GREEN-PROOF live Core scan registered unconditionally in canonical gate"

init_repo() {
  local root="$1"
  mkdir -p "$root"
  git -C "$root" init -q -b main
  git -C "$root" config user.email gate-test@example.invalid
  git -C "$root" config user.name "Gate Test"
}

WORKSPACE="$TMP/workspace"
CORE="$WORKSPACE/beamfall"
APPLE="$WORKSPACE/beamfall-apple"
ANDROID="$WORKSPACE/beamfall-android"

for repo in "$CORE" "$APPLE" "$ANDROID"; do
  init_repo "$repo"
  printf 'clean fixture\n' >"$repo/README.txt"
  git -C "$repo" add README.txt
done

APPLE_REAL="$(realpath "$APPLE")"

mkdir -p "$CORE/tools/gate" "$CORE/internal/testdata"
cp "$ROOT/tools/gate/secret-scan.py" "$CORE/tools/gate/secret-scan.py"
cp "$ROOT/tools/gate/secret-scan-allowlist.txt" "$CORE/tools/gate/secret-scan-allowlist.txt"

known_value="beamfall-fixture-bearer-token-00000001"
seeded_value="ghp_0123456789abcdefghijklmnopqrstuvwxyz"
apple_secret_digest="$(
  printf '%s' "$seeded_value" |
    python3 -c 'import hashlib, sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())'
)"
printf 'Authorization: Bearer %s\n' "$known_value" >"$CORE/internal/testdata/known-token.txt"
git -C "$CORE" add tools/gate internal/testdata/known-token.txt

clean_output="$(BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py")"
for label in beamfall beamfall-apple beamfall-android; do
  grep -q "PASS $label" <<<"$clean_output" || fail "clean scan omitted $label"
done
grep -q 'not all-tree coverage' <<<"$clean_output" ||
  fail "ordinary scan claimed more than best-effort coverage"
if grep -Fq 'complete required-tree coverage' <<<"$clean_output"; then
  fail "ordinary all-present scan overclaimed complete required-tree coverage"
fi
echo "secret-scan_test: GREEN-PROOF ordinary all-present scan omitted complete required-tree coverage claim"

apple_corpus_relative="Tests/BeamfallKitTests/Fixtures/corpus"
apple_fixture_relative="$apple_corpus_relative/next-contract/playback-info.json"
apple_manifest_relative="$apple_corpus_relative/corpus.manifest.json"
apple_version_relative="$apple_corpus_relative/corpus.version"
apple_wire_binding_relative="Tests/BeamfallKitTests/Fixtures/core-wire-client.generated.json"
apple_wire_contract_version="1.5.0"
apple_declared_hash="2662380161f5d3d0addcb05a395aa4ff0de1d6c00a0f84435be29a5ab2dd19c7"
apple_recomputed_hash="6c6fba3f970ad7ef09f0e94216d20bb4bbc1ac2ae3b834592da04749e1128092"
apple_fixture_file_digest="6810f27ff3061fe5c33d343c977dc53948d6b5e7974c2885ff59b685e5905eda"
mkdir -p "$APPLE/$apple_corpus_relative/next-contract"
printf 'token = "%s"\n' "$seeded_value" >"$APPLE/$apple_fixture_relative"
printf '{\n  "next-contract/playback-info.json": "%s"\n}\n' \
  "$apple_fixture_file_digest" >"$APPLE/$apple_manifest_relative"
printf '%s\n' "$apple_declared_hash" >"$APPLE/$apple_version_relative"
printf '{"artifactVersion":"%s"}\n' \
  "$apple_wire_contract_version" >"$APPLE/$apple_wire_binding_relative"
git -C "$APPLE" add \
  "$apple_fixture_relative" "$apple_manifest_relative" "$apple_version_relative" \
  "$apple_wire_binding_relative"
if [[ "$apple_declared_hash" == "$apple_recomputed_hash" ]]; then
  fail "disagreement fixture accidentally synchronized its declared and recomputed hashes"
fi
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/sibling-mismatch.out" 2>"$TMP/sibling-mismatch.err"; then
  fail "non-allowlisted sibling corpus token passed"
fi
grep -Fq "beamfall-apple:$apple_fixture_relative:1 rule=provider-github value=[REDACTED]" \
  "$TMP/sibling-mismatch.err" || fail "sibling corpus token finding was not reported"
grep -Fq "sibling beamfall-apple corpus fixture bytes sha256=$apple_recomputed_hash" \
  "$TMP/sibling-mismatch.err" || fail "sibling corpus failure omitted its recomputed identity"
grep -Fq "declared corpus.version=$apple_declared_hash disagrees with recomputed hash" \
  "$TMP/sibling-mismatch.err" || fail "sibling corpus failure omitted its declared-hash disagreement"
grep -Fq "wire-contract version=$apple_wire_contract_version" \
  "$TMP/sibling-mismatch.err" || fail "sibling corpus failure omitted its wire-contract version"
grep -Fq 'findings remain blocking' "$TMP/sibling-mismatch.err" ||
  fail "sibling corpus disagreement softened the findings"
grep -Fq 'matching corpus resync needs allowlist entries' "$TMP/sibling-mismatch.err" ||
  fail "sibling corpus failure omitted the resync action"
echo "secret-scan_test: RED-PROOF sibling corpus reported recomputed $apple_recomputed_hash, stale declared $apple_declared_hash, and their disagreement"

printf 'beamfall-apple\t%s\tprovider-github\t%s\n' \
  "$apple_fixture_relative" "$apple_secret_digest" >>"$CORE/tools/gate/secret-scan-allowlist.txt"
git -C "$CORE" add tools/gate/secret-scan-allowlist.txt
apple_live_relative="live-credential.txt"
printf 'token = "%s"\n' "$seeded_value" >"$APPLE/$apple_live_relative"
git -C "$APPLE" add "$apple_live_relative"
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/sibling-credential.out" 2>"$TMP/sibling-credential.err"; then
  fail "non-allowlisted sibling credential passed"
fi
grep -Fq "beamfall-apple:$apple_live_relative:1 rule=provider-github value=[REDACTED]" \
  "$TMP/sibling-credential.err" || fail "sibling credential finding did not name its repository and file"
grep -Fq 'credential findings remain blocking on their own terms' \
  "$TMP/sibling-credential.err" || fail "sibling credential finding was not reported on its own terms"
if grep -Fiq 'allowlist' "$TMP/sibling-credential.out" "$TMP/sibling-credential.err"; then
  fail "sibling credential finding prescribed allowlisting without resync evidence for its file"
fi
echo "secret-scan_test: RED-PROOF sibling credential named beamfall-apple:$apple_live_relative without allowlist guidance"
git -C "$APPLE" rm -q -f "$apple_live_relative"
cp "$ROOT/tools/gate/secret-scan-allowlist.txt" "$CORE/tools/gate/secret-scan-allowlist.txt"
git -C "$CORE" add tools/gate/secret-scan-allowlist.txt

printf '%s\n' "$apple_recomputed_hash" >"$APPLE/$apple_version_relative"
git -C "$APPLE" add "$apple_version_relative"
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/sibling-agreement.out" 2>"$TMP/sibling-agreement.err"; then
  fail "non-allowlisted sibling corpus token passed with an agreeing declaration"
fi
grep -Fq "beamfall-apple:$apple_fixture_relative:1 rule=provider-github value=[REDACTED]" \
  "$TMP/sibling-agreement.err" || fail "agreeing sibling corpus finding was not reported"
grep -Fq "sibling beamfall-apple corpus identity sha256=$apple_recomputed_hash (recomputed from fixture bytes; declared corpus.version agrees)" \
  "$TMP/sibling-agreement.err" || fail "agreeing sibling corpus failure omitted its consistent identity"
if [[ "$(grep -Fo "$apple_recomputed_hash" "$TMP/sibling-agreement.err" | wc -l | tr -d ' ')" != 1 ]]; then
  fail "agreeing sibling corpus did not report one consistent identity"
fi
grep -Fq 'credential findings remain blocking on their own terms' \
  "$TMP/sibling-agreement.err" || fail "agreeing sibling corpus finding was not reported on its own terms"
if grep -Fiq 'allowlist' "$TMP/sibling-agreement.out" "$TMP/sibling-agreement.err"; then
  fail "agreeing sibling corpus identity prescribed allowlisting without resync evidence"
fi
echo "secret-scan_test: RED-PROOF sibling corpus reported one consistent identity without allowlist guidance"

git -C "$APPLE" rm -q -f "$apple_version_relative" "$apple_manifest_relative"
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/sibling-undeclared.out" 2>"$TMP/sibling-undeclared.err"; then
  fail "sibling corpus token passed without a declared identity"
fi
grep -Fq "beamfall-apple:$apple_fixture_relative:1 rule=provider-github value=[REDACTED]" \
  "$TMP/sibling-undeclared.err" || fail "undeclared sibling corpus finding was not reported"
grep -Fq "sibling beamfall-apple corpus fixture bytes sha256=$apple_recomputed_hash (no declared corpus.version)" \
  "$TMP/sibling-undeclared.err" || fail "undeclared sibling corpus failure omitted its recomputed identity"
grep -Fq 'credential findings remain blocking on their own terms' \
  "$TMP/sibling-undeclared.err" || fail "undeclared sibling corpus finding was not reported on its own terms"
if grep -Fiq 'allowlist' "$TMP/sibling-undeclared.out" "$TMP/sibling-undeclared.err"; then
  fail "undeclared sibling corpus identity prescribed allowlisting without resync evidence"
fi
echo "secret-scan_test: RED-PROOF sibling corpus without a declaration reported its identity without allowlist guidance"

printf 'beamfall-apple\t%s\tprovider-github\t%s\n' \
  "$apple_fixture_relative" "$apple_secret_digest" >>"$CORE/tools/gate/secret-scan-allowlist.txt"
git -C "$CORE" add tools/gate/secret-scan-allowlist.txt
if ! BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/sibling-allowlisted.out" 2>"$TMP/sibling-allowlisted.err"; then
  fail "allowlisted sibling corpus token was rejected"
fi
grep -Fq 'secret-scan: PASS beamfall-apple files=' "$TMP/sibling-allowlisted.out" ||
  fail "allowlisted sibling corpus omitted its pass"
if grep -Fq 'corpus resync' "$TMP/sibling-allowlisted.out" "$TMP/sibling-allowlisted.err"; then
  fail "allowlisted sibling corpus emitted a resync diagnostic"
fi
echo "secret-scan_test: GREEN-PROOF allowlisted sibling corpus emitted no resync diagnostic"

cp "$ROOT/tools/gate/secret-scan-allowlist.txt" "$CORE/tools/gate/secret-scan-allowlist.txt"
git -C "$CORE" add tools/gate/secret-scan-allowlist.txt
git -C "$APPLE" rm -q -f "$apple_fixture_relative"
apple_unresolved_relative="outside-corpus.txt"
printf 'token = "%s"\n' "$seeded_value" >"$APPLE/$apple_unresolved_relative"
git -C "$APPLE" add "$apple_unresolved_relative"
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/sibling-unresolved.out" 2>"$TMP/sibling-unresolved.err"; then
  fail "sibling finding passed without resolvable corpus identity"
fi
grep -Fq "beamfall-apple:$apple_unresolved_relative:1 rule=provider-github value=[REDACTED]" \
  "$TMP/sibling-unresolved.err" || fail "unresolved sibling finding was not reported"
grep -Fq 'sibling beamfall-apple corpus identity is unresolved' \
  "$TMP/sibling-unresolved.err" || fail "sibling failure omitted unresolved corpus identity"
grep -Fq 'credential findings remain blocking on their own terms' "$TMP/sibling-unresolved.err" ||
  fail "unresolved sibling corpus identity softened the findings"
if grep -Fiq 'allowlist' "$TMP/sibling-unresolved.out" "$TMP/sibling-unresolved.err"; then
  fail "unresolved sibling corpus identity prescribed allowlisting"
fi
echo "secret-scan_test: RED-PROOF unresolved sibling corpus identity remained explicit and blocking without allowlist guidance"
git -C "$APPLE" rm -q -f "$apple_unresolved_relative"

changed_known_value="beamfall-fixture-bearer-token-00000002"
printf 'Authorization: Bearer %s\n' "$changed_known_value" >"$CORE/internal/testdata/known-token.txt"
git -C "$CORE" add internal/testdata/known-token.txt
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/changed-allowlisted.out" 2>"$TMP/changed-allowlisted.err"; then
  fail "changed bytes retained an exact-value allowlist exemption"
fi
grep -Fq 'beamfall:internal/testdata/known-token.txt:1 rule=credential-auth-header value=[REDACTED]' \
  "$TMP/changed-allowlisted.err" || fail "changed allowlisted fixture was not reported"
if grep -Fq "$changed_known_value" \
  "$TMP/changed-allowlisted.out" "$TMP/changed-allowlisted.err"; then
  fail "scanner exposed changed allowlisted fixture bytes in its output"
fi
echo "secret-scan_test: RED-PROOF changed fixture bytes invalidated the exact-value allowlist entry"

printf 'Authorization: Bearer %s\ntoken = "%s"\n' \
  "$known_value" "$seeded_value" >"$CORE/internal/testdata/known-token.txt"
git -C "$CORE" add internal/testdata/known-token.txt
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/live-beside-allowlisted.out" 2>"$TMP/live-beside-allowlisted.err"; then
  fail "live-looking credential beside an allowlisted value passed"
fi
grep -Fq 'beamfall:internal/testdata/known-token.txt:2 rule=provider-github value=[REDACTED]' \
  "$TMP/live-beside-allowlisted.err" || fail "live-looking credential beside allowlisted value was not reported"
if grep -Fq "$seeded_value" \
  "$TMP/live-beside-allowlisted.out" "$TMP/live-beside-allowlisted.err"; then
  fail "scanner exposed live-looking credential bytes in its output"
fi
echo "secret-scan_test: RED-PROOF allowlisted file still rejected a live-looking credential"
printf 'Authorization: Bearer %s\n' "$known_value" >"$CORE/internal/testdata/known-token.txt"
git -C "$CORE" add internal/testdata/known-token.txt

if "$CORE/tools/gate/secret-scan.py" --require-repos \
  --target "beamfall:$CORE" \
  --target "beamfall-apple:$APPLE" \
  --target "beamfall-android:$APPLE" \
  >"$TMP/duplicate-root.out" 2>"$TMP/duplicate-root.err"; then
  fail "required labels accepted the same repository directory"
fi
grep -Fq "beamfall-apple" "$TMP/duplicate-root.err" ||
  fail "duplicate-root failure did not name beamfall-apple"
grep -Fq "beamfall-android" "$TMP/duplicate-root.err" ||
  fail "duplicate-root failure did not name beamfall-android"
grep -Fq "$APPLE_REAL" "$TMP/duplicate-root.err" ||
  fail "duplicate-root failure did not name the resolved repository root"
echo "secret-scan_test: RED-PROOF required labels rejected duplicate repository root"

APPLE_ALIAS="$WORKSPACE/beamfall-apple-alias"
ln -s "$APPLE" "$APPLE_ALIAS"
if "$CORE/tools/gate/secret-scan.py" --require-repos \
  --target "beamfall:$CORE" \
  --target "beamfall-apple:$APPLE" \
  --target "beamfall-android:$APPLE_ALIAS" \
  >"$TMP/symlink-root.out" 2>"$TMP/symlink-root.err"; then
  fail "required labels accepted a symlink to the same repository directory"
fi
grep -Fq "beamfall-apple" "$TMP/symlink-root.err" ||
  fail "symlink-root failure did not name beamfall-apple"
grep -Fq "beamfall-android" "$TMP/symlink-root.err" ||
  fail "symlink-root failure did not name beamfall-android"
grep -Fq "$APPLE_REAL" "$TMP/symlink-root.err" ||
  fail "symlink-root failure did not name the resolved repository root"
echo "secret-scan_test: RED-PROOF required labels rejected symlinked repository root"

SHARED_MAIN="$TMP/shared-git-main"
SHARED_WORKTREE="$TMP/shared-git-worktree"
init_repo "$SHARED_MAIN"
printf 'clean shared fixture\n' >"$SHARED_MAIN/README.txt"
git -C "$SHARED_MAIN" add README.txt
git -C "$SHARED_MAIN" commit -q -m fixture
git -C "$SHARED_MAIN" worktree add -q -b linked "$SHARED_WORKTREE"
shared_common_dir="$(git -C "$SHARED_MAIN" rev-parse --path-format=absolute --git-common-dir)"
shared_common_dir="$(realpath "$shared_common_dir")"
if "$CORE/tools/gate/secret-scan.py" --require-repos \
  --target "shared-main:$SHARED_MAIN" \
  --target "shared-worktree:$SHARED_WORKTREE" \
  >"$TMP/shared-identity.out" 2>"$TMP/shared-identity.err"; then
  fail "required labels accepted two worktrees of one repository"
fi
grep -Fq "shared-main" "$TMP/shared-identity.err" ||
  fail "shared-identity failure did not name shared-main"
grep -Fq "shared-worktree" "$TMP/shared-identity.err" ||
  fail "shared-identity failure did not name shared-worktree"
grep -Fq "$shared_common_dir" "$TMP/shared-identity.err" ||
  fail "shared-identity failure did not name the common Git directory"
echo "secret-scan_test: RED-PROOF required labels rejected worktrees sharing one Git identity"

complete_output="$(make -s -C "$ROOT" secret-scan-release \
  APPLE_RELEASE_REPO="$APPLE" \
  ANDROID_RELEASE_REPO="$ANDROID")"
for repo in "$CORE" "$APPLE" "$ANDROID"; do
  label="${repo##*/}"
  grep -Fq "PASS $label " <<<"$complete_output" ||
    fail "secret-scan-release Makefile target omitted $label"
done
grep -Fq 'PASS complete required-tree coverage targets=3' <<<"$complete_output" ||
  fail "distinct repositories did not report complete required-tree coverage"
echo "secret-scan_test: GREEN-PROOF real secret-scan-release Makefile target reported complete distinct-repository coverage"

mv "$ANDROID" "$WORKSPACE/beamfall-android.absent"
skip_output="$(BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py")"
grep -Fq 'SKIP beamfall-android (missing ' <<<"$skip_output" ||
  fail "ordinary scan did not name the absent client tree"
grep -q 'not all-tree coverage' <<<"$skip_output" ||
  fail "partial ordinary scan claimed all-tree coverage"
if grep -Fq 'complete required-tree coverage' <<<"$skip_output"; then
  fail "partial ordinary scan overclaimed complete required-tree coverage"
fi
echo "secret-scan_test: GREEN-PROOF partial ordinary scan omitted complete required-tree coverage claim"
mv "$WORKSPACE/beamfall-android.absent" "$ANDROID"

mkdir "$WORKSPACE/not-a-repo"
if "$CORE/tools/gate/secret-scan.py" \
  --target "beamfall:$CORE" \
  --target "present-non-git:$WORKSPACE/not-a-repo" \
  >"$TMP/non-git.out" 2>"$TMP/non-git.err"; then
  fail "ordinary scan skipped a present non-git target"
fi
grep -q 'FAIL present-non-git cannot enumerate tracked files' "$TMP/non-git.err" ||
  fail "present non-git failure did not name the target"

printf 'Authorization: Bearer %s\n' "$known_value" >"$CORE/wrong-path.txt"
git -C "$CORE" add wrong-path.txt
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/allow-scope.out" 2>"$TMP/allow-scope.err"; then
  fail "allowlist fingerprint escaped its exact fixture path"
fi
grep -Fq 'beamfall:wrong-path.txt:1 rule=credential-auth-header value=[REDACTED]' \
  "$TMP/allow-scope.err" || fail "allowlist path-scope failure was not redacted"
git -C "$CORE" rm -q -f wrong-path.txt

printf 'token = "%s"\n' "$seeded_value" >"$CORE/leak.txt"
git -C "$CORE" add leak.txt
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/seeded.out" 2>"$TMP/seeded.err"; then
  fail "seeded fake credential did not red the Core scan"
fi
grep -Fq 'beamfall:leak.txt:1 rule=provider-github value=[REDACTED]' "$TMP/seeded.err" ||
  fail "seeded failure did not identify the redacted Core finding"
grep -Fxq 'secret-scan: FAIL beamfall findings=1' "$TMP/seeded.err" ||
  fail "Core finding summary changed"
expected_core_message="$(printf '%s\n%s' \
  'secret-scan: finding beamfall:leak.txt:1 rule=provider-github value=[REDACTED]' \
  'secret-scan: FAIL beamfall findings=1')"
if [[ "$(<"$TMP/seeded.err")" != "$expected_core_message" ]]; then
  fail "Core finding message changed"
fi
if grep -Fq 'corpus resync' "$TMP/seeded.out" "$TMP/seeded.err"; then
  fail "Core finding emitted a sibling corpus resync diagnostic"
fi
if grep -Fq "$seeded_value" "$TMP/seeded.out" "$TMP/seeded.err"; then
  fail "scanner exposed credential bytes in its output"
fi
echo "secret-scan_test: RED-PROOF Core seeded fake credential kept its existing failure message"
git -C "$CORE" rm -q -f leak.txt

printf 'token = "%s"\n' "$seeded_value" >"$CORE/staged-leak.txt"
git -C "$CORE" add staged-leak.txt
printf 'clean unstaged fixture\n' >"$CORE/staged-leak.txt"
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/staged-index.out" 2>"$TMP/staged-index.err"; then
  fail "staged fake credential was masked by clean working-tree bytes"
fi
grep -Fq 'beamfall:staged-leak.txt:1 rule=provider-github value=[REDACTED]' \
  "$TMP/staged-index.err" || fail "staged index finding was not redacted"
if grep -Fq "$seeded_value" "$TMP/staged-index.out" "$TMP/staged-index.err"; then
  fail "scanner exposed staged credential bytes in its output"
fi
echo "secret-scan_test: RED-PROOF staged index credential rejected despite clean working-tree bytes"
git -C "$CORE" rm -q -f staged-leak.txt

printf 'clean staged fixture\n' >"$CORE/benign-unstaged.txt"
git -C "$CORE" add benign-unstaged.txt
printf 'benign unstaged fixture\n' >"$CORE/benign-unstaged.txt"
if ! BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/benign-unstaged.out" 2>"$TMP/benign-unstaged.err"; then
  fail "benign unstaged tracked edit was rejected"
fi
if ! benign_unstaged_pass="$(
  grep -F 'secret-scan: PASS beamfall files=' "$TMP/benign-unstaged.out"
)"; then
  fail "benign unstaged tracked edit omitted the Core pass"
fi
if grep -Fq 'benign-unstaged.txt' "$TMP/benign-unstaged.out" "$TMP/benign-unstaged.err"; then
  fail "benign unstaged tracked edit was reported"
fi
echo "secret-scan_test: GREEN-PROOF benign-unstaged.txt accepted and not reported: $benign_unstaged_pass"
git -C "$CORE" rm -q -f benign-unstaged.txt

printf 'clean staged fixture\n' >"$CORE/unstaged-leak.txt"
git -C "$CORE" add unstaged-leak.txt
printf 'token = "%s"\n' "$seeded_value" >"$CORE/unstaged-leak.txt"
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/unstaged-worktree.out" 2>"$TMP/unstaged-worktree.err"; then
  fail "unstaged working-tree credential was skipped because index bytes were clean"
fi
if ! unstaged_worktree_finding="$(
  grep -F 'beamfall:unstaged-leak.txt:1 rule=provider-github value=[REDACTED]' \
    "$TMP/unstaged-worktree.err"
)"; then
  fail "unstaged working-tree finding was not redacted"
fi
if grep -Fq 'index and working tree differ' "$TMP/unstaged-worktree.err"; then
  fail "unstaged working-tree divergence was treated as a finding"
fi
if grep -Fq "$seeded_value" "$TMP/unstaged-worktree.out" "$TMP/unstaged-worktree.err"; then
  fail "scanner exposed unstaged credential bytes in its output"
fi
echo "secret-scan_test: RED-PROOF unstaged tracked edit credential rejected from worktree bytes: $unstaged_worktree_finding"
git -C "$CORE" rm -q -f unstaged-leak.txt

printf 'token = "%s"\n' "$seeded_value" >"$CORE/untracked-leak.txt"
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/untracked-file.out" 2>"$TMP/untracked-file.err"; then
  fail "non-ignored untracked credential was skipped"
fi
grep -Fq 'beamfall:untracked-leak.txt:1 rule=provider-github value=[REDACTED]' \
  "$TMP/untracked-file.err" || fail "untracked working-tree finding was not redacted"
if grep -Fq "$seeded_value" "$TMP/untracked-file.out" "$TMP/untracked-file.err"; then
  fail "scanner exposed untracked credential bytes in its output"
fi
echo "secret-scan_test: RED-PROOF non-ignored untracked working-tree credential rejected"
rm -f "$CORE/untracked-leak.txt"

printf '\0embedded %s suffix\0' "$seeded_value" >"$CORE/embedded.bin"
git -C "$CORE" add embedded.bin
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/binary.out" 2>"$TMP/binary.err"; then
  fail "provider credential embedded in a NUL-containing file passed"
fi
grep -Fq 'beamfall:embedded.bin:1 rule=provider-github value=[REDACTED]' \
  "$TMP/binary.err" || fail "binary credential finding was not redacted"
git -C "$CORE" rm -q -f embedded.bin

if ! openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 \
  -out "$CORE/fixture-private-key.pem" >/dev/null 2>&1; then
  fail "could not generate private-key fixture"
fi
private_key_marker="$(sed -n '1p' "$CORE/fixture-private-key.pem")"
private_key_digest="$(
  printf '%s' "$private_key_marker" |
    python3 -c 'import hashlib, sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())'
)"
printf 'beamfall\tfixture-private-key.pem\tprivate-key\t%s\n' \
  "$private_key_digest" >>"$CORE/tools/gate/secret-scan-allowlist.txt"
git -C "$CORE" add fixture-private-key.pem tools/gate/secret-scan-allowlist.txt
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/private-key.out" 2>"$TMP/private-key.err"; then
  fail "allowlist entry suppressed a NON_ALLOWLISTABLE_RULES finding"
fi
grep -Fq 'beamfall:fixture-private-key.pem:1 rule=private-key value=[REDACTED]' \
  "$TMP/private-key.err" || fail "private-key finding was not redacted"
if grep -Fq -- "$private_key_marker" "$TMP/private-key.out" "$TMP/private-key.err"; then
  fail "scanner exposed non-allowlistable fixture bytes in its output"
fi
echo "secret-scan_test: RED-PROOF NON_ALLOWLISTABLE_RULES entry did not suppress its finding"
git -C "$CORE" rm -q -f fixture-private-key.pem
cp "$ROOT/tools/gate/secret-scan-allowlist.txt" "$CORE/tools/gate/secret-scan-allowlist.txt"
git -C "$CORE" add tools/gate/secret-scan-allowlist.txt

credential_name="database_password"
punctuated_value="fixture!credential@value%2026"
printf '%s = "%s"\n' "$credential_name" "$punctuated_value" >"$CORE/composite.txt"
git -C "$CORE" add composite.txt
if BEAMFALL_WORKSPACE_ROOT="$WORKSPACE" "$CORE/tools/gate/secret-scan.py" \
  >"$TMP/composite.out" 2>"$TMP/composite.err"; then
  fail "composite credential name with punctuated value passed"
fi
grep -Fq 'beamfall:composite.txt:1 rule=credential-assignment value=[REDACTED]' \
  "$TMP/composite.err" || fail "composite credential finding was not redacted"
git -C "$CORE" rm -q -f composite.txt

mv "$ANDROID" "$WORKSPACE/beamfall-android.absent"
if make -s -C "$ROOT" secret-scan-release \
  APPLE_RELEASE_REPO="$APPLE" \
  ANDROID_RELEASE_REPO="$ANDROID" \
  >"$TMP/release.out" 2>"$TMP/release.err"; then
  fail "release secret scan accepted a missing client tree"
fi
grep -q 'FAIL beamfall-android missing required repo' "$TMP/release.err" ||
  fail "release failure did not name the absent client tree"
echo "secret-scan_test: RED-PROOF release scan rejected absent beamfall-android tree"
mv "$WORKSPACE/beamfall-android.absent" "$ANDROID"

aggregate_make="$TMP/release-aggregate-make"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'for target in "$@"; do' \
  '  if [[ "$target" == "secret-scan-release" ]]; then' \
  '    exec "$SECRET_SCAN_REAL_MAKE" -s -C "$SECRET_SCAN_ROOT" secret-scan-release \' \
  '      APPLE_RELEASE_REPO="$APPLE_RELEASE_REPO" \' \
  '      ANDROID_RELEASE_REPO="$ANDROID_RELEASE_REPO"' \
  '  fi' \
  'done' >"$aggregate_make"
chmod +x "$aggregate_make"

printf 'token = "%s"\n' "$seeded_value" >"$ANDROID/release-leak.txt"
git -C "$ANDROID" add release-leak.txt
if SECRET_SCAN_REAL_MAKE="$(command -v make)" \
  SECRET_SCAN_ROOT="$ROOT" \
  make -s -C "$ROOT" store-release-test-gates \
  "MAKE=$aggregate_make" \
  APPLE_RELEASE_REPO="$APPLE" \
  ANDROID_RELEASE_REPO="$ANDROID" \
  >"$TMP/release-aggregate.out" 2>"$TMP/release-aggregate.err"; then
  fail "store-release-test-gates did not enforce secret-scan-release for beamfall-android"
fi
grep -Fq 'secret-scan: PASS beamfall files=' "$TMP/release-aggregate.out" ||
  fail "store-release-test-gates secret-scan-release omitted beamfall"
grep -Fq 'secret-scan: PASS beamfall-apple files=' "$TMP/release-aggregate.out" ||
  fail "store-release-test-gates secret-scan-release omitted beamfall-apple"
grep -Fq 'beamfall-android:release-leak.txt:1 rule=provider-github value=[REDACTED]' \
  "$TMP/release-aggregate.err" || fail "aggregate release finding was not redacted"
echo "secret-scan_test: RED-PROOF real store-release-test-gates aggregate enforced real secret-scan-release Makefile target"
git -C "$ANDROID" rm -q -f release-leak.txt

echo "secret-scan_test: OK"
