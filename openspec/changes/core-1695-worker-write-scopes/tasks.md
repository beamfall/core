## 1. Build

- [ ] 1.1 Add the five permission kinds, high-risk set and dispatch entries
- [ ] 1.2 Add the Host methods, seams, wire methods and audit actions with Console labels
- [ ] 1.3 Wire the seams in `internal/app` (store, artwork service, watch-state adapter)
- [ ] 1.4 Update the glossary permission list

## 2. Prove

- [ ] 2.1 Tests: vocabulary sync and dispatch coverage, denial without approval, scope, audit
- [ ] 2.2 Sample Worker test creates a collection and sets artwork over the wire
- [ ] 2.3 `openspec validate --all --strict` passes
- [ ] 2.4 wsl preflight passes on the head

## 3. Close

- [ ] 3.1 Repin the plugin SDK release that carries the kinds
- [ ] 3.2 Archive this change after landing, before completing the ticket
