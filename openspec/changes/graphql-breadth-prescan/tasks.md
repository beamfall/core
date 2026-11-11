## 1. Pre-parse token budget

- [x] 1.1 Count lexical tokens in `internal/graphquery` and add the `maxDocumentTokens` budget
- [x] 1.2 Reject over-budget documents in the pre-parse check used by `PrepareOperation` and `TopLevelFields`
- [x] 1.3 Tests for counting, the boundary, pre-parser rejection and peak allocation of breadth documents
- [x] 1.4 Extend `FuzzDocumentNesting` to check the count bounds the parser's tokens

## 2. Verify and close

- [ ] 2.1 `go test -race ./internal/graph/ ./internal/graphquery/`
- [ ] 2.2 Deliberate-break proof recorded in `docs/release/evidence/COREAPI-GRAPHQL-BREADTH-PREPARSE-COST-1.md`
- [ ] 2.3 Run `make gate`
- [ ] 2.4 Run `openspec archive graphql-breadth-prescan` after landing, before completing CORE-0371
