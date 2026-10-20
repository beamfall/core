#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

cat >"$TMP/seed-schema.json" <<'JSON'
{"schemaFingerprint":"simulated-old-schema","releaseOrdinal":7}
JSON

"$ROOT/tools/gate/backup-fixture-staleness.sh" check \
  --metadata "$TMP/seed-schema.json" \
  --current-release-ordinal 8 \
  --fingerprint simulated-current-schema >/dev/null 2>&1

if "$ROOT/tools/gate/backup-fixture-staleness.sh" check \
  --metadata "$TMP/seed-schema.json" \
  --current-release-ordinal 9 \
  --fingerprint simulated-current-schema >/dev/null 2>&1; then
  echo "expected two-release schema drift to fail" >&2
  exit 1
fi

# A reader must wait for the metadata producer to exit after its first line.
export BACKUP_STALENESS_REAL_PYTHON="$(command -v python3)"
export BACKUP_STALENESS_PRODUCER_DONE="$TMP/producer-done"
mkdir "$TMP/bin"
cat >"$TMP/bin/python3" <<'SH'
#!/usr/bin/env bash
exec "$BACKUP_STALENESS_REAL_PYTHON" -c '
import os, pathlib, sys, time
print("simulated-old-schema 7", flush=True)
time.sleep(0.2)
pathlib.Path(os.environ["BACKUP_STALENESS_PRODUCER_DONE"]).touch()
'
SH
chmod +x "$TMP/bin/python3"
PATH="$TMP/bin:$PATH" "$ROOT/tools/gate/backup-fixture-staleness.sh" check \
  --metadata "$TMP/seed-schema.json" \
  --current-release-ordinal 8 \
  --fingerprint simulated-current-schema >/dev/null 2>&1
if ! test -f "$BACKUP_STALENESS_PRODUCER_DONE"; then
  echo "metadata reader returned before its producer exited" >&2
  exit 1
fi
