package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// ImportStatusInfo keeps the existing job-status shape and adds only the safe
// review fields for unmatched records from that run.
type ImportStatusInfo struct {
	JobInfo
	UnmatchedRecords []ImportUnmatchedRecordInfo `json:"unmatchedRecords"`
}

// ImportUnmatchedRecordInfo deliberately omits SourceHandle and Library
// identity: either may reveal a raw path or restricted Library name.
type ImportUnmatchedRecordInfo struct {
	ID             string  `json:"id"`
	ImporterID     string  `json:"importerID,omitempty"`
	CandidateID    string  `json:"candidateID,omitempty"`
	CandidateTitle string  `json:"candidateTitle,omitempty"`
	Confidence     float64 `json:"confidence"`
	FailureReason  string  `json:"failureReason,omitempty"`
	QueuedAt       string  `json:"queuedAt,omitempty"`
}

func (h *Handler) importStatusInfo(ctx context.Context, scope auth.Scope, job store.Job) (ImportStatusInfo, error) {
	info := ImportStatusInfo{
		JobInfo:          h.jobInfo(ctx, scope, job),
		UnmatchedRecords: []ImportUnmatchedRecordInfo{},
	}
	if !scope.Revealed {
		return info, nil
	}
	records, err := h.store.UnmatchedRecordsForImportRun(ctx, string(job.ID), scope.LibraryFilter())
	if err != nil {
		return ImportStatusInfo{}, err
	}
	for _, record := range records {
		item := ImportUnmatchedRecordInfo{
			ID:             record.ID,
			ImporterID:     record.ImporterID,
			CandidateID:    record.CandidateID,
			CandidateTitle: record.CandidateTitle,
			Confidence:     record.Confidence,
			FailureReason:  record.FailureReason,
		}
		if !record.QueuedAt.IsZero() {
			item.QueuedAt = record.QueuedAt.UTC().Format(time.RFC3339)
		}
		info.UnmatchedRecords = append(info.UnmatchedRecords, item)
	}
	return info, nil
}
