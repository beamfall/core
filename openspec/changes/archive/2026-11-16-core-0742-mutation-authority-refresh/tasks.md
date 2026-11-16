## 1. Server

- [x] 1.1 Re-resolve the session scope after each mutation field and before the next field of
      the same operation; admin API tokens are not re-resolved
- [x] 1.2 Re-authorize the next field (manager role, Admin Session) against the refreshed scope;
      a refusal or an unresolvable session stops the operation before that field
- [x] 1.3 Tests: `switchProfile` then `reveal` with the old Profile's PIN is refused and the
      session stays unrevealed on the new Profile (`TestAuditCrossProfileRevealBatch`); a later
      write uses the new Profile's Library and Reveal scope; a later Admin Session field is
      refused after the switch; `reveal` then a Reveal-gated field still works
