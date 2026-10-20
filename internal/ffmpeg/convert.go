package ffmpeg

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
)

// ConvertProfile is a named, server-side conversion preset (apple ADR-0003 §1):
// a small fixed set of {container, codecs, quality} that the Conversion
// Workflow drives FFmpeg with. It is a value type — never an arbitrary
// caller-supplied argument list — so a conversion can only ever ask for one of
// the audited presets, never inject FFmpeg options.
type ConvertProfile struct {
	// Name is the stable preset identifier persisted in a convert job payload.
	Name string
	// Container is the output container/extension (e.g. "mp4", "m4a").
	Container string
	// VideoCodec is the ffmpeg -c:v value; "" or "copy" stream-copies video,
	// and an empty value in an audio-only container omits video entirely.
	VideoCodec string
	// AudioCodec is the ffmpeg -c:a value; "" stream-copies audio.
	AudioCodec string
	// CRF is the libx264/libx265 constant-rate-factor quality target; 0 omits
	// the flag (used by stream-copy / audio-only presets).
	CRF int
	// AudioOnly drops the video stream entirely (-vn), for audio extraction
	// presets writing to an audio container.
	AudioOnly bool
}

// convertProfiles is the fixed catalog of conversion presets. A conversion
// names a preset; an unknown name is rejected before any FFmpeg run. Three
// presets cover the launch surface: H.264/MP4 (broad-compat re-encode),
// HEVC/MP4 (efficient re-encode), and AAC/M4A (audio extraction).
var convertProfiles = map[string]ConvertProfile{
	"h264-mp4": {Name: "h264-mp4", Container: "mp4", VideoCodec: "libx264", AudioCodec: "aac", CRF: 23},
	"hevc-mp4": {Name: "hevc-mp4", Container: "mp4", VideoCodec: "libx265", AudioCodec: "aac", CRF: 28},
	"aac-m4a":  {Name: "aac-m4a", Container: "m4a", AudioCodec: "aac", AudioOnly: true},
}

// ConvertProfileByName resolves a named preset, reporting whether it exists.
// Callers reject unknown profiles rather than fabricating FFmpeg arguments.
func ConvertProfileByName(name string) (ConvertProfile, bool) {
	p, ok := convertProfiles[name]
	return p, ok
}

// encodeArgs returns the codec-selection arguments this profile adds to a
// convert run. -vn drops video for audio-only presets; an empty VideoCodec on a
// video preset stream-copies (-c:v copy). Audio is always specified.
func (p ConvertProfile) encodeArgs() []string {
	var args []string
	if p.AudioOnly {
		args = append(args, "-vn")
	} else {
		codec := p.VideoCodec
		if codec == "" {
			codec = "copy"
		}
		args = append(args, "-c:v", codec)
		if p.CRF > 0 && codec != "copy" {
			args = append(args, "-crf", strconv.Itoa(p.CRF))
		}
		if codec != "copy" {
			args = append(args, "-pix_fmt", "yuv420p")
		}
	}
	audio := p.AudioCodec
	if audio == "" {
		audio = "copy"
	}
	args = append(args, "-c:a", audio)
	return args
}

// Convert transcodes src to dst using the named profile's codecs/container,
// writing dst atomically via a temp file in dst's directory (temp → atomic
// move), exactly like the other generation ops (FrameGrab/ScaleImage). FFmpeg
// runs as a least-privilege child through the hardening shim with read-only
// access to src and read-write access only to the temp output
// (transformConfinement); the file paths follow "--" so a filename can never
// inject options. On any failure the temp is removed and src is
// left untouched, so the original is never lost.
//
// Both ends of the conversion are bound to an identity rather than to a name.
// The input is identity-bound and held for the whole run, then re-proved
// afterwards; the output temp is reserved through a descriptor on the
// destination directory and published through that same descriptor. A source or
// destination swapped between the caller's validation and the publish is
// refused, so the admitted file always corresponds to the bytes that were
// actually authorised.
func (r *Runner) Convert(ctx context.Context, src, dst string, profile ConvertProfile) (err error) {
	if err := checkPath(src); err != nil {
		return err
	}
	if err := checkPath(dst); err != nil {
		return err
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg is not available: %w", err)
	}
	source, err := bindSource(src)
	if err != nil {
		return err
	}
	defer source.close()
	out, err := reserveOutput(dst)
	if err != nil {
		return err
	}
	defer out.joinDiscardError(&err)
	args := append([]string{"-v", "error", "-protocol_whitelist", productionProtocolAllowlist, "-i", src}, profile.encodeArgs()...)
	// The temp file carries no extension, so FFmpeg cannot infer the muxer from
	// it; -f names the container explicitly. mp4/m4a both mux as "mp4".
	format := profile.Container
	if format == "m4a" {
		format = "mp4"
	}
	args = append(args, "-f", format, "-y", "--", out.Path)
	cmd := r.convertCommand(ctx, transformConfinement(src, out.Path), ffmpegPath, args...)
	if detail, err := r.runDetail(ctx, cmd); err != nil {
		return fmt.Errorf("ffmpeg convert: %s", detail)
	}
	// A source swapped mid-run makes the output a transcode of something the
	// caller never authorised, so the artifact is discarded rather than published.
	if err := source.verify(); err != nil {
		return err
	}
	return out.publish(dst)
}
