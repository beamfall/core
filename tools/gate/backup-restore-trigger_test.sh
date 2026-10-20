#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TRIGGER="$ROOT/tools/gate/backup-restore-trigger.sh"

test "$("$TRIGGER" --mode pr)" = "none"
test "$("$TRIGGER" --mode pr --changed-file internal/store/migrate/catalog/0104_next.sql)" = "oldest-supported latest-1 latest"
test "$("$TRIGGER" --mode pr --changed-file internal/store/backup.go)" = "oldest-supported latest-1 latest"
test "$("$TRIGGER" --mode pr --changed-file internal/backup/set.go)" = "oldest-supported latest-1 latest"
test "$("$TRIGGER" --mode pr --changed-file cmd/beamfall/backup.go)" = "oldest-supported latest-1 latest"
test "$("$TRIGGER" --mode pr --changed-file internal/playback/session.go)" = "none"
test "$("$TRIGGER" --mode nightly)" = "full crash-injection"
