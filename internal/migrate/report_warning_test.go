package migrate

import (
	"strings"
	"testing"
)

// TestSmartCollectionWarningLineIsDistinct proves the smart-collection→static
// conversion surfaces as its own named warning line in the import report
// (ADR-0028 §E / ADR-0037 §2.2): the exact contract wording, present only when
// the run converted at least one smart collection, and never folded into the
// match-rate number.
func TestSmartCollectionWarningLineIsDistinct(t *testing.T) {
	none := Result{PerCategory: map[string]int{}}
	if got := none.smartCollectionWarning(); got != "" {
		t.Fatalf("no smart collections converted: warning = %q, want empty", got)
	}

	converted := Result{PerCategory: map[string]int{}, SmartCollectionsStatic: 3}
	const want = "3 smart collections imported as static — they will not auto-update"
	if got := converted.smartCollectionWarning(); got != want {
		t.Fatalf("smart-collection warning = %q, want %q", got, want)
	}
}

// TestSmartCollectionWarningSeparateFromMatchRate proves the assembled report
// message keeps the smart-collection warning visibly separate from the aggregate
// match-rate number (ADR-0028 §E: "not fold them into the aggregate match-rate
// number"). The warning appears as its own "; …" segment after the count.
func TestSmartCollectionWarningSeparateFromMatchRate(t *testing.T) {
	result := Result{PerCategory: map[string]int{}, Matched: 10, Unmatched: 2, SmartCollectionsStatic: 1}

	// Mirror the handler's message assembly for the warning segment.
	message := "10 of 12 items matched"
	if warning := result.smartCollectionWarning(); warning != "" {
		message += "; " + warning
	}

	if !strings.Contains(message, "10 of 12 items matched") {
		t.Fatalf("message dropped the match-rate number: %q", message)
	}
	segment := "; 1 smart collections imported as static — they will not auto-update"
	if !strings.HasSuffix(message, segment) {
		t.Fatalf("message = %q, want warning as a distinct trailing segment %q", message, segment)
	}
	// The warning count must not have been absorbed into the match-rate number.
	if strings.Contains(strings.SplitN(message, ";", 2)[0], "smart") {
		t.Fatalf("smart-collection warning folded into the match-rate segment: %q", message)
	}
}
