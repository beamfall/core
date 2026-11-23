## 1. Server (CORE-3309)

- [x] 1.1 OpenSpec proposal, spec delta, and tasks (`openspec validate --all --strict`).
- [x] 1.2 Events migration `0030_renderer_session_control.sql` and `schema_freeze.go` bump.
- [x] 1.3 Store revision/command-ledger writer (`ControlRendererSession`), read helper (`RendererSessionRevisions`), and revision bump in `AppendSessionControlEvent` / `AppendSessionControlEventWithActivity`.
- [x] 1.4 GraphQL `controlRendererSession` mutation, `ProfileSessionInfo.Revision` in `profileSessions`, error codes, registry/argument registrations, and `controlSession` ADR-0240 comment.
- [x] 1.5 Regenerate wire goldens (`make regen-wire-goldens`) and update conformance store inventory.
- [ ] 1.6 Focused store, graph, and conformance tests; `gofmt -l`, `golangci-lint run`, and `make gate` before landing; archive with `openspec archive core-3309-renderer-session-control` after landing.
