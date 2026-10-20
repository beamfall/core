#!/usr/bin/env bash
# DEC-8 internet-exposure posture gate.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

fail=0

emit() {
  echo "internet-exposure gate: $1" >&2
  fail=1
}

expected='${BEAMFALL_HTTP_BIND:-127.0.0.1}:${BEAMFALL_HTTP_PORT:-8800}:8800'
for file in packaging/compose/compose.cpu.yml packaging/compose/compose.nvidia.yml packaging/compose/compose.intel-vaapi.yml; do
  if ! grep -Fq "$expected" "$file"; then
    emit "$file must publish Beamfall on loopback by default"
  fi
  if grep -Eq '^[[:space:]]*-[[:space:]]*"\$\{BEAMFALL_HTTP_PORT:-8800\}:8800"' "$file"; then
    emit "$file still contains the old all-interface copy-paste port mapping"
  fi
done

grep -q 'profiles:[[:space:]]*\["public-bind"\]' packaging/compose/compose.public.yml ||
  emit "public compose override must require the public-bind profile"
# DD-10: accept caddy:2-alpine or caddy:2.x.y-alpine so operators can pin to
# a specific patch version without breaking this gate.
grep -Eq 'image:[[:space:]]*caddy:2' packaging/compose/compose.public.yml ||
  emit "public compose override must provide a TLS reverse-proxy service"
grep -q '\${BEAMFALL_PUBLIC_HTTPS_BIND:-0.0.0.0}:\${BEAMFALL_PUBLIC_HTTPS_PORT:-443}:443' packaging/compose/compose.public.yml ||
  emit "public compose override must publish HTTPS explicitly"
grep -q 'reverse_proxy beamfall:8800' packaging/compose/Caddyfile ||
  emit "public compose profile must reverse-proxy to the internal Beamfall service"

grep -q 'PublicMetrics[[:space:]]*bool' internal/config/config.go ||
  emit "internal/config/config.go must expose PublicMetrics"
grep -q 'RemoteEnrollmentEnabled:[[:space:]]*false' internal/store/console_config.go ||
  emit "remote enrollment must default to disabled"
grep -q 'TestRemoteEnrollmentDisabledByDefaultRefusesOffLAN' internal/pairing/remote_enrollment_test.go ||
  emit "remote enrollment default-off behavior must have a focused test"
grep -q 'loopbackRemote(r.RemoteAddr)' internal/server/server.go ||
  emit "the unauthenticated loopback-only control must reject non-loopback clients by default"
# GCR-012-H: /metrics no longer decides on proximity at all. A same-host proxy
# that neither declares itself nor adds a hop header is indistinguishable from a
# local client, so the credential is the only non-public path in.
grep -q 'metricsScrapeCredentialAccepted(r)' internal/server/metrics_credential.go ||
  emit "/metrics must require the scrape credential when the public opt-in is unset"
if grep -q 'requestIsLoopback' internal/server/metrics_credential.go; then
  emit "/metrics must not re-admit clients on proximity; the scrape credential is the only non-public path"
fi
grep -q -- '--profile public-bind' docs/security/runbooks/deploy.md ||
  emit "deploy docs must route public exposure through the hardened profile"
grep -q 'Caddy' docs/security/runbooks/deploy.md ||
  emit "deploy docs must describe TLS termination for the hardened profile"
grep -q 'BEAMFALL_PUBLIC_METRICS' docs/security/runbooks/metrics.md docs/security/runbooks/deploy.md docs/operations/runbook.md ||
  emit "metrics docs must mention BEAMFALL_PUBLIC_METRICS"
grep -q -- '--public-metrics' docs/security/runbooks/metrics.md docs/security/runbooks/deploy.md docs/operations/runbook.md ||
  emit "metrics docs must mention --public-metrics"

if [[ "$fail" -ne 0 ]]; then
  echo "==> internet-exposure gate FAILED (DEC-8)" >&2
  exit 1
fi

echo "internet-exposure gate: clean"
