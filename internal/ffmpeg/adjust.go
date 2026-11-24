package ffmpeg

import (
	"fmt"
	"math"
	"strconv"
)

// The accepted playback adjustment ranges. A pitch shift is whole semitones,
// and a tempo is a speed factor (1 is the recorded speed).
const (
	MinPitchShiftSemitones = -6
	MaxPitchShiftSemitones = 6
	MinTempo               = 0.75
	MaxTempo               = 1.25
)

// adjustSampleRate is the rate an adjusted encode resamples to around the
// pitch shift, so the shift factor does not depend on the source's rate.
const adjustSampleRate = 48000

// AudioAdjust is a pitch shift and a tempo change applied to an encode's
// audio. The pitch moves without changing the tempo and the tempo without
// changing the pitch. The zero value changes nothing.
type AudioAdjust struct {
	PitchSemitones int
	// Tempo is the speed factor; 0 means 1 (the recorded speed).
	Tempo float64
}

// IsZero reports whether the adjustment changes nothing.
func (a AudioAdjust) IsZero() bool {
	return a.PitchSemitones == 0 && a.tempo() == 1
}

func (a AudioAdjust) tempo() float64 {
	if a.Tempo == 0 {
		return 1
	}
	return a.Tempo
}

// SourceSeconds maps a position on the adjusted output to the source position
// it plays: output time multiplied by the tempo.
func (a AudioAdjust) SourceSeconds(outputSeconds float64) float64 {
	return outputSeconds * a.tempo()
}

// OutputSeconds is how long seconds of source last on the adjusted output,
// rounded up to whole seconds.
func (a AudioAdjust) OutputSeconds(sourceSeconds int) int {
	if sourceSeconds <= 0 {
		return sourceSeconds
	}
	return int(math.Ceil(float64(sourceSeconds)/a.tempo() - 1e-9))
}

// OutputPosition maps a source position to the adjusted output position that
// plays it, rounded down to whole seconds.
func (a AudioAdjust) OutputPosition(sourceSeconds int) int {
	if sourceSeconds <= 0 {
		return sourceSeconds
	}
	return int(math.Floor(float64(sourceSeconds)/a.tempo() + 1e-9))
}

// Filters is the audio filter chain for the adjustment, ahead of any other
// audio filter. A tempo change alone is atempo. A pitch shift resamples the
// audio at a rate scaled by 2^(semitones/12), which moves pitch and speed
// together, then atempo restores the wanted speed. Within the accepted ranges
// the atempo factor stays inside the 0.5 to 2 one filter instance takes.
func (a AudioAdjust) Filters() []string {
	return a.FiltersWithRubberband(false)
}

// FiltersWithRubberband builds the audio filter chain for the adjustment.
// When useRubberband is true and a pitch shift is requested, it emits
// rubberband with formant preservation for higher-quality vocal/music pitch
// shifting; otherwise it falls back to the portable asetrate + atempo chain.
func (a AudioAdjust) FiltersWithRubberband(useRubberband bool) []string {
	if a.IsZero() {
		return nil
	}
	tempo := a.tempo()
	if a.PitchSemitones == 0 {
		return []string{"atempo=" + formatFactor(tempo)}
	}
	if useRubberband {
		pitch := math.Pow(2, float64(a.PitchSemitones)/12)
		return []string{
			fmt.Sprintf("rubberband=pitch=%s:tempo=%s:formant=preserved", formatFactor(pitch), formatFactor(tempo)),
		}
	}
	rate := int(math.Round(adjustSampleRate * math.Pow(2, float64(a.PitchSemitones)/12)))
	shift := float64(rate) / adjustSampleRate
	return []string{
		fmt.Sprintf("aresample=%d", adjustSampleRate),
		fmt.Sprintf("asetrate=%d", rate),
		fmt.Sprintf("aresample=%d", adjustSampleRate),
		"atempo=" + formatFactor(tempo/shift),
	}
}

// formatFactor writes a filter factor to six decimals, without trailing zeros.
func formatFactor(v float64) string {
	return strconv.FormatFloat(math.Round(v*1e6)/1e6, 'f', -1, 64)
}

// formatSeconds writes a position to millisecond precision; a whole second
// prints as an integer, exactly as an unadjusted encode writes it.
func formatSeconds(v float64) string {
	return strconv.FormatFloat(math.Round(v*1000)/1000, 'f', -1, 64)
}
