//go:build !linux && !darwin && !windows

package hostmetrics

type noProbe struct{}

func newProbe() probe { return noProbe{} }

func (noProbe) cpuPercent() (float64, bool)           { return 0, false }
func (noProbe) memory() (used, total uint64, ok bool) { return 0, 0, false }
