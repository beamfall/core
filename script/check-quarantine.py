#!/usr/bin/env python3
"""check-quarantine.py — ADR-0121 §6 (+ Amendment A §3) quarantine manifest checker.

Called by script/check-quarantine.sh. Not intended to be run directly.

Reads a quarantine YAML manifest and fails (exit 1) if:
  - Any entry has no expiresAt (unbounded quarantine is prohibited).
  - Any entry's expiresAt is today or in the past (deadline passed).
  - The total entry count exceeds the manifest's maxEntries cap.
  - Any entry names no governed skip kind, or names more than one.

An entry names exactly one skip kind (its identifier), all governed identically:
  - test:       an in-test assumeTrue/XCTSkip/t.Skip green-skip (§6 original).
  - pathFilter: a workflow `paths:` filter that gates a test tier on file
                patterns (Amendment A §3).
  - envGate:    an env-var-gated tier whose variable is never set in CI
                (Amendment A §3).
  - strictFlag: a strictness flag defaulting off, making a check a no-op
                (Amendment A §3).
"""
import sys
from datetime import date

# Governed skip-kind identifier fields. Exactly one must appear per entry; the
# expiry / ticket / owner / cap governance is identical across all of them.
SKIP_KINDS = ("test", "pathFilter", "envGate", "strictFlag")


def parse_quarantine_yaml(path: str):
    """Minimal YAML parser for the quarantine manifest format.

    Handles only the key:value / list-item structure used in quarantine.yaml.
    Avoids a PyYAML dependency to keep the script self-contained.
    """
    with open(path) as f:
        lines = f.readlines()

    max_entries = 5
    entries: list[dict] = []
    current: dict | None = None
    in_entries = False

    for lineno, raw in enumerate(lines, 1):
        line = raw.rstrip("\n")
        stripped = line.strip()

        # Skip comments and blanks
        if not stripped or stripped.startswith("#"):
            continue

        # Top-level key: value (no leading indent)
        if not line.startswith((" ", "\t")):
            if stripped == "entries:":
                in_entries = True
                current = None
                continue
            in_entries = False
            if ":" in stripped:
                k, _, v = stripped.partition(":")
                k = k.strip()
                v = v.strip()
                if k == "maxEntries":
                    try:
                        max_entries = int(v)
                    except ValueError:
                        print(
                            f"check-quarantine: line {lineno}: maxEntries must be an integer",
                            file=sys.stderr,
                        )
                        sys.exit(2)
            continue

        if not in_entries:
            continue

        # Entry list item: "  - key: value"
        if stripped.startswith("- "):
            if current is not None:
                entries.append(current)
            kv = stripped[2:].strip()
            current = {}
            if ":" in kv:
                k, _, v = kv.partition(":")
                current[k.strip()] = v.strip().strip('"').strip("'")
            continue

        # Continuation key:value under current entry
        if current is not None and ":" in stripped:
            k, _, v = stripped.partition(":")
            current[k.strip()] = v.strip().strip('"').strip("'")
            continue

    if current is not None:
        entries.append(current)

    return max_entries, entries


def main() -> None:
    if len(sys.argv) < 2:
        print("Usage: check-quarantine.py <manifest.yaml>", file=sys.stderr)
        sys.exit(2)

    manifest_path = sys.argv[1]
    max_entries, entries = parse_quarantine_yaml(manifest_path)

    today = date.today()
    violations: list[str] = []

    # Check hard cap
    if len(entries) > max_entries:
        violations.append(
            f"  CAP: {len(entries)} entries exceed maxEntries={max_entries}; "
            "resolve quarantined skips before adding new ones."
        )

    for i, entry in enumerate(entries):
        present_kinds = [k for k in SKIP_KINDS if entry.get(k, "").strip()]
        ident = entry.get(present_kinds[0], f"entry[{i}]") if present_kinds else f"entry[{i}]"

        if not present_kinds:
            violations.append(
                f"  NO-KIND: '{ident}' names no skip kind — every entry must set "
                f"exactly one of {', '.join(SKIP_KINDS)} (ADR-0121 §6 + Amendment A §3)."
            )
            continue

        if len(present_kinds) > 1:
            violations.append(
                f"  MULTI-KIND: '{ident}' sets multiple skip kinds "
                f"({', '.join(present_kinds)}) — an entry governs exactly one."
            )
            continue

        expires_raw = entry.get("expiresAt", "").strip()

        if not expires_raw:
            violations.append(
                f"  UNBOUNDED: '{ident}' has no expiresAt — "
                "unbounded quarantine is prohibited (ADR-0121 §6)."
            )
            continue

        try:
            expires = date.fromisoformat(expires_raw)
        except ValueError:
            violations.append(
                f"  INVALID: '{ident}' has unparseable expiresAt='{expires_raw}' "
                "(expected YYYY-MM-DD)."
            )
            continue

        if expires <= today:
            ticket = entry.get("ticket", "no-ticket")
            owner = entry.get("owner", "no-owner")
            kind = present_kinds[0]
            violations.append(
                f"  EXPIRED: {kind} '{ident}' expired {expires_raw} "
                f"(ticket={ticket}, owner={owner}) — "
                "remove the entry or extend the deadline."
            )

    if violations:
        print(
            f"check-quarantine: FAIL — {len(violations)} violation(s) in {manifest_path}:",
            file=sys.stderr,
        )
        for v in violations:
            print(v, file=sys.stderr)
        sys.exit(1)

    entry_word = "entry" if len(entries) == 1 else "entries"
    print(
        f"check-quarantine: OK — {len(entries)}/{max_entries} "
        f"quarantined {entry_word}, all within deadline."
    )


if __name__ == "__main__":
    main()
