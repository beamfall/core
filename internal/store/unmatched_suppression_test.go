package store

import (
	"context"
	"testing"
)

// METAPARITY-7 (ADR-0068 §A): the three suppression scopes must be
// independently resettable, and each must carry the provenance the operator
// surface renders beside its own reset control.

// TestSuppressionScopesAreIndependent is the core contract: suppressing all
// three scopes and resetting ONE leaves both siblings suppressed. Resetting the
// candidate scope in particular must not un-suppress the whole record, and
// resetting the record must not restore an individually-ignored candidate.
func TestSuppressionScopesAreIndependent(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	candidateKey := CandidateSuppressionKey("urec-1", "cand-9")
	all := []struct{ scope, key string }{
		{SuppressionScopeSourceRecord, "urec-1"},
		{SuppressionScopeFile, "sha256:file-1"},
		{SuppressionScopeCandidate, candidateKey},
	}
	for _, target := range all {
		if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{
			Scope: target.scope, Key: target.key, RecordID: "urec-1", SourceHandle: "/media/one.mkv",
		}); err != nil {
			t.Fatalf("SuppressUnmatched(%s): %v", target.scope, err)
		}
	}

	for _, reset := range all {
		removed, err := st.ResetUnmatchedSuppression(ctx, reset.scope, reset.key)
		if err != nil || !removed {
			t.Fatalf("ResetUnmatchedSuppression(%s) = %v, %v; want a removal", reset.scope, removed, err)
		}
		for _, other := range all {
			suppressed, err := st.UnmatchedSuppressed(ctx, other.scope, other.key)
			if err != nil {
				t.Fatalf("UnmatchedSuppressed(%s): %v", other.scope, err)
			}
			want := other != reset
			if suppressed != want {
				t.Fatalf("after resetting %s, %s suppressed = %v, want %v", reset.scope, other.scope, suppressed, want)
			}
		}
		// Restore for the next iteration so each reset is tested against a
		// fully-suppressed sibling set.
		if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{
			Scope: reset.scope, Key: reset.key, RecordID: "urec-1",
		}); err != nil {
			t.Fatalf("re-suppress %s: %v", reset.scope, err)
		}
	}
}

// TestCandidateSuppressionIsPerRecord keeps an ignored suggestion scoped to the
// one record it was wrong for: the same candidate stays eligible everywhere else.
func TestCandidateSuppressionIsPerRecord(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{
		Scope:    SuppressionScopeCandidate,
		Key:      CandidateSuppressionKey("urec-1", "cand-9"),
		RecordID: "urec-1",
	}); err != nil {
		t.Fatalf("SuppressUnmatched: %v", err)
	}
	other, err := st.UnmatchedSuppressed(ctx, SuppressionScopeCandidate,
		CandidateSuppressionKey("urec-2", "cand-9"))
	if err != nil {
		t.Fatalf("UnmatchedSuppressed: %v", err)
	}
	if other {
		t.Fatal("suppressing a candidate for one record suppressed it for another record")
	}
}

// TestSuppressUnmatchedIsIdempotent covers the mockup's atomic-retry state: a
// retried suppression refreshes provenance instead of creating a second row, so
// a reset can never leave a duplicate behind.
func TestSuppressUnmatchedIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	for i := 0; i < 2; i++ {
		if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{
			Scope: SuppressionScopeSourceRecord, Key: "urec-1",
			RecordID: "urec-1", SourceHandle: "/media/one.mkv", ActorProfileID: "profile-adult",
		}); err != nil {
			t.Fatalf("SuppressUnmatched #%d: %v", i, err)
		}
	}
	listed, err := st.UnmatchedSuppressions(ctx, "urec-1")
	if err != nil {
		t.Fatalf("UnmatchedSuppressions: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("suppressions = %d, want 1 after a retry", len(listed))
	}
	got := listed[0]
	if got.SourceHandle != "/media/one.mkv" || got.ActorProfileID != "profile-adult" || got.CreatedAt.IsZero() {
		t.Fatalf("provenance = %#v, want actor, handle, and time", got)
	}
	removed, err := st.ResetUnmatchedSuppression(ctx, SuppressionScopeSourceRecord, "urec-1")
	if err != nil || !removed {
		t.Fatalf("reset = %v, %v; want a single removal", removed, err)
	}
	if removed, err = st.ResetUnmatchedSuppression(ctx, SuppressionScopeSourceRecord, "urec-1"); err != nil || removed {
		t.Fatalf("second reset = %v, %v; want no-op", removed, err)
	}
}

// TestSuppressUnmatchedRejectsUnknownScope keeps a typo from creating a
// suppression nothing knows how to reset.
func TestSuppressUnmatchedRejectsUnknownScope(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{Scope: "everything", Key: "urec-1"}); err == nil {
		t.Fatal("SuppressUnmatched accepted an unknown scope")
	}
	if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{Scope: SuppressionScopeFile}); err == nil {
		t.Fatal("SuppressUnmatched accepted an empty key")
	}
}
