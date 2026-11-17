package conductor

// BestCalibration picks the clock estimate to use from several four-timestamp
// exchanges (CORE-2244): the one with the lowest round trip. An exchange that
// was held up on the way out or the way back has a longer round trip and an
// offset that is wrong by up to half the delay, so the fastest exchange is the
// one least distorted by the network. ok is false when samples is empty.
func BestCalibration(samples []Calibration) (best Calibration, ok bool) {
	for i, s := range samples {
		if i == 0 || s.RoundTrip < best.RoundTrip {
			best = s
		}
	}
	return best, len(samples) > 0
}
