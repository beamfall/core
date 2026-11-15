package app

import (
	"context"
	"errors"
	"io"
	"math"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/mediahttp"
)

// subtitlePipeExtractor is the FFmpeg seam the encrypted subtitle extractor
// drives. Satisfied by *ffmpeg.Runner.
type subtitlePipeExtractor interface {
	ExtractSubtitleToPipe(ctx context.Context, in ffmpeg.PipeInput, streamIndex, limit int, stdout io.Writer) error
}

// encryptedSubtitleExtractor extracts an Encrypted Library's muxed subtitle
// through the shared encrypted-derivative helper (ADR-0235): encderiv serves
// the decrypted source on a per-item loopback endpoint, FFmpeg reads it there
// and writes WebVTT to stdout, and the output is captured in memory.
type encryptedSubtitleExtractor struct {
	content mediahttp.EncryptedByteReadSeeker
	runner  subtitlePipeExtractor
}

var _ mediahttp.EncryptedSubtitleExtractor = encryptedSubtitleExtractor{}

func (e encryptedSubtitleExtractor) ExtractEncryptedSubtitle(ctx context.Context, req mediahttp.EncryptedSubtitleRequest) ([]byte, error) {
	if e.content == nil || e.runner == nil {
		return nil, errors.New("encrypted subtitle extraction is not configured")
	}
	if req.MaxBytes <= 0 || req.MaxBytes > math.MaxInt32 {
		return nil, errors.New("encrypted subtitle extraction needs a bounded size")
	}
	open := func() (io.ReadSeekCloser, error) {
		return e.content.OpenDecryptReader(req.LibraryID, req.SourcePath, string(libencrypt.ObjectClassMediaBytes), req.Fingerprint)
	}
	size, err := plaintextSize(open)
	if err != nil {
		return nil, err
	}
	limit := int(req.MaxBytes)
	return encderiv.RunCapture(ctx, encderiv.Capture{
		Open:     open,
		Size:     size,
		MaxBytes: req.MaxBytes,
		Revoked:  req.Revoked,
		Transcode: func(ctx context.Context, in encderiv.Input, stdout io.Writer) error {
			return e.runner.ExtractSubtitleToPipe(ctx, ffmpeg.PipeInput{URL: in.URL, Secret: in.Secret}, req.StreamIndex, limit, stdout)
		},
	})
}

// plaintextSize measures a decrypted source by seeking to its end; stat would
// report the ciphertext length.
func plaintextSize(open encderiv.Opener) (int64, error) {
	reader, err := open()
	if err != nil {
		return 0, err
	}
	size, seekErr := reader.Seek(0, io.SeekEnd)
	if err := errors.Join(seekErr, reader.Close()); err != nil {
		return 0, err
	}
	if size < 0 {
		return 0, errors.New("encrypted source has an invalid plaintext size")
	}
	return size, nil
}
