package ffmpeg

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"slices"
	"time"
)

const maxContainerIndexBytes = 64 << 20 // 64 MiB safety cap for moov/Cues

// probeKeyframesFast attempts to read video keyframe timestamps directly from
// MP4 (moov/stss/stts/ctts) or Matroska/WebM (Info/Tracks/Cues) container
// indexes without spawning ffprobe. Returns (idx, true) on a clean,
// unambiguous index, or (_, false) to fall back to ffprobe.
func probeKeyframesFast(src string) (KeyframeIndex, bool) {
	f, err := os.Open(src) // #nosec G304 -- src is caller-validated via checkPath in ProbeKeyframes
	if err != nil {
		return KeyframeIndex{}, false
	}
	defer func() { _ = f.Close() }()

	var hdr [12]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return KeyframeIndex{}, false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return KeyframeIndex{}, false
	}
	switch {
	case string(hdr[4:8]) == "ftyp":
		return parseMP4Keyframes(f)
	case hdr[0] == 0x1a && hdr[1] == 0x45 && hdr[2] == 0xdf && hdr[3] == 0xa3:
		return parseMKVKeyframes(f)
	default:
		return KeyframeIndex{}, false
	}
}

func roundUpMicros(num, den int64) (time.Duration, bool) {
	if den <= 0 || num <= 0 {
		return 0, num == 0 && den > 0
	}
	// ceil(num * 1e6 / den) without overflow for normal media durations.
	us := (num*1_000_000 + den - 1) / den
	if us < 0 {
		return 0, false
	}
	return time.Duration(us) * time.Microsecond, true
}

func finalizeKeyframeIndex(kfs []time.Duration, dur time.Duration) (KeyframeIndex, bool) {
	if len(kfs) == 0 || len(kfs) > maxProbedKeyframes || dur <= 0 {
		return KeyframeIndex{}, false
	}
	slices.Sort(kfs)
	kfs = slices.Compact(kfs)
	if last := kfs[len(kfs)-1]; last > dur {
		dur = last
	}
	return KeyframeIndex{Keyframes: kfs, Duration: dur}, true
}

// --- MP4 / MOV / M4V ---

func readBoxHeader(r io.ReadSeeker, end int64) (boxType string, bodyStart, boxEnd int64, ok bool) {
	pos, err := r.Seek(0, io.SeekCurrent)
	if err != nil || pos+8 > end {
		return "", 0, 0, false
	}
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return "", 0, 0, false
	}
	size := int64(binary.BigEndian.Uint32(buf[0:4]))
	typ := string(buf[4:8])
	hdrLen := int64(8)
	switch size {
	case 1:
		if pos+16 > end {
			return "", 0, 0, false
		}
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return "", 0, 0, false
		}
		u := binary.BigEndian.Uint64(ext[:])
		if u < 16 || u > math.MaxInt64 {
			return "", 0, 0, false
		}
		size = int64(u)
		hdrLen = 16
	case 0:
		size = end - pos
	}
	if size < hdrLen || pos+size > end {
		return "", 0, 0, false
	}
	return typ, pos + hdrLen, pos + size, true
}

func parseMP4Keyframes(f *os.File) (KeyframeIndex, bool) {
	st, err := f.Stat()
	if err != nil {
		return KeyframeIndex{}, false
	}
	fileSize := st.Size()
	for {
		typ, bodyStart, boxEnd, ok := readBoxHeader(f, fileSize)
		if !ok {
			return KeyframeIndex{}, false
		}
		if typ == "moov" {
			size := boxEnd - bodyStart
			if size <= 0 || size > maxContainerIndexBytes {
				return KeyframeIndex{}, false
			}
			buf := make([]byte, size)
			if _, err := io.ReadFull(f, buf); err != nil {
				return KeyframeIndex{}, false
			}
			return parseMP4Moov(buf)
		}
		if _, err := f.Seek(boxEnd, io.SeekStart); err != nil {
			return KeyframeIndex{}, false
		}
	}
}

func iterBoxes(b []byte, fn func(typ string, body []byte) bool) bool {
	for len(b) >= 8 {
		size := uint64(binary.BigEndian.Uint32(b[0:4]))
		typ := string(b[4:8])
		hdr := uint64(8)
		switch size {
		case 1:
			if len(b) < 16 {
				return false
			}
			size = binary.BigEndian.Uint64(b[8:16])
			hdr = 16
		case 0:
			size = uint64(len(b))
		}
		if size < hdr || size > uint64(len(b)) {
			return false
		}
		if !fn(typ, b[hdr:size]) {
			return false
		}
		b = b[size:]
	}
	return len(b) == 0
}

func parseMP4Moov(moov []byte) (KeyframeIndex, bool) {
	var movieDur time.Duration
	var fragmented bool
	var traks [][]byte
	if !iterBoxes(moov, func(typ string, body []byte) bool {
		switch typ {
		case "mvex":
			fragmented = true
			return false
		case "mvhd":
			if d, ok := parseMP4Mvhd(body); ok {
				movieDur = d
			}
		case "trak":
			traks = append(traks, body)
		}
		return true
	}) || fragmented {
		return KeyframeIndex{}, false
	}
	for _, trak := range traks {
		if kfs, trackDur, ok := parseMP4VideoTrak(trak); ok {
			dur := movieDur
			if trackDur > dur {
				dur = trackDur
			}
			return finalizeKeyframeIndex(kfs, dur)
		}
	}
	return KeyframeIndex{}, false
}

func parseMP4Mvhd(b []byte) (time.Duration, bool) {
	if len(b) < 20 {
		return 0, false
	}
	ver := b[0]
	var timescale, dur int64
	switch ver {
	case 0:
		if len(b) < 20 {
			return 0, false
		}
		timescale = int64(binary.BigEndian.Uint32(b[12:16]))
		dur = int64(binary.BigEndian.Uint32(b[16:20]))
	case 1:
		if len(b) < 32 {
			return 0, false
		}
		timescale = int64(binary.BigEndian.Uint32(b[20:24]))
		u := binary.BigEndian.Uint64(b[24:32])
		if u > math.MaxInt64 {
			return 0, false
		}
		dur = int64(u)
	default:
		return 0, false
	}
	return roundUpMicros(dur, timescale)
}

type sttsEntry struct {
	count int64
	delta int64
}

type cttsEntry struct {
	count  int64
	offset int64
}

func parseMP4VideoTrak(trak []byte) ([]time.Duration, time.Duration, bool) {
	var mdia, edts []byte
	iterBoxes(trak, func(typ string, body []byte) bool {
		switch typ {
		case "mdia":
			mdia = body
		case "edts":
			edts = body
		}
		return true
	})
	if len(mdia) == 0 {
		return nil, 0, false
	}
	var isVideo bool
	var timescale, mdiaDur int64
	var minf []byte
	iterBoxes(mdia, func(typ string, body []byte) bool {
		switch typ {
		case "hdlr":
			if len(body) >= 12 && string(body[8:12]) == "vide" {
				isVideo = true
			}
		case "mdhd":
			timescale, mdiaDur = parseMP4Mdhd(body)
		case "minf":
			minf = body
		}
		return true
	})
	if !isVideo || timescale <= 0 || len(minf) == 0 {
		return nil, 0, false
	}
	var mediaStartOffset int64
	if len(edts) > 0 {
		var elstOK bool
		iterBoxes(edts, func(typ string, body []byte) bool {
			if typ == "elst" {
				mediaStartOffset, elstOK = parseMP4SimpleElst(body)
				return false
			}
			return true
		})
		if !elstOK {
			return nil, 0, false
		}
	}
	var stbl []byte
	iterBoxes(minf, func(typ string, body []byte) bool {
		if typ == "stbl" {
			stbl = body
			return false
		}
		return true
	})
	if len(stbl) == 0 {
		return nil, 0, false
	}
	var stts []sttsEntry
	var ctts []cttsEntry
	var stss []int64
	var hasStss bool
	iterBoxes(stbl, func(typ string, body []byte) bool {
		switch typ {
		case "stts":
			stts = parseMP4Stts(body)
		case "ctts":
			ctts = parseMP4Ctts(body)
		case "stss":
			stss = parseMP4Stss(body)
			hasStss = true
		}
		return true
	})
	if !hasStss || len(stss) == 0 || len(stts) == 0 || len(stss) > maxProbedKeyframes {
		return nil, 0, false
	}

	kfs := make([]time.Duration, 0, len(stss))
	sttsIdx, sttsRem := 0, stts[0].count
	cttsIdx, cttsRem := 0, int64(0)
	if len(ctts) > 0 {
		cttsRem = ctts[0].count
	}
	var sampleIdx int64 = 1
	var dts int64
	for _, syncSample := range stss {
		if syncSample < sampleIdx {
			return nil, 0, false
		}
		for sampleIdx < syncSample {
			step := syncSample - sampleIdx
			if sttsIdx >= len(stts) {
				return nil, 0, false
			}
			if sttsRem < step {
				step = sttsRem
			}
			dts += step * stts[sttsIdx].delta
			sampleIdx += step
			sttsRem -= step
			for sttsRem == 0 && sttsIdx+1 < len(stts) {
				sttsIdx++
				sttsRem = stts[sttsIdx].count
			}
			if len(ctts) > 0 {
				adv := step
				for adv > 0 {
					if cttsIdx >= len(ctts) {
						return nil, 0, false
					}
					take := adv
					if cttsRem < take {
						take = cttsRem
					}
					adv -= take
					cttsRem -= take
					for cttsRem == 0 && cttsIdx+1 < len(ctts) {
						cttsIdx++
						cttsRem = ctts[cttsIdx].count
					}
				}
			}
		}
		pts := dts - mediaStartOffset
		if len(ctts) > 0 {
			if cttsIdx >= len(ctts) {
				return nil, 0, false
			}
			pts += ctts[cttsIdx].offset
		}
		if pts < 0 {
			pts = 0
		}
		d, ok := roundUpMicros(pts, timescale)
		if !ok {
			return nil, 0, false
		}
		kfs = append(kfs, d)
	}
	dur, _ := roundUpMicros(mdiaDur, timescale)
	return kfs, dur, true
}

func parseMP4Mdhd(b []byte) (timescale, dur int64) {
	if len(b) < 20 {
		return 0, 0
	}
	switch b[0] {
	case 0:
		return int64(binary.BigEndian.Uint32(b[12:16])), int64(binary.BigEndian.Uint32(b[16:20]))
	case 1:
		if len(b) < 32 {
			return 0, 0
		}
		u := binary.BigEndian.Uint64(b[24:32])
		if u > math.MaxInt64 {
			return 0, 0
		}
		return int64(binary.BigEndian.Uint32(b[20:24])), int64(u)
	default:
		return 0, 0
	}
}

func parseMP4SimpleElst(b []byte) (mediaTime int64, ok bool) {
	if len(b) < 8 {
		return 0, false
	}
	ver := b[0]
	count := binary.BigEndian.Uint32(b[4:8])
	if count == 0 {
		return 0, true
	}
	if count > 1 {
		return 0, false
	}
	if ver == 0 && len(b) >= 20 {
		u := binary.BigEndian.Uint32(b[12:16])
		if u > math.MaxInt32 {
			return 0, false
		}
		return int64(u), true
	}
	if ver == 1 && len(b) >= 28 {
		u := binary.BigEndian.Uint64(b[16:24])
		if u > math.MaxInt64 {
			return 0, false
		}
		return int64(u), true
	}
	return 0, false
}

func parseMP4Stts(b []byte) []sttsEntry {
	if len(b) < 8 {
		return nil
	}
	count := int(binary.BigEndian.Uint32(b[4:8]))
	if count <= 0 || 8+count*8 > len(b) {
		return nil
	}
	out := make([]sttsEntry, count)
	for i := range count {
		off := 8 + i*8
		out[i] = sttsEntry{
			count: int64(binary.BigEndian.Uint32(b[off : off+4])),
			delta: int64(binary.BigEndian.Uint32(b[off+4 : off+8])),
		}
	}
	return out
}

func parseMP4Ctts(b []byte) []cttsEntry {
	if len(b) < 8 {
		return nil
	}
	ver := b[0]
	count := int(binary.BigEndian.Uint32(b[4:8]))
	if count <= 0 || 8+count*8 > len(b) {
		return nil
	}
	out := make([]cttsEntry, count)
	for i := range count {
		off := 8 + i*8
		var offset int64
		if ver == 0 {
			offset = int64(binary.BigEndian.Uint32(b[off+4 : off+8]))
		} else {
			u := binary.BigEndian.Uint32(b[off+4 : off+8])
			if u <= math.MaxInt32 {
				offset = int64(u)
			} else {
				offset = int64(u) - (1 << 32)
			}
		}
		out[i] = cttsEntry{
			count:  int64(binary.BigEndian.Uint32(b[off : off+4])),
			offset: offset,
		}
	}
	return out
}

func parseMP4Stss(b []byte) []int64 {
	if len(b) < 8 {
		return nil
	}
	count := int(binary.BigEndian.Uint32(b[4:8]))
	if count <= 0 || count > maxProbedKeyframes || 8+count*4 > len(b) {
		return nil
	}
	out := make([]int64, count)
	for i := range count {
		off := 8 + i*4
		out[i] = int64(binary.BigEndian.Uint32(b[off : off+4]))
	}
	return out
}

// --- MKV / WebM (EBML) ---

func readEBMLID(r io.Reader) (uint64, int, bool) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, 0, false
	}
	b := first[0]
	if b == 0 {
		return 0, 0, false
	}
	n := 1
	mask := byte(0x80)
	for b&mask == 0 {
		mask >>= 1
		n++
		if n > 4 {
			return 0, 0, false
		}
	}
	val := uint64(b)
	if n > 1 {
		var rest [3]byte
		if _, err := io.ReadFull(r, rest[:n-1]); err != nil {
			return 0, 0, false
		}
		for i := 0; i < n-1; i++ {
			val = (val << 8) | uint64(rest[i])
		}
	}
	return val, n, true
}

func readEBMLSize(r io.Reader) (uint64, int, bool) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, 0, false
	}
	b := first[0]
	if b == 0 {
		return 0, 0, false
	}
	n := 1
	mask := byte(0x80)
	for b&mask == 0 {
		mask >>= 1
		n++
		if n > 8 {
			return 0, 0, false
		}
	}
	val := uint64(b &^ mask)
	if n > 1 {
		var rest [7]byte
		if _, err := io.ReadFull(r, rest[:n-1]); err != nil {
			return 0, 0, false
		}
		for i := 0; i < n-1; i++ {
			val = (val << 8) | uint64(rest[i])
		}
	}
	return val, n, true
}

func readEBMLIDMem(b []byte) (uint64, int, bool) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0, false
	}
	n := 1
	mask := byte(0x80)
	for b[0]&mask == 0 {
		mask >>= 1
		n++
		if n > 4 || n > len(b) {
			return 0, 0, false
		}
	}
	val := uint64(b[0])
	for i := 1; i < n; i++ {
		val = (val << 8) | uint64(b[i])
	}
	return val, n, true
}

func readEBMLSizeMem(b []byte) (uint64, int, bool) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0, false
	}
	n := 1
	mask := byte(0x80)
	for b[0]&mask == 0 {
		mask >>= 1
		n++
		if n > 8 || n > len(b) {
			return 0, 0, false
		}
	}
	val := uint64(b[0] &^ mask)
	for i := 1; i < n; i++ {
		val = (val << 8) | uint64(b[i])
	}
	return val, n, true
}

func iterEBML(b []byte, fn func(id uint64, body []byte) bool) bool {
	for len(b) > 0 {
		id, n1, ok := readEBMLIDMem(b)
		if !ok {
			return false
		}
		b = b[n1:]
		sz, n2, ok := readEBMLSizeMem(b)
		if !ok || sz > uint64(len(b[n2:])) {
			return false
		}
		b = b[n2:]
		if !fn(id, b[:sz]) {
			return false
		}
		b = b[sz:]
	}
	return true
}

func ebmlUint(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = (v << 8) | uint64(x)
	}
	return v
}

func ebmlFloat(b []byte) (float64, bool) {
	switch len(b) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), true
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(b)), true
	default:
		return 0, false
	}
}

func parseMKVKeyframes(f *os.File) (KeyframeIndex, bool) {
	st, err := f.Stat()
	if err != nil {
		return KeyframeIndex{}, false
	}
	fileSize := st.Size()

	// Skip EBML header, locate Segment (0x18538067).
	var segStart, segEnd int64
	for {
		pos, err := f.Seek(0, io.SeekCurrent)
		if err != nil || pos >= fileSize {
			return KeyframeIndex{}, false
		}
		id, _, ok := readEBMLID(f)
		if !ok {
			return KeyframeIndex{}, false
		}
		sz, _, ok := readEBMLSize(f)
		if !ok {
			return KeyframeIndex{}, false
		}
		bodyStart, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return KeyframeIndex{}, false
		}
		if id == 0x18538067 {
			segStart = bodyStart
			rem := fileSize - bodyStart
			if rem < 0 {
				return KeyframeIndex{}, false
			}
			if sz == (1<<56)-1 || sz > math.MaxInt64 || int64(sz) > rem {
				segEnd = fileSize
			} else {
				segEnd = bodyStart + int64(sz)
			}
			break
		}
		if sz > math.MaxInt64 || int64(sz) > fileSize-bodyStart {
			return KeyframeIndex{}, false
		}
		if _, err := f.Seek(bodyStart+int64(sz), io.SeekStart); err != nil {
			return KeyframeIndex{}, false
		}
	}

	var infoBuf, tracksBuf, cuesBuf []byte
	var seekOffsets []int64
	for pos := segStart; pos < segEnd; {
		if _, err := f.Seek(pos, io.SeekStart); err != nil {
			return KeyframeIndex{}, false
		}
		id, _, ok := readEBMLID(f)
		if !ok {
			break
		}
		sz, _, ok := readEBMLSize(f)
		if !ok {
			break
		}
		bodyStart, err := f.Seek(0, io.SeekCurrent)
		if err != nil || bodyStart > segEnd || sz > math.MaxInt64 || int64(sz) > segEnd-bodyStart {
			break
		}
		nextPos := bodyStart + int64(sz)
		switch id {
		case 0x114D9B74: // SeekHead
			if sz <= 1<<20 {
				buf := make([]byte, sz)
				if _, err := io.ReadFull(f, buf); err == nil {
					seekOffsets = append(seekOffsets, parseMKVSeekHead(buf, segStart)...)
				}
			}
		case 0x1549A966: // Info
			if sz <= maxContainerIndexBytes {
				infoBuf = make([]byte, sz)
				if _, err := io.ReadFull(f, infoBuf); err != nil {
					return KeyframeIndex{}, false
				}
			}
		case 0x1654AE6B: // Tracks
			if sz <= maxContainerIndexBytes {
				tracksBuf = make([]byte, sz)
				if _, err := io.ReadFull(f, tracksBuf); err != nil {
					return KeyframeIndex{}, false
				}
			}
		case 0x1C53BB6B: // Cues
			if sz <= maxContainerIndexBytes {
				cuesBuf = make([]byte, sz)
				if _, err := io.ReadFull(f, cuesBuf); err != nil {
					return KeyframeIndex{}, false
				}
			}
		case 0x1F43B675: // Cluster: stop linear scan and jump to SeekHead targets if Cues wasn't before Clusters.
			pos = segEnd
			continue
		}
		if infoBuf != nil && tracksBuf != nil && cuesBuf != nil {
			break
		}
		pos = nextPos
	}

	// Follow SeekHead offsets for any missing metadata/Cues placed at EOF.
	for _, off := range seekOffsets {
		if infoBuf != nil && tracksBuf != nil && cuesBuf != nil {
			break
		}
		if off < segStart || off >= segEnd {
			continue
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			continue
		}
		id, _, ok := readEBMLID(f)
		if !ok {
			continue
		}
		sz, _, ok := readEBMLSize(f)
		if !ok || sz > maxContainerIndexBytes {
			continue
		}
		bodyStart, err := f.Seek(0, io.SeekCurrent)
		if err != nil || bodyStart > segEnd || sz > math.MaxInt64 || int64(sz) > segEnd-bodyStart {
			continue
		}
		switch id {
		case 0x1549A966:
			if infoBuf == nil {
				infoBuf = make([]byte, sz)
				_, _ = io.ReadFull(f, infoBuf)
			}
		case 0x1654AE6B:
			if tracksBuf == nil {
				tracksBuf = make([]byte, sz)
				_, _ = io.ReadFull(f, tracksBuf)
			}
		case 0x1C53BB6B:
			if cuesBuf == nil {
				cuesBuf = make([]byte, sz)
				_, _ = io.ReadFull(f, cuesBuf)
			}
		}
	}

	if len(infoBuf) == 0 || len(tracksBuf) == 0 || len(cuesBuf) == 0 {
		return KeyframeIndex{}, false
	}

	tcScale := uint64(1_000_000)
	var durTicks float64
	iterEBML(infoBuf, func(id uint64, body []byte) bool {
		switch id {
		case 0x2AD7B1: // TimecodeScale
			if v := ebmlUint(body); v > 0 {
				tcScale = v
			}
		case 0x4489: // Duration
			if v, ok := ebmlFloat(body); ok {
				durTicks = v
			}
		}
		return true
	})
	if durTicks <= 0 {
		return KeyframeIndex{}, false
	}
	durNs := int64(math.Ceil(durTicks * float64(tcScale)))
	dur, ok := roundUpMicros(durNs, 1_000_000_000)
	if !ok {
		return KeyframeIndex{}, false
	}

	var videoTrack uint64
	var codecDelay uint64
	iterEBML(tracksBuf, func(id uint64, body []byte) bool {
		if id != 0xAE || videoTrack != 0 {
			return true
		}
		var tNum, tType, delay uint64
		iterEBML(body, func(eid uint64, ebody []byte) bool {
			switch eid {
			case 0xD7:
				tNum = ebmlUint(ebody)
			case 0x83:
				tType = ebmlUint(ebody)
			case 0x56AA:
				delay = ebmlUint(ebody)
			}
			return true
		})
		if tType == 1 && tNum > 0 {
			videoTrack = tNum
			codecDelay = delay
			return false
		}
		return true
	})
	if videoTrack == 0 || codecDelay > 0 {
		return KeyframeIndex{}, false
	}

	var kfs []time.Duration
	var parseOK = true
	iterEBML(cuesBuf, func(id uint64, body []byte) bool {
		if id != 0xBB { // CuePoint
			return true
		}
		var cueTime uint64
		var hasTime, matchesTrack bool
		iterEBML(body, func(cid uint64, cbody []byte) bool {
			switch cid {
			case 0xB3: // CueTime
				cueTime = ebmlUint(cbody)
				hasTime = true
			case 0xB7: // CueTrackPositions
				iterEBML(cbody, func(pid uint64, pbody []byte) bool {
					if pid == 0xF7 && ebmlUint(pbody) == videoTrack {
						matchesTrack = true
					}
					return true
				})
			}
			return true
		})
		if hasTime && matchesTrack {
			if len(kfs) >= maxProbedKeyframes {
				parseOK = false
				return false
			}
			ns := cueTime * tcScale
			if ns > math.MaxInt64 {
				parseOK = false
				return false
			}
			d, ok := roundUpMicros(int64(ns), 1_000_000_000)
			if !ok {
				parseOK = false
				return false
			}
			kfs = append(kfs, d)
		}
		return true
	})
	if !parseOK {
		return KeyframeIndex{}, false
	}
	return finalizeKeyframeIndex(kfs, dur)
}

func parseMKVSeekHead(b []byte, segStart int64) []int64 {
	var offsets []int64
	iterEBML(b, func(id uint64, body []byte) bool {
		if id != 0x4DBB { // Seek
			return true
		}
		var targetID uint64
		var pos uint64
		var hasPos bool
		iterEBML(body, func(sid uint64, sbody []byte) bool {
			switch sid {
			case 0x53AB:
				targetID = ebmlUint(sbody)
			case 0x53AC:
				pos = ebmlUint(sbody)
				hasPos = true
			}
			return true
		})
		if hasPos && pos <= math.MaxInt64 {
			switch targetID {
			case 0x1549A966, 0x1654AE6B, 0x1C53BB6B:
				offsets = append(offsets, segStart+int64(pos))
			}
		}
		return true
	})
	return offsets
}
