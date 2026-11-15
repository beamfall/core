package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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
	// MaxHeight caps the output height in lines, never upscaling and keeping
	// the aspect ratio; 0 keeps the source size.
	MaxHeight int
	// MaxVideoKbps caps the video bitrate (-maxrate, with a two-second
	// buffer); 0 leaves it to the CRF.
	MaxVideoKbps int
	// AudioKbps sets the audio bitrate; 0 leaves the encoder default.
	AudioKbps int
	// Stereo downmixes audio to two channels.
	Stereo bool
	// NoSubtitles drops subtitle streams, which an MP4 cannot always carry.
	NoSubtitles bool
	// FastStart moves the MP4 index to the front so playback can start before
	// the whole file is read.
	FastStart bool
	// sourceTransfer and sourceDoViProfile describe the source's video signal
	// (see WithSourceVideo). The zero values mean an SDR source.
	sourceTransfer    string
	sourceDoViProfile int
}

// WithSourceVideo returns a copy of the profile that knows the source's video
// signal: its ffprobe color_transfer and Dolby Vision profile (0 for none). A
// video re-encode of an HDR source then leads its filter graph with the live
// transcode's HDR-to-SDR tone-map chain (Rendition.toneMapChain), so the H.264
// output is SDR BT.709 rather than washed-out HDR, and Convert refuses an HDR
// source that chain cannot handle.
func (p ConvertProfile) WithSourceVideo(colorTransfer string, doViProfile int) ConvertProfile {
	p.sourceTransfer = colorTransfer
	p.sourceDoViProfile = doViProfile
	return p
}

// sourceRendition is the source signal as the live transcode's Rendition, so
// a conversion consults the same tone-map decision and chain as HLS.
func (p ConvertProfile) sourceRendition() Rendition {
	return Rendition{ColorTransfer: p.sourceTransfer, DoViProfile: p.sourceDoViProfile}
}

// toneMapRefusal names why this profile's video re-encode of its source must
// not run, or "" when it may (see Rendition.ToneMapRefusal).
func (p ConvertProfile) toneMapRefusal() string {
	if p.AudioOnly || p.VideoCodec == "" || p.VideoCodec == "copy" {
		return ""
	}
	return p.sourceRendition().ToneMapRefusal()
}

// ErrToneMapRefused is returned by Convert for an HDR source the tone-map
// chain cannot convert to SDR on this build.
var ErrToneMapRefused = errors.New("ffmpeg convert: HDR source cannot be tone-mapped to SDR")

// convertProfiles is the fixed catalog of conversion presets. A conversion
// names a preset; an unknown name is rejected before any FFmpeg run. Three
// presets cover the launch surface: H.264/MP4 (broad-compat re-encode),
// HEVC/MP4 (efficient re-encode), and AAC/M4A (audio extraction).
var convertProfiles = map[string]ConvertProfile{
	"h264-mp4": {Name: "h264-mp4", Container: "mp4", VideoCodec: "libx264", AudioCodec: "aac", CRF: 23},
	"hevc-mp4": {Name: "hevc-mp4", Container: "mp4", VideoCodec: "libx265", AudioCodec: "aac", CRF: 28},
	"aac-m4a":  {Name: "aac-m4a", Container: "m4a", AudioCodec: "aac", AudioOnly: true},
}

// packProfiles is the fixed catalog of offline pack download presets: H.264/AAC
// MP4 bounded in height and bitrate so a download fits a phone. They are kept
// apart from convertProfiles because a conversion replaces the Library file,
// while a pack transcode only ever writes a temporary copy.
var packProfiles = map[string]ConvertProfile{
	"pack-1080p": packProfile("pack-1080p", 1080, 21, 8000, 192),
	"pack-720p":  packProfile("pack-720p", 720, 22, 4000, 160),
	"pack-480p":  packProfile("pack-480p", 480, 23, 1500, 128),
}

func packProfile(name string, height, crf, videoKbps, audioKbps int) ConvertProfile {
	return ConvertProfile{
		Name: name, Container: "mp4", VideoCodec: "libx264", AudioCodec: "aac", CRF: crf,
		MaxHeight: height, MaxVideoKbps: videoKbps, AudioKbps: audioKbps,
		Stereo: true, NoSubtitles: true, FastStart: true,
	}
}

// PackProfileByName resolves a named offline pack preset, reporting whether it
// exists.
func PackProfileByName(name string) (ConvertProfile, bool) {
	p, ok := packProfiles[name]
	return p, ok
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
			args = append(args, p.boundArgs()...)
		}
	}
	audio := p.AudioCodec
	if audio == "" {
		audio = "copy"
	}
	args = append(args, "-c:a", audio)
	if audio != "copy" {
		if p.AudioKbps > 0 {
			args = append(args, "-b:a", strconv.Itoa(p.AudioKbps)+"k")
		}
		if p.Stereo {
			args = append(args, "-ac", "2")
		}
	}
	if p.NoSubtitles {
		args = append(args, "-sn")
	}
	if p.FastStart {
		args = append(args, "-movflags", "+faststart")
	}
	return args
}

// boundArgs tone-maps an HDR source and caps a re-encode's height and
// bitrate. The tone-map chain leads the filter graph, as in the live
// transcode. The height is held even, as yuv420p requires, and never exceeds
// the source's.
func (p ConvertProfile) boundArgs() []string {
	var args []string
	filters := p.sourceRendition().toneMapFilters()
	if p.MaxHeight > 0 {
		height := strconv.Itoa(p.MaxHeight)
		filters = append(filters, "scale=-2:trunc(min(ih\\,"+height+")/2)*2")
	}
	if len(filters) > 0 {
		args = append(args, "-vf", strings.Join(filters, ","))
	}
	if p.MaxVideoKbps > 0 {
		args = append(args, "-maxrate", strconv.Itoa(p.MaxVideoKbps)+"k",
			"-bufsize", strconv.Itoa(2*p.MaxVideoKbps)+"k")
	}
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
	if reason := profile.toneMapRefusal(); reason != "" {
		return fmt.Errorf("%w: %s", ErrToneMapRefused, reason)
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
