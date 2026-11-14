package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/hostmetrics"
	"github.com/beamfall/core/internal/store"
)

// Console dashboard reads: host resource samples, the play history and the
// top-profile / most-played rankings. All three are Admin-Session reads that
// answer from this server alone — the samples from an in-memory ring, the
// history and rankings from the engagement event log.

// HostMetricsSource is the in-memory host sampler. Defined at the consumer;
// the app passes the running hostmetrics.Sampler.
type HostMetricsSource interface {
	Interval() time.Duration
	Samples() []hostmetrics.Sample
}

// HostMetricsInfo is the retained window of host samples, oldest first.
type HostMetricsInfo struct {
	IntervalSeconds int                    `json:"intervalSeconds"`
	Samples         []HostMetricSampleInfo `json:"samples"`
}

// HostMetricSampleInfo is one host sample. CPUPercent is meaningful only when
// CPUKnown is true; the network rates are this server's own traffic.
type HostMetricSampleInfo struct {
	At                     string  `json:"at"`
	CPUPercent             float64 `json:"cpuPercent"`
	CPUKnown               bool    `json:"cpuKnown"`
	MemoryUsedBytes        float64 `json:"memoryUsedBytes"`
	MemoryTotalBytes       float64 `json:"memoryTotalBytes"`
	SentBytesPerSecond     float64 `json:"sentBytesPerSecond"`
	ReceivedBytesPerSecond float64 `json:"receivedBytesPerSecond"`
}

func (h *Handler) hostMetricsInfo() HostMetricsInfo {
	info := HostMetricsInfo{Samples: []HostMetricSampleInfo{}}
	if h.hostMetrics == nil {
		return info
	}
	info.IntervalSeconds = int(h.hostMetrics.Interval() / time.Second)
	samples := h.hostMetrics.Samples()
	info.Samples = make([]HostMetricSampleInfo, len(samples))
	for i, s := range samples {
		info.Samples[i] = HostMetricSampleInfo{
			At:                     s.At.UTC().Format(time.RFC3339),
			CPUPercent:             s.CPUPercent,
			CPUKnown:               s.CPUKnown,
			MemoryUsedBytes:        float64(s.MemoryUsedBytes),
			MemoryTotalBytes:       float64(s.MemoryTotalBytes),
			SentBytesPerSecond:     s.SentBytesPerSecond,
			ReceivedBytesPerSecond: s.ReceivedBytesPerSecond,
		}
	}
	return info
}

// PlayInfo is one play on the wire. Title and library are empty when the Media
// is outside the operator's current visibility.
type PlayInfo struct {
	ProfileID      string `json:"profileID"`
	ProfileName    string `json:"profileName"`
	MediaID        string `json:"mediaID"`
	Title          string `json:"title,omitempty"`
	LibraryID      string `json:"libraryID,omitempty"`
	LibraryTitle   string `json:"libraryTitle,omitempty"`
	StartedAt      string `json:"startedAt"`
	WatchedSeconds int    `json:"watchedSeconds"`
	Finished       bool   `json:"finished"`
}

const playHistoryDefaultLimit = 100

// playHistory lists plays newest first, filtered by the optional profileID,
// libraryID, since and until variables.
func (h *Handler) playHistory(ctx context.Context, scope auth.Scope, req request) ([]PlayInfo, error) {
	since, err := parseMyDataInstant(req, "since")
	if err != nil {
		return nil, err
	}
	until, err := parseMyDataInstant(req, "until")
	if err != nil {
		return nil, err
	}
	plays, err := h.store.PlayHistory(ctx, scope.LibraryFilter(), store.PlayHistoryQuery{
		ProfileID: store.ProfileID(trimmedVariable(req, "profileID")),
		LibraryID: store.LibraryID(trimmedVariable(req, "libraryID")),
		Since:     since,
		Until:     until,
		Limit:     boundedIntVar(req, "limit", playHistoryDefaultLimit, store.PlayHistoryMaxLimit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]PlayInfo, len(plays))
	for i, p := range plays {
		out[i] = PlayInfo{
			ProfileID:      string(p.ProfileID),
			ProfileName:    p.ProfileName,
			MediaID:        string(p.MediaID),
			Title:          p.Title,
			LibraryID:      string(p.LibraryID),
			LibraryTitle:   p.LibraryTitle,
			StartedAt:      p.StartedAt.UTC().Format(time.RFC3339),
			WatchedSeconds: p.WatchedSeconds,
			Finished:       p.Finished,
		}
	}
	return out, nil
}

// PlayStatsInfo ranks profiles and Media by plays over a window of days.
type PlayStatsInfo struct {
	WindowDays int                    `json:"windowDays"`
	Profiles   []PlayStatsProfileInfo `json:"profiles"`
	Items      []PlayStatsItemInfo    `json:"items"`
}

// PlayStatsProfileInfo is one profile's plays in the window.
type PlayStatsProfileInfo struct {
	ProfileID      string `json:"profileID"`
	ProfileName    string `json:"profileName"`
	Plays          int    `json:"plays"`
	WatchedSeconds int    `json:"watchedSeconds"`
}

// PlayStatsItemInfo is one Media's plays in the window.
type PlayStatsItemInfo struct {
	MediaID        string `json:"mediaID"`
	Title          string `json:"title"`
	LibraryID      string `json:"libraryID"`
	Plays          int    `json:"plays"`
	WatchedSeconds int    `json:"watchedSeconds"`
}

// playStats ranks the window named by the windowDays variable: 7, 30 or 90.
func (h *Handler) playStats(ctx context.Context, scope auth.Scope, req request) (PlayStatsInfo, error) {
	days := intVar(req, "windowDays")
	if days != 7 && days != 30 && days != 90 {
		return PlayStatsInfo{}, clientError("windowDays must be 7, 30 or 90")
	}
	since := time.Now().UTC().AddDate(0, 0, -days)
	stats, err := h.store.PlayStats(ctx, scope.LibraryFilter(), since)
	if err != nil {
		return PlayStatsInfo{}, err
	}
	info := PlayStatsInfo{
		WindowDays: days,
		Profiles:   make([]PlayStatsProfileInfo, len(stats.Profiles)),
		Items:      make([]PlayStatsItemInfo, len(stats.Items)),
	}
	for i, p := range stats.Profiles {
		info.Profiles[i] = PlayStatsProfileInfo{
			ProfileID: string(p.ProfileID), ProfileName: p.ProfileName,
			Plays: p.Plays, WatchedSeconds: p.WatchedSeconds,
		}
	}
	for i, item := range stats.Items {
		info.Items[i] = PlayStatsItemInfo{
			MediaID: string(item.MediaID), Title: item.Title, LibraryID: string(item.LibraryID),
			Plays: item.Plays, WatchedSeconds: item.WatchedSeconds,
		}
	}
	return info, nil
}
