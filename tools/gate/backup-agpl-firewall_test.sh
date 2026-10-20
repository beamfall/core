#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/tools/gate" "$TMP/internal/backup" "$TMP/internal/graph" "$TMP/internal/config"
cp "$ROOT/tools/gate/backup-agpl-firewall.sh" "$TMP/tools/gate/backup-agpl-firewall.sh"

git -C "$TMP" init -b main >/dev/null
git -C "$TMP" config user.email gate-test@example.invalid
git -C "$TMP" config user.name "Gate Test"

cat >"$TMP/internal/backup/service.go" <<'GO'
package backup

type BackupKey struct{}
GO

cat >"$TMP/internal/graph/backup.go" <<'GO'
package graph

type BackupResult struct {
	Manifest string `json:"manifest"`
}
GO

cat >"$TMP/internal/config/config.go" <<'GO'
package config

type Config struct {
	BackupDestination string
}
GO

git -C "$TMP" add .
"$TMP/tools/gate/backup-agpl-firewall.sh" >/dev/null

cat >"$TMP/internal/backup/schema.go" <<'GO'
package backup

type HostedStoreEntitlement struct{}
GO
git -C "$TMP" add internal/backup/schema.go
if "$TMP/tools/gate/backup-agpl-firewall.sh" >/tmp/backup-agpl.out 2>/tmp/backup-agpl.err; then
  echo "expected backup package entitlement schema to fail" >&2
  exit 1
fi
grep -qi 'entitlement' /tmp/backup-agpl.err
rm "$TMP/internal/backup/schema.go"
git -C "$TMP" add -u internal/backup/schema.go

cat >"$TMP/internal/graph/backup_api.go" <<'GO'
package graph

type BackupStatus struct {
	AccountID string `json:"accountId"`
}
GO
git -C "$TMP" add internal/graph/backup_api.go
if "$TMP/tools/gate/backup-agpl-firewall.sh" >/tmp/backup-agpl.out 2>/tmp/backup-agpl.err; then
  echo "expected backup GraphQL API account field to fail" >&2
  exit 1
fi
grep -qi 'account' /tmp/backup-agpl.err
rm "$TMP/internal/graph/backup_api.go"
git -C "$TMP" add -u internal/graph/backup_api.go

cat >"$TMP/internal/backup/log.go" <<'GO'
package backup

var logMessage = "backup subscription entitlement missing"
GO
git -C "$TMP" add internal/backup/log.go
if "$TMP/tools/gate/backup-agpl-firewall.sh" >/tmp/backup-agpl.out 2>/tmp/backup-agpl.err; then
  echo "expected backup entitlement log string to fail" >&2
  exit 1
fi
grep -qi 'subscription' /tmp/backup-agpl.err
rm "$TMP/internal/backup/log.go"
git -C "$TMP" add -u internal/backup/log.go

cat >"$TMP/internal/config/config.go" <<'GO'
package config

type BackupConfig struct {
	AccountID string
}
GO
git -C "$TMP" add internal/config/config.go
if "$TMP/tools/gate/backup-agpl-firewall.sh" >/tmp/backup-agpl.out 2>/tmp/backup-agpl.err; then
  echo "expected backup account config to fail" >&2
  exit 1
fi
grep -qi 'account' /tmp/backup-agpl.err
rm "$TMP/internal/config/config.go"
cat >"$TMP/internal/config/config.go" <<'GO'
package config

type Config struct {
	BackupDestination string
}
GO
git -C "$TMP" add internal/config/config.go

# Regression (CORE-ARCH-1A): the bare word "subscription" is the content
# feed-subscription feature, not billing. An alphabetically-sorted dispatch
# registry that places a content-subscription entry within the ±3-line
# backup_scope_match window of an unrelated "backup"/"backuphistory" entry must
# NOT false-positive — the exact shape that failed CORE-ARCH-1A's re-gate.
cat >"$TMP/internal/graph/graph_dispatch.go" <<'GO'
package graph

var fieldHandlers = map[string]fieldHandler{
	"addsubscription":            newFieldHandler("addSubscription", "addSubscription", fieldMutation, 54),
	"associatesubscriptionentry": newFieldHandler("associateSubscriptionEntry", "associateSubscriptionEntry", fieldMutation, 54),
	"backup":                     newFieldHandler("backup", "backup", fieldMutation, 65),
	"backuphistory":              newFieldHandler("backupHistory", "backupHistory", fieldRead, 65),
	"removesubscription":         newFieldHandler("removeSubscription", "removeSubscription", fieldMutation, 54),
	"setsubscriptionretention":   newFieldHandler("setSubscriptionRetention", "setSubscriptionRetention", fieldMutation, 54),
	"subscriptions":              newFieldHandler("subscriptions", "subscriptions", fieldRead, 54),
}
GO
git -C "$TMP" add internal/graph/graph_dispatch.go
if ! "$TMP/tools/gate/backup-agpl-firewall.sh" >/tmp/backup-agpl.out 2>/tmp/backup-agpl.err; then
  echo "content feed-subscription registry adjacent to backup entries false-positived" >&2
  cat /tmp/backup-agpl.err >&2
  exit 1
fi
rm "$TMP/internal/graph/graph_dispatch.go"
git -C "$TMP" add -u internal/graph/graph_dispatch.go

# True-positive (proximity scope): genuine billing/entitlement subscription
# vocabulary is compound/qualified, and must be caught in internal/graph when in
# the backup proximity window.
cat >"$TMP/internal/graph/backup_billing.go" <<'GO'
package graph

type BackupPlan struct {
	Tier premiumSubscription
}

func restoreState(s subscriptionRenewed) {}
GO
git -C "$TMP" add internal/graph/backup_billing.go
if "$TMP/tools/gate/backup-agpl-firewall.sh" >/tmp/backup-agpl.out 2>/tmp/backup-agpl.err; then
  echo "expected qualified billing subscription in graph backup scope to fail" >&2
  exit 1
fi
grep -qi 'subscription' /tmp/backup-agpl.err
rm "$TMP/internal/graph/backup_billing.go"
git -C "$TMP" add -u internal/graph/backup_billing.go

# Strict scope: in a DEDICATED backup package every line is unconditionally in
# scope, so the bare word `subscription` — even a shape the qualified list would
# miss (subscriptionID) — must still be caught. This is the teeth the global
# narrowing must not remove.
cat >"$TMP/internal/backup/leak.go" <<'GO'
package backup

type restoreManifest struct {
	subscriptionID string
}
GO
git -C "$TMP" add internal/backup/leak.go
if "$TMP/tools/gate/backup-agpl-firewall.sh" >/tmp/backup-agpl.out 2>/tmp/backup-agpl.err; then
  echo "expected bare subscription in a dedicated backup package to fail" >&2
  exit 1
fi
grep -qi 'subscription' /tmp/backup-agpl.err
rm "$TMP/internal/backup/leak.go"
git -C "$TMP" add -u internal/backup/leak.go

# Negative twin: the same bare `subscription` shape in proximity scope
# (internal/graph, no backup context nearby) is the legitimate content feature
# and must stay clean.
cat >"$TMP/internal/graph/feed.go" <<'GO'
package graph

type SubscriptionInfo struct {
	subscriptionID string
	FeedURL        string
}
GO
git -C "$TMP" add internal/graph/feed.go
if ! "$TMP/tools/gate/backup-agpl-firewall.sh" >/tmp/backup-agpl.out 2>/tmp/backup-agpl.err; then
  echo "content SubscriptionInfo outside backup scope false-positived" >&2
  cat /tmp/backup-agpl.err >&2
  exit 1
fi
rm "$TMP/internal/graph/feed.go"
git -C "$TMP" add -u internal/graph/feed.go

echo "backup-agpl-firewall_test: OK"
