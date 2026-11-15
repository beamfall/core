package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

// renditionPipeRunner is the FFmpeg seam the encrypted precompute generator
// drives. Satisfied by *ffmpeg.Runner.
type renditionPipeRunner interface {
	RenditionToPipe(ctx context.Context, in ffmpeg.PipeInput, rendition ffmpeg.Rendition, segmentSeconds int, stdout io.Writer) error
	ProbePipeDuration(ctx context.Context, in ffmpeg.PipeInput) (time.Duration, error)
}

// precomputeContent is the crypto seam: it decrypts the source and the
// rendition members and encrypts each member from memory. Satisfied by
// appLibEncryptDBCipher.
type precomputeContent interface {
	OpenDecryptReader(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) (io.ReadSeekCloser, error)
	DecryptBytes(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) ([]byte, error)
	EncryptObject(libraryID store.LibraryID, src io.Reader, ciphertextPath, objectClass, immutableObjectID string) error
}

// encryptedPrecomputeGenerator precomputes an Unsealed Encrypted Library's
// HLS rendition through the shared encrypted-derivative helper (ADR-0235):
// encderiv serves the decrypted source on a per-item loopback endpoint, FFmpeg
// writes one fragmented MP4 to stdout, encderiv stages it under an ephemeral
// key, and the stream is cut into an init section and segments in memory, each
// encrypted before it reaches disk. Playlists are built in memory and
// encrypted the same way, so no member of the generation is ever plaintext on
// disk.
type encryptedPrecomputeGenerator struct {
	content precomputeContent
	runner  renditionPipeRunner
	// pollInterval overrides encderiv's revocation poll (tests).
	pollInterval time.Duration
}

var _ playback.EncryptedPrecomputeGenerator = encryptedPrecomputeGenerator{}

func (g encryptedPrecomputeGenerator) PrecomputeEncryptedHLS(ctx context.Context, spec playback.EncryptedPrecomputeSpec) error {
	switch {
	case g.content == nil || g.runner == nil:
		return errors.New("encrypted precompute is not configured")
	case spec.Revoked == nil:
		return errors.New("encrypted precompute needs a revocation check")
	case spec.MaxBytes <= 0:
		return errors.New("encrypted precompute needs a bounded size")
	}
	open := func() (io.ReadSeekCloser, error) {
		return g.content.OpenDecryptReader(spec.LibraryID, spec.SourcePath, string(libencrypt.ObjectClassMediaBytes), spec.ContentFingerprint)
	}
	size, err := plaintextSize(open)
	if err != nil {
		return err
	}
	rendition := spec.Rendition()
	result, err := encderiv.Run(ctx, encderiv.Job{
		Open:      open,
		Size:      size,
		StageDir:  spec.StageDir,
		StageName: spec.StageName,
		MaxBytes:  spec.MaxBytes,
		ProbeDuration: func(ctx context.Context, in encderiv.Input) (time.Duration, error) {
			return g.runner.ProbePipeDuration(ctx, ffmpeg.PipeInput{URL: in.URL, Secret: in.Secret})
		},
		PollInterval: g.pollInterval,
		Revoked:      spec.Revoked,
		Transcode: func(ctx context.Context, in encderiv.Input, stdout io.Writer) error {
			return g.runner.RenditionToPipe(ctx, ffmpeg.PipeInput{URL: in.URL, Secret: in.Secret}, rendition, playback.PrecomputeSegmentSeconds, stdout)
		},
	})
	if err != nil {
		return err
	}
	defer result.Close()
	stream, err := result.Open()
	if err != nil {
		return err
	}
	defer stream.Close()

	if err := os.MkdirAll(filepath.Join(spec.OutputDir, spec.RungID), 0o700); err != nil {
		return err
	}
	put := func(name string, data []byte) error {
		if err := spec.Revoked(ctx); err != nil {
			return err
		}
		return g.content.EncryptObject(spec.LibraryID, bytes.NewReader(data),
			filepath.Join(spec.OutputDir, filepath.FromSlash(name)),
			string(libencrypt.ObjectClassGeneratedAsset), playback.PrecomputeMemberObjectID(spec.RenditionID, name))
	}
	var playlist []playback.PrecomputeSegment
	_, err = encderiv.SegmentFMP4(ctx, stream, encderiv.SegmentOptions{Target: playback.PrecomputeSegmentSeconds * time.Second}, encderiv.SegmentSink{
		Init: func(data []byte) error { return put(spec.RungID+"/init.mp4", data) },
		Segment: func(seg encderiv.Segment, data []byte) error {
			uri := fmt.Sprintf("seg%05d.m4s", seg.Index)
			playlist = append(playlist, playback.PrecomputeSegment{URI: uri, Duration: seg.Duration})
			return put(spec.RungID+"/"+uri, data)
		},
	})
	if err != nil {
		return err
	}
	if len(playlist) == 0 {
		return errors.New("encrypted precompute produced no segments")
	}
	if err := put(spec.RungID+"/index.m3u8", playback.PrecomputeMediaPlaylist(playlist)); err != nil {
		return err
	}
	if spec.SourceVideo.NeedsToneMap() {
		if err := put(playback.PrecomputeToneMapMarker, []byte("ok\n")); err != nil {
			return err
		}
	}
	return put("master.m3u8", playback.PrecomputeMasterPlaylist(spec.PrecomputeSpec))
}

func (g encryptedPrecomputeGenerator) DecryptPrecomputeMember(libraryID store.LibraryID, renditionID store.PlaybackRenditionID, name, ciphertextPath string) ([]byte, error) {
	if g.content == nil {
		return nil, errors.New("encrypted precompute is not configured")
	}
	return g.content.DecryptBytes(libraryID, ciphertextPath, string(libencrypt.ObjectClassGeneratedAsset), playback.PrecomputeMemberObjectID(renditionID, name))
}

func (encryptedPrecomputeGenerator) IsPrecomputeCiphertext(r io.Reader) (bool, error) {
	return libencrypt.HasObjectHeader(r)
}

// encryptedPrecomputePurger removes a Library's precomputed rendition
// ciphertext when its key is rewrapped: the renditions are regenerable, so
// they are purged rather than rewrapped. It runs while the Library is Sealed
// and touches only the cache directory.
type encryptedPrecomputePurger struct {
	cacheRoot string
}

var _ graph.EncryptedDerivativePurger = encryptedPrecomputePurger{}

func (p encryptedPrecomputePurger) PurgeEncryptedDerivatives(_ context.Context, libraryID store.LibraryID) error {
	return playback.PurgeEncryptedPrecomputedRenditions(p.cacheRoot, libraryID)
}
