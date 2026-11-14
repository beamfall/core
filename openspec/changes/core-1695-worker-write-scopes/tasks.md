## 1. Build

- [x] 1.1 Add the five permission kinds, high-risk set and dispatch entries
- [x] 1.2 Add the Host methods, seams, wire methods and audit actions with Console labels
- [x] 1.3 Wire the seams in `internal/app` (store, artwork service, watch-state adapter)
- [x] 1.4 Update the glossary permission list
- [x] 1.5 Bind descendant outbox rows to the caller's causation chain; add the promotion rows to ARCHITECTURE.md section 4

## 2. Prove

- [x] 2.1 Tests: vocabulary sync and dispatch coverage, denial without approval, scope, audit
- [x] 2.2 Sample Worker test creates a collection and sets artwork over the wire
- [x] 2.5 Self-chain test: a Worker's own descendant rows return its token and stop at the budget
- [x] 2.6 Unique descendant keys, a per-Worker ledger share, validate before admitting, release on
      audit failure (Codex review)
- [x] 2.7 Bindings outlive a restamped row's fresh root; store-invalid metadata refused before
      audit; seam failure releases the rows (Codex review)
- [x] 2.3 `openspec validate --all --strict` passes
- [ ] 2.4 wsl preflight passes on the head

## 3. Close

- [ ] 3.1 Repin the plugin SDK release that carries the kinds
- [ ] 3.2 Archive this change after landing, before completing the ticket
