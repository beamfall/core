package migrate

import (
	"fmt"
	"strings"
)

const (
	dryRunWouldApply       = "would-apply"
	dryRunWouldConvert     = "would-convert-static"
	dryRunSkip             = "skip"
	dryRunReasonCategory   = "category-not-selected"
	dryRunReasonUnresolved = "unresolved-user"

	matchDispositionExternalID        = "external-id"
	matchDispositionFilePath          = "file-path"
	matchDispositionFilenameHeuristic = "filename-heuristic"
	matchDispositionUnmatched         = "unmatched"
)

type dryRunItemDisposition struct {
	Index            int
	Disposition      string
	MatchDisposition string
	Reason           string
}

func (r *Result) recordDryRunItem(index int, disposition, matchDisposition, reason string) {
	if !r.dryRun {
		return
	}
	r.dryRunItems = append(r.dryRunItems, dryRunItemDisposition{
		Index:            index,
		Disposition:      disposition,
		MatchDisposition: matchDisposition,
		Reason:           reason,
	})
}

func (r Result) dryRunItemReport() (string, error) {
	if !r.dryRun {
		return "", nil
	}
	if len(r.dryRunItems) != r.dryRunItemCount {
		return "", fmt.Errorf("migrate: dry-run report has %d item dispositions for %d source items", len(r.dryRunItems), r.dryRunItemCount)
	}
	if len(r.dryRunItems) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(r.dryRunItems))
	for _, item := range r.dryRunItems {
		if item.Disposition == "" {
			return "", fmt.Errorf("migrate: dry-run item %d is missing a disposition", item.Index)
		}
		if item.Disposition == dryRunSkip && item.Reason == "" {
			return "", fmt.Errorf("migrate: dry-run item %d skip is missing a reason", item.Index)
		}
		if !validMatchDisposition(item.MatchDisposition) {
			return "", fmt.Errorf("migrate: dry-run item %d has invalid match disposition %q", item.Index, item.MatchDisposition)
		}
		if item.MatchDisposition == matchDispositionUnmatched && item.Disposition != dryRunSkip {
			return "", fmt.Errorf("migrate: dry-run item %d is unmatched but not skipped", item.Index)
		}
		disposition := item.Disposition
		var details []string
		if item.MatchDisposition != "" {
			details = append(details, "match="+item.MatchDisposition)
		}
		if item.Reason != "" {
			details = append(details, "reason="+item.Reason)
		}
		if len(details) > 0 {
			disposition += "(" + strings.Join(details, ",") + ")"
		}
		parts = append(parts, fmt.Sprintf("#%d=%s", item.Index, disposition))
	}
	return "items: " + strings.Join(parts, ", "), nil
}

func validMatchDisposition(disposition string) bool {
	switch disposition {
	case "", matchDispositionExternalID, matchDispositionFilePath, matchDispositionFilenameHeuristic, matchDispositionUnmatched:
		return true
	default:
		return false
	}
}

func reportMatchDisposition(method string) string {
	switch method {
	case matchMethodExternalID:
		return matchDispositionExternalID
	case matchMethodPath:
		return matchDispositionFilePath
	case matchMethodFilename:
		return matchDispositionFilenameHeuristic
	default:
		return ""
	}
}
