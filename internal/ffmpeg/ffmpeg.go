// Package ffmpeg is the boundary to the FFmpeg binaries (ADR-0013,
// docs/architecture/ARCHITECTURE.md §5.3). Binary resolution is lazy: New
// succeeds on machines with no FFmpeg installed, and calls return the
// configuration error instead.
//
// Security model: every path argument is validated before use
// (checkPath rejects option-looking paths that start with "-"), and file paths
// are always passed after "--" so an attacker-controlled filename cannot inject
// extra options into the ffmpeg/ffprobe argument list.
package ffmpeg

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

//go:embed ffmpeg.pin
var pinnedVersion string

// PinnedVersion is the normalized version token required for reproducible
// fixture capture and shipped container builds.
func PinnedVersion() string {
	return strings.TrimSuffix(pinnedVersion, "\n")
}

// checkPath rejects any path that looks like an option flag (starts with "-").
// All user-controlled paths — ingest files, source media, output destinations —
// must pass this gate before being forwarded to ffmpeg/ffprobe. A leading "-"
// is never a valid path component on the file systems Beamfall targets.
func checkPath(path string) error {
	if strings.HasPrefix(path, "-") {
		return fmt.Errorf("ffmpeg: unsafe path (looks like an option flag): %q", path)
	}
	return nil
}

// Probe is the media-relevant subset of an ffprobe result (ADR-0029 §9:
// audio fields and tags ride along so audio-class media and tag-driven
// identification need no second probe).
type Probe struct {
	Container       string
	VideoCodec      string
	AudioCodec      string
	HasVideo        bool
	DurationSeconds int
	// VideoWidth/VideoHeight are the first video stream's frame size in
	// pixels; 0 when there is no video stream or the probe omits it. The
	// transcode ladder reads these to never advertise a rendition above the
	// source (ADR-0010 §8: honest ABR).
	VideoWidth  int
	VideoHeight int
	// Tags are the container-level format tags (title, artist, album, …),
	// keys lowercased.
	Tags map[string]string
	// Streams is the full per-stream inventory captured at scan:
	// every stream's type, codec, profile, language, default/forced flags,
	// channel layout, bit depth, and HDR fields. Track menus, subtitle
	// conversion, and the HDR decision table all read from here. Indexed in
	// ffprobe stream order.
	Streams []ProbeStream
	// FontAttachments lists the muxed font attachments (ADR-0010 §4/§5: libass
	// renders ASS with fonts pulled from attachments). Each attachment also
	// appears in Streams as a stream of type "attachment".
	FontAttachments []FontAttachment
	// Chapters are embedded container chapters: generic
	// timeline ranges suitable for import as core Markers.
	Chapters              []ProbeChapter
	embeddedArtworkStream int
}

// AudioWithAttachedPicture reports an audio track whose only "video" stream is
// embedded cover art (attached_pic). Before CORE-1151 such a file was imported
// as video; the CORE-1262 repair uses this to find and reclassify it.
func (p Probe) AudioWithAttachedPicture() bool {
	if p.HasVideo || p.AudioCodec == "" {
		return false
	}
	for _, stream := range p.Streams {
		if stream.AttachedPicture {
			return true
		}
	}
	return false
}

// ProbeChapter is one embedded chapter reported by ffprobe.
type ProbeChapter struct {
	StartSeconds float64
	EndSeconds   float64
	Title        string
}

// ProbeStream is one stream of a probed media file — the per-stream inventory
// row ADR-0010 §4 persists at scan. Zero values mean "ffprobe did not report
// this field" (e.g. BitDepth 0, empty ChannelLayout).
type ProbeStream struct {
	// Index is the absolute ffprobe stream index used by exact -map selectors.
	Index int
	// Type is the ffprobe codec_type: "video", "audio", "subtitle",
	// "attachment", or "data".
	Type string
	// Codec is the ffprobe codec_name (e.g. "hevc", "truehd",
	// "hdmv_pgs_subtitle").
	Codec string
	// Profile is the codec profile (e.g. "Main 10", "High"); "" when absent.
	Profile string
	// Level is the codec level as reported by ffprobe (e.g. H.264 level 4.1 is
	// 41, HEVC level 5.1 is 153); 0 when absent.
	Level int
	// Language is the BCP-47-ish ISO-639 language tag from the stream tags
	// ("eng", "spa"); "" when untagged.
	Language string
	// Title is the stream's container title tag ("English (SDH)"); "" when
	// untagged.
	Title string
	// Default, Forced, HearingImpaired, VisualImpaired, and Commentary mirror
	// the ffprobe disposition flags. A subtitle stream also takes Forced,
	// HearingImpaired and Commentary from words in its Title (CORE-2523).
	Default         bool
	Forced          bool
	HearingImpaired bool
	VisualImpaired  bool
	Commentary      bool
	// AttachedPicture identifies embedded artwork streams (for example an
	// audio file's cover image); it is never treated as a generated frame.
	AttachedPicture bool
	// Channels and ChannelLayout describe audio streams (e.g. 8, "7.1").
	Channels      int
	ChannelLayout string
	// BitDepth is the coded sample bit depth (bits_per_raw_sample, falling
	// back to bits_per_sample); 0 when unreported.
	BitDepth int
	// SampleAspectRatio and DisplayAspectRatio carry ffprobe's SAR/DAR strings
	// (e.g. "32:27", "16:9"). FrameRate is r_frame_rate; AverageFrameRate is
	// avg_frame_rate, which can diverge on VFR sources.
	SampleAspectRatio  string
	DisplayAspectRatio string
	FieldOrder         string
	FrameRate          string
	AverageFrameRate   string
	// ColorTransfer and ColorPrimaries carry the HDR transfer characteristics
	// (e.g. "smpte2084" for PQ/HDR10, "arib-std-b67" for HLG) and color
	// primaries (e.g. "bt2020"); "" when absent. The HDR decision table reads
	// these alongside DoViProfile.
	ColorTransfer  string
	ColorPrimaries string
	// HDR10Plus is true when ffprobe reports HDR10+ dynamic metadata
	// (SMPTE ST 2094-40) for the stream.
	HDR10Plus bool
	// DoViProfile is the Dolby Vision profile (5/7/8) from the stream's DOVI
	// configuration record side data; 0 when the stream carries no DV.
	// DoViBLCompatID is the base-layer signal compatibility ID (1 = HDR10,
	// 2 = SDR, 4 = HLG, 6 = Ultra HD Blu-ray HDR10), used to tell profile 8.1
	// from 8.2 and 8.4.
	DoViProfile    int
	DoViBLCompatID int
	// SampleRate is an audio stream's sample rate in Hz (ffprobe sample_rate,
	// CORE-1627); 0 when unreported or on a non-audio stream.
	// EncoderDelaySamples, EncoderPaddingSamples and DecodedSamples are an
	// audio stream's gapless facts (CORE-1626), set by the gapless probe Probe
	// runs on audio-only files: the priming samples at the start and the
	// padding samples at the end of the decoded stream as the stream declares
	// them, and the content length in between (0 when unmeasured). All zero
	// when nothing was declared.
	SampleRate            int
	EncoderDelaySamples   int
	EncoderPaddingSamples int
	DecodedSamples        int64
}

// AudioProfile is the HLS transcode audio target selected by playback.
// Zero-value preserves the legacy AAC stereo behavior.
type AudioProfile struct {
	Codec    string
	Channels int
	GainDB   *float64
}

// FontAttachment is one muxed font attachment: libass pulls
// these to render ASS subtitles with full fidelity on the libmpv rung.
type FontAttachment struct {
	Filename string
	MimeType string
}

// Runner invokes the FFmpeg binaries.
type Runner struct {
	resolveOnce sync.Once
	ffprobePath string
	resolveErr  error
	hwAccel     HWAccel
	// hwHEVC reports that the probe verified hwAccel's HEVC encoder. HEVC is
	// advertised and selected only when it is set (CORE-2088).
	hwHEVC bool
	// hwAV1 reports that the probe verified hwAccel's AV1 encoder, and
	// hwLowPower that hwAccel encodes only in low-power mode (CORE-2094).
	hwAV1      bool
	hwLowPower bool
	// generationNiceness is the Unix niceness applied to background generation
	// processes (frame grabs, sprite sheets, preview clips, image scales) on
	// Linux so a realtime transcode — never niced — always wins the CPU.
	// Zero means no de-prioritization.
	generationNiceness int
	// shimPath is the beamfall binary to re-exec as the FFmpeg hardening shim.
	// Empty (the default) means hardening is off and ffmpeg is
	// exec'd directly; the server entrypoint sets it via SetHardeningShim so
	// only the real server hardens — a test binary never re-execs itself.
	shimPath string
	// policy is the Console transcode policy (CORE-2452). Sessions read it as
	// they start, so a change applies to the next session without a restart.
	// nil is the default policy.
	policy atomic.Pointer[EncodePolicy]
}

// New creates a Runner. The ffprobe binary is resolved on first use.
func New() *Runner {
	return &Runner{}
}

// Version returns the normalized token from the first line of ffmpeg -version.
//
//io:ignore ffmpeg-version — build/test provenance check, not a server lifecycle I/O journey.
func (r *Runner) Version(ctx context.Context) (string, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg is not available: %w", err)
	}
	// #nosec G204 -- path is resolved from PATH and receives a fixed argument.
	output, _, err := r.run(ctx, exec.CommandContext(ctx, path, "-version"))
	if err != nil {
		return "", fmt.Errorf("ffmpeg version: %w", err)
	}
	fields := strings.Fields(strings.SplitN(string(output), "\n", 2)[0])
	if len(fields) < 3 || fields[0] != "ffmpeg" || fields[1] != "version" {
		return "", fmt.Errorf("ffmpeg version: unexpected output %q", strings.TrimSpace(string(output)))
	}
	return fields[2], nil
}

// SetGenerationNiceness sets the Unix niceness applied to background FFmpeg
// generation processes on Linux. Realtime HLS transcode is never
// niced. A no-op on non-Linux platforms (nicePriority returns 0 there).
//
//io:ignore — config setter, not an I/O operation (cmd/ioe2e ledger).
func (r *Runner) SetGenerationNiceness(niceness int) {
	r.generationNiceness = niceness
}

// SetHardwareAccel selects the verified hardware encoder used by HLS
// transcodes. The zero value means software libx264. hevc reports that the
// probe also verified accel's HEVC encoder; without it the runner neither
// advertises nor selects HEVC output. It is SetHardwareEncode without AV1 or
// low-power mode.
//
//io:ignore — config setter, not an I/O operation (cmd/ioe2e ledger).
func (r *Runner) SetHardwareAccel(accel HWAccel, hevc bool) {
	r.SetHardwareEncode(HardwareEncode{Accel: accel, HEVC: hevc})
}

// SetHardwareEncode selects the verified hardware encode path used by HLS
// transcodes (see HWAccelResult.HardwareEncode). A codec the probe did not
// verify is neither advertised nor selected, and low-power mode applies only
// to the accelerators that have it.
//
//io:ignore — config setter, not an I/O operation (cmd/ioe2e ledger).
func (r *Runner) SetHardwareEncode(encode HardwareEncode) {
	if _, ok := hwAccelEncoder[encode.Accel]; !ok {
		encode = HardwareEncode{}
	}
	r.hwAccel = encode.Accel
	r.hwHEVC = encode.HEVC
	r.hwAV1 = encode.AV1
	r.hwLowPower = encode.LowPower && hwAccelLowPowerCapable(encode.Accel)
}

// EncodePolicy is the operator's Console transcode policy (CORE-2452). The
// zero value is the default: the verified hardware encoder, HEVC where it was
// verified, and balanced quality.
type EncodePolicy struct {
	// SoftwareOnly turns the verified hardware encoder off: video encodes with
	// libx264, with no device decode or hardware tone-map, and no HEVC or AV1
	// output is offered, since the realtime path has no software encoder for
	// them.
	SoftwareOnly bool
	// H264Only withholds HEVC and AV1 output even where the probe verified it.
	H264Only bool
	// Quality is QualityLow, QualityBalanced or QualityHigh; empty is balanced.
	Quality string
}

// The Console quality values (CORE-2452).
const (
	QualityLow      = "low"
	QualityBalanced = "balanced"
	QualityHigh     = "high"
)

// SetEncodePolicy applies the Console transcode policy to sessions started
// after the call. Sessions already running keep their encoder.
//
//io:ignore — config setter, not an I/O operation (cmd/ioe2e ledger).
func (r *Runner) SetEncodePolicy(policy EncodePolicy) {
	r.policy.Store(&policy)
}

// EncodePolicy is the Console transcode policy new sessions start under. A
// playback session snapshots it at creation and pins it on every rendition
// (WithEncodePolicy), so a later Console save never changes the encoder of a
// session already playing.
//
//io:ignore — in-memory config read, not an I/O operation (cmd/ioe2e ledger).
func (r *Runner) EncodePolicy() EncodePolicy {
	if policy := r.policy.Load(); policy != nil {
		return *policy
	}
	return EncodePolicy{}
}

// renditionPolicy is the policy a rendition encodes under: its pinned
// snapshot, else the runner's current policy.
func (r *Runner) renditionPolicy(rendition Rendition) EncodePolicy {
	if rendition.policy != nil {
		return *rendition.policy
	}
	return r.EncodePolicy()
}

// withQuality applies only the Console quality, for the software-only paths
// (encrypted and composed) that never take a hardware encoder.
func (r *Runner) withQuality(rendition Rendition) Rendition {
	rendition.quality = r.renditionPolicy(rendition).Quality
	return rendition
}

// renditionHWAccel is the accelerator a rendition encodes on. An HEVC or AV1
// rendition whose encoder the probe did not verify gets none, so its upload
// filters, tone-map backend and encoder all take the software path together
// instead of launching an unverified encoder (CORE-2088, CORE-2094). The
// Console's software-only policy gives every rendition none (CORE-2452).
func (r *Runner) renditionHWAccel(rendition Rendition, policy EncodePolicy) HWAccel {
	switch {
	case policy.SoftwareOnly,
		rendition.VideoCodec == "hevc" && !r.hwHEVC,
		rendition.VideoCodec == "av1" && !r.hwAV1:
		return ""
	}
	return r.hwAccel
}

// hardwareRendition applies the runner's encode policy to a rendition: its
// accelerator, the low-power flag when that accelerator encodes only in
// low-power mode, and the Console quality.
func (r *Runner) hardwareRendition(rendition Rendition) Rendition {
	policy := r.renditionPolicy(rendition)
	accel := r.renditionHWAccel(rendition, policy)
	rendition = rendition.withHWAccel(accel)
	rendition.hwLowPower = accel != "" && r.hwLowPower
	rendition.quality = policy.Quality
	return rendition
}

// launchRendition prepares a video rendition for spawn: the encode policy,
// then the tone-map backend and device decode. Under the software-only
// policy the tone-map and decode stay on the CPU too, so no stage of the
// conversion touches a graphics device (CORE-2452).
func (r *Runner) launchRendition(rendition Rendition) Rendition {
	rendition = r.hardwareRendition(rendition)
	if r.renditionPolicy(rendition).SoftwareOnly {
		return rendition
	}
	rendition = rendition.withToneMapAccel(selectRenditionToneMapBackend(rendition))
	return rendition.withDeviceDecode(selectRenditionDeviceDecode(rendition))
}

//io:ignore — in-memory capability check, not an I/O operation (cmd/ioe2e ledger).
func (r *Runner) SupportsTranscodeCodec(codec string) bool {
	return r.SupportsTranscodeCodecUnder(r.EncodePolicy(), codec)
}

// SupportsTranscodeCodecUnder is SupportsTranscodeCodec under a given encode
// policy, so a session can decide its ladder and its encoder from one
// snapshot (CORE-2452).
//
//io:ignore — in-memory capability check, not an I/O operation (cmd/ioe2e ledger).
func (r *Runner) SupportsTranscodeCodecUnder(policy EncodePolicy, codec string) bool {
	switch {
	case codec == "" || codec == "h264":
		return true
	case policy.SoftwareOnly || policy.H264Only:
		return false
	case codec == "hevc":
		return r.hwHEVC && hwAccelVideoEncoder(r.hwAccel, "hevc") != ""
	case codec == "av1":
		return r.hwAV1 && hwAccelVideoEncoder(r.hwAccel, "av1") != ""
	default:
		return false
	}
}

// SetHardeningShim enables the FFmpeg hardening re-exec shim by
// pointing this Runner at the beamfall binary to re-exec for every ffmpeg/
// ffprobe spawn. The shim applies per-process rlimits (all platforms) and Linux
// landlock filesystem confinement, then execs the real binary. Pass
// DefaultShimPath() from the server entrypoint. An empty path leaves hardening
// off (direct exec), so a non-server process — notably a test binary, which
// would otherwise re-exec itself recursively — never wraps its spawns.
//
//io:ignore — config setter, not an I/O operation (cmd/ioe2e ledger).
func (r *Runner) SetHardeningShim(shimPath string) {
	r.shimPath = shimPath
}

// generationCommand builds an exec.Cmd for a background generation FFmpeg run,
// hardened per ADR-0012 §10 (rlimits on every platform + Linux landlock
// confinement to conf via the re-exec shim) and de-prioritized via `nice -n N`
// when a niceness is configured and the platform honors it (Linux). The shim
// and `nice` both exec their target in place (same PID), so the Pdeathsig
// orphan guard still binds the FFmpeg process. When no niceness applies, ffmpeg
// is launched directly under the shim. The caller still applies applySysProcAttr
// to set the death signal.
func (r *Runner) generationCommand(ctx context.Context, conf confinement, ffmpegPath string, args ...string) *exec.Cmd {
	binPath, binArgs := r.niceWrap(ffmpegPath, args)
	return r.confinedCommand(ctx, binPath, conf, binArgs)
}

// niceWrap prefixes `nice -n N` to a background FFmpeg run when a generation
// niceness is configured and the platform honors it (Linux).
func (r *Runner) niceWrap(ffmpegPath string, args []string) (string, []string) {
	if nice := nicePriority(r.generationNiceness); nice > 0 {
		if nicePath, err := exec.LookPath("nice"); err == nil {
			return nicePath, append([]string{"-n", strconv.Itoa(nice), ffmpegPath}, args...)
		}
	}
	return ffmpegPath, args
}

// convertCommand builds the FFmpeg command for a conversion-workflow run and,
// on macOS, additionally confines it under the OS-enforced `sandbox-exec`
// deny-all SBPL profile. It first builds the same hardened
// command as generationCommand (rlimits + nice + the SEC-2 re-exec shim), then
// wraps that whole command in the sandbox so the kernel Seatbelt sandbox
// enforces the convert allow-list — read-only staged input, read-write
// staging/trash root — overriding any inherited full-disk authority. On Linux
// the SEC-2 shim already enforces seccomp+landlock, so sandboxWrap is a
// pass-through and this is identical to generationCommand. The "--" terminator
// is NOT used: sandbox-exec takes `-p <profile>` then the command verbatim.
func (r *Runner) convertCommand(ctx context.Context, conf confinement, ffmpegPath string, args ...string) *exec.Cmd {
	inner := r.generationCommand(ctx, conf, ffmpegPath, args...)
	binPath, binArgs, wrapped := sandboxWrap(conf.ReadOnly, conf.ReadWrite, inner.Path, inner.Args[1:])
	if !wrapped {
		return inner
	}
	return exec.CommandContext(ctx, binPath, binArgs...)
}

// Probe inspects a media file with ffprobe.
func (r *Runner) Probe(ctx context.Context, path string) (Probe, error) {
	if err := checkPath(path); err != nil {
		return Probe{}, err
	}
	r.resolveOnce.Do(func() {
		r.ffprobePath, r.resolveErr = exec.LookPath("ffprobe")
	})
	if r.resolveErr != nil {
		return Probe{}, fmt.Errorf("ffprobe is not available: %w", r.resolveErr)
	}
	cmd := r.hardenedCommand(ctx, r.ffprobePath, readConfinement(path).ReadOnly, nil,
		append(append([]string{
			"-v", "error",
			"-print_format", "json",
			"-show_format",
			"-show_streams",
			"-show_chapters",
		}, inputProtocolArgs()...), "--", path))
	stdout, stderr, err := r.run(ctx, cmd)
	if err != nil {
		if detail := captureDetail(stdout, stderr, nil); detail != "" {
			return Probe{}, fmt.Errorf("ffprobe: %s", detail)
		}
		return Probe{}, fmt.Errorf("ffprobe: %w", err)
	}
	probe, err := parseProbe(stdout, path)
	if err != nil {
		return Probe{}, err
	}
	if !probe.HasVideo && probe.AudioCodec != "" {
		r.probeGapless(ctx, path, &probe)
	}
	return probe, nil
}

// FrameGrab extracts one video frame as a JPEG, scaled to width (keeping
// aspect), writing dst atomically via a temp file in dst's directory.
func (r *Runner) FrameGrab(ctx context.Context, src string, atSeconds float64, width int, dst string) (err error) {
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
	out, err := reserveOutput(dst)
	if err != nil {
		return err
	}
	defer out.joinDiscardError(&err)
	cmd := r.generationCommand(ctx, transformConfinement(src, out.Path), ffmpegPath,
		"-v", "error",
		"-ss", strconv.FormatFloat(atSeconds, 'f', 3, 64),
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:-2", width),
		"-f", "image2",
		"-y", "--", out.Path,
	)
	if detail, err := r.runDetail(ctx, cmd); err != nil {
		return fmt.Errorf("ffmpeg frame grab: %s", detail)
	}
	return out.publish(dst)
}

// ExtractEmbeddedArtwork writes the first attached-picture stream as a JPEG.
// found is false when the container has no embedded artwork.
func (r *Runner) ExtractEmbeddedArtwork(ctx context.Context, src string, width int, dst string) (found bool, err error) {
	probe, err := r.Probe(ctx, src)
	if err != nil {
		return false, err
	}
	streamIndex := probe.embeddedArtworkStream
	if streamIndex < 0 {
		return false, nil
	}
	if err := checkPath(dst); err != nil {
		return false, err
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return false, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	out, err := reserveOutput(dst)
	if err != nil {
		return false, err
	}
	defer out.joinDiscardError(&err)
	cmd := r.generationCommand(ctx, transformConfinement(src, out.Path), ffmpegPath,
		"-v", "error",
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-map", fmt.Sprintf("0:%d", streamIndex),
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=min(%d\\,iw):-2", width),
		"-f", "image2",
		"-y", "--", out.Path,
	)
	if detail, runErr := r.runDetail(ctx, cmd); runErr != nil {
		err = runErr
		return false, fmt.Errorf("ffmpeg embedded artwork: %s", detail)
	}
	if err = out.publish(dst); err != nil {
		return false, err
	}
	return true, nil
}

// ScaleImage re-encodes the src image scaled to at most width pixels wide
// (keeping aspect, never upscaling), writing dst atomically via a temp file in
// dst's directory. A dst ending in .png is an RGBA PNG, so the source's alpha
// channel survives; every other dst is a JPEG.
func (r *Runner) ScaleImage(ctx context.Context, src string, width int, dst string) (err error) {
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
	out, err := reserveOutput(dst)
	if err != nil {
		return err
	}
	defer out.joinDiscardError(&err)
	args := []string{
		"-v", "error",
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=min(%d\\,iw):-2", width),
	}
	// The temp output has no image extension, so image2 picks JPEG unless the
	// codec is named.
	if strings.EqualFold(filepath.Ext(dst), ".png") {
		args = append(args, "-c:v", "png", "-pix_fmt", "rgba")
	}
	args = append(args, "-f", "image2", "-y", "--", out.Path)
	cmd := r.generationCommand(ctx, transformConfinement(src, out.Path), ffmpegPath, args...)
	if detail, err := r.runDetail(ctx, cmd); err != nil {
		return fmt.Errorf("ffmpeg image scale: %s", detail)
	}
	return out.publish(dst)
}

// SpriteSheets samples one frame every intervalSeconds from src, scales each
// to tileWidth (keeping aspect), and tiles them into columns×rows JPEG sprite
// sheets written to dstPattern (an image2 sequence pattern like sheet-%04d.jpg,
// numbered from 1). One FFmpeg pass — fps + scale + tile filters, no per-frame
// seeking — on one thread at the generation niceness. Only keyframes are
// decoded (CORE-2570): the fps filter takes the keyframe nearest each interval,
// so the sheet count and layout are those of trickplay:v1 while the decoder
// skips every frame the filter would drop. The final sampled frame is cloned
// to fill frameCount, the count advertised by the manifest. When a VA-API or QSV device was
// verified and the Console has not chosen software only, the keyframes decode
// on it. A failed pass is retried in software, then decoding every frame, the
// order Jellyfin uses. Sheets are written directly; the caller publishes a
// manifest last, so readers never trust a directory without one.
func (r *Runner) SpriteSheets(ctx context.Context, src string, intervalSeconds, tileWidth, columns, rows, frameCount int, dstPattern string) error {
	if frameCount <= 0 {
		return errors.New("sprite sheets need a positive frame count")
	}
	if err := checkPath(src); err != nil {
		return err
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg is not available: %w", err)
	}
	run := func(pass spriteSheetPass) error {
		args := append([]string{"-v", "error", "-protocol_whitelist", productionProtocolAllowlist},
			spriteSheetDecodeArgs(pass)...)
		args = append(args,
			"-i", src,
			"-vf", spriteSheetFilter(intervalSeconds, tileWidth, columns, rows, frameCount),
			"-q:v", "5",
			"-f", "image2",
			"-y", "--", dstPattern,
		)
		conf := transformConfinement(src, dstPattern).withGPU(pass.accel)
		if detail, err := r.runDetail(ctx, r.generationCommand(ctx, conf, ffmpegPath, args...)); err != nil {
			return fmt.Errorf("ffmpeg sprite sheets: %s", detail)
		}
		return nil
	}
	for _, pass := range r.spriteSheetPasses() {
		if err = run(pass); err == nil || ctx.Err() != nil {
			return err
		}
	}
	return err
}

// spriteSheetFilter pads at the sampling rate, so sparse keyframes still fill
// exactly the manifest's frame count without decoding or cloning at source FPS.
func spriteSheetFilter(intervalSeconds, tileWidth, columns, rows, frameCount int) string {
	return fmt.Sprintf("fps=1/%d:eof_action=pass,tpad=stop_mode=clone:stop=-1,trim=end_frame=%d,scale=%d:-2,tile=%dx%d",
		intervalSeconds, frameCount, tileWidth, columns, rows)
}

// spriteSheetPass is one way to decode a trickplay pass: on accel (none for
// software), keyframes only or every frame.
type spriteSheetPass struct {
	accel         HWAccel
	keyframesOnly bool
}

// spriteSheetPasses are the decodes a trickplay pass tries in order: keyframes
// on the verified VA-API or QSV device unless the Console chose software only,
// keyframes in software, then every frame in software for a file whose
// keyframes the decoder cannot skip to.
func (r *Runner) spriteSheetPasses() []spriteSheetPass {
	passes := []spriteSheetPass{{keyframesOnly: true}, {}}
	if driAccel(r.hwAccel) && !r.EncodePolicy().SoftwareOnly {
		passes = append([]spriteSheetPass{{accel: r.hwAccel, keyframesOnly: true}}, passes...)
	}
	return passes
}

// spriteSheetDecodeArgs are the input options of a trickplay pass: one decode
// thread, keyframes only when asked, and VA-API on the render node when the
// pass's accel uses /dev/dri. No hwaccel output format is set, so decoded
// frames come back to system memory for the software scale and tile filters.
func spriteSheetDecodeArgs(pass spriteSheetPass) []string {
	args := []string{"-threads", "1"}
	if pass.keyframesOnly {
		args = append(args, "-skip_frame", "nokey")
	}
	if driAccel(pass.accel) {
		args = append(args, "-hwaccel", "vaapi", "-hwaccel_device", vaapiDevicePath())
	}
	return args
}

// SampleClip encodes a silent H.264 MP4 stitched from segments of src: each
// offset starts a segmentSeconds slice, selected in one pass (select + setpts
// filters), scaled to width and capped at maxFPS. The clip is
// written atomically via a temp file in dst's directory.
func (r *Runner) SampleClip(ctx context.Context, src string, offsets []float64, segmentSeconds float64, width, maxFPS int, dst string) (err error) {
	if len(offsets) == 0 {
		return errors.New("sample clip needs at least one offset")
	}
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
	selects := make([]string, len(offsets))
	for i, offset := range offsets {
		selects[i] = fmt.Sprintf("between(t,%.3f,%.3f)", offset, offset+segmentSeconds)
	}
	filter := fmt.Sprintf("select='%s',setpts=N/FRAME_RATE/TB,scale=%d:-2,fps=%d",
		strings.Join(selects, "+"), width, maxFPS)
	if err := checkPath(src); err != nil {
		return err
	}
	if err := checkPath(dst); err != nil {
		return err
	}
	out, err := reserveOutput(dst)
	if err != nil {
		return err
	}
	defer out.joinDiscardError(&err)
	cmd := r.generationCommand(ctx, transformConfinement(src, out.Path), ffmpegPath,
		"-v", "error",
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-an",
		"-vf", filter,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "28",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-f", "mp4",
		"-y", "--", out.Path,
	)
	if detail, err := r.runDetail(ctx, cmd); err != nil {
		return fmt.Errorf("ffmpeg sample clip: %s", detail)
	}
	return out.publish(dst)
}

// waveformWindows is the number of fixed-size analysis windows astats reports
// across the full duration — the FFmpeg-side resolution, oversampled well above
// the served envelope point count so genassets' bucket step has headroom to fold
// down without aliasing a short track to a coarse shape. The window length in
// samples is derived from the source's own sample count, so the count is
// duration-independent (a 30s track and a 30min track both yield this many
// windows).
const waveformWindows = 4000

// waveformSampleRate is the mono rate the source is resampled to before
// analysis. A fixed rate makes the per-window sample math deterministic and
// independent of the source rate; the envelope is an amplitude shape, not an
// audio signal, so downsampling to a coarse rate loses nothing the envelope
// renders.
const waveformSampleRate = 8000

// rmsLevelPrefix is the ametadata key astats prints per window for the overall
// RMS level in dBFS (e.g. "lavfi.astats.Overall.RMS_level=-23.4"). "-inf" marks
// a fully silent window.
const rmsLevelPrefix = "lavfi.astats.Overall.RMS_level="

// Silence-detection tunables (ADR-0052 §2.4(a)). silencedetect's defaults
// (-60dB, 2s) miss the soft between-segment dead air the trim-silence seek
// targets, so the threshold is loosened to -50dB while keeping a 2s minimum —
// long enough that ordinary speech pauses never register, short enough to catch
// the gaps between ad reads and segments. These are server tunables, not client
// commitments: the wire contract is the {start,end} span list, not the recipe.
const (
	silenceThresholdDB    = -50.0
	silenceMinDurationSec = 2.0
)

// silenceStartPrefix / silenceEndPrefix are the tokens silencedetect prints on
// stderr for each detected span (e.g. "silence_start: 12.34",
// "silence_end: 15.6 | silence_duration: 3.26"). The end line carries trailing
// fields after a "|", parsed off here.
const (
	silenceStartPrefix = "silence_start:"
	silenceEndPrefix   = "silence_end:"
)

// loudnessStats is the raw EBU R128 loudness measurement of a source's primary
// audio (ADR-0098 area 3). IntegratedLUFS is the program loudness (LUFS),
// LoudnessRangeLU the LRA in LU, and TruePeakDBTP the true-peak in dBTP. FromTags
// records provenance: a measurement lifted from trusted ReplayGain/R128 container
// tags (no decode) vs one computed by an ebur128 analysis pass. This type is
// package-internal: LoudnessMeasure flattens it to a primitive tuple at the
// genassets seam so the ffmpeg boundary never leaks into the loudness wire
// contract — the same discipline as SilenceSpans returning [][2]float64.
type loudnessStats struct {
	IntegratedLUFS  float64
	LoudnessRangeLU float64
	TruePeakDBTP    float64
	FromTags        bool
}

// ReplayGain / R128 tag keys (lowercased to match Probe.Tags). A track-gain tag
// is a dB offset relative to the ReplayGain -18 LUFS / -23 LUFS (R128) reference;
// the integrated loudness is recovered as reference - gain. Track scope is
// preferred over album scope so per-item normalization stays per item.
const (
	replayGainTrackGainTag = "replaygain_track_gain"
	replayGainTrackPeakTag = "replaygain_track_peak"
	r128TrackGainTag       = "r128_track_gain"
)

// ebur128 summary line prefixes. The filter prints a trailing "Summary:" block
// to stderr at end of stream with "I:" (integrated LUFS), "LRA:" (loudness
// range), and "Peak:" (true peak dBTP) lines under indented headings.
const (
	ebur128IntegratedPrefix = "I:"
	ebur128RangePrefix      = "LRA:"
	ebur128TruePeakPrefix   = "Peak:"
)

// replayGainReferenceLUFS is the ReplayGain reference level a track-gain tag is
// measured against (-18 LUFS, the historical ReplayGain 2.0 reference). r128
// gain tags use -23 LUFS (EBU R128). The integrated loudness implied by a gain
// tag is reference - gain.
const (
	replayGainReferenceLUFS = -18.0
	r128ReferenceLUFS       = -23.0
	// r128GainQ256 converts an R128_TRACK_GAIN tag (a signed Q7.8 integer in
	// 1/256 dB units per the Ogg R128 spec) to dB.
	r128GainQ256 = 256.0
)

// LoudnessMeasure returns the EBU R128 loudness of src's primary audio stream as
// a flat tuple (integratedLUFS, loudnessRangeLU, truePeakDBTP, fromTags). The
// primitive return — no shared struct — keeps the ffmpeg boundary out of the
// genassets/playback import set, exactly like SilenceSpans returning [][2]float64.
// Trusted-tags-first (ADR-0098 area 3): when the container carries a usable
// ReplayGain or R128 track-gain tag the measurement is recovered from it with no
// decode (fromTags=true); otherwise the source is decoded once through the
// ebur128 filter under the null muxer (-f null, no media bytes — only the printed
// summary on stderr, parsed here) exactly like the waveform/silence passes.
func (r *Runner) LoudnessMeasure(ctx context.Context, src string) (integratedLUFS, loudnessRangeLU, truePeakDBTP float64, fromTags bool, err error) {
	if err := checkPath(src); err != nil {
		return 0, 0, 0, false, err
	}
	// Trusted-tag fast path: a probe is cheap and reads no media bytes. A present,
	// parseable track-gain tag is authoritative — the file was tagged by a trusted
	// analyser, so honoring it avoids a full decode (ADR-0098: "reads trusted
	// ReplayGain/R128-style tags when present").
	if probe, perr := r.Probe(ctx, src); perr == nil {
		if stats, ok := loudnessFromTags(probe.Tags); ok {
			return stats.IntegratedLUFS, stats.LoudnessRangeLU, stats.TruePeakDBTP, stats.FromTags, nil
		}
	}
	ffmpegPath, lookErr := exec.LookPath("ffmpeg")
	if lookErr != nil {
		return 0, 0, 0, false, fmt.Errorf("ffmpeg is not available: %w", lookErr)
	}
	// Read-only job: ebur128 under the null muxer produces no media bytes — only
	// the stderr summary parsed here — so only the source's directory needs to be
	// reachable, exactly like the waveform/silence passes.
	cmd := r.generationCommand(ctx, readConfinement(src), ffmpegPath,
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-map", "0:a:0",
		"-af", "ebur128=peak=true",
		"-f", "null",
		"--", os.DevNull,
	)
	// ebur128 prints its summary at the "info" log level, so -v error would
	// suppress it; keep only the stderr tail where the summary appears. The tail
	// buffer is self-bounding, so it runs under the supervisor via runWith (tree
	// kill + reap on cancel/timeout) rather than a raw exec.
	var output tailBuffer
	output.limit = 1 << 20
	if runErr := r.runWith(ctx, cmd, &output, &output); runErr != nil {
		detail := strings.TrimSpace(output.String())
		if detail == "" {
			detail = runErr.Error()
		}
		return 0, 0, 0, false, fmt.Errorf("ffmpeg loudness analysis: %s", detail)
	}
	stats, ok := parseEBUR128Summary(output.String())
	if !ok {
		return 0, 0, 0, false, errors.New("ffmpeg loudness analysis produced no summary")
	}
	return stats.IntegratedLUFS, stats.LoudnessRangeLU, stats.TruePeakDBTP, false, nil
}

type tailBuffer struct {
	data  []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		return len(p), nil
	}
	overflow := len(b.data) + len(p) - b.limit
	if overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *tailBuffer) String() string {
	return string(b.data)
}

// loudnessFromTags recovers an integrated-loudness measurement from a trusted
// ReplayGain or R128 track-gain container tag. ReplayGain track gain is a "+N.NN
// dB"/"-N.NN dB" string relative to -18 LUFS; R128 track gain is a signed integer
// in 1/256 dB units relative to -23 LUFS. The implied integrated loudness is
// reference - gain. ok=false means no usable tag was present.
func loudnessFromTags(tags map[string]string) (loudnessStats, bool) {
	if tags == nil {
		return loudnessStats{}, false
	}
	if raw, present := tags[replayGainTrackGainTag]; present {
		if gain, ok := parseReplayGainDB(raw); ok {
			stats := loudnessStats{IntegratedLUFS: replayGainReferenceLUFS - gain, FromTags: true}
			if peak, ok := parseBoundedNumber(tags[replayGainTrackPeakTag], "", math.SmallestNonzeroFloat64, 64); ok {
				// ReplayGain peak is a linear [0,1+] sample amplitude; report it as
				// dBTP so the wire struct is single-unit (dBTP) regardless of source.
				stats.TruePeakDBTP = 20 * math.Log10(peak)
			}
			return stats, true
		}
	}
	if raw, present := tags[r128TrackGainTag]; present {
		if q256, ok := parseBoundedInteger(raw, -120*int64(r128GainQ256), 120*int64(r128GainQ256)); ok {
			gain := float64(q256) / r128GainQ256
			return loudnessStats{IntegratedLUFS: r128ReferenceLUFS - gain, FromTags: true}, true
		}
	}
	return loudnessStats{}, false
}

// parseReplayGainDB parses the ReplayGain-standard "-6.94 dB" shape. The unit
// is mandatory so an unrelated numeric tag cannot silently become a gain.
func parseReplayGainDB(s string) (float64, bool) {
	return parseBoundedNumber(s, "dB", -120, 120)
}

// parseEBUR128Summary extracts the integrated loudness, loudness range, and true
// peak from the trailing "Summary:" block ebur128 prints on stderr at end of
// stream. The block looks like:
//
//	[Parsed_ebur128_0 @ ...] Summary:
//	  Integrated loudness:
//	    I:         -14.7 LUFS
//	    Threshold: ...
//	  Loudness range:
//	    LRA:         6.2 LU
//	    ...
//	  True peak:
//	    Peak:       -1.1 dBFS
//
// Only the I:/LRA:/Peak: data lines are read; the headings and threshold lines
// are ignored. ok=false when no integrated-loudness line was seen — the marker of
// a real measurement, distinct from a malformed run.
func parseEBUR128Summary(out string) (loudnessStats, bool) {
	var stats loudnessStats
	sawIntegrated := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, ebur128IntegratedPrefix):
			if v, ok := parseBoundedNumber(strings.TrimPrefix(line, ebur128IntegratedPrefix), "LUFS", -120, 20); ok {
				stats.IntegratedLUFS = v
				sawIntegrated = true
			}
		case strings.HasPrefix(line, ebur128RangePrefix):
			if v, ok := parseBoundedNumber(strings.TrimPrefix(line, ebur128RangePrefix), "LU", 0, 120); ok {
				stats.LoudnessRangeLU = v
			}
		case strings.HasPrefix(line, ebur128TruePeakPrefix):
			if v, ok := parseBoundedNumber(strings.TrimPrefix(line, ebur128TruePeakPrefix), "dBFS", -120, 20); ok {
				stats.TruePeakDBTP = v
			}
		}
	}
	return stats, sawIntegrated
}

// WaveformAmplitudes analyses src and returns one linear amplitude in [0,1] per
// analysis window across the full duration. The pass downmixes to
// mono at a fixed low rate, slices the stream into waveformWindows fixed-size
// windows (asetnsamples), and runs astats per window (reset=1) printing the
// per-window RMS level in dBFS via ametadata. Each dBFS value is converted to a
// linear magnitude (10^(dB/20)); a "-inf" silent window is 0. The caller buckets
// and re-normalises these windows into the served envelope, so the values are
// raw per-window magnitudes, not the final normalised points.
//
// The whole analysis runs in ffmpeg's null muxer (-f null), so no media bytes
// are produced — only the printed metadata on stderr, which is parsed here.
func (r *Runner) WaveformAmplitudes(ctx context.Context, src string) ([]float64, error) {
	if err := checkPath(src); err != nil {
		return nil, err
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	// Probe the source's resampled sample count so windows partition the whole
	// stream: total samples = duration × rate; per-window samples = that ÷ the
	// window count, floored to at least 1. asetnsamples needs a concrete window
	// size, so the duration must be known first.
	probe, err := r.Probe(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg waveform probe: %w", err)
	}
	if probe.DurationSeconds <= 0 {
		return nil, errors.New("ffmpeg waveform: source has no known duration")
	}
	totalSamples := probe.DurationSeconds * waveformSampleRate
	windowSamples := totalSamples / waveformWindows
	if windowSamples < 1 {
		windowSamples = 1
	}
	filter := fmt.Sprintf(
		// astats injects the metadata key as "lavfi.astats.Overall.RMS_level";
		// ametadata's `key=` option matches the FULL key, so `key=Overall.RMS_level`
		// matches nothing and emits zero windows. Print all astats metadata and let
		// parseWaveformAmplitudes filter by the full prefix (rmsLevelPrefix) — robust
		// across ffmpeg versions.
		"aformat=channel_layouts=mono:sample_rates=%d,asetnsamples=n=%d:p=0,astats=metadata=1:reset=1,ametadata=print:file=-",
		waveformSampleRate, windowSamples)
	// Read-only job: the null muxer produces no media bytes — only stderr
	// metadata parsed here — so only the source's directory needs to be
	// reachable. The shim always permits /dev/null writes (the null muxer's
	// only output sink).
	cmd := r.generationCommand(ctx, readConfinement(src), ffmpegPath,
		"-v", "error",
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-map", "0:a:0",
		"-af", filter,
		"-f", "null",
		"--", os.DevNull,
	)
	stdout, stderr, err := r.run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg waveform analysis: %s", captureDetail(stdout, stderr, err))
	}
	amps := parseWaveformAmplitudes(string(stdout) + string(stderr))
	if len(amps) == 0 {
		return nil, errors.New("ffmpeg waveform analysis produced no windows")
	}
	return amps, nil
}

// parseWaveformAmplitudes extracts the per-window RMS levels from astats'
// ametadata=print output and converts each dBFS value to a linear magnitude.
// Lines that are not an Overall.RMS_level entry are ignored, so the parser is
// robust to ametadata's surrounding "frame:"/"pts:" header lines. "-inf" (a
// fully silent window) maps to 0; any unparseable level is skipped rather than
// poisoning the envelope.
func parseWaveformAmplitudes(out string) []float64 {
	var amps []float64
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		idx := strings.Index(line, rmsLevelPrefix)
		if idx < 0 {
			continue
		}
		value := strings.TrimSpace(line[idx+len(rmsLevelPrefix):])
		if value == "-inf" || value == "-inf dB" {
			amps = append(amps, 0)
			continue
		}
		db, ok := parseBoundedNumber(value, "", -200, 100)
		if !ok {
			continue
		}
		// dBFS → linear magnitude: 0 dBFS = 1.0, every -20 dB is one decade down.
		// A positive level (rare; possible with inter-sample peaks) clamps to 1.
		amp := math.Pow(10, db/20)
		if amp > 1 {
			amp = 1
		}
		amps = append(amps, amp)
	}
	return amps
}

// SilenceSpans analyses src's primary audio stream with FFmpeg's silencedetect
// filter and returns the quiet regions above the configured threshold/duration
// (ADR-0052 §2.4(a)) as {start,end} second pairs. silencedetect prints
// "silence_start"/"silence_end" pairs on stderr; the pass runs under the null
// muxer (-f null), so no media bytes are produced — only the printed events,
// parsed here. The result is a primitive [start,end] pair shape (no exported
// span struct) so the genassets seam consuming this never has to import ffmpeg
// — genassets owns the wire span type and maps these pairs into it. Values are
// raw stream-relative seconds, not a client-facing recipe.
func (r *Runner) SilenceSpans(ctx context.Context, src string) ([][2]float64, error) {
	if err := checkPath(src); err != nil {
		return nil, err
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	filter := fmt.Sprintf("silencedetect=noise=%gdB:d=%g", silenceThresholdDB, silenceMinDurationSec)
	// Read-only job: the null muxer produces no media bytes — only stderr events
	// parsed here — so only the source's directory needs to be reachable, exactly
	// like the waveform pass. The shim always permits /dev/null writes.
	cmd := r.generationCommand(ctx, readConfinement(src), ffmpegPath,
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-map", "0:a:0",
		"-af", filter,
		"-f", "null",
		"--", os.DevNull,
	)
	// silencedetect prints its events at the "info" log level, so -v error would
	// suppress them; the supervisor captures stderr where the events appear.
	stdout, stderr, err := r.run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg silence analysis: %s", captureDetail(stdout, stderr, err))
	}
	return parseSilenceSpans(string(stdout) + string(stderr)), nil
}

// parseSilenceSpans extracts {start,end} spans from silencedetect's stderr.
// silencedetect emits a "silence_start: <t>" line when it enters a quiet region
// and a "silence_end: <t> | silence_duration: <d>" line when it leaves; the two
// are matched in order. A dangling start with no matching end (silence running
// to end-of-file — silencedetect does not print an end in that case) is dropped
// rather than guessed, since the true end is unknown without the duration; an
// unparseable token resets the pending start so a malformed line can't pair
// across spans.
func parseSilenceSpans(out string) [][2]float64 {
	var spans [][2]float64
	pendingStart := math.NaN()
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, silenceStartPrefix); idx >= 0 {
			rest := strings.TrimSpace(line[idx+len(silenceStartPrefix):])
			pendingStart = parseSilenceSeconds(rest)
			continue
		}
		if idx := strings.Index(line, silenceEndPrefix); idx >= 0 {
			rest := strings.TrimSpace(line[idx+len(silenceEndPrefix):])
			if cut := strings.IndexByte(rest, '|'); cut >= 0 {
				rest = strings.TrimSpace(rest[:cut])
			}
			end := parseSilenceSeconds(rest)
			if math.IsNaN(pendingStart) || math.IsNaN(end) || end < pendingStart {
				pendingStart = math.NaN()
				continue
			}
			spans = append(spans, [2]float64{pendingStart, end})
			pendingStart = math.NaN()
		}
	}
	return spans
}

// parseSilenceSeconds reads one bounded seconds token from silencedetect,
// returning NaN for malformed, non-finite, or out-of-range values.
func parseSilenceSeconds(s string) float64 {
	v, ok := parseBoundedNumber(s, "", 0, maxMediaSeconds)
	if !ok {
		return math.NaN()
	}
	return v
}

// ExtractSubtitleToWebVTT extracts the text subtitle stream at the given
// absolute ffprobe stream index from src and converts it to WebVTT, writing
// dst atomically via a temp file in dst's directory (ADR-0010 §5: muxed text
// subtitles — SRT/ASS — are extracted and converted server-side to WebVTT for
// AVPlayer and the web `<video>` element, served as a sidecar). ffmpeg's webvtt
// muxer performs the SRT/ASS → WebVTT conversion; ASS override tags collapse to
// plain cues here (full-fidelity ASS is the libmpv rung's job, not the
// sidecar's). streamIndex must be >= 0 and name a text subtitle stream — a
// PGS/bitmap stream cannot be muxed to WebVTT and takes the burn-in branch
// instead (ResolveSubtitleDelivery decides which).
func (r *Runner) ExtractSubtitleToWebVTT(ctx context.Context, src string, streamIndex int, dst string) (err error) {
	if streamIndex < 0 {
		return fmt.Errorf("ffmpeg: subtitle stream index must be >= 0, got %d", streamIndex)
	}
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
	out, err := reserveOutput(dst)
	if err != nil {
		return err
	}
	defer out.joinDiscardError(&err)
	cmd := r.generationCommand(ctx, transformConfinement(src, out.Path), ffmpegPath,
		"-v", "error",
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		// Map exactly the requested subtitle stream by absolute index; drop
		// everything else so the output is a pure WebVTT track.
		"-map", fmt.Sprintf("0:%d", streamIndex),
		"-c:s", "webvtt",
		"-f", "webvtt",
		"-y", "--", out.Path,
	)
	if detail, err := r.runDetail(ctx, cmd); err != nil {
		return fmt.Errorf("ffmpeg subtitle convert: %s", detail)
	}
	return out.publish(dst)
}

// ExtractSubtitlesToWebVTT extracts several text subtitle streams of src in
// ONE ffmpeg run, writing outputs[streamIndex] for each entry exactly as
// ExtractSubtitleToWebVTT would (CORE-2315). Every stream costs a full read of
// the source, so a file with many text tracks pays that read once instead of
// once per track. The run is all-or-nothing: a failure publishes no output and
// the caller can fall back to per-stream extraction to isolate a bad track.
func (r *Runner) ExtractSubtitlesToWebVTT(ctx context.Context, src string, outputs map[int]string) (err error) {
	if len(outputs) == 0 {
		return nil
	}
	if err := checkPath(src); err != nil {
		return err
	}
	indices := make([]int, 0, len(outputs))
	for streamIndex, dst := range outputs {
		if streamIndex < 0 {
			return fmt.Errorf("ffmpeg: subtitle stream index must be >= 0, got %d", streamIndex)
		}
		if err := checkPath(dst); err != nil {
			return err
		}
		indices = append(indices, streamIndex)
	}
	slices.Sort(indices)
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg is not available: %w", err)
	}
	reserved := make([]*outputTemp, 0, len(indices))
	defer func() {
		for _, out := range reserved {
			out.joinDiscardError(&err)
		}
	}()
	args := []string{
		"-v", "error",
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
	}
	paths := make([]string, 0, len(indices))
	for _, streamIndex := range indices {
		out, err := reserveOutput(outputs[streamIndex])
		if err != nil {
			return err
		}
		reserved = append(reserved, out)
		paths = append(paths, out.Path)
		// One output per stream. checkPath has refused a leading "-" on every
		// destination, and each temp keeps its destination's directory, so no
		// output name can parse as an option.
		args = append(args,
			"-map", fmt.Sprintf("0:%d", streamIndex),
			"-c:s", "webvtt",
			"-f", "webvtt",
			"-y", out.Path,
		)
	}
	cmd := r.generationCommand(ctx, transformConfinement(src, paths...), ffmpegPath, args...)
	if detail, err := r.runDetail(ctx, cmd); err != nil {
		return fmt.Errorf("ffmpeg subtitle convert: %s", detail)
	}
	for i, streamIndex := range indices {
		if err := reserved[i].publish(outputs[streamIndex]); err != nil {
			return err
		}
	}
	return nil
}

// decodeVerifySampleSeconds is the length of the sample window the decode-verify
// pass actually decodes at each probe point. Decoding the
// whole file every week would be ruinous I/O on a large library; a bounded
// sample at the start, middle, and end of the runtime catches the common
// corruption shapes — a damaged header, a bad block mid-stream, and (because the
// tail window forces a seek to the end) a truncated container — for a fraction
// of the cost. It is a server tunable, not a wire commitment.
const decodeVerifySampleSeconds = 15

// DecodeVerify sample-decodes src's bytes via FFmpeg and reports whether the
// file is broken — corrupt or truncated. It decodes a
// bounded sample window at the start, middle, and end of the runtime under the
// null muxer (-f null), so no media bytes are produced; FFmpeg's own decoder is
// the oracle. A clean file decodes every sampled window and exits zero (broken
// = false); a corrupt or truncated file makes the decoder error out with a
// non-zero exit (broken = true). durationSeconds positions the middle and end
// windows; pass <= 0 (unknown runtime) to decode only from the start.
//
// A genuine inability to RUN ffmpeg (binary missing, bad path) is returned as an
// error, never reported as broken — only the decoder's own verdict on bytes it
// successfully opened sets broken, so a misconfigured host can never mass-flag a
// healthy library. The error-level log is captured to surface the decoder's
// reason in the returned bool's caller logging, not parsed for specific tokens:
// any non-zero decode exit over real input is "broken", which keeps the verdict
// robust across FFmpeg versions and codecs.
func (r *Runner) DecodeVerify(ctx context.Context, src string, durationSeconds int) (broken bool, err error) {
	if err := checkPath(src); err != nil {
		return false, err
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return false, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	// Sample offsets across the runtime: always the start; the middle and a
	// near-end window too when the runtime is known and long enough that they
	// don't overlap the start window. The end window deliberately seeks close to
	// the tail so a truncated container surfaces as a read error there.
	offsets := []int{0}
	if durationSeconds > decodeVerifySampleSeconds*2 {
		offsets = append(offsets, durationSeconds/2)
		end := durationSeconds - decodeVerifySampleSeconds
		if end > durationSeconds/2+decodeVerifySampleSeconds {
			offsets = append(offsets, end)
		}
	}
	for _, offset := range offsets {
		// -xerror makes the decoder exit non-zero on the first decode error
		// rather than logging and continuing, so a single bad block anywhere in
		// the sampled window is a definitive "broken" verdict. -ss before -i seeks
		// without decoding the skipped span (cheap); -t bounds the decoded window.
		args := []string{
			"-xerror",
			"-v", "error",
		}
		if offset > 0 {
			args = append(args, "-ss", strconv.Itoa(offset))
		}
		args = append(args,
			"-protocol_whitelist", productionProtocolAllowlist,
			"-i", src,
			"-t", strconv.Itoa(decodeVerifySampleSeconds),
			"-f", "null",
			"--", os.DevNull,
		)
		// Read-only job, exactly like the waveform/silence passes: the null muxer
		// writes no media bytes, so only the source's directory needs to be
		// reachable. The shim always permits /dev/null writes. The decode itself
		// produces no output to capture, so it runs under the supervisor (tree kill
		// + reap on cancel/timeout) writing both streams to io.Discard.
		cmd := r.generationCommand(ctx, readConfinement(src), ffmpegPath, args...)
		if cmdErr := r.runWith(ctx, cmd, io.Discard, io.Discard); cmdErr != nil {
			var exitErr *exec.ExitError
			if errors.As(cmdErr, &exitErr) {
				// FFmpeg ran and the decoder rejected the bytes — a real "broken"
				// verdict, not an infrastructure failure.
				return true, nil
			}
			// Could not run ffmpeg at all (e.g. context canceled, spawn failure):
			// surface as an error so the caller never mistakes it for a corrupt
			// file and flags a healthy library.
			return false, fmt.Errorf("ffmpeg decode-verify: %w", cmdErr)
		}
	}
	return false, nil
}

// SubtitleDelivery names how a subtitle stream reaches a given engine.
type SubtitleDelivery int

const (
	// SubtitleSidecar serves a WebVTT sidecar track under a Stream Token: the
	// resolution for any text subtitle (SRT/ASS), and for any subtitle whose
	// native format the engine already renders.
	SubtitleSidecar SubtitleDelivery = iota
	// SubtitleBurnIn composites the subtitle into the video during transcode:
	// the resolution for a PGS/bitmap subtitle against an engine that cannot
	// render bitmap subs (the only path off libmpv, ADR-0010 §5).
	SubtitleBurnIn
)

// isBitmapSubtitleCodec reports whether an ffprobe subtitle codec_name is a
// bitmap/image format (PGS, VobSub, DVB) that cannot be converted to WebVTT.
func isBitmapSubtitleCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "hdmv_pgs_subtitle", "pgssub", "pgs",
		"dvd_subtitle", "vobsub",
		"dvb_subtitle", "dvbsub",
		"xsub":
		return true
	}
	return false
}

// ResolveSubtitleDelivery decides how a subtitle stream reaches an engine.
// subtitleCodec is the ffprobe codec_name of the subtitle
// stream; engineSubtitleFormats is the engine's declared SubtitleFormats
// vocabulary ("srt", "webvtt", "ass", "pgs" — the CapabilityProfile field).
//
//   - A text subtitle (SRT/ASS/…) always resolves to a WebVTT sidecar: it
//     converts cleanly and the sidecar is the cheap, universal path.
//   - A bitmap subtitle (PGS/VobSub/DVB) resolves to a sidecar only if the
//     engine natively renders that bitmap format (e.g. libmpv lists "pgs");
//     otherwise it resolves to burn-in — the only path off libmpv.
//   - An unknown codec is treated conservatively as text (sidecar): the WebVTT
//     muxer either converts it or the conversion fails loudly server-side,
//     which is safer than burning a re-encode for a stream we misclassified.
func ResolveSubtitleDelivery(subtitleCodec string, engineSubtitleFormats []string) SubtitleDelivery {
	if !isBitmapSubtitleCodec(subtitleCodec) {
		return SubtitleSidecar
	}
	// Bitmap subtitle: sidecar only if the engine renders this bitmap format
	// natively (libmpv). Otherwise burn it in.
	if engineRendersBitmap(subtitleCodec, engineSubtitleFormats) {
		return SubtitleSidecar
	}
	return SubtitleBurnIn
}

// engineRendersBitmap reports whether the engine's declared subtitle formats
// cover this bitmap subtitle codec. The CapabilityProfile vocabulary names the
// family ("pgs"), so the ffprobe codec is mapped to its family before the
// membership check.
func engineRendersBitmap(subtitleCodec string, engineSubtitleFormats []string) bool {
	family := bitmapSubtitleFamily(subtitleCodec)
	if family == "" {
		return false
	}
	for _, f := range engineSubtitleFormats {
		if strings.EqualFold(f, family) {
			return true
		}
	}
	return false
}

// bitmapSubtitleFamily maps an ffprobe bitmap subtitle codec_name to the
// capability-profile vocabulary word for its family ("pgs", "vobsub", "dvbsub").
func bitmapSubtitleFamily(codec string) string {
	switch strings.ToLower(codec) {
	case "hdmv_pgs_subtitle", "pgssub", "pgs":
		return "pgs"
	case "dvd_subtitle", "vobsub":
		return "vobsub"
	case "dvb_subtitle", "dvbsub":
		return "dvbsub"
	}
	return ""
}

// Rendition is the encode target of one HLS transcode (ADR-0010 §8: a live
// FFmpeg session produces one rendition). Height caps the output frame height
// — the scale filter never upscales past the source, so a cap above the
// source is a no-op, not a lie. VideoKbps caps the video bitrate (maxrate);
// zero leaves CRF unconstrained. The zero value encodes at source size.
//
// CopyVideo turns the rendition into the audio-only-transcode mode (ADR-0010
// §5, "the single biggest transcode-cost win"): the video stream is muxed
// through untouched (`-c:v copy`) and only the audio is transcoded to AAC.
// This is the case where the client can decode the video as-is but not the
// audio codec (e.g. an H.264/HEVC video with TrueHD or DTS audio). A copied
// video stream cannot be rescaled or bitrate-capped, so Height and VideoKbps
// are ignored when CopyVideo is set.
//
// BurnSubtitleIndex, when >= 0, is the absolute ffprobe stream index of a
// PGS/bitmap subtitle to burn into the video during transcode (ADR-0010 §5:
// the only path off libmpv for a PGS-only file against an engine that cannot
// render bitmap subs). Burn-in composites the subtitle onto the video frames,
// which is impossible on a copied stream — so it forces the libx264 re-encode
// path and is mutually exclusive with CopyVideo (CopyVideo wins if both are
// somehow set, since the audio-only-transcode win takes priority and the
// burn-in branch only ever resolves against a re-encoding engine). The default
// value 0 would name stream 0, so the field is interpreted as "no burn-in"
// when it is the zero value of a fresh Rendition; callers opt in explicitly by
// setting it to a real subtitle index via WithBurnSubtitle.
type Rendition struct {
	Height            int
	VideoCodec        string
	VideoKbps         int
	CopyVideo         bool
	AudioCodec        string
	AudioChannels     int
	AudioGainDB       *float64
	AudioStreamIndex  *int
	ColorTransfer     string
	ColorPrimaries    string
	DoViProfile       int
	DoViBLCompatID    int
	SampleAspectRatio string
	FieldOrder        string
	// SourceVideoCodec and SourceVideoBitDepth describe the source's primary
	// video stream. They decide whether the source can be decoded on the
	// device (see withDeviceDecode); zero values keep the software decode.
	SourceVideoCodec    string
	SourceVideoBitDepth int
	// AudioOnly drops the video: the encode writes only the selected audio
	// stream (an audio-only HLS rendition).
	AudioOnly bool
	// KeepPriming marks an audio-only source: a segmenting encode that starts
	// at position zero then omits its input seek. An input seek, even to
	// zero, drops the source's pre-roll packet, so the decoder starts cold
	// and the output's first frame is corrupt and its length one frame short
	// of the gapless arithmetic playbackInfo reports (CORE-1626).
	KeepPriming bool
	// burnSubtitleIndex is the 1-based-plus-one encoding of the burn-in
	// subtitle stream index: 0 means "no burn-in", and a value v>0 names
	// absolute stream index v-1. This keeps the zero Rendition burn-in-free
	// while still allowing stream index 0 to be selected.
	burnSubtitleIndex int
	hwAccel           HWAccel
	// hwLowPower runs hwAccel's encoder in low-power mode (CORE-2094).
	hwLowPower bool
	// quality is the Console quality (CORE-2452); empty is balanced.
	quality string
	// policy is the encode policy snapshot the rendition's session started
	// under (WithEncodePolicy); nil takes the runner's current policy.
	policy *EncodePolicy
	// toneMapAccel is the accelerator the HDR→SDR transform itself runs on.
	// It is NOT hwAccel: the encoder may be hardware while the tone-map is
	// software (that is the shipped behaviour), and ADR-0075 Amendment
	// 2026-07-26 D4 requires a *separately verified* per-transfer-class verdict
	// before a tone-map may run on a device. "" is the software chain.
	toneMapAccel HWAccel
	// deviceDecode keeps decode, scale and tone-map on the encoder's device
	// (CORE-2156). It is set only after the device-decode probe certified that
	// chain for hwAccel and this source's transfer class.
	deviceDecode bool
	// picture, when set, replaces the source's video with a composed picture
	// (see WithComposedPicture). The zero value composes nothing.
	picture ComposedPicture
	// adjust is the pitch shift and tempo change applied to the audio (see
	// WithAudioAdjust). The zero value changes nothing.
	adjust AudioAdjust
}

// WithAudioAdjust returns a copy whose audio is pitch shifted and tempo
// changed by adjust. Segment positions are then output positions: segment N
// starts at source position N x segment length x tempo. An adjusted encode is
// audio-only or a composed picture; the source's own video is never retimed.
func (r Rendition) WithAudioAdjust(adjust AudioAdjust) Rendition {
	r.adjust = adjust
	return r
}

// AudioAdjust reports the pitch shift and tempo change this rendition applies.
func (r Rendition) AudioAdjust() AudioAdjust {
	return r.adjust
}

// WithComposedPicture returns a copy whose segment encode draws picture over
// the source's audio instead of transcoding the source's video (the live form
// of a composed rendition). The source's own video, burn-in and copy settings
// do not apply to such an encode.
func (r Rendition) WithComposedPicture(picture ComposedPicture) Rendition {
	r.picture = picture
	return r
}

// Picture reports the composed picture this rendition draws, if any.
func (r Rendition) Picture() (ComposedPicture, bool) {
	return r.picture, r.picture != ComposedPicture{}
}

// WithEncodePolicy pins the encode policy a rendition launches under, so a
// session's restarts and seeks keep the encoder it started with after the
// Console policy changes (CORE-2452).
func (r Rendition) WithEncodePolicy(policy EncodePolicy) Rendition {
	r.policy = &policy
	return r
}

// PinnedEncodePolicy is the policy WithEncodePolicy pinned, if any.
func (r Rendition) PinnedEncodePolicy() (EncodePolicy, bool) {
	if r.policy == nil {
		return EncodePolicy{}, false
	}
	return *r.policy, true
}

// WithBurnSubtitle returns a copy of the rendition that burns the subtitle at
// the given absolute ffprobe stream index into the video during transcode.
// streamIndex must be >= 0 (the absolute index of a PGS/bitmap subtitle stream
// in the source). Burn-in re-encodes the video, so the returned rendition
// drops CopyVideo.
func (r Rendition) WithBurnSubtitle(streamIndex int) Rendition {
	if streamIndex < 0 {
		return r
	}
	r.burnSubtitleIndex = streamIndex + 1
	r.CopyVideo = false
	return r
}

// burnsSubtitle reports whether this rendition burns a subtitle in, and the
// absolute source stream index to burn when it does.
func (r Rendition) burnsSubtitle() (int, bool) {
	if r.burnSubtitleIndex == 0 {
		return 0, false
	}
	return r.burnSubtitleIndex - 1, true
}

// BurnSubtitleIndex reports the selected subtitle burn-in stream, if any.
func (r Rendition) BurnSubtitleIndex() (int, bool) {
	return r.burnsSubtitle()
}

func (r Rendition) withHWAccel(accel HWAccel) Rendition {
	r.hwAccel = accel
	return r
}

// withToneMapAccel returns a copy whose HDR→SDR transform runs on the named
// hardware backend. "" keeps the software chain, which is the fallback for
// every source class with no verified hardware verdict (ADR-0075 Amendment
// 2026-07-26 D3: a class with no hardware row falls back to software, never to
// a refusal).
func (r Rendition) withToneMapAccel(accel HWAccel) Rendition {
	r.toneMapAccel = accel
	return r
}

// withDeviceDecode returns a copy that decodes, scales and tone-maps on the
// encoder's device when on is true. The tone-map then runs on the encoder's
// backend, so toneMapAccel follows hwAccel.
func (r Rendition) withDeviceDecode(on bool) Rendition {
	r.deviceDecode = on
	if on {
		r.toneMapAccel = r.hwAccel
	}
	return r
}

// deviceDecodeClass is the transfer class the device-decode chain tone-maps
// for this source. A Dolby Vision profile 8 stream is classed by its base
// layer: the device decoder reads that layer alone, which is the picture the
// software chain tone-maps for the same source. Profile 7 carries a second
// layer and stays on the software chain.
func (r Rendition) deviceDecodeClass() (TransferClass, bool) {
	profile := r.DoViProfile
	if profile == 8 {
		profile = 0
	}
	class, classified := TransferClassFor(r.ColorTransfer, profile)
	return class, classified && HardwareCertifiableClass(class)
}

// deviceDecodeEligible reports whether this rendition's shape allows the
// device-decode chain: an HEVC source with a PQ or HLG picture that is
// re-encoded on QSV or VA-API with nothing that needs a system-memory filter.
// It says nothing about the host; the device-decode probe decides that.
func (r Rendition) deviceDecodeEligible() bool {
	if _, known := deviceDecodeTargets[r.hwAccel]; !known {
		return false
	}
	if r.CopyVideo || r.AudioOnly || r.ToneMapUnavailable() || !r.needsToneMap() {
		return false
	}
	if _, certifiable := r.deviceDecodeClass(); !certifiable {
		return false
	}
	if r.SourceVideoCodec != "hevc" || (r.SourceVideoBitDepth != 0 && r.SourceVideoBitDepth != 10) {
		return false
	}
	if _, composed := r.Picture(); composed {
		return false
	}
	return r.burnSubtitleIndex == 0 && !interlacedFieldOrder(r.FieldOrder) && !nonSquareSampleAspect(r.SampleAspectRatio)
}

// usesDeviceDecode reports whether this rendition emits the device-decode
// chain. Clearing hwAccel or toneMapAccel (the encrypted-live and pipe paths)
// turns it off.
func (r Rendition) usesDeviceDecode() bool {
	return r.deviceDecode && r.toneMapAccel == r.hwAccel && r.deviceDecodeEligible()
}

// inputArgs returns the pre-input arguments: the tone-map chain's device
// initialisation (ADR-0075 Amendment 2026-07-26 D1a §1) followed by the
// encoder's own VAAPI device. The two are separate handles — the tone-map runs
// on an OpenCL device derived from a named VAAPI device, while the encoder
// keeps the implicit `-vaapi_device` handle it has always used — so neither
// path can silently repurpose the other's device. The QSV encoder initialises
// its own QSV device on the selected render node (CORE-2092) unless a QSV
// tone-map chain already created one there: a second QSV device would leave
// the encoder unable to choose and fall back to libmfx's default adapter.
func (r Rendition) inputArgs() []string {
	chain := r.toneMapChain()
	if chain.DeviceDecode {
		// The chain's own devices serve the decoder and the encoder; a second
		// encoder device would leave the encoder unable to choose.
		return append(slices.Clone(chain.InitArgs), chain.InputArgs...)
	}
	args := chain.InitArgs
	switch {
	case r.hwAccel == HWAccelVAAPI:
		args = append(append([]string(nil), args...), "-vaapi_device", vaapiDevicePath())
	case r.hwAccel == HWAccelQSV && chain.Backend != HWAccelQSV:
		args = append(append([]string(nil), args...), qsvInitArgs()...)
	}
	return args
}

// videoArgs returns the video-codec selection for this rendition: `-c:v copy`
// in audio-only mode (no re-encode), otherwise the verified hardware encoder or
// the libx264 fallback.
func (r Rendition) videoArgs() []string {
	if r.CopyVideo {
		return []string{"-c:v", "copy"}
	}
	if encoder := hwAccelVideoEncoder(r.hwAccel, r.VideoCodec); encoder != "" {
		return append([]string{"-c:v", encoder}, r.lowPowerArgs()...)
	}
	return []string{
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", r.softwareCRF(),
		"-pix_fmt", "yuv420p",
	}
}

// softwareCRF is libx264's constant-rate factor for the Console quality.
func (r Rendition) softwareCRF() string {
	switch r.quality {
	case QualityLow:
		return "28"
	case QualityHigh:
		return "20"
	default:
		return "23"
	}
}

// hardwareTargetKbps is a hardware encoder's bitrate target for the Console
// quality: the rung, or three quarters of it at low quality. -maxrate stays
// at the rung either way.
func (r Rendition) hardwareTargetKbps() int {
	if r.quality == QualityLow {
		return max(r.VideoKbps*3/4, 1)
	}
	return r.VideoKbps
}

// lowPowerArgs selects the low-power encode on accelerators that have it.
func (r Rendition) lowPowerArgs() []string {
	if !r.hwLowPower || !hwAccelLowPowerCapable(r.hwAccel) {
		return nil
	}
	return slices.Clone(lowPowerArgs)
}

// rateControlArgs returns the bitrate and encoder-tuning arguments for a
// re-encoded video stream (CORE-2089). libx264 is CRF-driven and VideoKbps only
// caps it. A hardware encoder has no CRF; given only -maxrate, FFmpeg's
// defaults leave h264/hevc_vaapi in CQP (the cap is ignored), NVENC at its
// built-in 2 Mb/s VBR target whatever the rung, and QSV runtime-dependent. So
// every hardware path gets an explicit target equal to the rung, a VBR mode
// capped by -maxrate, and a realtime-friendly preset where the encoder has one.
// Segment keyframes come from -force_key_frames on every path; NVENC needs
// -forced-idr (AMF: -forced_idr) so those frames are IDRs that start an HLS
// segment cleanly.
func (r Rendition) rateControlArgs() []string {
	var out []string
	hw := hwAccelVideoEncoder(r.hwAccel, r.VideoCodec) != ""
	if hw {
		// High quality takes a slower preset where the encoder has one
		// (CORE-2452); VA-API and VideoToolbox have none.
		high := r.quality == QualityHigh
		switch r.hwAccel {
		case HWAccelNVENC:
			out = append(out, "-preset", pick(high, "p6", "p4"), "-forced-idr", "1")
		case HWAccelQSV:
			out = append(out, "-preset", pick(high, "medium", "veryfast"))
		case HWAccelAMF:
			out = append(out, "-quality", pick(high, "balanced", "speed"), "-forced_idr", "1")
		}
	}
	if r.VideoKbps <= 0 {
		return out
	}
	maxrate := r.VideoKbps
	if hw {
		target := r.hardwareTargetKbps()
		switch r.hwAccel {
		case HWAccelNVENC:
			out = append(out, "-rc", "vbr")
		case HWAccelVAAPI:
			out = append(out, "-rc_mode", "VBR")
		case HWAccelAMF:
			// Peak-constrained VBR: -b:v is the target, -maxrate the cap.
			out = append(out, "-rc", "vbr_peak")
		case HWAccelQSV:
			// h264/hevc_qsv pick CBR when -maxrate equals -b:v; one kb/s of
			// headroom keeps them in capped VBR instead of padding to the rung.
			if target == maxrate {
				maxrate++
			}
		}
		out = append(out, "-b:v", fmt.Sprintf("%dk", target))
	}
	return append(out,
		"-maxrate", fmt.Sprintf("%dk", maxrate),
		"-bufsize", fmt.Sprintf("%dk", 2*r.VideoKbps),
	)
}

// pick returns a when cond holds, else b.
func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// ToneMapUnavailable reports source HDR shapes this FFmpeg recipe refuses
// rather than producing a known-wrong SDR encode. It is a source-shape
// predicate only; for the full spawn-time decision use ToneMapRefusal.
func (r Rendition) ToneMapUnavailable() bool {
	return !r.CopyVideo && r.DoViProfile == 5
}

// ToneMapRefusal names why this rendition's video re-encode must not be
// spawned, or "" when it may be. It is the single fail-closed gate both HLS
// entry points consult, and it returns the same wire reasons playbackInfo
// reports, so the transcoder and the negotiated response can never disagree.
//
// Two refusals: Dolby Vision profile 5 has no base layer to tone-map, and an
// HDR source on a build whose tone-map chain has not been PROVEN runnable
// (ToneMapSupported). The alternative to the second refusal is spawning a
// filter graph the binary rejects at parse time — the FFMPEG-TONEMAP-BT2390
// failure — or, worse, quietly emitting an untone-mapped washed-out encode. A
// third refusal, ToneMapRefusalDisabled, applies while the operator has turned
// tone-mapping off (SetToneMapEnabled).
func (r Rendition) ToneMapRefusal() string {
	if r.ToneMapUnavailable() {
		return ToneMapRefusalDoViP5
	}
	if !r.CopyVideo && r.needsToneMap() {
		if !ToneMapEnabled() {
			return ToneMapRefusalDisabled
		}
		if !toneMapSupported() {
			return ToneMapRefusalUnverified
		}
	}
	return ""
}

// args returns the encode arguments this rendition adds to an HLS transcode.
// In audio-only mode there is no video re-encode, so the scale filter and
// bitrate cap (which only apply to a re-encoded video stream) are omitted.
//
// In the burn-in branch the video must be re-encoded, so the scale (if any) is
// folded into a -filter_complex graph that overlays the bitmap subtitle onto
// the video stream rather than the plain -vf scale used otherwise; the bitrate
// cap still applies to the re-encoded output.
func (r Rendition) args() []string {
	if r.CopyVideo {
		return nil
	}
	if burnIndex, ok := r.burnsSubtitle(); ok {
		return r.burnInArgs(burnIndex)
	}
	var out []string
	if filters := r.videoFilters(); len(filters) > 0 {
		out = append(out, "-vf", strings.Join(filters, ","))
	}
	return append(out, r.rateControlArgs()...)
}

func (r Rendition) audioArgs() []string {
	codec := r.AudioCodec
	if codec == "" {
		codec = "aac"
	}
	if codec == "copy" {
		if r.adjust.IsZero() {
			return []string{"-c:a", "copy"}
		}
		codec = "aac" // an adjusted stream is re-encoded
	}
	channels := r.AudioChannels
	if channels <= 0 {
		channels = 2
	}
	args := []string{"-c:a", codec, "-ac", strconv.Itoa(channels)}
	filters := r.adjust.Filters()
	if channels <= 2 {
		filters = append(filters, "aresample=matrix_encoding=dplii")
	}
	if r.AudioGainDB != nil {
		filters = append(filters, fmt.Sprintf("volume=%gdB", *r.AudioGainDB))
	}
	if len(filters) > 0 {
		args = append(args, "-af", strings.Join(filters, ","))
	}
	return args
}

// videoFilters is the composed -vf graph. The tone-map chain leads it, because
// a hardware tone-map must run on the device before any software filter can
// touch the frame (ADR-0075 Amendment 2026-07-26 D1a §4 — the ordering
// restructure). Everything after it — deinterlace, scale, SAR correction, the
// encoder-side upload — is composition around the chain and is explicitly NOT
// what the probe certifies (D8).
func (r Rendition) videoFilters() []string {
	chain := r.toneMapChain()
	if chain.DeviceDecode {
		// Decode, scale and tone-map all ran on the device, and the frames are
		// already in the encoder's format.
		return slices.Clone(chain.Filters)
	}
	filters := append([]string(nil), chain.Filters...)
	if interlacedFieldOrder(r.FieldOrder) {
		filters = append(filters, "yadif=deint=interlaced")
	}
	if r.Height > 0 {
		// min(ih, H): cap at the rendition height, never upscale (honest ABR).
		filters = append(filters, fmt.Sprintf("scale=-2:min(ih\\,%d)", r.Height))
	}
	if nonSquareSampleAspect(r.SampleAspectRatio) {
		filters = append(filters, "setsar=1")
	}
	return append(filters, r.encoderIngestFilters(chain)...)
}

// encoderIngestFilters hands the composed graph to the encoder in the pixel
// format it consumes. A chain that keeps frames on the device needs neither the
// encoder-side upload nor a system-memory conversion — the frames are already
// where the encoder wants them.
func (r Rendition) encoderIngestFilters(chain ToneMapChain) []string {
	if r.toneMapStaysOnDevice() {
		return nil
	}
	if upload := r.hwUploadFilters(); len(upload) > 0 {
		return upload
	}
	if len(chain.Filters) > 0 {
		return []string{"format=yuv420p"}
	}
	return nil
}

func (r Rendition) hwUploadFilters() []string {
	switch r.hwAccel {
	case HWAccelQSV:
		return []string{"format=nv12"}
	case HWAccelVAAPI:
		return []string{"format=nv12", "hwupload"}
	case HWAccelAMF:
		// AMF uploads system-memory NV12 frames itself (CORE-2091).
		return []string{"format=nv12"}
	default:
		return nil
	}
}

// needsToneMap reports whether this source shape requires an HDR→SDR transform
// on re-encode. It is a property of the SOURCE alone — what the host's FFmpeg
// build can actually do about it is ToneMapRefusal's business.
func (r Rendition) needsToneMap() bool {
	switch {
	case r.DoViProfile == 7 || r.DoViProfile == 8:
		return true
	case r.ColorTransfer == "smpte2084" || r.ColorTransfer == "arib-std-b67":
		return true
	default:
		return false
	}
}

// toneMapFilters is the filter-graph half of this rendition's tone-map chain.
func (r Rendition) toneMapFilters() []string {
	return r.toneMapChain().Filters
}

// toneMapChain is the single production chain this rendition emits — the same
// function the capability probe calls, so Beamfall can never certify a chain
// other than the one it ships (ADR-0075 Amendment 2026-07-26 D1a).
func (r Rendition) toneMapChain() ToneMapChain {
	// A copied video stream is not re-encoded, so it has no filter graph at
	// all. inputArgs reads this chain, so the guard has to live here and not
	// only in args().
	if r.CopyVideo || r.ToneMapUnavailable() || !r.needsToneMap() {
		return ToneMapChain{}
	}
	if r.usesDeviceDecode() {
		return BuildToneMapChain(ToneMapChainRequest{Backend: r.hwAccel, DeviceDecode: true, Height: r.Height})
	}
	return BuildToneMapChain(ToneMapChainRequest{
		Backend:  r.toneMapAccel,
		Download: !r.toneMapStaysOnDevice(),
	})
}

// toneMapStaysOnDevice reports whether tone-mapped frames may reach the encoder
// without a hwdownload (ADR-0075 Amendment 2026-07-26 D1a §3). Two conditions
// must both hold: the encoder consumes this backend's device frames, and no
// software filter runs between the tone-map and the encoder — a system-memory
// scale or deinterlace cannot read a device frame.
func (r Rendition) toneMapStaysOnDevice() bool {
	spec, ok := hwToneMapSpecs[r.toneMapAccel]
	if !ok || !spec.encoderConsumesDeviceFrames {
		return false
	}
	return !r.hasSoftwareIntermediateFilters()
}

// hasSoftwareIntermediateFilters reports whether videoFilters composes any
// system-memory filter after the tone-map.
func (r Rendition) hasSoftwareIntermediateFilters() bool {
	switch {
	case r.burnSubtitleIndex != 0:
		// The burn-in overlay composites a system-memory bitmap stream.
		return true
	case interlacedFieldOrder(r.FieldOrder):
		return true
	case r.Height > 0:
		return true
	case nonSquareSampleAspect(r.SampleAspectRatio):
		return true
	default:
		return false
	}
}

func interlacedFieldOrder(fieldOrder string) bool {
	switch strings.ToLower(fieldOrder) {
	case "", "progressive", "unknown":
		return false
	default:
		return true
	}
}

func nonSquareSampleAspect(sampleAspect string) bool {
	switch sampleAspect {
	case "", "0:1", "1:1":
		return false
	default:
		return true
	}
}

// hdrToSDRFilters is the HDR→SDR tone-map chain: jellyfin-ffmpeg's SIMD
// software tone-mapper, configured for the BT.2390 EETF and a BT.709 SDR
// output.
//
// It must NOT be the software `tonemap` filter. `bt2390` is a mode of
// tonemap_opencl / tonemap_cuda / tonemap_videotoolbox / tonemapx — never of
// `tonemap`, whose modes are none|linear|gamma|clip|reinhard|hable|mobius. The
// previous recipe (`zscale=t=linear:npl=100,tonemap=bt2390,zscale=…`) therefore
// died at filter-option parse on EVERY platform, including the shipped Docker
// image: verified against the pinned jellyfin-ffmpeg 7.1.4-3 build, which
// answers `Unable to parse option value "bt2390"`.
//
// A different curve (hable, reinhard, linear) is NOT an acceptable substitute:
// it silently changes picture rendition, turning a loud failure into a wrong
// one. Where tonemapx is unavailable the rendition is refused instead — see
// Rendition.ToneMapRefusal.
//
// tonemapx is self-contained (it linearizes, applies the EETF, and converts to
// the target transfer/matrix/primaries/range itself), so it needs neither
// zscale nor libzimg. Output pixel format is deliberately left at its `same`
// default so videoFilters' existing format=yuv420p / format=nv12 append stays
// the single source of truth for the software and hwupload paths.
func hdrToSDRFilters() []string {
	return []string{
		"tonemapx=tonemap=bt2390:desat=0:peak=1000:t=bt709:m=bt709:p=bt709:range=tv",
	}
}

// ToneMapChain is one backend's staged HDR→SDR tone-map chain: the device
// initialisation that must precede -i, and the filter legs that perform the
// BT.2390 EETF.
//
// Exactly one function builds it — BuildToneMapChain — and both the transcode
// argument builder (Rendition.videoFilters) and the capability probe
// (ProbeToneMap) call that one function. A probe that assembled its own copy of
// the chain would certify something Beamfall does not ship, which ADR-0075
// Amendment 2026-07-26 D1a explicitly does not accept.
type ToneMapChain struct {
	// InitArgs are the device-initialisation arguments, emitted before -i.
	InitArgs []string
	// InputArgs are the input options a device-decode chain adds before -i,
	// after InitArgs. Empty for every other chain.
	InputArgs []string
	// DeviceDecode reports that the chain decodes on the device, so its
	// filters start from device frames and end in the encoder's format.
	DeviceDecode bool
	// Filters are the filter-graph legs, in order.
	Filters []string
	// Backend is the accelerator the tone-map itself runs on; "" is software.
	Backend HWAccel
}

// Hardware reports whether this chain performs the tone-map on a device.
func (c ToneMapChain) Hardware() bool {
	return c.Backend != ""
}

// ToneMapChainRequest names the chain to build.
type ToneMapChainRequest struct {
	// Backend is the accelerator; "" builds the software chain.
	Backend HWAccel
	// DevicePath is the DRM render node for the VAAPI/QSV family. Empty
	// resolves the host's first render node.
	DevicePath string
	// Download emits hwdownload plus the conversion back to system memory.
	// False keeps frames on the device, which is only correct where the
	// encoder consumes that device's frames.
	Download bool
	// DeviceDecode builds the chain that also decodes and scales on the device
	// (CORE-2156). Backend must be QSV or VAAPI.
	DeviceDecode bool
	// Height caps the device scale at the rendition height; 0 keeps the
	// source height. Only a device-decode chain reads it.
	Height int
}

// hwToneMapSpec is one backend's hardware tone-map recipe. Every filter name
// and option spelling below was read from `ffmpeg -h filter=<name>` on a real
// build and is recorded verbatim in testdata/ffmpeg-pin-filters.txt; a spelling
// this tree names but no recorded build accepts is a hard test failure, and a
// backend no recorded build covers is simply not certifiable (D1a §5).
type hwToneMapSpec struct {
	// initArgs initialises the device the tone-map filter runs on.
	initArgs func(devicePath string) []string
	// usesDRMNode reports whether initArgs needs a /dev/dri render node.
	usesDRMNode bool
	// uploadFormat is the system-memory pixel format the device ingests. The
	// probe source and every real HDR source are 10-bit, so this is p010.
	uploadFormat string
	// toneMap is the on-device BT.2390 EETF leg.
	toneMap string
	// downloadFormat is the system-memory pixel format frames return in.
	downloadFormat string
	// encoderConsumesDeviceFrames reports whether this backend's own encoder
	// takes the tone-mapper's device frames directly. It is false for every
	// backend today and that is a fact about the recipes, not an oversight:
	// the VAAPI and QSV chains tone-map on an OpenCL device derived from the
	// VAAPI/QSV one (the interop topology ADR-0075 Amendment 2026-07-26 D1a §1
	// names as expected), and h264_vaapi/h264_qsv do not consume OpenCL
	// frames; the CUDA chain pairs with an NVENC wiring that has never been
	// given a device-frame path in this tree. Flipping one of these to true is
	// what a native same-backend tone-mapper (tonemap_vaapi on a build that
	// ships it) would do, and videoFilters already honours it.
	encoderConsumesDeviceFrames bool
}

// Verified BT.2390 EETF legs. Options mirror hdrToSDRFilters (bt2390 curve,
// desat=0, peak=1000, BT.709 limited-range output) so a hardware rendition and
// a software one target the same picture. tonemap_opencl and tonemap_cuda were
// read from the pinned jellyfin-ffmpeg 7.1.4-3 linux/arm64 artifact;
// tonemap_opencl sets tonemap_mode=max: measured on an Intel UHD 630 it lands
// within 7 code values of tonemapx on the probe fixture at three saturation
// levels, where the default mode left highlights 55 code values brighter
// (CORE-2156).
// tonemap_videotoolbox exists on no build available to this tree, so its
// spelling is recorded as the darwin candidate and VideoToolbox stays
// uncertified until a darwin manifest section records it (D1a §5, D5).
const (
	toneMapOpenCLFilter       = "tonemap_opencl=tonemap=bt2390:tonemap_mode=max:t=bt709:m=bt709:p=bt709:r=tv:desat=0:peak=1000:format=nv12"
	toneMapCUDAFilter         = "tonemap_cuda=tonemap=bt2390:t=bt709:m=bt709:p=bt709:r=tv:desat=0:peak=1000:format=nv12"
	toneMapVideoToolboxFilter = "tonemap_videotoolbox=tonemap=bt2390:t=bt709:m=bt709:p=bt709:r=tv:desat=0:peak=1000:format=nv12"
)

// hwToneMapSpecs is the chain-builder's dispatch table, keyed by accelerator.
var hwToneMapSpecs = map[HWAccel]hwToneMapSpec{
	HWAccelVAAPI: {
		// OpenCL interop over a named VAAPI device: the pinned build ships no
		// tonemap_vaapi at all, and D1a §1 names `opencl=ocl@va` as an
		// accepted and expected topology rather than a deviation.
		initArgs: func(devicePath string) []string {
			return []string{
				"-init_hw_device", "vaapi=va:" + devicePath,
				"-init_hw_device", "opencl=ocl@va",
				"-filter_hw_device", "ocl",
			}
		},
		usesDRMNode:    true,
		uploadFormat:   "p010",
		toneMap:        toneMapOpenCLFilter,
		downloadFormat: "nv12",
	},
	HWAccelQSV: {
		initArgs: func(devicePath string) []string {
			// A QSV device string selects the MFX implementation ("hw"); the
			// render node is the child VA-API device (CORE-2092).
			return []string{
				"-init_hw_device", qsvDeviceSpec("hw", devicePath),
				"-init_hw_device", "opencl=ocl@hw",
				"-filter_hw_device", "ocl",
			}
		},
		usesDRMNode:    true,
		uploadFormat:   "p010",
		toneMap:        toneMapOpenCLFilter,
		downloadFormat: "nv12",
	},
	HWAccelNVENC: {
		initArgs: func(string) []string {
			return []string{"-init_hw_device", "cuda=cu", "-filter_hw_device", "cu"}
		},
		uploadFormat:   "p010",
		toneMap:        toneMapCUDAFilter,
		downloadFormat: "nv12",
	},
	HWAccelVideoToolbox: {
		initArgs: func(string) []string {
			return []string{"-init_hw_device", "videotoolbox=vt", "-filter_hw_device", "vt"}
		},
		uploadFormat:   "p010",
		toneMap:        toneMapVideoToolboxFilter,
		downloadFormat: "nv12",
	},
}

// BuildToneMapChain is THE production tone-map chain builder, keyed by
// accelerator (ADR-0075 Amendment 2026-07-26 D1a). An unknown accelerator
// yields the zero chain — no filters, so nothing is emitted and nothing can be
// certified — rather than silently falling back to software under a hardware
// label.
func BuildToneMapChain(req ToneMapChainRequest) ToneMapChain {
	if req.DeviceDecode {
		return buildDeviceDecodeChain(req)
	}
	if req.Backend == "" {
		return ToneMapChain{Filters: hdrToSDRFilters()}
	}
	spec, known := hwToneMapSpecs[req.Backend]
	if !known {
		return ToneMapChain{}
	}
	filters := []string{"format=" + spec.uploadFormat, "hwupload", spec.toneMap}
	if req.Download {
		filters = append(filters, "hwdownload", "format="+spec.downloadFormat)
	}
	return ToneMapChain{
		InitArgs: spec.initArgs(toneMapDevicePath(spec, req.DevicePath)),
		Filters:  filters,
		Backend:  req.Backend,
	}
}

func toneMapDevicePath(spec hwToneMapSpec, requested string) string {
	switch {
	case !spec.usesDRMNode:
		return ""
	case requested != "":
		return requested
	default:
		return vaapiDevicePath()
	}
}

// toneMapChainWithoutTransform is the pass-through control leg: the same
// topology as chain with the tone-map filter removed. It is what proves a
// measured highlight drop came from the tone-map filter rather than from the
// device round-trip around it.
func toneMapChainWithoutTransform(chain ToneMapChain) ToneMapChain {
	spec, known := hwToneMapSpecs[chain.Backend]
	if !known {
		return ToneMapChain{InitArgs: chain.InitArgs, Backend: chain.Backend}
	}
	filters := make([]string, 0, len(chain.Filters))
	for _, leg := range chain.Filters {
		switch leg {
		case spec.toneMap:
		case "format=" + spec.downloadFormat:
			// Without the tone-map the device still holds what was uploaded,
			// and hwdownload only returns a frame in its own format.
			filters = append(filters, "format="+spec.uploadFormat)
		default:
			filters = append(filters, leg)
		}
	}
	return ToneMapChain{InitArgs: chain.InitArgs, Filters: filters, Backend: chain.Backend}
}

// deviceDecodeTargets maps an encoder backend to the hardware pixel format its
// encoder consumes, which is also the device type the tone-mapped frames are
// mapped back onto.
var deviceDecodeTargets = map[HWAccel]string{
	HWAccelQSV:   "qsv",
	HWAccelVAAPI: "vaapi",
}

// deviceDecodeScaleFilter decodes to 10-bit device frames at the rendition
// height. min(ih, H) never upscales, as in the software chain.
func deviceDecodeScaleFilter(height int) string {
	if height <= 0 {
		return "scale_vaapi=format=p010"
	}
	return fmt.Sprintf("scale_vaapi=w=-2:h=min(ih\\,%d):format=p010", height)
}

// buildDeviceDecodeChain is the chain that keeps every frame on the GPU
// (CORE-2156): VA-API decode, scale_vaapi, an OpenCL tone-map on the same
// device, and a reverse map onto the encoder's device. Every leg was run on an
// Intel UHD 630 with jellyfin-ffmpeg 7.1.4; the device-decode probe runs them
// again on each host before a transcode may use them.
func buildDeviceDecodeChain(req ToneMapChainRequest) ToneMapChain {
	target, known := deviceDecodeTargets[req.Backend]
	if !known {
		return ToneMapChain{}
	}
	node := req.DevicePath
	if node == "" {
		node = vaapiDevicePath()
	}
	init := []string{"-init_hw_device", "vaapi=va:" + node}
	if req.Backend == HWAccelQSV {
		init = append(init, "-init_hw_device", "qsv=qs@va")
	}
	init = append(init, "-init_hw_device", "opencl=ocl@va", "-filter_hw_device", "ocl")
	filters := []string{
		deviceDecodeScaleFilter(req.Height),
		"hwmap=derive_device=opencl",
		toneMapOpenCLFilter,
		"hwmap=derive_device=" + target + ":reverse=1",
		"format=" + target,
	}
	if req.Download {
		filters = append(filters, "hwdownload", "format=nv12")
	}
	return ToneMapChain{
		InitArgs:     init,
		InputArgs:    []string{"-hwaccel", "vaapi", "-hwaccel_device", "va", "-hwaccel_output_format", "vaapi"},
		DeviceDecode: true,
		Filters:      filters,
		Backend:      req.Backend,
	}
}

// ToneMapChainFilterNames lists every filter name a backend's chain can emit.
// It is the set the pinned-build capability manifest must cover before that
// backend may be certified (ADR-0075 Amendment 2026-07-26 D1a §5).
func ToneMapChainFilterNames(backend HWAccel) []string {
	chain := BuildToneMapChain(ToneMapChainRequest{Backend: backend, DevicePath: "/dev/dri/renderD128", Download: true})
	names := make([]string, 0, len(chain.Filters))
	for _, leg := range chain.Filters {
		name, _, _ := strings.Cut(leg, "=")
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// burnInArgs builds the burn-in encode args: a -filter_complex graph that
// overlays the bitmap subtitle stream (by absolute index) onto the first video
// stream, optionally scaling the composited output to the rendition height.
// The overlay output is mapped explicitly; because a -map disables ffmpeg's
// automatic stream selection, the first audio stream is mapped too (the `?`
// makes a video-only source tolerable). The audio still transcodes via the
// HLS builder's unconditional `-c:a aac`. The bitrate cap rides along
// unchanged. -vf and -filter_complex are mutually exclusive in ffmpeg, so the
// scale lives inside the complex graph here.
func (r Rendition) burnInArgs(burnIndex int) []string {
	// overlay composites [0:v:0] (first video stream) with the bitmap subtitle
	// stream at the absolute source index. min(ih,H) keeps the honest-ABR cap.
	var graphParts []string
	videoInput := "[0:v:0]"
	chain := r.toneMapChain()
	if len(chain.Filters) > 0 {
		graphParts = append(graphParts, "[0:v:0]"+strings.Join(chain.Filters, ",")+"[vtonemap]")
		videoInput = "[vtonemap]"
	}
	if interlacedFieldOrder(r.FieldOrder) {
		graphParts = append(graphParts, videoInput+"yadif=deint=interlaced[vdeint]")
		videoInput = "[vdeint]"
	}
	postOverlay := []string{}
	if r.Height > 0 {
		postOverlay = append(postOverlay, fmt.Sprintf("scale=-2:min(ih\\,%d)", r.Height))
	}
	if nonSquareSampleAspect(r.SampleAspectRatio) {
		postOverlay = append(postOverlay, "setsar=1")
	}
	postOverlay = append(postOverlay, r.encoderIngestFilters(chain)...)
	graph := fmt.Sprintf("%s[0:%d]overlay[vsub]", videoInput, burnIndex)
	mapLabel := "[vsub]"
	if len(postOverlay) > 0 {
		graph = fmt.Sprintf("%s[0:%d]overlay[vo];[vo]%s[vsub]", videoInput, burnIndex, strings.Join(postOverlay, ","))
	}
	graphParts = append(graphParts, graph)
	audioMap := "0:a:0?"
	if r.AudioStreamIndex != nil {
		audioMap = fmt.Sprintf("0:%d", *r.AudioStreamIndex)
	}
	out := []string{"-filter_complex", graph, "-map", mapLabel, "-map", audioMap}
	if len(graphParts) > 1 {
		out[1] = strings.Join(graphParts, ";")
	}
	return append(out, r.rateControlArgs()...)
}

// hlsStderrTailBytes bounds the stderr an HLS process keeps for its exit
// diagnostic. The encoders run at -v error, so a few KB holds the last errors
// without letting a chatty failure grow server memory.
const hlsStderrTailBytes = 4 << 10

// hlsStderrWaitDelay bounds how long Wait lets a stray descendant holding the
// stderr pipe delay the reap once the leader has exited.
const hlsStderrWaitDelay = time.Second

const hlsStopBound = time.Second

// HLSProcess is a running HLS transcode owned by its caller.
type HLSProcess struct {
	cmd     *exec.Cmd
	wait    sync.Once
	done    chan struct{}
	waitErr error
	// signalMu serializes signals through the final group kill, before Wait
	// releases the leader PID for reuse.
	signalMu   sync.Mutex
	paused     bool
	stopping   bool
	killed     bool
	escalation *time.Timer
	// stopRequested records that the owner asked for this exit (Stop or context
	// cancellation), so a supervisor can tell an expected kill from a crash.
	stopRequested atomic.Bool
	stderr        lockedTail
	// progress parses the -progress stream on stdout when the argv asked for
	// one; progressedAt is the last advance of the encoder's output time as an
	// offset from started, 0 until the first advance. The offset keeps the
	// monotonic clock reading, so a wall-clock jump cannot fake a stall.
	progress        progressWriter
	started         time.Time
	progressedAt    atomic.Int64
	reportsProgress bool
	// stderrWithheld marks an encrypted live run, whose stderr is discarded:
	// FFmpeg there decodes an Encrypted Library's plaintext, and its
	// diagnostics (or a compromised decoder) could carry that plaintext into
	// the server's logs.
	stderrWithheld bool
}

// hlsProgressLineBytes bounds a partial -progress line kept between writes;
// real lines are a few dozen bytes, so anything longer is discarded.
const hlsProgressLineBytes = 4 << 10

// progressWriter consumes ffmpeg's -progress key=value stream and reports each
// increase of out_time_us. Anything else in the stream is ignored.
type progressWriter struct {
	mu      sync.Mutex
	line    []byte
	lastOut int64
	advance func()
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, b := range p {
		if b != '\n' {
			if len(w.line) < hlsProgressLineBytes {
				w.line = append(w.line, b)
			}
			continue
		}
		w.parseLine(string(w.line))
		w.line = w.line[:0]
	}
	return len(p), nil
}

func (w *progressWriter) parseLine(line string) {
	value, ok := strings.CutPrefix(strings.TrimSpace(line), "out_time_us=")
	if !ok {
		return
	}
	out, err := strconv.ParseInt(value, 10, 64)
	if err != nil || out <= w.lastOut {
		return // N/A before the first frame, or no forward motion
	}
	w.lastOut = out
	if w.advance != nil {
		w.advance()
	}
}

// requestsProgress reports whether argv asks ffmpeg to write its -progress
// stream to stdout, the stream ProgressedAt is computed from.
func requestsProgress(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-progress" && args[i+1] == "pipe:1" {
			return true
		}
	}
	return false
}

// lockedTail is a goroutine-safe tailBuffer: exec's copy goroutine writes
// while a supervisor may read the tail.
type lockedTail struct {
	mu   sync.Mutex
	tail tailBuffer
}

func (l *lockedTail) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tail.Write(p)
}

func (l *lockedTail) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tail.String()
}

// startHLSProcess starts an HLS command. Concurrent Wait and Stop calls share
// a single exec.Cmd.Wait. Unless the caller routes stderr, StderrTail keeps
// its bounded tail.
func startHLSProcess(cmd *exec.Cmd) (*HLSProcess, error) {
	applySysProcAttr(cmd)
	configureProcessGroup(cmd)
	p := &HLSProcess{cmd: cmd, done: make(chan struct{})}
	p.stderr.tail.limit = hlsStderrTailBytes
	cmd.Cancel = func() error {
		p.stopRequested.Store(true)
		p.killTree()
		return nil
	}
	if cmd.Stderr == nil {
		cmd.Stderr = &p.stderr
		cmd.WaitDelay = hlsStderrWaitDelay
	}
	reportsProgress := cmd.Stdout == nil && requestsProgress(cmd.Args)
	if reportsProgress {
		p.progress.advance = func() { p.progressedAt.Store(int64(time.Since(p.started))) }
		cmd.Stdout = &p.progress
		cmd.WaitDelay = hlsStderrWaitDelay
	}
	p.started = time.Now()
	p.reportsProgress = reportsProgress
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return p, nil
}

// StartHLSCommand starts an already-built command under the same HLS process
// lifecycle the Runner's starters use; cmd must come from exec.CommandContext.
// It is the seam for a Transcoder that
// builds its own command (test fakes standing in for ffmpeg); production
// starters build the hardened ffmpeg command themselves.
func StartHLSCommand(cmd *exec.Cmd) (*HLSProcess, error) {
	return startHLSProcess(cmd)
}

func (p *HLSProcess) killTree() {
	p.signalMu.Lock()
	defer p.signalMu.Unlock()
	p.killTreeLocked()
}

// killTreeLocked disarms any pending escalation before releasing the leader PID.
func (p *HLSProcess) killTreeLocked() {
	if p.killed {
		return
	}
	p.stopping = true
	p.killed = true
	if p.escalation != nil {
		p.escalation.Stop()
		p.escalation = nil
	}
	p.killGroup()
}

// Running reports whether this process wraps a started OS child.
func (p *HLSProcess) Running() bool {
	return p != nil && p.cmd != nil && p.cmd.Process != nil
}

// Pause suspends the encoder's process group. It reports whether the group
// is paused, and returns false on unsupported platforms or during teardown.
func (p *HLSProcess) Pause() bool {
	if !SupportsHLSPause || !p.Running() {
		return false
	}
	p.signalMu.Lock()
	defer p.signalMu.Unlock()
	if p.stopping {
		return false
	}
	if !p.paused {
		p.paused = p.pauseGroup()
	}
	return p.paused
}

// Resume continues the encoder's process group. It returns false on
// unsupported platforms, during teardown, or if a paused group cannot resume.
func (p *HLSProcess) Resume() bool {
	if !SupportsHLSPause || !p.Running() {
		return false
	}
	p.signalMu.Lock()
	defer p.signalMu.Unlock()
	if p.stopping {
		return false
	}
	if p.paused && p.resumeGroup() {
		p.paused = false
	}
	return !p.paused
}

// Wait blocks until the transcode exits and exec.Cmd.Wait finishes.
func (p *HLSProcess) Wait() error {
	if !p.Running() {
		return nil
	}
	p.startWait()
	<-p.done
	return p.waitErr
}

func (p *HLSProcess) startWait() {
	p.wait.Do(func() {
		go func() {
			p.waitErr = p.waitForExit()
			close(p.done)
		}()
	})
}

// Stop requests termination and joins the leader. If the join times out, the
// caller must keep resources the process could still use until Wait completes.
func (p *HLSProcess) Stop() error {
	if !p.Running() {
		return nil
	}
	p.stopRequested.Store(true)
	p.signalMu.Lock()
	if !p.stopping {
		p.stopping = true
		p.terminateGroupLocked()
	}
	p.signalMu.Unlock()

	p.startWait()
	timer := time.NewTimer(hlsStopBound)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("ffmpeg: encoder stop did not join within %s", hlsStopBound)
	}
}

// Kill terminates the process tree without marking the exit as requested, so
// a supervisor that kills a wedged encoder still sees a failed run.
func (p *HLSProcess) Kill() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	p.killTree()
}

// ReportsProgress reports whether the process writes ffmpeg's -progress stream,
// which is what makes ProgressedAt meaningful.
func (p *HLSProcess) ReportsProgress() bool {
	return p != nil && p.reportsProgress
}

// ProgressedAt returns when the encoder's output time last advanced, or its
// start time before the first advance. It only moves when ReportsProgress.
func (p *HLSProcess) ProgressedAt() time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.started.Add(time.Duration(p.progressedAt.Load()))
}

// StopRequested reports whether the owner asked this process to exit (Stop or
// context cancellation). An exit without it is the encoder's own: end of input
// when Wait returns nil, a crash or an outside kill otherwise.
func (p *HLSProcess) StopRequested() bool {
	return p != nil && p.stopRequested.Load()
}

// StderrTail returns the last few KB the process wrote to stderr. It is
// complete once Wait has returned.
func (p *HLSProcess) StderrTail() string {
	if p == nil {
		return ""
	}
	if p.stderrWithheld {
		return "stderr withheld (encrypted Library)"
	}
	return strings.TrimSpace(p.stderr.String())
}

// StartHLS launches an H.264/AAC HLS transcode of src into dir, writing
// index.m3u8 plus numbered fMP4 segments (init.mp4 + seg*.m4s — CMAF, so
// MSE-based players like hls.js can play it, not just native HLS). The event
// playlist type keeps every segment listed so clients can seek back without a
// VOD finalize step; ffmpeg emits the #EXT-X-MAP line itself here. temp_file
// makes segment publication atomic (write .tmp, rename), so a watcher never
// reads a half-written fragment.
func (r *Runner) StartHLS(ctx context.Context, src, dir string, rendition Rendition) (*HLSProcess, error) {
	return r.startHLS(ctx, src, dir, rendition, false)
}

// StartBackgroundHLS launches an HLS encode for background generation work
// (precomputed renditions). It uses the same args as StartHLS but applies the
// generation FFmpeg niceness policy where the platform supports it.
func (r *Runner) StartBackgroundHLS(ctx context.Context, src, dir string, rendition Rendition) (*HLSProcess, error) {
	return r.startHLS(ctx, src, dir, rendition, true)
}

func (r *Runner) startHLS(ctx context.Context, src, dir string, rendition Rendition, background bool) (*HLSProcess, error) {
	if err := checkPath(src); err != nil {
		return nil, err
	}
	if err := checkPath(dir); err != nil {
		return nil, err
	}
	if reason := rendition.ToneMapRefusal(); reason != "" {
		return nil, errors.New("ffmpeg: " + reason)
	}
	if !rendition.AudioOnly {
		rendition = r.launchRendition(rendition)
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	args := hlsArgs(src, dir, rendition)
	// Confine to the source file's directory (read-only) and the segment output
	// directory (read-write), plus the GPU when the rendition runs on one.
	conf := dirConfinement(src, dir).withGPU(rendition.hwAccel, rendition.toneMapAccel)
	var cmd *exec.Cmd
	if background {
		cmd = r.generationCommand(ctx, conf, ffmpegPath, args...)
	} else {
		cmd = r.confinedCommand(ctx, ffmpegPath, conf, args)
	}
	process, err := startHLSProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("starting ffmpeg transcode: %w", err)
	}
	return process, nil
}

// hlsProgressArgs asks ffmpeg for its machine-readable progress stream on
// stdout, which the HLS process turns into ProgressedAt so a supervisor can
// tell a hung encoder from a slow one.
func hlsProgressArgs() []string {
	return []string{"-progress", "pipe:1"}
}

func hlsArgs(src, dir string, rendition Rendition) []string {
	args := []string{
		"-v", "error",
	}
	args = append(args, hlsProgressArgs()...)
	args = append(args, rendition.inputArgs()...)
	args = append(args, inputProtocolArgs()...)
	args = append(args,
		"-i", src,
	)
	if rendition.AudioOnly {
		args = append(args, "-map", audioOnlyMap(rendition))
		args = append(args, rendition.audioArgs()...)
		return append(args, hlsMuxArgs(dir)...)
	}
	args = append(args, rendition.videoArgs()...)
	args = append(args, rendition.selectedStreamMapArgs()...)
	if !rendition.CopyVideo {
		args = append(args, "-force_key_frames", "expr:gte(t,n_forced*4)")
	}
	args = append(args, rendition.args()...)
	args = append(args, rendition.audioArgs()...)
	return append(args, hlsMuxArgs(dir)...)
}

// selectedStreamMapArgs maps the first video stream and the selected audio
// stream when an audio stream is selected. A burned subtitle maps its own
// overlay output and the selected audio in burnInArgs instead, and with no
// selection FFmpeg's default stream choice stands.
func (r Rendition) selectedStreamMapArgs() []string {
	if _, burnsSubtitle := r.burnsSubtitle(); r.AudioStreamIndex == nil || burnsSubtitle {
		return nil
	}
	return []string{"-map", "0:v:0", "-map", fmt.Sprintf("0:%d", *r.AudioStreamIndex)}
}

// audioOnlyMap is the single -map an audio-only rendition encodes: the
// selected audio stream, or the first audio stream when none is selected.
func audioOnlyMap(rendition Rendition) string {
	if rendition.AudioStreamIndex != nil {
		return fmt.Sprintf("0:%d", *rendition.AudioStreamIndex)
	}
	return "0:a:0"
}

// hlsMuxArgs is the CMAF fMP4 HLS muxer tail every file-to-HLS encode shares:
// 4-second segments, an event playlist written through temp files, and the
// init/segment/playlist names the HLS serving path expects in dir.
func hlsMuxArgs(dir string) []string {
	return []string{
		"-f", "hls",
		"-hls_time", "4",
		"-hls_playlist_type", "event",
		"-hls_flags", "temp_file",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(dir, "seg%05d.m4s"),
		"--", filepath.Join(dir, "index.m3u8"),
	}
}

// StartHLSSegments transcodes src into numbered fMP4 segments (init.mp4 +
// seg*.m4s) beginning at startSegment (time offset startSegment*segmentSeconds).
// Keyframes are forced on segment boundaries so the segment map a synthesized
// VOD playlist declares stays accurate, and mux timestamps are offset to the
// absolute position with -output_ts_offset. With fmp4 segments, ffmpeg 8.1
// materializes that offset as a sidx earliestPresentationTime plus an
// empty-edit elst in init.mp4 — tfdt baseMediaDecodeTime still restarts at 0
// per process, so a restart's segments contradict an init a client fetched
// earlier. Both hls.js and Apple's CoreMedia HLS engine (Safari/AVPlayer)
// ignore the container timestamps and anchor fragments to playlist positions,
// so far seeks survive anyway — verified live against both on 2026-06-11. The
// hls muxer is used for its fMP4 segmenter only: its own playlist goes to
// live.m3u8, which nothing serves — the synthesized VOD index.m3u8 is the one
// clients see. A restart rewrites init.mp4 with identical encoder parameters
// (only the elst offset differs, which players ignore per the above), so the
// map a client already fetched stays valid.
func (r *Runner) StartHLSSegments(ctx context.Context, src, dir string, startSegment, segmentSeconds int, rendition Rendition) (*HLSProcess, error) {
	if picture, ok := rendition.Picture(); ok {
		if err := checkPath(src); err != nil {
			return nil, err
		}
		if err := checkPath(dir); err != nil {
			return nil, err
		}
		return r.startComposedHLSSegments(ctx, src, dir, picture, startSegment, segmentSeconds, rendition)
	}
	return r.startHLSSegmentProcess(ctx, src, dir, rendition, func(rendition Rendition) []string {
		return hlsSegmentArgs(src, dir, startSegment, segmentSeconds, rendition)
	})
}

// keyframeSegmentHLSTime is the -hls_time for keyframe-aligned segmenting. The
// hls muxer cuts at the first keyframe whose elapsed time reaches
// hls_time*segment_number, so with a uniform target the cut points depend on
// where a restart began. A target below any GOP length makes every keyframe a
// cut, so each segment is exactly one source GOP whatever the start.
const keyframeSegmentHLSTime = "0.001"

// StartHLSKeyframeSegments segments a copied video stream at its own keyframes,
// one segment per source GOP, beginning at startSegment whose first keyframe is
// presented at start (a time from a KeyframeIndex, relative to the container
// start). The input -ss and -output_ts_offset are both exactly start, so the
// copied keyframe that begins startSegment is stamped at its real time and the
// transcoded audio is trimmed to the same instant. Only copy-video renditions
// without an audio adjustment are accepted: a re-encode has no source GOP to
// follow, and a copied picture cannot follow a changed tempo.
func (r *Runner) StartHLSKeyframeSegments(ctx context.Context, src, dir string, startSegment int, start time.Duration, rendition Rendition) (*HLSProcess, error) {
	if !rendition.CopyVideo {
		return nil, errors.New("ffmpeg: keyframe-aligned segments need a copied video stream")
	}
	if !rendition.adjust.IsZero() {
		return nil, errors.New("ffmpeg: keyframe-aligned segments cannot carry an audio adjustment")
	}
	if startSegment < 0 || start < 0 {
		return nil, fmt.Errorf("ffmpeg: invalid keyframe segment start %d at %v", startSegment, start)
	}
	return r.startHLSSegmentProcess(ctx, src, dir, rendition, func(rendition Rendition) []string {
		return keyframeSegmentArgs(src, dir, startSegment, start, rendition)
	})
}

// startHLSSegmentProcess starts a segmenting encode for a rendition without a
// composed picture. buildArgs receives the rendition after hardware decode and
// tone-map backend selection.
func (r *Runner) startHLSSegmentProcess(ctx context.Context, src, dir string, rendition Rendition, buildArgs func(Rendition) []string) (*HLSProcess, error) {
	if err := checkPath(src); err != nil {
		return nil, err
	}
	if err := checkPath(dir); err != nil {
		return nil, err
	}
	if reason := rendition.ToneMapRefusal(); reason != "" {
		return nil, errors.New("ffmpeg: " + reason)
	}
	if !rendition.adjust.IsZero() && !rendition.AudioOnly {
		return nil, errors.New("ffmpeg: an adjusted encode must be audio-only or a composed picture")
	}
	if !rendition.AudioOnly {
		rendition = r.launchRendition(rendition)
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	args := buildArgs(rendition)
	conf := dirConfinement(src, dir).withGPU(rendition.hwAccel, rendition.toneMapAccel)
	cmd := r.confinedCommand(ctx, ffmpegPath, conf, args)
	process, err := startHLSProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("starting ffmpeg transcode: %w", err)
	}
	return process, nil
}

// hlsSegmentArgs is the StartHLSSegments command line for a rendition without
// a composed picture. The input seek is the source position of startSegment,
// which an adjusted tempo scales; the output timestamps stay on the output
// timeline.
func hlsSegmentArgs(src, dir string, startSegment, segmentSeconds int, rendition Rendition) []string {
	return segmentArgsFor(fileSegmentIO(src, dir), startSegment, segmentSeconds, rendition)
}

// segmentArgsFor is hlsSegmentArgs over any segment input and output.
func segmentArgsFor(io segmentIO, startSegment, segmentSeconds int, rendition Rendition) []string {
	seek := formatSeconds(rendition.adjust.SourceSeconds(float64(startSegment * segmentSeconds)))
	var forceKeyframes []string
	// Forced keyframes align segment boundaries for the re-encode path. A
	// copied video stream keeps its source GOP structure, so the encoder-only
	// -force_key_frames is omitted for it (it would be ignored on the copied
	// stream anyway).
	if !rendition.CopyVideo {
		forceKeyframes = []string{"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds)}
	}
	if startSegment == 0 && rendition.KeepPriming {
		seek = ""
	}
	return segmentArgs(io, startSegment, seek, strconv.Itoa(startSegment*segmentSeconds), strconv.Itoa(segmentSeconds), rendition.selectedStreamMapArgs(), forceKeyframes, rendition)
}

// keyframeSegmentArgs is the StartHLSKeyframeSegments command line: the input
// seek and the output offset are both the keyframe's presentation time. The
// keyframe times come from the first video stream (ProbeKeyframes reads v:0),
// so that stream is always mapped: FFmpeg's own choice among several video
// streams may be another one with different keyframes (CORE-1735). Mapping
// disables the automatic audio choice too, so the selected audio stream is
// mapped, or the first one when none is selected.
//
// The copied streams start exactly at the seek (-copypriorss 0). FFmpeg backs
// an input seek off by a fraction of a second when the video has B-frames, so
// a seek to a keyframe's own time lands on the keyframe before it, and stream
// copy keeps the packets ahead of the seek point by default. That whole
// earlier GOP, stamped before start, would then fill startSegment and push the
// requested GOP into the next file. With a transcoded E-AC-3 track it is worse:
// the segmenter cuts at the requested keyframe before any audio packet reached
// the muxer, so init.mp4 is written without its dec3 box (unreadable) and
// startSegment carries no media at all (CORE-2618). Copy then drops any packet
// stamped before the seek, and keyframe times are rounded up to the
// microsecond, so a keyframe between two microseconds (a fractional frame rate
// in a fine MP4 timescale) would sit just before a seek to its own rounded time
// and be dropped with its whole GOP. The seek and output offset therefore sit
// one microsecond earlier: the keyframe stays at or after the copy start, the
// frame before it (a whole frame earlier) does not, and output timestamps are
// unchanged because the offset matches the seek. An audio-only rendition takes
// no -copypriorss and keeps the seek as given.
func keyframeSegmentArgs(src, dir string, startSegment int, start time.Duration, rendition Rendition) []string {
	copyStart := start
	if copyStart > 0 && !rendition.AudioOnly {
		copyStart -= time.Microsecond
	}
	offset := formatMicros(copyStart)
	audioMap := "0:a:0?"
	if rendition.AudioStreamIndex != nil {
		audioMap = fmt.Sprintf("0:%d", *rendition.AudioStreamIndex)
	}
	streamMaps := []string{"-map", "0:v:0", "-map", audioMap}
	seek := offset
	if start == 0 && rendition.KeepPriming {
		seek = ""
	}
	return segmentArgs(fileSegmentIO(src, dir), startSegment, seek, offset, keyframeSegmentHLSTime, streamMaps, []string{"-copypriorss", "0"}, rendition)
}

// segmentArgs assembles a segmenting command line: seek is the input position
// ("" for no input seek, see Rendition.KeepPriming), outputOffset the first
// output timestamp. An audio-only rendition maps just the selected audio
// stream and drops the video arguments; a video rendition adds streamMaps and
// videoOutputArgs (forced keyframes, or the stream-copy start).
func segmentArgs(io segmentIO, startSegment int, seek, outputOffset, hlsTime string, streamMaps, videoOutputArgs []string, rendition Rendition) []string {
	args := []string{
		"-v", "error",
	}
	args = append(args, hlsProgressArgs()...)
	if seek != "" {
		args = append(args, "-ss", seek)
	}
	args = append(args, rendition.inputArgs()...)
	args = append(args, io.inputOptions...)
	args = append(args,
		"-i", io.input,
	)
	if rendition.AudioOnly {
		args = append(args, "-map", audioOnlyMap(rendition))
	} else {
		args = append(args, rendition.videoArgs()...)
		args = append(args, streamMaps...)
		args = append(args, videoOutputArgs...)
		args = append(args, rendition.args()...)
	}
	args = append(args, rendition.audioArgs()...)
	args = append(args, segmentMuxBase(startSegment, outputOffset, hlsTime)...)
	return append(args, io.outputArgs...)
}

// segmentIO is where a segmenting encode reads its input and writes its
// segments: local files, or a loopback endpoint (ADR-0236).
type segmentIO struct {
	input        string
	inputOptions []string
	// outputArgs follow the shared muxer options: the output's own options,
	// the segment name template and the playlist operand.
	outputArgs []string
}

// fileSegmentIO reads src under the file-only protocol allowlist and writes
// the segments into dir, each renamed into place when complete.
func fileSegmentIO(src, dir string) segmentIO {
	return segmentIO{
		input:        src,
		inputOptions: inputProtocolArgs(),
		outputArgs: []string{
			"-hls_flags", "temp_file",
			"-hls_segment_filename", filepath.Join(dir, "seg%05d.m4s"),
			"--", filepath.Join(dir, "live.m3u8"),
		},
	}
}

// segmentMuxBase is the muxer configuration every segmenting encode shares.
func segmentMuxBase(startSegment int, outputOffset, hlsTime string) []string {
	return []string{
		"-output_ts_offset", outputOffset,
		"-f", "hls",
		"-hls_time", hlsTime,
		"-hls_list_size", "0",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-start_number", strconv.Itoa(startSegment),
	}
}

// segmentMuxArgs is the numbered-segment muxer tail StartHLSSegments
// documents: timestamps offset to outputOffset, fMP4 segments numbered from
// startSegment, and an unserved live.m3u8.
func segmentMuxArgs(dir string, startSegment int, outputOffset, hlsTime string) []string {
	return append(segmentMuxBase(startSegment, outputOffset, hlsTime), fileSegmentIO("", dir).outputArgs...)
}

// probeDurationSeconds is the container duration, or — when the container
// declares none (a remuxed or live-captured MKV often leaves format.duration
// empty, CORE-2314) — the longest stream duration, then the longest DURATION
// tag ("HH:MM:SS.fraction", as mkvmerge writes it) on a stream or the format.
// It returns 0 when nothing declares a usable duration.
func probeDurationSeconds(doc *probeDocument) float64 {
	if seconds, ok := parseBoundedNumber(doc.Format.Duration, "", 0, maxMediaSeconds); ok && seconds > 0 {
		return seconds
	}
	longest := 0.0
	for _, stream := range doc.Streams {
		if seconds, ok := parseBoundedNumber(stream.Duration, "", 0, maxMediaSeconds); ok && seconds > longest {
			longest = seconds
		}
	}
	if longest > 0 {
		return longest
	}
	for _, stream := range doc.Streams {
		if seconds := durationTagSeconds(stream.Tags); seconds > longest {
			longest = seconds
		}
	}
	if longest > 0 {
		return longest
	}
	return durationTagSeconds(doc.Format.Tags)
}

// durationTagSeconds reads the longest DURATION / DURATION-<lang> tag.
func durationTagSeconds(tags map[string]string) float64 {
	longest := 0.0
	for key, value := range tags {
		upper := strings.ToUpper(key)
		if upper != "DURATION" && !strings.HasPrefix(upper, "DURATION-") {
			continue
		}
		if seconds, ok := parseClockDuration(value); ok && seconds > longest {
			longest = seconds
		}
	}
	return longest
}

// parseClockDuration parses "HH:MM:SS[.fraction]" into seconds, bounded by
// maxMediaSeconds.
func parseClockDuration(raw string) (float64, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 {
		return 0, false
	}
	hours, err := strconv.Atoi(parts[0])
	if err != nil || hours < 0 {
		return 0, false
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil || minutes < 0 || minutes > 59 {
		return 0, false
	}
	seconds, ok := parseBoundedNumber(parts[2], "", 0, 60)
	if !ok || seconds >= 60 {
		return 0, false
	}
	total := float64(hours)*3600 + float64(minutes)*60 + seconds
	if total > maxMediaSeconds {
		return 0, false
	}
	return total, true
}

type probeDocument struct {
	Format struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		Index            int               `json:"index"`
		CodecType        string            `json:"codec_type"`
		CodecName        string            `json:"codec_name"`
		Duration         string            `json:"duration"`
		Profile          string            `json:"profile"`
		Level            int               `json:"level"`
		Width            int               `json:"width"`
		Height           int               `json:"height"`
		Channels         int               `json:"channels"`
		ChannelLayout    string            `json:"channel_layout"`
		BitsPerRawSample string            `json:"bits_per_raw_sample"`
		BitsPerSample    int               `json:"bits_per_sample"`
		SampleRate       string            `json:"sample_rate"`
		SampleAspect     string            `json:"sample_aspect_ratio"`
		DisplayAspect    string            `json:"display_aspect_ratio"`
		FieldOrder       string            `json:"field_order"`
		RFrameRate       string            `json:"r_frame_rate"`
		AvgFrameRate     string            `json:"avg_frame_rate"`
		ColorTransfer    string            `json:"color_transfer"`
		ColorPrimaries   string            `json:"color_primaries"`
		Disposition      map[string]int    `json:"disposition"`
		Tags             map[string]string `json:"tags"`
		SideDataList     []struct {
			SideDataType string `json:"side_data_type"`
			DVProfile    int    `json:"dv_profile"`
			DVBLCompatID int    `json:"dv_bl_signal_compatibility_id"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Chapters []struct {
		StartTime string            `json:"start_time"`
		EndTime   string            `json:"end_time"`
		Tags      map[string]string `json:"tags"`
	} `json:"chapters"`
}

func parseProbe(data []byte, path string) (Probe, error) {
	var doc probeDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return Probe{}, fmt.Errorf("parsing ffprobe output: %w", err)
	}
	probe := Probe{Container: containerFrom(doc.Format.FormatName, path), embeddedArtworkStream: -1}
	for _, stream := range doc.Streams {
		switch stream.CodecType {
		case "video":
			// Embedded cover art (attached_pic) is an mjpeg/png "video" stream;
			// it never makes a track a video (CORE-1143).
			if probe.VideoCodec == "" && stream.Disposition["attached_pic"] != 1 {
				probe.VideoCodec = stream.CodecName
				probe.HasVideo = true
				probe.VideoWidth = stream.Width
				probe.VideoHeight = stream.Height
			}
		case "audio":
			if probe.AudioCodec == "" {
				probe.AudioCodec = stream.CodecName
			}
		}
		ps := ProbeStream{
			Index:              stream.Index,
			Type:               stream.CodecType,
			Codec:              stream.CodecName,
			Profile:            cleanProfile(stream.Profile),
			Level:              stream.Level,
			Language:           stream.Tags["language"],
			Title:              strings.TrimSpace(caseInsensitiveTag(stream.Tags, "title")),
			Default:            stream.Disposition["default"] == 1,
			Forced:             stream.Disposition["forced"] == 1,
			HearingImpaired:    stream.Disposition["hearing_impaired"] == 1,
			VisualImpaired:     stream.Disposition["visual_impaired"] == 1,
			Commentary:         stream.Disposition["comment"] == 1,
			AttachedPicture:    stream.Disposition["attached_pic"] == 1,
			Channels:           stream.Channels,
			ChannelLayout:      stream.ChannelLayout,
			BitDepth:           bitDepthFrom(stream.BitsPerRawSample, stream.BitsPerSample),
			SampleRate:         sampleRateFrom(stream.CodecType, stream.SampleRate),
			SampleAspectRatio:  stream.SampleAspect,
			DisplayAspectRatio: stream.DisplayAspect,
			FieldOrder:         stream.FieldOrder,
			FrameRate:          stream.RFrameRate,
			AverageFrameRate:   stream.AvgFrameRate,
			ColorTransfer:      stream.ColorTransfer,
			ColorPrimaries:     stream.ColorPrimaries,
		}
		if ps.Type == "subtitle" {
			applySubtitleTitleFlags(&ps)
		}
		if ps.AttachedPicture && probe.embeddedArtworkStream < 0 {
			probe.embeddedArtworkStream = stream.Index
		}
		for _, side := range stream.SideDataList {
			if isHDR10PlusSideData(side.SideDataType) {
				ps.HDR10Plus = true
			}
			if side.DVProfile > 0 {
				ps.DoViProfile = side.DVProfile
				ps.DoViBLCompatID = side.DVBLCompatID
			}
		}
		probe.Streams = append(probe.Streams, ps)
		if stream.CodecType == "attachment" && isFontMime(stream.Tags["mimetype"]) {
			probe.FontAttachments = append(probe.FontAttachments, FontAttachment{
				Filename: stream.Tags["filename"],
				MimeType: stream.Tags["mimetype"],
			})
		}
	}
	if len(doc.Format.Tags) > 0 {
		probe.Tags = make(map[string]string, len(doc.Format.Tags))
		for key, value := range doc.Format.Tags {
			probe.Tags[strings.ToLower(key)] = value
		}
	}
	for _, chapter := range doc.Chapters {
		start, ok := parseProbeTimestamp(chapter.StartTime)
		if !ok {
			continue
		}
		end, ok := parseProbeTimestamp(chapter.EndTime)
		if !ok || end <= start {
			continue
		}
		title := strings.TrimSpace(caseInsensitiveTag(chapter.Tags, "title"))
		if title == "" {
			continue
		}
		probe.Chapters = append(probe.Chapters, ProbeChapter{
			StartSeconds: start,
			EndSeconds:   end,
			Title:        title,
		})
	}
	if seconds := probeDurationSeconds(&doc); seconds > 0 {
		probe.DurationSeconds = int(math.Round(seconds))
	}
	if probe.VideoCodec == "" && probe.AudioCodec == "" && probe.DurationSeconds == 0 {
		return Probe{}, errors.New("ffprobe found no media streams or duration")
	}
	return probe, nil
}

func parseProbeTimestamp(value string) (float64, bool) {
	return parseBoundedNumber(value, "", 0, maxMediaSeconds)
}

// applySubtitleTitleFlags sets the forced, hearing-impaired and commentary
// flags a subtitle stream's title names but its dispositions do not: many
// files mark "English (SDH)" or "Forced" only in the title (CORE-2523).
// "non-SDH", "Not Forced" and "No SDH or CC" deny the roles they name: a
// negation covers the role words (and "and", "or", "nor") after it, up to the
// next bracket, comma, semicolon, bar or spaced dash, so "(non-SDH) [Forced]"
// and "non-SDH - Forced" are forced.
func applySubtitleTitleFlags(ps *ProbeStream) {
	words := subtitleTitleWords(ps.Title)
	negated := false
	for i, word := range words {
		switch word {
		case "non", "not", "no":
			negated = true
		case "sdh", "cc":
			ps.HearingImpaired = ps.HearingImpaired || !negated
		case "hearing":
			if i+1 < len(words) && words[i+1] == "impaired" {
				ps.HearingImpaired = ps.HearingImpaired || !negated
			}
		case "impaired", "and", "or", "nor":
			// Inside a role phrase or list; a negation keeps running.
		case "forced":
			ps.Forced = ps.Forced || !negated
		case "commentary":
			ps.Commentary = ps.Commentary || !negated
		default:
			negated = false
		}
	}
}

// subtitleTitleWords lowercases a title into its letter/digit words, with ","
// standing for each qualifier boundary (a bracket, comma, semicolon, bar, en or
// em dash, or a hyphen with a space beside it; "non-SDH" stays one phrase).
func subtitleTitleWords(title string) []string {
	var words []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	runes := []rune(strings.ToLower(title))
	for i, r := range runes {
		spacedHyphen := r == '-' && ((i > 0 && unicode.IsSpace(runes[i-1])) ||
			(i+1 < len(runes) && unicode.IsSpace(runes[i+1])))
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(r)
		case spacedHyphen || strings.ContainsRune("()[]{},;|–—", r):
			flush()
			words = append(words, ",")
		default:
			flush()
		}
	}
	flush()
	return words
}

func caseInsensitiveTag(tags map[string]string, name string) string {
	for key, value := range tags {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// cleanProfile normalises ffprobe's profile field: ffprobe emits "unknown"
// (lowercase) for streams with no meaningful profile, which is noise we store
// as "".
func cleanProfile(profile string) string {
	if profile == "unknown" {
		return ""
	}
	return profile
}

// bitDepthFrom reads the coded sample bit depth. ffprobe reports
// bits_per_raw_sample as a string on video streams (e.g. "10" for Main 10) and
// bits_per_sample as a number on audio/PCM streams; either may be absent (0/"").
func bitDepthFrom(raw string, perSample int) int {
	if raw != "" {
		if n, ok := parseBoundedInteger(raw, 1, 64); ok {
			return int(n)
		}
	}
	if perSample >= 1 && perSample <= 64 {
		return perSample
	}
	return 0
}

// maxProbeSampleRate bounds a reported audio sample rate (Hz). It sits well
// above the highest PCM and DSD rates FFmpeg reports, so only a corrupt value
// is dropped.
const maxProbeSampleRate = 50_000_000

// sampleRateFrom parses ffprobe's sample_rate string for an audio stream
// (CORE-1627). Non-audio streams, an absent field and an out-of-range value
// all report 0, which means unreported.
func sampleRateFrom(codecType, raw string) int {
	if codecType != "audio" || raw == "" {
		return 0
	}
	if n, ok := parseBoundedInteger(raw, 1, maxProbeSampleRate); ok {
		return int(n)
	}
	return 0
}

// isFontMime reports whether an attachment's MIME type is a font (ADR-0010 §4:
// only font attachments matter — libass renders ASS with them). Covers the
// common embedded-subtitle font types muxers emit.
func isFontMime(mime string) bool {
	switch strings.ToLower(mime) {
	case "application/x-truetype-font",
		"application/x-font-ttf",
		"application/font-sfnt",
		"font/ttf",
		"font/otf",
		"font/sfnt",
		"application/vnd.ms-opentype",
		"application/x-font-otf":
		return true
	}
	return false
}

func isHDR10PlusSideData(sideDataType string) bool {
	sideDataType = strings.ToLower(sideDataType)
	return strings.Contains(sideDataType, "hdr10+") ||
		strings.Contains(sideDataType, "smpte2094-40") ||
		strings.Contains(sideDataType, "smpte st 2094-40")
}

// containerFrom maps ffprobe's comma-separated format_name (e.g.
// "mov,mp4,m4a,3gp,3g2,mj2") to the single container word the capability
// profiles speak. A file extension that names a member of the probed family
// wins; otherwise the family's first name stands.
func containerFrom(formatName, path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	names := strings.Split(formatName, ",")
	for _, name := range names {
		if name == ext {
			return ext
		}
	}
	if ext == "mp4" || ext == "m4v" || ext == "mov" {
		for _, name := range names {
			if name == "mp4" || name == "mov" {
				return ext
			}
		}
	}
	if names[0] != "" {
		return names[0]
	}
	return ext
}
