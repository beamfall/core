# Canonical-gate control for the GCR-022-B seeded-red harness. This is the
# non-recursive control suite: the production canary's gate leg calls
# `make gate`, while this suite stubs that call and verifies the harness itself.
.PHONY: seeded-red-canary-controls

seeded-red-canary-controls:
	@tools/gate/seeded_red_canary_test.sh

# Mutation testing on the named security/crypto packages (ADR-0121 §5, TEST-CORE-6).
# Runs gremlins (NOT ./...) on docs/testing/mutation-budget.json's package set and
# enforces the ratcheting surviving-mutant budget. ADVISORY nightly tier only
# (CI: mutation-nightly.yml) — deliberately NOT in `make gate` (too slow per merge,
# and "release-ready" must not rest on a tier the gate runs). Requires `gremlins`:
# go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0
mutation:
	@script/mutation-test.sh named

# The `-race` tiers (ADR-0121 §3 remediation, TEST-CORE-1).
# race-short: the merge-blocking race subset (`-race -short`, excludes the slow
#   graph/store packages); included in `make gate-race`. race-full: the nightly
#   `-race ./...` over everything with raised timeout + capped parallelism.
race-short:
	@python3 tools/gate/slow_orchestration_cache.py run --root . --cache race-short -- \
		script/race-test.sh short

race-full:
	@script/race-test.sh full

# Prove the race detector is live: runs the deliberately-racy fixture under
# `-race` with the proof env var set. EXPECTED to fail (the detector REDs); this
# target asserts that failure and is itself green only when the detector fires.
#
# "Non-zero exit" is NOT proof: a compile error, a missing C toolchain, a panic,
# or an unrelated assertion failure all exit non-zero while proving nothing about
# the detector. The output is therefore kept and classified, in precedence order,
# so each rejection reports its true cause (a build failure also exits 1).
# Regression-tested by tools/gate/gate_rerun_test.sh.
race-prove:
	@out="$$(BEAMFALL_PROVE_RACE=1 CGO_ENABLED=1 go test -race ./internal/racefixture 2>&1)"; \
	status=$$?; \
	if printf '%s\n' "$$out" | grep -Eq 'build failed|cannot find package|no Go files|no required module provides'; then \
		printf '%s\n' "$$out" >&2; \
		echo "race-prove: FAIL — build/toolchain failure, not a race proof" >&2; exit 1; \
	fi; \
	if [ "$$status" -eq 0 ]; then \
		echo "race-prove: FAIL — the racy fixture did NOT red under -race; detector is not live" >&2; exit 1; \
	fi; \
	if [ "$$status" -ne 1 ]; then \
		printf '%s\n' "$$out" >&2; \
		echo "race-prove: FAIL — expected the test-failure exit 1, got $$status (build/toolchain error, not a race)" >&2; exit 1; \
	fi; \
	if ! printf '%s\n' "$$out" | grep -q 'WARNING: DATA RACE'; then \
		printf '%s\n' "$$out" >&2; \
		echo "race-prove: FAIL — red without a WARNING: DATA RACE report; that is not detector proof" >&2; exit 1; \
	fi; \
	if ! printf '%s\n' "$$out" | grep -q 'race detected during execution of test'; then \
		printf '%s\n' "$$out" >&2; \
		echo "race-prove: FAIL — no 'race detected during execution of test' marker; the failure was not the race" >&2; exit 1; \
	fi; \
	echo "race-prove: ok — race detector flagged the deliberate race (RED as intended)"
