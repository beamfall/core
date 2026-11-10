## 1. Pre-parse scan

- [ ] 1.1 Add the lexical nesting scan and `maxValueDepth` budget in `internal/graphquery`
- [ ] 1.2 Run it before `parser.ParseQuery` in `PrepareOperation` and `TopLevelFields`
- [ ] 1.3 Tests for limits, string and comment skipping, pre-parser rejection and peak allocation
- [ ] 1.4 `FuzzDocumentNesting`, listed in `testing/fuzz-targets.json`

## 2. Verify and close

- [ ] 2.1 `go test -race ./internal/graph/ ./internal/graphquery/`
- [ ] 2.2 Deliberate-break proof recorded in `docs/release/evidence/COREAPI-AUDIT-0822-1.md`
- [ ] 2.3 Run `make gate`
- [ ] 2.4 Run `openspec archive graphql-nesting-prescan` after landing, before completing CORE-0052
