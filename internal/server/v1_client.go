package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

// The /v1 client surface: the operations a client app (first- or third-party)
// uses with the enrolled device Session it holds after pairing. Each response
// is a frozen projection of the internal shape, so the internal playback and
// engagement types can keep changing without breaking a published client.

// Limits on the arrays the playback-info projection returns. The decision
// produces far fewer entries than these in practice; the limits make the
// contract's list bounds explicit.
const (
	v1PlaybackStreamLimit       = 16
	v1PlaybackRenditionLimit    = 16
	v1PlaybackCaptionTrackLimit = 64
)

// V1ProgressRecorder records a playback position for the request's Profile,
// applying the same resume policy and watched transition as the first-party
// clients. It is implemented outside the server package (interface at the
// consumer) and wired at the composition root.
type V1ProgressRecorder interface {
	RecordPlaybackProgress(ctx context.Context, scope auth.Scope, report store.PlaybackProgress, finished bool) (store.PlaybackProgress, error)
}

// SetV1ProgressRecorder wires PUT /v1/media/{mediaID}/progress. Without it the
// route reports the surface unavailable (503).
func (s *Server) SetV1ProgressRecorder(recorder V1ProgressRecorder) {
	s.v1Progress = recorder
}

// v1Caller resolves a request on a route that accepts both a scoped developer
// key holding required and an enrolled device Session. It writes 401 for an
// unresolved credential and 403 for any other credential, and returns false
// when it has written a response.
func (s *Server) v1Caller(w http.ResponseWriter, r *http.Request, required store.APIScope) (auth.Scope, bool) {
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeScopeError(w, r, err, "invalid credential")
		return auth.Scope{}, false
	}
	if scope.IsEnrolledDeviceSession() || scope.HasAPIScope(required) {
		return scope, true
	}
	writeError(w, r, http.StatusForbidden, "API key missing required scope: "+string(required))
	return auth.Scope{}, false
}

// v1DeviceSession resolves a request on a route that accepts only an enrolled
// device Session. Scoped keys and admin API tokens get 403.
func (s *Server) v1DeviceSession(w http.ResponseWriter, r *http.Request) (auth.Scope, bool) {
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeScopeError(w, r, err, "invalid credential")
		return auth.Scope{}, false
	}
	if !scope.IsEnrolledDeviceSession() {
		writeError(w, r, http.StatusForbidden, "route requires an enrolled device session")
		return auth.Scope{}, false
	}
	return scope, true
}

// v1MediaVisible reports whether the caller's Profile can play mediaID. Hidden
// and unknown Media both answer 404, so a client route is never an existence
// oracle for Media the Profile cannot see. It returns false when it has
// written a response.
func (s *Server) v1MediaVisible(w http.ResponseWriter, r *http.Request, scope auth.Scope, mediaID store.MediaID) bool {
	if _, err := s.v1.Media(r.Context(), mediaID, scope.PlaybackFilter()); err != nil {
		writeError(w, r, http.StatusNotFound, "media not found")
		return false
	}
	return true
}

// v1CapabilityProfile is the playback capability a client declares. It is the
// stable subset of the internal capability profile.
type v1CapabilityProfile struct {
	Client          string            `json:"client"`
	Containers      []string          `json:"containers"`
	DirectCodecs    []string          `json:"directCodecs"`
	VideoCodecs     []v1VideoCodecCap `json:"videoCodecs,omitempty"`
	AudioCodecs     []v1AudioCodecCap `json:"audioCodecs,omitempty"`
	HDRFormats      []string          `json:"hdrFormats,omitempty"`
	SubtitleFormats []string          `json:"subtitleFormats,omitempty"`
}

type v1VideoCodecCap struct {
	Codec      string   `json:"codec"`
	Profiles   []string `json:"profiles,omitempty"`
	MaxHeight  int      `json:"maxHeight,omitempty"`
	MaxBitrate int      `json:"maxBitrate,omitempty"`
	MaxLevel   int      `json:"maxLevel,omitempty"`
}

type v1AudioCodecCap struct {
	Codec       string `json:"codec"`
	MaxChannels int    `json:"maxChannels,omitempty"`
}

type v1PlaybackInfoRequest struct {
	CapabilityProfile v1CapabilityProfile `json:"capabilityProfile"`
}

type v1PlaybackInfoResponse struct {
	MediaID             string             `json:"mediaID"`
	Decision            string             `json:"decision"`
	Reason              string             `json:"reason,omitempty"`
	StreamTokenRequired bool               `json:"streamTokenRequired"`
	Streams             []v1PlaybackStream `json:"streams"`
	DurationSeconds     int                `json:"durationSeconds"`
	PositionSeconds     int                `json:"positionSeconds,omitempty"`
	CaptionTracks       []v1CaptionTrack   `json:"captionTracks,omitempty"`
}

type v1PlaybackStream struct {
	ID         string        `json:"id"`
	Kind       string        `json:"kind"`
	URL        string        `json:"url,omitempty"`
	Token      string        `json:"token,omitempty"`
	ExpiresAt  string        `json:"expiresAt,omitempty"`
	Renditions []v1Rendition `json:"renditions,omitempty"`
}

type v1Rendition struct {
	ID        string `json:"id"`
	Height    int    `json:"height,omitempty"`
	Bandwidth int    `json:"bandwidth"`
}

type v1CaptionTrack struct {
	ID     string `json:"id"`
	URL    string `json:"url,omitempty"`
	Format string `json:"format"`
	// StreamIndex selects a subtitle stream muxed in the media; a track has
	// either a URL (a caption file) or a StreamIndex.
	StreamIndex *int   `json:"streamIndex,omitempty"`
	Language    string `json:"language,omitempty"`
	Forced      bool   `json:"forced,omitempty"`
	SDH         bool   `json:"sdh,omitempty"`
}

func (p v1CapabilityProfile) internal() playback.CapabilityProfile {
	out := playback.CapabilityProfile{
		Client:          p.Client,
		Containers:      p.Containers,
		DirectCodecs:    p.DirectCodecs,
		HDRFormats:      p.HDRFormats,
		SubtitleFormats: p.SubtitleFormats,
	}
	for _, c := range p.VideoCodecs {
		out.VideoCodecs = append(out.VideoCodecs, playback.VideoCodecCap{
			Codec: c.Codec, Profiles: c.Profiles, MaxHeight: c.MaxHeight, MaxBitrate: c.MaxBitrate, MaxLevel: c.MaxLevel,
		})
	}
	for _, c := range p.AudioCodecs {
		out.AudioCodecs = append(out.AudioCodecs, playback.AudioCodecCap{Codec: c.Codec, MaxChannels: c.MaxChannels})
	}
	return out
}

func v1PlaybackInfoFrom(resp playback.InfoResponse) v1PlaybackInfoResponse {
	out := v1PlaybackInfoResponse{
		MediaID:             resp.MediaID,
		Decision:            string(resp.Decision),
		Reason:              resp.Reason,
		StreamTokenRequired: resp.StreamTokenRequired,
		Streams:             make([]v1PlaybackStream, 0, min(len(resp.Streams), v1PlaybackStreamLimit)),
		DurationSeconds:     resp.Timeline.DurationSeconds,
		PositionSeconds:     resp.Timeline.PositionSeconds,
	}
	for _, stream := range resp.Streams[:min(len(resp.Streams), v1PlaybackStreamLimit)] {
		item := v1PlaybackStream{
			ID: stream.ID, Kind: stream.Kind, URL: stream.URL, Token: stream.Token, ExpiresAt: stream.ExpiresAt,
		}
		for _, rendition := range stream.Renditions[:min(len(stream.Renditions), v1PlaybackRenditionLimit)] {
			item.Renditions = append(item.Renditions, v1Rendition{ID: rendition.ID, Height: rendition.Height, Bandwidth: rendition.Bandwidth})
		}
		out.Streams = append(out.Streams, item)
	}
	for _, track := range resp.CaptionTracks[:min(len(resp.CaptionTracks), v1PlaybackCaptionTrackLimit)] {
		out.CaptionTracks = append(out.CaptionTracks, v1CaptionTrack{
			ID: track.ID, URL: track.URL, Format: track.Format, StreamIndex: track.StreamIndex,
			Language: track.Language, Forced: track.Forced, SDH: track.SDH,
		})
	}
	return out
}

// v1PlaybackInfo answers POST /v1/media/{mediaID}/playback-info: the playback
// decision for the declared capability profile, with stream URLs and tokens.
// It runs the same decision as the first-party clients, as the device
// Session's Profile.
func (s *Server) v1PlaybackInfo(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	scope, ok := s.v1DeviceSession(w, r)
	if !ok {
		return
	}
	if s.v1 == nil || s.playback == nil {
		writeError(w, r, http.StatusServiceUnavailable, "playback unavailable")
		return
	}
	mediaID := store.MediaID(r.PathValue("mediaID"))
	if mediaID == "" {
		writeError(w, r, http.StatusBadRequest, "mediaID is required")
		return
	}
	var req v1PlaybackInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if !s.v1MediaVisible(w, r, scope, mediaID) {
		return
	}
	resp, err := s.playback.Info(r.Context(), scope, playback.InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: req.CapabilityProfile.internal(),
		Foreground:        true,
		RemoteClient:      !requestIsLAN(r),
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, v1PlaybackInfoFrom(resp))
	case errors.Is(err, playback.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "media not found")
	case errors.Is(err, playback.ErrInvalidRequest), errors.Is(err, playback.ErrInvalidSelector):
		writeError(w, r, http.StatusBadRequest, "invalid playback request")
	default:
		writeErrorCause(w, r, http.StatusInternalServerError, "playback info unavailable", err)
	}
}

type v1ProgressRequest struct {
	// PositionSeconds is required: a pointer tells a missing or null value
	// from 0, so an empty report cannot reset the resume point.
	PositionSeconds *int `json:"positionSeconds"`
	DurationSeconds int  `json:"durationSeconds,omitempty"`
	Finished        bool `json:"finished,omitempty"`
	// EventTimeMS is the client's clock, in Unix milliseconds, when it
	// observed the position. The server keeps the latest report by this time,
	// so a delayed report cannot move the position backwards.
	EventTimeMS int64 `json:"eventTimeMS,omitempty"`
	// PlaybackID is the client's random ID for this play, new each time the
	// player starts the item and kept across pauses and seeks; play history
	// groups a play by it (CORE-1703). Optional, validated exactly as sent
	// (store.ValidatePlaybackID), the same as GraphQL reportPlayback.
	PlaybackID string `json:"playbackID,omitempty"`
	// Paused says whether the player was paused when it reported; a client
	// that omits it reads as playing.
	Paused *bool `json:"paused,omitempty"`
	// PlayedMS is the time, in milliseconds, the client has actually played
	// under PlaybackID so far, counting only while media plays (not paused,
	// seeking or buffering); play history credits its growth.
	PlayedMS *int64 `json:"playedMS,omitempty"`
}

type v1ProgressResponse struct {
	MediaID         string `json:"mediaID"`
	PositionSeconds int    `json:"positionSeconds"`
	Watched         bool   `json:"watched"`
}

// v1ReportProgress answers PUT /v1/media/{mediaID}/progress for the device
// Session's Profile. Hidden and unknown Media both answer 404.
func (s *Server) v1ReportProgress(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	scope, ok := s.v1DeviceSession(w, r)
	if !ok {
		return
	}
	if s.v1 == nil || s.v1Progress == nil {
		writeError(w, r, http.StatusServiceUnavailable, "progress unavailable")
		return
	}
	mediaID := store.MediaID(r.PathValue("mediaID"))
	if mediaID == "" {
		writeError(w, r, http.StatusBadRequest, "mediaID is required")
		return
	}
	var req v1ProgressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if req.PositionSeconds == nil {
		writeError(w, r, http.StatusBadRequest, "positionSeconds is required")
		return
	}
	if *req.PositionSeconds < 0 || req.DurationSeconds < 0 || req.EventTimeMS < 0 || (req.PlayedMS != nil && *req.PlayedMS < 0) {
		writeError(w, r, http.StatusBadRequest, "positionSeconds, durationSeconds, eventTimeMS and playedMS must not be negative")
		return
	}
	if err := store.ValidatePlaybackID(req.PlaybackID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if !s.v1MediaVisible(w, r, scope, mediaID) {
		return
	}
	progress, err := s.v1Progress.RecordPlaybackProgress(r.Context(), scope, store.PlaybackProgress{
		ProfileID:       scope.ProfileID,
		MediaID:         mediaID,
		PositionSeconds: *req.PositionSeconds,
		DurationSeconds: req.DurationSeconds,
		EventTimeMS:     req.EventTimeMS,
		PlaybackID:      req.PlaybackID,
		Paused:          req.Paused,
		PlayedMS:        req.PlayedMS,
	}, req.Finished)
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "progress unavailable", err)
		return
	}
	writeJSON(w, http.StatusOK, v1ProgressResponse{
		MediaID:         string(mediaID),
		PositionSeconds: progress.PositionSeconds,
		Watched:         progress.Watched,
	})
}
