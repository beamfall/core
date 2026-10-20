#!/bin/sh
set -eu

root=${1:-.}
files="docs/security/CLAIMS.md SECURITY.md CONTRIBUTING.md CODE_OF_CONDUCT.md README.md"

# These sentence-level expressions are the committed forbidden-language list. They target trust or
# governance claims, not unrelated technical identifiers. The register's explicit limitations use
# necessary descriptive terms and are excluded by matching only affirmative claim forms.
patterns='(^|[^[:alpha:]])(we|beamfall|the project|the foundation)[[:space:]]+(hereby[[:space:]]+)?(pledge|promise|guarantee|warrant)([^[:alpha:]]|$)
ask[- ]first
no[- ]rug[- ]pull
warrant[[:space:]-]+canary
(trust|security|open)[[:space:]-]+badge
certified[[:space:]]+(secure|safe|trusted|open)
trademark[[:space:]-]+(badge|license|program)
(trust|open|closed)[[:space:]-]+manifest(o)?
(amend|modify|replace|rewrite|change)[^.]*(contributor[[:space:]]+license[[:space:]]+agreement|CLA)
(foundation)[[:space:]]+(will|shall|commits|pledges|promises)'

for file in $files; do
  if [ ! -f "$root/$file" ]; then
    echo "trust-docs: missing required file: $file" >&2
    exit 1
  fi
done

if [ -e "$root/MANIFEST.md" ]; then
  echo "trust-docs: forbidden file exists: MANIFEST.md" >&2
  exit 1
fi

status=0
while IFS= read -r pattern; do
  [ -n "$pattern" ] || continue
  for file in $files; do
    # Literal examples and command receipts are evidence, not project claims. Strip Markdown code
    # spans and fenced blocks before evaluating the surrounding prose.
    if awk '
      /^```/ { fenced = !fenced; next }
      !fenced { line = $0; gsub(/`[^`]*`/, "", line); print line }
    ' "$root/$file" | LC_ALL=C grep -Ein "$pattern"; then
      echo "trust-docs: forbidden language matched in $file: $pattern" >&2
      status=1
    fi
  done
done <<EOF
$patterns
EOF

if [ "$status" -ne 0 ]; then
  exit "$status"
fi

echo "trust-docs: PASS"
