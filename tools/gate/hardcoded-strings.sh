#!/usr/bin/env bash
# ADR-0034 §4 hardcoded-string gate.
#
# ADR-0034 makes a hardcoded user-facing string a CI-greppable defect: every
# display string must live in the locale catalog (BCP-47 map, English
# fallback), never as a bare literal in source. This check enforces that for
# the web UI, where strings are untyped text the Go compiler cannot police.
#
# Scope and why each surface is checked the way it is:
#
#   * Canonical React app — internal/web/app/src/*.tsx (ADR-0007).
#     Catalog anchor: internal/web/app/src/strings.ts. Every user-facing
#     string flows through t("someKey") from lib/i18n.tsx; a StringKey is a
#     single camelCase token (no spaces). So two things are defects:
#       (a) a JSX text node — `>Hello world<` — instead of `{t("...")}`;
#       (b) a *multi-word* string literal in a user-facing prop
#           (placeholder / aria-label / title / alt / label). Single-token
#           values are StringKeys (e.g. title="consoleConfigHeading") and are
#           NOT flagged — only a value containing a space, which a real
#           sentence has and a StringKey never does.
#     internal/web/app/src/components/ui/ is vendored shadcn scaffold (carries
#     sr-only "Close" labels), not Beamfall-authored area code — excluded.
#
# Deliberately NOT covered here (documented in the L10N-1 report):
#   * First-party plugin display strings. Those struct fields are typed
#     plugin.LocaleString (internal/plugin/plugin.go); a bare Go literal there
#     is a compile error already caught by `go build ./...` in the gate. The
#     type system is a stronger guarantee than a grep, so no grep is added.
#   * JSON config (PWA manifest name/short_name), CSS class names, route
#     paths, and test files — never user-facing source literals.
#
# The check is pure grep over a fixed file set: deterministic and fast.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

fail=0

emit() {
  # emit <file> <line> <reason>
  echo "ADR-0034 hardcoded string: $1:$2 — $3" >&2
  fail=1
}

# --- Canonical React app (internal/web/app/src) ---------------------------
# Authored .tsx only: exclude vendored components/ui/ and *.test.tsx.
while IFS= read -r file; do
  case "$file" in
    */components/ui/*) continue ;;
    *.test.tsx) continue ;;
  esac

  # (a) JSX text nodes: >Letters and spaces< between tags. The only such
  #     matches in the tree are the vendored ui/ "Close" labels (excluded
  #     above), so any hit in authored code is a planted/real literal.
  while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    line=${hit%%:*}
    text=${hit#*:}
    emit "$file" "$line" "JSX text node $text — use {t(\"key\")} with a key in strings.ts"
  done < <(grep -noE '>[A-Za-z][A-Za-z ]*<' "$file" || true)

  # (b) Multi-word literal in a user-facing prop. A space distinguishes a
  #     real sentence from a single-token StringKey.
  while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    line=${hit%%:*}
    text=${hit#*:}
    emit "$file" "$line" "user-facing prop literal $text — pass a StringKey resolved via t()"
  done < <(grep -noE '(placeholder|aria-label|title|alt|label)="[A-Za-z]+ [^"]*"' "$file" || true)

  # (c) lead= prop with a multi-word string literal. The lead prop renders as
  #     visible section description text; it must flow through t() like any
  #     other display string. A space distinguishes a real sentence from a
  #     single-token StringKey.
  while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    line=${hit%%:*}
    text=${hit#*:}
    emit "$file" "$line" "lead= prop literal $text — pass a StringKey resolved via t()"
  done < <(grep -noE 'lead="[A-Za-z]+ [^"]*"' "$file" || true)

  # (d) setError( / setNotice( called with a multi-word string literal. These
  #     display user-facing messages; they must use t()-resolved keys rather
  #     than bare English literals. Template-literal calls (${ }) and t() calls
  #     are allowed; only plain string literals are flagged.
  while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    line=${hit%%:*}
    text=${hit#*:}
    emit "$file" "$line" "setError/setNotice literal $text — pass t(\"key\") with a key in strings.ts"
  done < <(grep -noE 'set(Error|Notice)\("[A-Za-z]+ [^"]*"\)' "$file" || true)
done < <(find internal/web/app/src -name '*.tsx' -type f | sort)

if [[ "$fail" -ne 0 ]]; then
  echo "==> hardcoded-string gate FAILED (ADR-0034 §4): move the string(s) above into the locale catalog" >&2
  exit 1
fi

echo "hardcoded-string gate: clean"
