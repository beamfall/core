package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
)

// KeyframeIndex is a source's video keyframe map: the presentation time of
// every video keyframe relative to the container start (the zero an input -ss
// counts from), ascending and deduplicated, plus the container duration.
//
// Times are rounded up to the microsecond, so seeking to one with an input -ss
// lands on that keyframe and never on the one before it.
type KeyframeIndex struct {
	Keyframes []time.Duration
	Duration  time.Duration
}

// maxProbedKeyframes bounds the index one probe may return. Three hours at one
// keyframe per 50ms is far below it; past it the source is not a sane
// passthrough candidate and the probe fails instead of growing without limit.
const maxProbedKeyframes = 1 << 20

// ProbeKeyframes reads src's first video stream packet by packet (a demux
// only, nothing is decoded) and returns its keyframe index. The output is
// parsed as it streams, so only the keyframes are held in memory however long
// the file is.
func (r *Runner) ProbeKeyframes(ctx context.Context, src string) (KeyframeIndex, error) {
	if err := checkPath(src); err != nil {
		return KeyframeIndex{}, err
	}
	ffprobePath, err := r.resolveProbe(ctx)
	if err != nil {
		return KeyframeIndex{}, fmt.Errorf("ffprobe is not available: %w", err)
	}
	args := append(append([]string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "packet=pts,dts,flags:stream=time_base:format=start_time,duration",
		"-of", "csv=p=1",
	}, inputProtocolArgs()...), "--", src)
	cmd := r.hardenedCommand(ctx, ffprobePath, readConfinement(src).ReadOnly, nil, args)
	parser := &keyframeParser{}
	group := newProcessGroup(cmd)
	stderr := &boundedBuffer{limit: captureLimitBytes, onOverflow: group.kill}
	parser.onFail = group.kill
	runErr := superviseGroup(ctx, group, parser, stderr)
	if stderr.overflowed() {
		return KeyframeIndex{}, overflowError(runErr)
	}
	if parser.err != nil {
		return KeyframeIndex{}, errors.Join(fmt.Errorf("ffprobe keyframes: %w", parser.err), survivedError(runErr))
	}
	if runErr != nil {
		if detail := captureDetail(nil, stderr.bytes(), nil); detail != "" {
			return KeyframeIndex{}, errors.Join(fmt.Errorf("ffprobe keyframes: %s", detail), survivedError(runErr))
		}
		return KeyframeIndex{}, fmt.Errorf("ffprobe keyframes: %w", runErr)
	}
	return parser.index()
}

// ParseKeyframeCSV parses ffprobe's `-show_entries
// packet=pts,dts,flags:stream=time_base:format=start_time,duration -of
// csv=p=1` output for one video stream into a KeyframeIndex.
func ParseKeyframeCSV(out []byte) (KeyframeIndex, error) {
	p := &keyframeParser{}
	if _, err := p.Write(out); err != nil {
		return KeyframeIndex{}, err
	}
	if p.err != nil {
		return KeyframeIndex{}, p.err
	}
	return p.index()
}

// keyframeParser is an io.Writer that parses ffprobe's packet CSV line by line
// as it streams, keeping only the keyframe timestamps.
type keyframeParser struct {
	partial    []byte
	keyframes  []int64
	timeBase   *big.Rat
	startUs    int64
	durationUs int64
	sawFormat  bool
	err        error
	onFail     func()
}

func (p *keyframeParser) Write(b []byte) (int, error) {
	if p.err != nil {
		return len(b), nil
	}
	data := b
	if len(p.partial) > 0 {
		data = append(p.partial, b...)
		p.partial = nil
	}
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		if err := p.line(strings.TrimRight(string(data[:i]), "\r")); err != nil {
			p.err = err
			if p.onFail != nil {
				go p.onFail()
			}
			return len(b), nil
		}
		data = data[i+1:]
	}
	if len(data) > 4096 {
		p.err = errors.New("ffprobe line too long")
		if p.onFail != nil {
			go p.onFail()
		}
		return len(b), nil
	}
	p.partial = append([]byte(nil), data...)
	return len(b), nil
}

func (p *keyframeParser) line(line string) error {
	fields := strings.Split(line, ",")
	switch fields[0] {
	case "packet":
		if len(fields) < 4 {
			return fmt.Errorf("malformed packet line %q", line)
		}
		flags := fields[3]
		if !strings.Contains(flags, "K") || strings.Contains(flags, "D") {
			return nil
		}
		ts := fields[1]
		if ts == "N/A" || ts == "" {
			ts = fields[2]
		}
		if ts == "N/A" || ts == "" {
			return nil
		}
		v, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return fmt.Errorf("packet timestamp %q: %w", ts, err)
		}
		if len(p.keyframes) >= maxProbedKeyframes {
			return fmt.Errorf("more than %d keyframes", maxProbedKeyframes)
		}
		p.keyframes = append(p.keyframes, v)
	case "stream":
		if len(fields) < 2 {
			return fmt.Errorf("malformed stream line %q", line)
		}
		tb, ok := new(big.Rat).SetString(fields[1])
		if !ok || tb.Sign() <= 0 {
			return fmt.Errorf("stream time_base %q", fields[1])
		}
		p.timeBase = tb
	case "format":
		if len(fields) < 3 {
			return fmt.Errorf("malformed format line %q", line)
		}
		// ffmpeg keeps the container start and duration in microseconds, so
		// their six-decimal print is exact.
		start, err := parseMicros(fields[1])
		if err != nil {
			return fmt.Errorf("format start_time: %w", err)
		}
		duration, err := parseMicros(fields[2])
		if err != nil {
			return fmt.Errorf("format duration: %w", err)
		}
		p.startUs, p.durationUs, p.sawFormat = start, duration, true
	case "":
	default:
		// Side data or a section this parser does not ask for; ignore it.
	}
	return nil
}

func (p *keyframeParser) index() (KeyframeIndex, error) {
	if len(p.partial) > 0 {
		line := strings.TrimRight(string(p.partial), "\r")
		p.partial = nil
		if err := p.line(line); err != nil {
			return KeyframeIndex{}, err
		}
	}
	if p.timeBase == nil {
		return KeyframeIndex{}, errors.New("no video stream")
	}
	if !p.sawFormat || p.durationUs <= 0 {
		return KeyframeIndex{}, errors.New("unknown duration")
	}
	if len(p.keyframes) == 0 {
		return KeyframeIndex{}, errors.New("no video keyframes")
	}
	scale := new(big.Rat).Mul(p.timeBase, big.NewRat(1_000_000, 1))
	out := make([]time.Duration, 0, len(p.keyframes))
	for _, ts := range p.keyframes {
		us := new(big.Rat).Mul(new(big.Rat).SetInt64(ts), scale)
		// Round up to the microsecond: a seek target at or after the keyframe
		// lands on it, one before lands on the previous keyframe.
		q, rem := new(big.Int).QuoRem(us.Num(), us.Denom(), new(big.Int))
		if rem.Sign() > 0 {
			q.Add(q, big.NewInt(1))
		}
		if !q.IsInt64() {
			return KeyframeIndex{}, fmt.Errorf("keyframe timestamp %d out of range", ts)
		}
		rel := q.Int64() - p.startUs
		if rel < 0 {
			rel = 0
		}
		out = append(out, time.Duration(rel)*time.Microsecond)
	}
	slices.Sort(out)
	out = slices.Compact(out)
	return KeyframeIndex{Keyframes: out, Duration: time.Duration(p.durationUs) * time.Microsecond}, nil
}

// parseMicros parses a decimal seconds value ("12.345678") into whole
// microseconds without going through a float.
func parseMicros(s string) (int64, error) {
	if s == "" || s == "N/A" {
		return 0, fmt.Errorf("missing value %q", s)
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 6 {
		frac = frac[:6]
	}
	frac += strings.Repeat("0", 6-len(frac))
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, err
	}
	us := w*1_000_000 + f
	if neg {
		us = -us
	}
	return us, nil
}

// formatMicros prints d as decimal seconds with microsecond precision, the form
// -ss and -output_ts_offset accept exactly.
func formatMicros(d time.Duration) string {
	us := d.Microseconds()
	sign := ""
	if us < 0 {
		sign, us = "-", -us
	}
	return fmt.Sprintf("%s%d.%06d", sign, us/1_000_000, us%1_000_000)
}
