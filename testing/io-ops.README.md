# `io-ops.yaml` — the I/O-operations ledger

The hand-maintained half of the I/O coverage axis. Read by `cmd/ioe2e` (the I/O
sibling of `cmd/featurecheck`). It lists the I/O ops that a tool **cannot** derive
from code: outbound network **egress** ops and **filesystem** subsystem ops.

ffmpeg `*Runner` methods are **not** here — `ioe2e` enumerates them live from
`internal/ffmpeg` via `go/packages`, so they stay rename-safe and never drift.
Do not add ffmpeg ops to this file.

## How coverage works

`ioe2e` reconciles three axes into one report:

1. **ffmpeg ops** — code-derived: every exported method on `*ffmpeg.Runner`.
2. **markers** — `// io:<id>` comments in test sources. A marker credits its op.
3. **this ledger** — the egress + fs ops below.

An op is **covered** when a `// io:<id>` marker with its id exists in any test
source. An ffmpeg method with no marker must instead be **ignored** via a
`//io:ignore` directive comment on/near the method (used for config setters
like `SetGenerationNiceness` / `SetHardeningShim`, which are not I/O). A method
that is neither covered nor ignored fails `ioe2e check`. A ledger op with no
marker is reported MISSING.

A ledger op may also be **blocked**: a documented, accepted coverage gap that
`ioe2e check` surfaces but does not fail on. Add a `blocked:` reason to the
entry (see below); cite the residual-work doc so the gap is tracked, never
silent. Closing the gap = land the journey + delete the `blocked:` line.

## Adding an op

Append a block to the `ops:` list:

```yaml
  - id: egress-newsource-search   # dashed lowercase; also the marker token
    desc: One-line description of the I/O the op performs
    pkg: internal/yourpkg          # package path that owns it
    kind: egress                   # egress | fs
    # blocked: <reason + cite the residual-work doc>   # optional; documented gap, not failing
```

Then add `// io:egress-newsource-search` to the test that exercises it. The id
in the marker must match the ledger id exactly.

## Marker placement

- **Go tests:** a `// io:<id>` comment anywhere in a `_test.go` file.
- **Release gate:** carry the id in the test *name* (e.g. `TestEgressWebhook_io_egress_webhook_post`)
  so `ioe2e release-check <junit.xml>` can confirm the test actually PASSED, not
  just that the op was marked.

## Subcommands

```sh
go run ./cmd/ioe2e list                  # union of all ops + covered/missing/ignored/blocked
go run ./cmd/ioe2e check                 # gate: non-zero if any non-ignored, non-blocked op lacks a marker
go run ./cmd/ioe2e release-check j.xml    # fail if a marked op's test did not PASS
```

Exit codes: `0` ok · `1` a check failed · `2` usage / I/O error.
