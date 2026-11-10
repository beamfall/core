## 1. Core pilot

- [x] 1.1 Run `openspec init` and write project context in `openspec/config.yaml`
- [x] 1.2 Add the OpenSpec section to `AGENTS.md` and ignore `.agents/`
- [x] 1.3 File the ticket in `queue:core:main` and record its ID in `proposal.md`
- [ ] 1.4 Owner accepts this proposal
- [ ] 1.5 Wire `openspec validate --all --strict` into `make gate-preflight`

## 2. Rollout

- [ ] 2.1 Apply the same recipe to each active repository through a worktree and a pull request
- [ ] 2.2 Add an OpenSpec row to rule 2 of the workspace `DOCUMENTATION-GOVERNANCE.md`
- [ ] 2.3 Take one real feature change from proposal to archive in core

## 3. Close

- [ ] 3.1 Run `make gate`
- [ ] 3.2 Archive this change with `openspec archive adopt-openspec`
