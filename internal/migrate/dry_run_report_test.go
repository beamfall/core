package migrate

import "testing"

func TestDryRunItemReportRejectsIncompleteEvidence(t *testing.T) {
	tests := []struct {
		name   string
		result Result
	}{
		{
			name: "missing disposition",
			result: Result{
				dryRun:          true,
				dryRunItemCount: 1,
				dryRunItems:     []dryRunItemDisposition{{Index: 1}},
			},
		},
		{
			name:   "missing item",
			result: Result{dryRun: true, dryRunItemCount: 1},
		},
		{
			name: "skip without reason",
			result: Result{
				dryRun:          true,
				dryRunItemCount: 1,
				dryRunItems:     []dryRunItemDisposition{{Index: 1, Disposition: dryRunSkip}},
			},
		},
		{
			name: "invented match taxonomy",
			result: Result{
				dryRun:          true,
				dryRunItemCount: 1,
				dryRunItems: []dryRunItemDisposition{{
					Index:            1,
					Disposition:      dryRunWouldApply,
					MatchDisposition: "exact-id",
				}},
			},
		},
		{
			name: "unmatched item would apply",
			result: Result{
				dryRun:          true,
				dryRunItemCount: 1,
				dryRunItems: []dryRunItemDisposition{{
					Index:            1,
					Disposition:      dryRunWouldApply,
					MatchDisposition: matchDispositionUnmatched,
				}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.result.dryRunItemReport(); err == nil {
				t.Fatal("dry-run report accepted incomplete item evidence")
			}
		})
	}
}
