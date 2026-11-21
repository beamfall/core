package ffmpeg

import (
	"context"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

// Gapless facts (CORE-1626). Lossy encoders prime their output with a run of
// decoder warm-up samples (the encoder delay) and pad the last frame out to a
// whole frame (the end padding). A player that trims neither leaves a short
// gap between two tracks cut from one continuous recording.
//
// FFmpeg's demuxers report both as "Skip Samples" packet side data, whatever
// the container's own signalling (the MP4 edit list, the LAME/Xing header,
// the Opus pre-skip): skip_samples on a stream's first packet is the delay,
// and discard_padding on its last packet is the end padding. The probe reads
// only the first packets and the end of the file, so a long file costs it a
// few kilobytes of I/O.
//
// FFmpeg 8.0 does not export the MP4 end padding as side data (FFmpeg 9
// does): its MP4 demuxer only shortens the last packet by it. For AAC, whose frames are all
// one length, the shortfall of the last packet against the one before it is
// that padding (CORE-1039).

// gaplessHeadPackets is how many leading packets the gapless probe reads.
// The delay rides on the first packet; the rest are margin.
const gaplessHeadPackets = 16

// gaplessTailSeconds is how much of the file's end the gapless probe reads to
// find the last packet. A file no longer than twice this is read whole, so
// the head and tail intervals never overlap.
const gaplessTailSeconds = 5

// gaplessArgs is the ffprobe command line of the gapless probe.
func gaplessArgs(path string, durationSeconds int) []string {
	args := []string{
		"-v", "error",
		"-select_streams", "a",
		"-show_entries", "stream=index,codec_name,sample_rate,time_base:packet=stream_index,pts,duration:packet_side_data=skip_samples,discard_padding",
		"-print_format", "json",
	}
	if durationSeconds > 2*gaplessTailSeconds {
		args = append(args, "-read_intervals", "%+#"+strconv.Itoa(gaplessHeadPackets)+","+strconv.Itoa(durationSeconds-gaplessTailSeconds)+"%")
	}
	args = append(args, inputProtocolArgs()...)
	return append(args, "--", path)
}

// probeGapless runs the gapless probe and merges its facts into probe's audio
// streams. It is best effort: a failure leaves the streams without gapless
// facts, which every consumer reads as "not declared". It returns only a
// process group that survived its kill.
func (r *Runner) probeGapless(ctx context.Context, path string, probe *Probe) error {
	ffprobePath, err := r.resolveProbe(ctx)
	if err != nil {
		return nil
	}
	cmd := r.hardenedCommand(ctx, ffprobePath, readConfinement(path).ReadOnly, nil, gaplessArgs(path, probe.DurationSeconds))
	stdout, _, err := r.run(ctx, cmd)
	if err != nil {
		return survivedError(err)
	}
	facts, err := parseGapless(stdout)
	if err != nil {
		return nil
	}
	applyGapless(probe, facts)
	return nil
}

// gaplessFacts is what the gapless probe learned about one audio stream.
type gaplessFacts struct {
	SampleRate     int
	DelaySamples   int64
	PaddingSamples int64
	// DecodedSamples is the stream's content length once the delay and the
	// padding are trimmed; 0 when it could not be measured.
	DecodedSamples int64
}

type gaplessDocument struct {
	Packets []struct {
		StreamIndex int    `json:"stream_index"`
		PTS         *int64 `json:"pts"`
		Duration    *int64 `json:"duration"`
		SideData    []struct {
			SkipSamples    int64 `json:"skip_samples"`
			DiscardPadding int64 `json:"discard_padding"`
		} `json:"side_data_list"`
	} `json:"packets"`
	Streams []struct {
		Index      int    `json:"index"`
		CodecName  string `json:"codec_name"`
		SampleRate string `json:"sample_rate"`
		TimeBase   string `json:"time_base"`
	} `json:"streams"`
}

type gaplessPacket struct {
	pts, duration int64
	timed         bool
}

type gaplessStream struct {
	facts             gaplessFacts
	timeBase          *big.Rat
	first, prev, last gaplessPacket
	packets           int
	// fixedFrames marks a codec whose every frame but the last is one
	// length, so a short last packet measures the end padding.
	fixedFrames bool
}

// parseGapless reads the gapless probe's JSON into per-stream facts, keyed by
// the absolute ffprobe stream index.
func parseGapless(data []byte) (map[int]gaplessFacts, error) {
	var doc gaplessDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	streams := make(map[int]*gaplessStream, len(doc.Streams))
	for _, stream := range doc.Streams {
		rate, err := strconv.Atoi(stream.SampleRate)
		if err != nil || rate <= 0 {
			continue
		}
		timeBase, ok := new(big.Rat).SetString(stream.TimeBase)
		if !ok || timeBase.Sign() <= 0 {
			continue
		}
		streams[stream.Index] = &gaplessStream{facts: gaplessFacts{SampleRate: rate}, timeBase: timeBase, fixedFrames: stream.CodecName == "aac"}
	}
	for _, packet := range doc.Packets {
		stream, ok := streams[packet.StreamIndex]
		if !ok {
			continue
		}
		current := gaplessPacket{}
		if packet.PTS != nil && packet.Duration != nil {
			current = gaplessPacket{pts: *packet.PTS, duration: *packet.Duration, timed: true}
		}
		var skip, discard int64
		for _, side := range packet.SideData {
			skip = max(skip, side.SkipSamples)
			discard = max(discard, side.DiscardPadding)
		}
		if stream.packets == 0 {
			stream.first = current
			stream.facts.DelaySamples = skip
		}
		stream.prev, stream.last = stream.last, current
		// The padding is the last packet's; a mid-stream discard is
		// superseded by the packets after it.
		stream.facts.PaddingSamples = discard
		stream.packets++
	}
	facts := make(map[int]gaplessFacts, len(streams))
	for index, stream := range streams {
		if stream.packets == 0 {
			continue
		}
		if stream.facts.PaddingSamples == 0 {
			stream.facts.PaddingSamples = stream.shortLastFrame()
		}
		stream.facts.DecodedSamples = stream.decodedSamples()
		facts[index] = stream.facts
	}
	return facts, nil
}

// shortLastFrame is how many samples a fixed-frame stream's last packet falls
// short of the packet before it: the end padding a demuxer trimmed without
// declaring it. It is 0 for a variable-frame codec, a whole last frame, or a
// time base coarser than one sample (Matroska's milliseconds), whose rounded
// durations would mismeasure it.
func (s *gaplessStream) shortLastFrame() int64 {
	if !s.fixedFrames || s.packets < 2 || !s.prev.timed || !s.last.timed || s.last.duration >= s.prev.duration {
		return 0
	}
	if samplesPerTick := new(big.Rat).Mul(s.timeBase, big.NewRat(int64(s.facts.SampleRate), 1)); samplesPerTick.Num().Cmp(big.NewInt(1)) != 0 {
		return 0
	}
	return ticksToSamples(s.prev.duration-s.last.duration, s.timeBase, s.facts.SampleRate)
}

// decodedSamples is the stream's content length from its first packet to the
// end of its last, less the delay and the padding; 0 when the packets do not
// measure it.
func (s *gaplessStream) decodedSamples() int64 {
	if s.packets < 2 || !s.first.timed || !s.last.timed {
		return 0
	}
	ticks := s.last.pts + s.last.duration - s.first.pts
	decoded := ticksToSamples(ticks, s.timeBase, s.facts.SampleRate) - s.facts.DelaySamples
	// The MP4 and Ogg demuxers already shorten the last packet by the
	// padding; the MP3 demuxer leaves it a whole frame, as long as the one
	// before it.
	if s.facts.PaddingSamples > 0 && s.prev.timed && s.last.duration >= s.prev.duration {
		decoded -= s.facts.PaddingSamples
	}
	return max(decoded, 0)
}

// ticksToSamples converts a duration in stream time-base ticks to samples at
// the stream's sample rate, rounding to the nearest sample.
func ticksToSamples(ticks int64, timeBase *big.Rat, sampleRate int) int64 {
	samples := new(big.Rat).Mul(big.NewRat(ticks, 1), timeBase)
	samples.Mul(samples, big.NewRat(int64(sampleRate), 1))
	value, _ := samples.Float64()
	if value < 0 {
		return 0
	}
	return int64(value + 0.5)
}

// applyGapless merges the probe's facts into the audio streams. An iTunes
// gapless tag (iTunSMPB) is the authoritative declaration for the file's
// first audio stream: FFmpeg honours it only through an MP4 edit list, so a
// file carrying the tag without one shows no delay in its side data. When
// the tag and the side data disagree on the delay, the decoded length the
// side data measured does not match the declared trim and is dropped.
func applyGapless(probe *Probe, facts map[int]gaplessFacts) {
	tagDelay, tagPadding, tagged := parseITunSMPB(probe.Tags["itunsmpb"])
	firstAudio := true
	for i := range probe.Streams {
		stream := &probe.Streams[i]
		if stream.Type != "audio" {
			continue
		}
		fact, ok := facts[stream.Index]
		if ok && firstAudio && tagged {
			if fact.DelaySamples != tagDelay {
				fact.DecodedSamples = 0
			}
			fact.DelaySamples, fact.PaddingSamples = tagDelay, tagPadding
		}
		firstAudio = false
		if !ok {
			continue
		}
		stream.SampleRate = fact.SampleRate
		stream.EncoderDelaySamples = int(fact.DelaySamples)
		stream.EncoderPaddingSamples = int(fact.PaddingSamples)
		stream.DecodedSamples = fact.DecodedSamples
	}
}

// maxGaplessTagSamples bounds an iTunSMPB delay or padding: an encoder primes
// and pads by a few frames, so a larger value marks a corrupt tag.
const maxGaplessTagSamples = 1 << 16

// parseITunSMPB reads the encoder delay and end padding from an iTunes
// gapless tag: space-separated hex fields, of which the second is the delay
// and the third the padding (" 00000000 00000840 000001C0 ...").
func parseITunSMPB(tag string) (delay, padding int64, ok bool) {
	fields := strings.Fields(tag)
	if len(fields) < 3 {
		return 0, 0, false
	}
	delay, err := strconv.ParseInt(fields[1], 16, 64)
	if err != nil || delay < 0 || delay > maxGaplessTagSamples {
		return 0, 0, false
	}
	padding, err = strconv.ParseInt(fields[2], 16, 64)
	if err != nil || padding < 0 || padding > maxGaplessTagSamples {
		return 0, 0, false
	}
	if delay == 0 && padding == 0 {
		return 0, 0, false
	}
	return delay, padding, true
}
