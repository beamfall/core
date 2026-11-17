- [x] `schedule.Spec.DefaultDisabled`, honoured only when Reconcile creates a row
- [x] `playback.PrecomputeLibraryJobs`: budget-bounded, skips ready/generating renditions,
      ineligible Media and Encrypted Libraries without the key
- [x] `internal/app/precompute_schedule.go`: per-Library Spec, registration, sealed predicate
- [ ] Wire it in `internal/app/app.go` (boot registration, sealed predicate, Library
      create/delete/restore observer) at landing
- [x] Tests: seeded disabled and stays disabled across Reconcile; enabled Schedule queues the
      Library's precompute; budget respected; Encrypted Library without its key skipped
- [ ] Console: show which Library a per-Library Schedule belongs to (separate change)
