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
// is outside the operator's current visibility; the device fields are empty
// for plays recorded before devices were, or whose Device is gone.
type PlayInfo struct {
	ProfileID      string `json:"profileID"`
	ProfileName    string `json:"profileName"`
	MediaID        string `json:"mediaID"`
	Title          string `json:"title,omitempty"`
	LibraryID      string `json:"libraryID,omitempty"`
	LibraryTitle   string `json:"libraryTitle,omitempty"`
	DeviceID       string `json:"deviceID,omitempty"`
	DeviceName     string `json:"deviceName,omitempty"`
	DeviceClient   string `json:"deviceClient,omitempty"`
	PlayMethod     string `json:"playMethod"`
	StartedAt      string `json:"startedAt"`
	WatchedSeconds int    `json:"watchedSeconds"`
	Finished       bool   `json:"finished"`
}

const (
	playHistoryDefaultLimit = 100
	// playStatsMaxRange bounds a playStats date range.
	playStatsMaxRange = 366 * 24 * time.Hour
	// playStatsMaxOffsetMinutes bounds utcOffsetMinutes to real time zones.
	playStatsMaxOffsetMinutes = 14 * 60
)

// sharedPlayFilter reads the filters playHistory and playStats share.
func sharedPlayFilter(req request) store.PlayFilter {
	return store.PlayFilter{
		MediaID:  store.MediaID(trimmedVariable(req, "mediaID")),
		DeviceID: store.DeviceID(trimmedVariable(req, "deviceID")),
		Method:   trimmedVariable(req, "playMethod"),
	}
}

// consolePlayFilter is the Console's filter: every Profile's plays, narrowed
// by the optional profileID and libraryID as well.
func consolePlayFilter(req request) store.PlayFilter {
	filter := sharedPlayFilter(req)
	filter.ProfileID = store.ProfileID(trimmedVariable(req, "profileID"))
	filter.LibraryID = store.LibraryID(trimmedVariable(req, "libraryID"))
	return filter
}

// ownPlayFilter is a viewer's filter: pinned to the caller's Profile and
// dropping plays of Media the caller cannot see.
func ownPlayFilter(scope auth.Scope, req request) store.PlayFilter {
	filter := sharedPlayFilter(req)
	filter.ProfileID = scope.ProfileID
	filter.VisibleOnly = true
	return filter
}

// checkPlayFilter refuses an unknown play method and an own read without a
// Profile, which would otherwise read every Profile's plays.
func checkPlayFilter(filter store.PlayFilter) error {
	if filter.Method != "" && !store.ValidPlayMethod(filter.Method) {
		return clientError("playMethod must be direct, remux, transcode or unknown")
	}
	if filter.VisibleOnly && filter.ProfileID == "" {
		return clientError("a profile is required")
	}
	return nil
}

// playHistory lists plays newest first, filtered by the optional profileID,
// libraryID, mediaID, deviceID, playMethod, since and until variables. With
// ownPlayFilter it is myPlayHistory: the caller's own plays only.
func (h *Handler) playHistory(ctx context.Context, scope auth.Scope, req request, filter store.PlayFilter) ([]PlayInfo, error) {
	if err := checkPlayFilter(filter); err != nil {
		return nil, err
	}
	since, err := parseMyDataInstant(req, "since")
	if err != nil {
		return nil, err
	}
	until, err := parseMyDataInstant(req, "until")
	if err != nil {
		return nil, err
	}
	plays, err := h.store.PlayHistory(ctx, scope.LibraryFilter(), store.PlayHistoryQuery{
		PlayFilter: filter,
		Since:      since,
		Until:      until,
		Limit:      boundedIntVar(req, "limit", playHistoryDefaultLimit, store.PlayHistoryMaxLimit),
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
			DeviceID:       string(p.DeviceID),
			DeviceName:     p.DeviceName,
			DeviceClient:   p.DeviceClient,
			PlayMethod:     p.Method,
			StartedAt:      p.StartedAt.UTC().Format(time.RFC3339),
			WatchedSeconds: p.WatchedSeconds,
			Finished:       p.Finished,
		}
	}
	return out, nil
}

// PlayStatsInfo totals and breaks down the plays over a window of days or a
// date range. WindowDays is zero for a range; Since and Until echo the range
// read.
type PlayStatsInfo struct {
	WindowDays          int                    `json:"windowDays"`
	Since               string                 `json:"since"`
	Until               string                 `json:"until"`
	TotalPlays          int                    `json:"totalPlays"`
	TotalWatchedSeconds int                    `json:"totalWatchedSeconds"`
	Profiles            []PlayStatsProfileInfo `json:"profiles"`
	Items               []PlayStatsItemInfo    `json:"items"`
	Devices             []PlayStatsDeviceInfo  `json:"devices"`
	Methods             []PlayStatsMethodInfo  `json:"methods"`
	Days                []PlayStatsDayInfo     `json:"days"`
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

// PlayStatsDeviceInfo is one Device's plays in the window. An empty deviceID
// gathers plays recorded before devices were.
type PlayStatsDeviceInfo struct {
	DeviceID       string `json:"deviceID"`
	DeviceName     string `json:"deviceName"`
	DeviceClient   string `json:"deviceClient"`
	Plays          int    `json:"plays"`
	WatchedSeconds int    `json:"watchedSeconds"`
}

// PlayStatsMethodInfo is one play method's plays in the window.
type PlayStatsMethodInfo struct {
	PlayMethod     string `json:"playMethod"`
	Plays          int    `json:"plays"`
	WatchedSeconds int    `json:"watchedSeconds"`
}

// PlayStatsDayInfo is one local day's plays; day is YYYY-MM-DD in the
// caller's utcOffsetMinutes. Days without plays are omitted.
type PlayStatsDayInfo struct {
	Day            string `json:"day"`
	Plays          int    `json:"plays"`
	WatchedSeconds int    `json:"watchedSeconds"`
}

// playStatsRange reads the range a playStats read covers: windowDays (7, 30
// or 90) back from now, or since up to until (now when absent), at most
// playStatsMaxRange long.
func playStatsRange(req request, now time.Time) (int, time.Time, time.Time, error) {
	days := intVar(req, "windowDays")
	since, err := parseMyDataInstant(req, "since")
	if err != nil {
		return 0, time.Time{}, time.Time{}, err
	}
	until, err := parseMyDataInstant(req, "until")
	if err != nil {
		return 0, time.Time{}, time.Time{}, err
	}
	if days != 0 {
		if days != 7 && days != 30 && days != 90 {
			return 0, time.Time{}, time.Time{}, clientError("windowDays must be 7, 30 or 90")
		}
		if !since.IsZero() || !until.IsZero() {
			return 0, time.Time{}, time.Time{}, clientError("windowDays and since must not both be set")
		}
		return days, now.AddDate(0, 0, -days), now, nil
	}
	if since.IsZero() {
		return 0, time.Time{}, time.Time{}, clientError("windowDays or since is required")
	}
	if until.IsZero() {
		until = now
	}
	if !until.After(since) {
		return 0, time.Time{}, time.Time{}, clientError("until must be after since")
	}
	if until.Sub(since) > playStatsMaxRange {
		return 0, time.Time{}, time.Time{}, clientError("the range must be at most 366 days")
	}
	return 0, since, until, nil
}

// playStats totals and breaks down the plays in the range playStatsRange
// reads, filtered like playHistory. With ownPlayFilter it is myPlayStats: the
// caller's own plays only.
func (h *Handler) playStats(ctx context.Context, scope auth.Scope, req request, filter store.PlayFilter) (PlayStatsInfo, error) {
	if err := checkPlayFilter(filter); err != nil {
		return PlayStatsInfo{}, err
	}
	days, since, until, err := playStatsRange(req, time.Now().UTC())
	if err != nil {
		return PlayStatsInfo{}, err
	}
	offset := intVar(req, "utcOffsetMinutes")
	if offset < -playStatsMaxOffsetMinutes || offset > playStatsMaxOffsetMinutes {
		return PlayStatsInfo{}, clientError("utcOffsetMinutes must be between -840 and 840")
	}
	stats, err := h.store.PlayStats(ctx, scope.LibraryFilter(), store.PlayStatsQuery{
		PlayFilter: filter,
		Since:      since,
		Until:      until,
		UTCOffset:  time.Duration(offset) * time.Minute,
	})
	if err != nil {
		return PlayStatsInfo{}, err
	}
	info := PlayStatsInfo{
		WindowDays:          days,
		Since:               since.UTC().Format(time.RFC3339),
		Until:               until.UTC().Format(time.RFC3339),
		TotalPlays:          stats.Plays,
		TotalWatchedSeconds: stats.WatchedSeconds,
		Profiles:            make([]PlayStatsProfileInfo, len(stats.Profiles)),
		Items:               make([]PlayStatsItemInfo, len(stats.Items)),
		Devices:             make([]PlayStatsDeviceInfo, len(stats.Devices)),
		Methods:             make([]PlayStatsMethodInfo, len(stats.Methods)),
		Days:                make([]PlayStatsDayInfo, len(stats.Days)),
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
	for i, d := range stats.Devices {
		info.Devices[i] = PlayStatsDeviceInfo{
			DeviceID: string(d.DeviceID), DeviceName: d.DeviceName, DeviceClient: d.DeviceClient,
			Plays: d.Plays, WatchedSeconds: d.WatchedSeconds,
		}
	}
	for i, m := range stats.Methods {
		info.Methods[i] = PlayStatsMethodInfo{PlayMethod: m.Method, Plays: m.Plays, WatchedSeconds: m.WatchedSeconds}
	}
	for i, d := range stats.Days {
		info.Days[i] = PlayStatsDayInfo{Day: d.Day, Plays: d.Plays, WatchedSeconds: d.WatchedSeconds}
	}
	return info, nil
}
