// Package convert is the server-side Conversion Workflow (apple ADR-0003 §1): a
// user-directed conversion that orchestrates the core FFmpeg engine to write a
// NEW managed file, preserving the original in trash (invariant 6), under the
// jobs/governor with a per-op Profile-authority check. It is NOT a
// duplicate media engine and adds no un-audited exec/mutation path — every step
// routes through an existing audited seam: FFmpeg via the core Runner (a
// least-privilege child), the catalog admit + trash via managed-file-ops, and
// preemption via the resource governor at the composition root.
package convert

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// JobKind is the durable convert-to-managed-file job. Background class, so the
// composition root's governedGeneration wrapper lets an interactive transcode /
// playback preempt it. The DedupeKey (library+path+profile)
// keeps a re-requested identical conversion from stacking.
const JobKind store.JobKind = "convert-managed-file"

// ManagedConverter is the store seam this package drives: the per-op authority
// gate + audited convert/admit/trash mutation owned by managed-file-ops. The
// lease is the ADR-0174 ownership-succession token — the handler passes its
// job's own lease through verbatim, so every durable write and side effect the
// store performs on the job's behalf is fenced on the live lease.
type ManagedConverter interface {
	ConvertManagedFile(ctx context.Context, libraryID store.LibraryID, srcPath, profile string, converter store.ManagedFileConverter, filter store.LibraryFilter, lease store.ConvertOwnerLease, now time.Time) (store.ConvertResult, error)
}

// Service holds the store seam and the injected core FFmpeg converter. The
// converter is the boundary to the FFmpeg binaries; the store owns every file
// mutation (arch conformance), so this package never writes files directly.
type Service struct {
	store     ManagedConverter
	converter store.ManagedFileConverter
}

// NewService wires the conversion workflow over the store mutation seam and the
// core FFmpeg converter.
func NewService(st ManagedConverter, converter store.ManagedFileConverter) *Service {
	return &Service{store: st, converter: converter}
}

// convertPayload is the durable conversion request. It snapshots the caller's
// LibraryFilter so the per-op Profile-authority check runs against the authority
// the request was issued under, not against ambient worker authority (mirroring
// offlinepack's caller-authority snapshot, ADR-0114). A conversion enqueued by a
// profile without authority for the library is rejected when the handler calls
// the store, not silently run with elevated rights.
type convertPayload struct {
	Version   int                 `json:"version"`
	LibraryID store.LibraryID     `json:"libraryID"`
	SrcPath   string              `json:"srcPath"`
	Profile   string              `json:"profile"`
	Filter    store.LibraryFilter `json:"filter"`
}

// NewConvertJob builds a deduped background conversion job. profile must name a
// known preset (validated here so an unknown preset never enqueues); the
// LibraryFilter snapshot carries the caller's authority for the run-time gate.
func NewConvertJob(libraryID store.LibraryID, srcPath, profile string, filter store.LibraryFilter, createdAt time.Time) (store.Job, error) {
	if strings.TrimSpace(srcPath) == "" {
		return store.Job{}, store.ValidationError("conversion source path is required")
	}
	if _, ok := ffmpeg.ConvertProfileByName(profile); !ok {
		return store.Job{}, store.ValidationError("unknown conversion profile")
	}
	payload, err := json.Marshal(convertPayload{
		Version:   1,
		LibraryID: libraryID,
		SrcPath:   srcPath,
		Profile:   profile,
		Filter:    filter,
	})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind:      JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(JobKind) + ":" + string(libraryID) + ":" + srcPath + ":" + profile,
		Payload:   payload,
		CreatedAt: createdAt,
	}, nil
}

// Handler returns the job handler that runs one conversion: it unmarshals the
// request and drives the store's audited ConvertManagedFile (authority gate →
// FFmpeg convert → journal → admit new managed file → trash original) under
// the job's own lease — the ADR-0174 ownership-succession token that fences
// every durable write and side effect against a stale generation. A conversion
// failure (bad profile, missing authority, FFmpeg error) fails the job without
// partial mutation — the original is never lost.
func Handler(service *Service) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var payload convertPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Result{}, err
		}
		lease := store.ConvertOwnerLease{JobID: job.ID, Owner: job.LeaseOwner}
		_, err := service.store.ConvertManagedFile(ctx, payload.LibraryID, payload.SrcPath, payload.Profile, service.converter, payload.Filter, lease, time.Now().UTC())
		if err != nil {
			return jobs.Result{}, err
		}
		return jobs.Result{}, nil
	}
}
