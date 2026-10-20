#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/tools/gate" "$TMP/internal/server" "$TMP/internal/release"
cp "$ROOT/tools/gate/agpl-forbidden-symbols.sh" "$TMP/tools/gate/agpl-forbidden-symbols.sh"

git -C "$TMP" init -b main >/dev/null
git -C "$TMP" config user.email gate-test@example.invalid
git -C "$TMP" config user.name "Gate Test"

cat >"$TMP/internal/server/server.go" <<'GO'
package server

var clean = true
GO

cat >"$TMP/internal/release/sbom.go" <<'GO'
package release

type SBOM struct {
	DataLicense string `json:"dataLicense"`
}
GO

git -C "$TMP" add .
"$TMP/tools/gate/agpl-forbidden-symbols.sh" >/dev/null

cat >"$TMP/internal/server/entitlement.go" <<'GO'
package server

var entitlementState = true
GO
git -C "$TMP" add internal/server/entitlement.go
if "$TMP/tools/gate/agpl-forbidden-symbols.sh" >/tmp/agpl-forbidden.out 2>/tmp/agpl-forbidden.err; then
  echo "expected entitlement symbol to fail" >&2
  exit 1
fi
grep -qi 'entitlement' /tmp/agpl-forbidden.err
rm "$TMP/internal/server/entitlement.go"
git -C "$TMP" add -u internal/server/entitlement.go

cat >"$TMP/internal/server/account.go" <<'GO'
package server

type AccountService struct{}
GO
git -C "$TMP" add internal/server/account.go
if "$TMP/tools/gate/agpl-forbidden-symbols.sh" >/tmp/agpl-forbidden.out 2>/tmp/agpl-forbidden.err; then
  echo "expected AccountService symbol to fail" >&2
  exit 1
fi
grep -qi 'account' /tmp/agpl-forbidden.err
rm "$TMP/internal/server/account.go"
git -C "$TMP" add -u internal/server/account.go

cat >"$TMP/internal/server/stripe.go" <<'GO'
package server

var StripeWebhook = "forbidden"
GO
git -C "$TMP" add internal/server/stripe.go
if "$TMP/tools/gate/agpl-forbidden-symbols.sh" >/tmp/agpl-forbidden.out 2>/tmp/agpl-forbidden.err; then
  echo "expected Stripe symbol to fail" >&2
  exit 1
fi
grep -qi 'stripe' /tmp/agpl-forbidden.err
rm "$TMP/internal/server/stripe.go"
git -C "$TMP" add -u internal/server/stripe.go

# XCONTRACT-9: the proprietary-module firewall. Each case RED-proves one surface
# that could create an AGPL -> proprietary build edge, then reverts so the next
# case starts from a clean tree.
cat >"$TMP/go.mod" <<'MOD'
module github.com/beamfall/core

go 1.24
MOD
git -C "$TMP" add go.mod
"$TMP/tools/gate/agpl-forbidden-symbols.sh" >/dev/null

cat >>"$TMP/go.mod" <<'MOD'

require github.com/beamfall/cloud v0.1.0
MOD
if "$TMP/tools/gate/agpl-forbidden-symbols.sh" >/tmp/agpl-forbidden.out 2>/tmp/agpl-forbidden.err; then
  echo "expected a proprietary go.mod require to fail" >&2
  exit 1
fi
grep -q 'proprietary module reference: go.mod' /tmp/agpl-forbidden.err
grep -q 'github.com/beamfall/cloud' /tmp/agpl-forbidden.err

cat >"$TMP/go.mod" <<'MOD'
module github.com/beamfall/core

go 1.24
MOD
"$TMP/tools/gate/agpl-forbidden-symbols.sh" >/dev/null

cat >"$TMP/go.sum" <<'SUM'
github.com/beamfall/relay v0.4.0 h1:0000000000000000000000000000000000000000000=
SUM
git -C "$TMP" add go.sum
if "$TMP/tools/gate/agpl-forbidden-symbols.sh" >/tmp/agpl-forbidden.out 2>/tmp/agpl-forbidden.err; then
  echo "expected a proprietary go.sum entry to fail" >&2
  exit 1
fi
grep -q 'proprietary module reference: go.sum' /tmp/agpl-forbidden.err
rm "$TMP/go.sum"
git -C "$TMP" add -u go.sum

cat >"$TMP/internal/server/tunnel.go" <<'GO'
package server

import "github.com/beamfall/relay/protocol"

var _ = protocol.Frame{}
GO
git -C "$TMP" add internal/server/tunnel.go
if "$TMP/tools/gate/agpl-forbidden-symbols.sh" >/tmp/agpl-forbidden.out 2>/tmp/agpl-forbidden.err; then
  echo "expected a proprietary Go import to fail" >&2
  exit 1
fi
grep -q 'proprietary module reference: internal/server/tunnel.go' /tmp/agpl-forbidden.err
rm "$TMP/internal/server/tunnel.go"
git -C "$TMP" add -u internal/server/tunnel.go

# Prose may name the proprietary repos — the ADRs and this repo's own comments do.
cat >"$TMP/internal/server/tunnel_notes.go" <<'GO'
package server

// The relay tunnel is re-implemented here rather than imported from
// github.com/beamfall/relay, which is proprietary.
var tunnelNote = "ok"
GO
git -C "$TMP" add internal/server/tunnel_notes.go
"$TMP/tools/gate/agpl-forbidden-symbols.sh" >/dev/null

echo "agpl-forbidden-symbols_test: OK"
